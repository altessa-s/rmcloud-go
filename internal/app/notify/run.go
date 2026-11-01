// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package notify

import (
	"context"
	"errors"
	"time"

	"github.com/altessa-s/go-atlas-core/retry"
	"github.com/altessa-s/go-atlas-core/types/redacted"

	"github.com/altessa-s/rmcloud-go/internal/domain"
	"github.com/altessa-s/rmcloud-go/internal/port"
)

func upgradeHeader(userAgent string, token redacted.RedactedString) port.Header {
	header := port.Header{}
	if userAgent != "" {
		header[domain.HeaderUserAgent] = []string{userAgent}
	}
	header[domain.HeaderAuthorization] = []string{"Bearer " + token.Expose()}
	return header
}

func (s *Stream) run(ctx context.Context, done chan struct{}) {
	defer close(done)
	var failures int
	for {
		if ctx.Err() != nil {
			return
		}
		token, err := s.tokens.UserToken(ctx)
		var sent redacted.RedactedString
		var sock port.Socket
		if err == nil {
			sent = token
			endpoint := s.hosts.Notifications(ctx)
			sock, err = s.dialer.Dial(ctx, endpoint, upgradeHeader(s.userAgent, token))
		}
		if ctx.Err() != nil {
			if sock != nil {
				_ = sock.Close()
			}
			return
		}
		if err != nil {
			if s.fail(ctx, err, sent, &failures) {
				return
			}
			if !s.backoff(ctx, failures) {
				return
			}
			continue
		}
		s.setCurrent(sock)
		if ctx.Err() != nil {
			s.clearCurrent(sock)
			_ = sock.Close()
			return
		}
		failures = 0
		s.logger().Info("notification socket connected")
		s.signal(Signal{Kind: SignalConnected})
		err = s.listen(ctx, sock, domain.DeviceID(token.Expose()))
		s.clearCurrent(sock)
		_ = sock.Close()
		if ctx.Err() != nil {
			return
		}
		if s.fail(ctx, err, sent, &failures) {
			return
		}
		if !s.backoff(ctx, failures) {
			return
		}
	}
}

func (s *Stream) listen(ctx context.Context, sock port.Socket, own *string) error {
	kctx, cancel := context.WithCancel(ctx)
	kdone := make(chan struct{})
	go func() {
		defer close(kdone)
		s.keepAlive(kctx, sock)
	}()
	defer func() {
		cancel()
		<-kdone
	}()
	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		frame, err := sock.Receive(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return err
		}
		event, ok := parse(frame, own)
		if !ok {
			s.logger().Debug("notification event", "name", "unparsed")
			s.signal(Signal{Kind: SignalUnreadable, Frame: frame})
			continue
		}
		name := event.Name
		if name == "" {
			name = "unnamed"
		}
		s.logger().Debug("notification event", "name", name)
		s.signal(Signal{Kind: SignalEvent, Event: event, Frame: frame})
	}
}

func (s *Stream) fail(ctx context.Context, err error, token redacted.RedactedString, failures *int) bool {
	if ctx.Err() != nil {
		return true
	}
	if token != "" && errors.Is(err, domain.ErrUnauthorized) {
		s.tokens.InvalidateUserToken(token)
	}
	if domain.IsKind(err, domain.KindUnsupportedPlatform) {
		s.signal(Signal{Kind: SignalDropped, Err: err})
		return true
	}
	s.logger().Debug("notification socket dropped", "err", err)
	s.signal(Signal{Kind: SignalDropped, Err: err})
	*failures++
	return ctx.Err() != nil
}

func (s *Stream) backoff(ctx context.Context, failures int) bool {
	if err := s.clock.Sleep(ctx, reconnectDelay(failures)); err != nil || ctx.Err() != nil {
		return false
	}
	return true
}

// The reconnect pause doubles from two seconds after the first failure up
// to a minute.
const (
	reconnectBase   = time.Second
	reconnectFactor = 2
	reconnectMax    = time.Minute
)

// reconnectDelay is the pause before a reconnect after failures in a row.
func reconnectDelay(failures int) time.Duration {
	return retry.Exponential(reconnectBase,
		retry.WithExponentialFactor(reconnectFactor),
		retry.WithExponentialMaxDelay(reconnectMax),
	)(failures, nil)
}
