//go:build integration

// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

// Integration tests that dial real WebSocket servers on the loopback, over
// TLS where the notification socket runs in production.

package ws_test

import (
	"context"
	"crypto/x509"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/altessa-s/rmcloud-go/internal/adapter/httpapi"
	"github.com/altessa-s/rmcloud-go/internal/adapter/ws"
	"github.com/altessa-s/rmcloud-go/internal/domain"
	"github.com/altessa-s/rmcloud-go/internal/port"
)

const itSlack = 5 * time.Second

// itSocketServer starts a TLS server that upgrades every request and hands
// the connection to serve. serve owns the connection until it returns.
func itSocketServer(t *testing.T, serve func(ctx context.Context, conn *websocket.Conn, r *http.Request)) (*httptest.Server, ws.Dialer) {
	t.Helper()
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer func() { _ = conn.CloseNow() }()
		serve(r.Context(), conn, r)
	}))
	t.Cleanup(srv.Close)
	roots := x509.NewCertPool()
	roots.AddCert(srv.Certificate())
	transport := httpapi.Pooled(roots)
	t.Cleanup(transport.CloseIdleConnections)
	return srv, ws.Dialer{Transport: transport}
}

func itURL(t *testing.T, raw string) url.URL {
	t.Helper()
	u, err := url.Parse(raw + "/notifications/ws/json/1")
	if err != nil {
		t.Fatal(err)
	}
	return *u
}

func TestIntegration_DialOverTLSCarriesFramesAndPings(t *testing.T) {
	t.Parallel()

	var auth, ua atomic.Value
	release := make(chan struct{})
	srv, dialer := itSocketServer(t, func(ctx context.Context, conn *websocket.Conn, r *http.Request) {
		auth.Store(r.Header.Get("Authorization"))
		ua.Store(r.Header.Get("User-Agent"))
		_ = conn.Write(ctx, websocket.MessageText, []byte(`{"event":"one"}`))
		_ = conn.Write(ctx, websocket.MessageBinary, []byte{1, 2, 3})
		// Reading answers the client's pings and sees its close.
		readDone := make(chan struct{})
		go func() {
			defer close(readDone)
			for {
				if _, _, err := conn.Read(ctx); err != nil {
					return
				}
			}
		}()
		select {
		case <-readDone:
		case <-release:
		}
	})
	defer close(release)

	sock, err := dialer.Dial(t.Context(), itURL(t, srv.URL), port.Header{
		"Authorization": {"Bearer user"},
		"User-Agent":    {"desktop/3.28.0 (macos 14.5)"},
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), itSlack)
	defer cancel()
	text, err := sock.Receive(ctx)
	if err != nil || !text.Textual || text.Text != `{"event":"one"}` {
		t.Fatalf("%#v %v", text, err)
	}
	bin, err := sock.Receive(ctx)
	if err != nil || bin.Textual || string(bin.Binary) != "\x01\x02\x03" {
		t.Fatalf("%#v %v", bin, err)
	}
	// A pong is read by a Receive in progress, as the notification stream
	// keeps one while it pings.
	received := make(chan error, 1)
	go func() {
		_, err := sock.Receive(ctx)
		received <- err
	}()
	if err := sock.Ping(ctx); err != nil {
		t.Fatalf("ping: %v", err)
	}
	if auth.Load() != "Bearer user" || ua.Load() != "desktop/3.28.0 (macos 14.5)" {
		t.Fatalf("auth %v ua %v", auth.Load(), ua.Load())
	}
	if err := sock.Close(); err != nil {
		t.Fatal(err)
	}
	if err := <-received; err == nil {
		t.Fatal("a closed socket produced a frame")
	}
	if err := sock.Close(); err != nil {
		t.Fatalf("a second close: %v", err)
	}
}

func TestIntegration_DialWithoutUserAgentSendsNone(t *testing.T) {
	t.Parallel()

	var sawUA atomic.Bool
	srv, dialer := itSocketServer(t, func(_ context.Context, _ *websocket.Conn, r *http.Request) {
		_, ok := r.Header["User-Agent"]
		sawUA.Store(ok)
	})
	sock, err := dialer.Dial(t.Context(), itURL(t, srv.URL), port.Header{"Authorization": {"Bearer user"}})
	if err != nil {
		t.Fatal(err)
	}
	_ = sock.Close()
	if sawUA.Load() {
		t.Fatal("a Go default User-Agent went out")
	}
}

