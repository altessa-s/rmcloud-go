// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package notify

import (
	"context"
	"log/slog"
	"sync"

	"github.com/altessa-s/go-atlas-core/time/clock"

	"github.com/altessa-s/rmcloud-go/internal/port"
)

// Hosts names the notification socket.
type Hosts = port.NotificationHosts

// Tokens supplies the user token sent on the upgrade.
type Tokens = port.RefreshableTokens

// SignalKind and Signal are the port's: the facade reads them without
// importing this package.
type (
	SignalKind = port.SignalKind
	Signal     = port.Signal
)

// The signal kinds.
const (
	SignalConnected  = port.SignalConnected
	SignalEvent      = port.SignalEvent
	SignalUnreadable = port.SignalUnreadable
	SignalDropped    = port.SignalDropped
)

// Stream reads the notification socket until Stop.
type Stream struct {
	hosts  Hosts
	tokens Tokens
	dialer port.Dialer
	options

	onSignal func(Signal)
	clock    clock.Clock

	mu      sync.Mutex
	running bool
	cancel  context.CancelFunc
	done    chan struct{}
	current port.Socket
}

// New returns a stream. A missing dependency is a programming error and
// panics here, not on the stream goroutine. A nil option is skipped.
// onSignal runs on the stream's goroutine and must not call Stop. The
// default User-Agent is none; the composition root sets it.
//
// hosts names the socket, tokens authorize the upgrade, dialer opens it, and
// clk paces the pings and the reconnects.
func New(hosts Hosts, tokens Tokens, dialer port.Dialer, clk clock.Clock, onSignal func(Signal), opts ...Option) *Stream {
	switch {
	case hosts == nil:
		panic("notify: Hosts is required")
	case tokens == nil:
		panic("notify: Tokens is required")
	case dialer == nil:
		panic("notify: Dialer is required")
	case clk == nil:
		panic("notify: Clock is required")
	}
	s := &Stream{
		options:  *newOptions(opts...),
		hosts:    hosts,
		tokens:   tokens,
		dialer:   dialer,
		clock:    clk,
		onSignal: onSignal,
	}
	return s
}

func (s *Stream) logger() *slog.Logger { return s.log }

// Start begins reading. A second call does nothing while a run is still
// tracked, including after that run has already returned.
func (s *Stream) Start(ctx context.Context) {
	s.mu.Lock()
	if s.running {
		s.mu.Unlock()
		return
	}
	runCtx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	s.running = true
	s.cancel = cancel
	s.done = done
	s.mu.Unlock()
	go s.run(runCtx, done)
}

// Stop ends the run and waits for it. The wait exists so a later Start is
// not closed by the run that is leaving. Stop without Start is safe, and a
// second Stop is safe.
func (s *Stream) Stop() {
	s.mu.Lock()
	cancel, sock, done := s.cancel, s.current, s.done
	s.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	if sock != nil {
		_ = sock.Close()
	}
	if done != nil {
		<-done
	}
	s.mu.Lock()
	if s.done == done {
		s.cancel = nil
		s.done = nil
		s.running = false
		if s.current == sock {
			s.current = nil
		}
	}
	s.mu.Unlock()
}

func (s *Stream) signal(sig Signal) {
	if s.onSignal != nil {
		s.onSignal(sig)
	}
}

func (s *Stream) setCurrent(sock port.Socket) {
	s.mu.Lock()
	s.current = sock
	s.mu.Unlock()
}

func (s *Stream) clearCurrent(sock port.Socket) {
	s.mu.Lock()
	if s.current == sock {
		s.current = nil
	}
	s.mu.Unlock()
}
