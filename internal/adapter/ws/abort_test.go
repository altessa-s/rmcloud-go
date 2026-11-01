// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package ws

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/coder/websocket"
)

// peerServer accepts the socket. A silent peer never reads it, so no ping and
// no close frame is ever answered; an answering peer reads until the socket
// ends, which answers both. A hijacked connection outlives the request
// context, so the handler ends when the test does.
func peerServer(t *testing.T, answers bool) *httptest.Server {
	t.Helper()
	done := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
		if err != nil {
			return
		}
		defer func() { _ = conn.CloseNow() }()
		if !answers {
			<-done
			return
		}
		ctx, cancel := context.WithCancel(context.WithoutCancel(r.Context()))
		defer cancel()
		go func() {
			select {
			case <-done:
				cancel()
			case <-ctx.Done():
			}
		}()
		for {
			if _, _, err := conn.Read(ctx); err != nil {
				return
			}
		}
	}))
	t.Cleanup(srv.Close)
	// Cleanups run last-in first-out: the handlers end before the server.
	t.Cleanup(func() { close(done) })
	return srv
}

func TestAbort(t *testing.T) {
	t.Run("does not wait for a silent peer", func(t *testing.T) {
		t.Parallel()
		srv := peerServer(t, false)
		sock, err := Dialer{}.Dial(dialCtx(t), dialPath(t, srv.URL), nil)
		if err != nil {
			t.Fatal(err)
		}
		start := time.Now()
		if err := sock.Abort(); err != nil {
			t.Fatalf("abort: %v", err)
		}
		// A graceful close waits up to five seconds for the peer's close frame.
		if took := time.Since(start); took > time.Second {
			t.Fatalf("abort took %s", took)
		}
		if _, err := sock.Receive(dialCtx(t)); err == nil {
			t.Fatal("receive on an aborted socket succeeded")
		}
	})

	t.Run("repeats and mixes with close", func(t *testing.T) {
		t.Parallel()
		srv := peerServer(t, false)
		sock, err := Dialer{}.Dial(dialCtx(t), dialPath(t, srv.URL), nil)
		if err != nil {
			t.Fatal(err)
		}
		for i, step := range []func() error{sock.Abort, sock.Abort, sock.Close} {
			if err := step(); err != nil {
				t.Fatalf("step %d: %v", i, err)
			}
		}
	})

	t.Run("after close is a no-op", func(t *testing.T) {
		t.Parallel()
		srv := peerServer(t, true)
		sock, err := Dialer{}.Dial(dialCtx(t), dialPath(t, srv.URL), nil)
		if err != nil {
			t.Fatal(err)
		}
		if err := sock.Close(); err != nil {
			t.Fatalf("close: %v", err)
		}
		if err := sock.Abort(); err != nil {
			t.Fatalf("abort after close: %v", err)
		}
	})
}