func TestIntegration_ServerCloseEndsReceive(t *testing.T) {
	t.Parallel()

	srv, dialer := itSocketServer(t, func(_ context.Context, conn *websocket.Conn, _ *http.Request) {
		_ = conn.Close(websocket.StatusGoingAway, "maintenance")
	})
	sock, err := dialer.Dial(t.Context(), itURL(t, srv.URL), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sock.Close() })
	ctx, cancel := context.WithTimeout(t.Context(), itSlack)
	defer cancel()
	_, err = sock.Receive(ctx)
	if websocket.CloseStatus(err) != websocket.StatusGoingAway {
		t.Fatalf("%v", err)
	}
}

func TestIntegration_ReceiveHonorsCancellation(t *testing.T) {
	t.Parallel()

	release := make(chan struct{})
	srv, dialer := itSocketServer(t, func(context.Context, *websocket.Conn, *http.Request) { <-release })
	defer close(release)
	sock, err := dialer.Dial(t.Context(), itURL(t, srv.URL), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sock.Close() })

	const wait = 100 * time.Millisecond
	ctx, cancel := context.WithTimeout(t.Context(), wait)
	defer cancel()
	start := time.Now()
	if _, err := sock.Receive(ctx); err == nil {
		t.Fatal("a silent socket produced a frame")
	}
	if elapsed := time.Since(start); elapsed < wait || elapsed > wait+itSlack {
		t.Fatalf("returned after %s", elapsed)
	}
}

func TestIntegration_RefusedUpgradeIsAProtocolError(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		status int
		check  func(error) bool
	}{
		{name: "unauthorized", status: http.StatusUnauthorized, check: func(err error) bool { return errors.Is(err, domain.ErrUnauthorized) }},
		{name: "rate limited", status: http.StatusTooManyRequests, check: func(err error) bool {
			limited, ok := errors.AsType[*domain.RateLimitedError](err)
			return ok && limited.HasRetryAfter && limited.RetryAfter == 3*time.Second
		}},
		{name: "server error", status: http.StatusBadGateway, check: func(err error) bool {
			status, ok := errors.AsType[*domain.HTTPError](err)
			return ok && status.Status == http.StatusBadGateway
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Retry-After", "3")
				w.WriteHeader(tt.status)
			}))
			t.Cleanup(srv.Close)
			roots := x509.NewCertPool()
			roots.AddCert(srv.Certificate())
			transport := httpapi.Pooled(roots)
			t.Cleanup(transport.CloseIdleConnections)

			_, err := ws.Dialer{Transport: transport}.Dial(t.Context(), itURL(t, srv.URL), nil)
			if !tt.check(err) {
				t.Fatalf("%v", err)
			}
		})
	}
}

func TestIntegration_RedirectIsNotFollowed(t *testing.T) {
	t.Parallel()

	var followed atomic.Bool
	target := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { followed.Store(true) }))
	t.Cleanup(target.Close)
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL, http.StatusFound)
	}))
	t.Cleanup(srv.Close)
	roots := x509.NewCertPool()
	roots.AddCert(srv.Certificate())
	transport := httpapi.Pooled(roots)
	t.Cleanup(transport.CloseIdleConnections)

	_, err := ws.Dialer{Transport: transport}.Dial(t.Context(), itURL(t, srv.URL), port.Header{"Authorization": {"Bearer user"}})
	if err == nil {
		t.Fatal("a redirect opened a socket")
	}
	if followed.Load() {
		t.Fatal("the upgrade followed a redirect with the token")
	}
}

func TestIntegration_ClosedPortCannotConnect(t *testing.T) {
	t.Parallel()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	_ = ln.Close()

	u := url.URL{Scheme: "https", Host: addr, Path: "/notifications/ws/json/1"}
	_, err = ws.Dialer{Transport: httpapi.Pooled(nil)}.Dial(t.Context(), u, nil)
	transport, ok := errors.AsType[*domain.TransportError](err)
	if !ok || transport.Reason != domain.ReasonCannotConnectToHost {
		t.Fatalf("%v", err)
	}
}
