//go:build integration

// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

// Integration tests that run the notification stream with the real socket
// dialer and the system clock against a loopback WebSocket server, with ping
// timings short enough to watch the keepalive work.

package notify_test

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/altessa-s/go-atlas-core/time/clock"
	"github.com/altessa-s/go-atlas-core/types/redacted"

	"github.com/altessa-s/rmcloud-go/internal/adapter/ws"
	"github.com/altessa-s/rmcloud-go/internal/app/notify"
	"github.com/altessa-s/rmcloud-go/internal/port"
)

const (
	itSlack        = 5 * time.Second
	itPingInterval = 50 * time.Millisecond
	itPingTimeout  = 200 * time.Millisecond
	// abortSlack bounds how late an aborted socket may be reported dropped.
	// It is well below the five seconds a graceful close waits for a silent
	// peer, so a drop that waits for the handshake fails the test.
	abortSlack = 2 * time.Second
)

type itHost struct{ u url.URL }

func (h itHost) Notifications(context.Context) url.URL { return h.u }

type itTokens struct{ refused atomic.Int32 }

func (*itTokens) UserToken(context.Context) (redacted.RedactedString, error) {
	return redacted.RedactedString("user-token"), nil
}

func (t *itTokens) InvalidateUserToken(redacted.RedactedString) { t.refused.Add(1) }

// itStream starts a stream against a server whose sockets are handed to
// serve, and returns its signals. The stream is stopped at cleanup.
func itStream(t *testing.T, pings *atomic.Int32, serve func(ctx context.Context, conn *websocket.Conn)) <-chan port.Signal {
	t.Helper()
	// A hijacked connection's request context is not canceled when the
	// client goes, so serve watches this one, canceled at cleanup.
	live, stop := context.WithCancel(context.Background())
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{
			OnPingReceived: func(context.Context, []byte) bool {
				pings.Add(1)
				return true
			},
		})
		if err != nil {
			return
		}
		defer func() { _ = conn.CloseNow() }()
		serve(live, conn)
	}))
	t.Cleanup(srv.Close)
	t.Cleanup(stop)
	u, err := url.Parse(srv.URL + "/notifications/ws/json/1")
	if err != nil {
		t.Fatal(err)
	}

	signals := make(chan port.Signal, 64)
	stream := notify.New(itHost{u: *u}, &itTokens{}, ws.Dialer{}, clock.Real(),
		func(s port.Signal) {
			select {
			case signals <- s:
			default:
			}
		},
		notify.WithPingInterval(itPingInterval),
		notify.WithPingTimeout(itPingTimeout),
		notify.WithLogger(slog.New(slog.DiscardHandler)),
	)
	stream.Start(t.Context())
	t.Cleanup(stream.Stop)
	return signals
}

func waitSignal(t *testing.T, signals <-chan port.Signal, kind port.SignalKind) time.Time {
	t.Helper()
	return waitSignalWithin(t, signals, kind, itSlack)
}

func waitSignalWithin(t *testing.T, signals <-chan port.Signal, kind port.SignalKind, within time.Duration) time.Time {
	t.Helper()
	deadline := time.After(within)
	for {
		select {
		case s := <-signals:
			if s.Kind == kind {
				return time.Now()
			}
		case <-deadline:
			t.Fatalf("no signal %d within %s", kind, within)
			return time.Time{}
		}
	}
}

func TestIntegration_KeepAliveHoldsAnAnsweringSocket(t *testing.T) {
	t.Parallel()

	const wantPings = 4
	var pings atomic.Int32
	reached := make(chan struct{})
	signals := itStream(t, &pings, func(ctx context.Context, conn *websocket.Conn) {
		// Reading is what answers the pings.
		for {
			if _, _, err := conn.Read(ctx); err != nil {
				return
			}
		}
	})
	connected := waitSignal(t, signals, port.SignalConnected)

	go func() {
		for pings.Load() < wantPings {
			time.Sleep(itPingInterval / 5)
		}
		close(reached)
	}()
	select {
	case <-reached:
	case <-time.After(itSlack):
		t.Fatalf("%d pings within %s", pings.Load(), itSlack)
	}
	if elapsed := time.Since(connected); elapsed < wantPings*itPingInterval {
		t.Fatalf("%d pings after %s: faster than the interval", wantPings, elapsed)
	}
	select {
	case s := <-signals:
		t.Fatalf("an answering socket signaled %d (%v)", s.Kind, s.Err)
	default:
	}
}

func TestIntegration_KeepAliveDropsASilentSocket(t *testing.T) {
	t.Parallel()

	var pings atomic.Int32
	// The server never reads, so no ping is ever answered.
	signals := itStream(t, &pings, func(ctx context.Context, _ *websocket.Conn) { <-ctx.Done() })
	connected := waitSignal(t, signals, port.SignalConnected)
	dropped := waitSignalWithin(t, signals, port.SignalDropped, itSlack)

	// The socket is aborted, so the drop does not wait for a close handshake
	// the silent peer would never complete.
	least := itPingInterval + itPingTimeout
	if gap := dropped.Sub(connected); gap < least || gap > least+abortSlack {
		t.Fatalf("dropped %s after connecting, want between %s and %s", gap, least, least+abortSlack)
	}
	if pings.Load() != 0 {
		t.Fatalf("a server that never reads saw %d pings", pings.Load())
	}
}
