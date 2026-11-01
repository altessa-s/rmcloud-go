// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package ws

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/altessa-s/rmcloud-go/internal/domain"
	"github.com/altessa-s/rmcloud-go/internal/port"
)

func mustURL(t *testing.T, raw string) url.URL {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	return *u
}

func dialPath(t *testing.T, raw string) url.URL {
	t.Helper()
	u := mustURL(t, raw)
	u.Path = "/notifications/ws/json/1"
	return u
}

func dialCtx(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	t.Cleanup(cancel)
	return ctx
}

func assertHTTP(t *testing.T, err error, status int, path string) {
	t.Helper()
	got, ok := errors.AsType[*domain.HTTPError](err)
	if !ok {
		t.Fatalf("got %v", err)
	}
	if got.Status != status || got.Path != path {
		t.Fatalf("status %d path %q", got.Status, got.Path)
	}
}

func TestDial(t *testing.T) {
	t.Run("frames", func(t *testing.T) {
		t.Parallel()
		var hdr atomic.Value
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/notifications/ws/json/1" {
				http.NotFound(w, r)
				return
			}
			hdr.Store(r.Header.Clone())
			conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
			if err != nil {
				return
			}
			go func(ctx context.Context) {
				for {
					if _, _, err := conn.Read(ctx); err != nil {
						return
					}
				}
			}(context.WithoutCancel(r.Context()))
			_ = conn.Write(r.Context(), websocket.MessageText, []byte("hello"))
			_ = conn.Write(r.Context(), websocket.MessageBinary, []byte{1, 2, 3})
		}))
		t.Cleanup(srv.Close)

		ctx := dialCtx(t)
		sock, err := Dialer{}.Dial(ctx, dialPath(t, srv.URL), port.Header{
			"User-Agent":    []string{"desktop/3.28.0 (macos 1)"},
			"Authorization": []string{"Bearer secret"},
		})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = sock.Close() })

		got, _ := hdr.Load().(http.Header)
		if got.Get("User-Agent") != "desktop/3.28.0 (macos 1)" || got.Get("Authorization") != "Bearer secret" {
			t.Fatalf("headers %v", got)
		}
		frame, err := sock.Receive(ctx)
		if err != nil || !frame.Textual || frame.Text != "hello" {
			t.Fatalf("frame %+v err %v", frame, err)
		}
		frame, err = sock.Receive(ctx)
		if err != nil || frame.Textual || frame.Text != "" || !bytes.Equal(frame.Binary, []byte{1, 2, 3}) {
			t.Fatalf("frame %+v err %v", frame, err)
		}
		// Ping waits for a pong that only a concurrent Read observes.
		readDone := make(chan struct{})
		go func() {
			_, _ = sock.Receive(context.Background())
			close(readDone)
		}()
		if err := sock.Ping(ctx); err != nil {
			t.Fatal(err)
		}
		if err := sock.Close(); err != nil {
			t.Fatal(err)
		}
		if err := sock.Close(); err != nil {
			t.Fatal(err)
		}
		select {
		case <-readDone:
		case <-time.After(5 * time.Second):
			t.Fatal("read did not return")
		}
	})

	t.Run("omits default user-agent when absent", func(t *testing.T) {
		t.Parallel()
		var sawUA atomic.Bool
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if _, ok := r.Header["User-Agent"]; ok {
				sawUA.Store(true)
			}
			conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
			if err != nil {
				return
			}
			_ = conn.Close(websocket.StatusNormalClosure, "")
		}))
		t.Cleanup(srv.Close)

		sock, err := Dialer{}.Dial(dialCtx(t), dialPath(t, srv.URL), port.Header{
			"Authorization": []string{"Bearer secret"},
		})
		if err != nil {
			t.Fatal(err)
		}
		_ = sock.Close()
		if sawUA.Load() {
			t.Fatal("upgrade sent a User-Agent header when none was set")
		}
	})

	t.Run("unauthorized", func(t *testing.T) {
		t.Parallel()
		for _, status := range []int{http.StatusUnauthorized, http.StatusForbidden} {
			t.Run(http.StatusText(status), func(t *testing.T) {
				t.Parallel()
				srv := statusServer(t, status, nil)
				sock, err := Dialer{}.Dial(dialCtx(t), dialPath(t, srv.URL), nil)
				if sock != nil {
					_ = sock.Close()
					t.Fatal("socket")
				}
				if !errors.Is(err, domain.ErrUnauthorized) {
					t.Fatal(err)
				}
			})
		}
	})

	t.Run("http", func(t *testing.T) {
		t.Parallel()
		for _, status := range []int{http.StatusInternalServerError, http.StatusNotFound} {
			t.Run(http.StatusText(status), func(t *testing.T) {
				t.Parallel()
				srv := statusServer(t, status, nil)
				sock, err := Dialer{}.Dial(dialCtx(t), dialPath(t, srv.URL), nil)
				if sock != nil {
					_ = sock.Close()
					t.Fatal("socket")
				}
				assertHTTP(t, err, status, "/notifications/ws/json/1")
			})
		}
	})

	t.Run("rate limited", func(t *testing.T) {
		t.Parallel()
		srv := statusServer(t, http.StatusTooManyRequests, http.Header{"Retry-After": []string{"1.5"}})
		sock, err := Dialer{}.Dial(dialCtx(t), dialPath(t, srv.URL), nil)
		if sock != nil {
			_ = sock.Close()
			t.Fatal("socket")
		}
		got, ok := errors.AsType[*domain.RateLimitedError](err)
		if !ok || !got.HasRetryAfter || got.RetryAfter != 1500*time.Millisecond {
			t.Fatal(err)
		}
	})

	t.Run("ok is not a socket", func(t *testing.T) {
		t.Parallel()
		srv := statusServer(t, http.StatusOK, nil)
		sock, err := Dialer{}.Dial(dialCtx(t), dialPath(t, srv.URL), nil)
		if sock != nil {
			_ = sock.Close()
			t.Fatal("socket")
		}
		if err == nil {
			t.Fatal("nil error")
		}
		if _, ok := domain.KindOf(err); ok {
			t.Fatalf("domain error %v", err)
		}
	})

	t.Run("redirect", func(t *testing.T) {
		t.Parallel()
		var hits atomic.Int32
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch r.URL.Path {
			case "/notifications/ws/json/1":
				http.Redirect(w, r, "/elsewhere", http.StatusFound)
			case "/elsewhere":
				hits.Add(1)
				w.WriteHeader(http.StatusOK)
			default:
				http.NotFound(w, r)
			}
		}))
		t.Cleanup(srv.Close)
		sock, err := Dialer{}.Dial(dialCtx(t), dialPath(t, srv.URL), nil)
		if sock != nil {
			_ = sock.Close()
			t.Fatal("socket")
		}
		if hits.Load() != 0 {
			t.Fatalf("followed redirect %d", hits.Load())
		}
		assertHTTP(t, err, http.StatusFound, "/notifications/ws/json/1")
	})

	t.Run("refused", func(t *testing.T) {
		t.Parallel()
		srv := httptest.NewServer(http.NotFoundHandler())
		closed := srv.URL
		srv.Close()
		sock, err := Dialer{}.Dial(dialCtx(t), dialPath(t, closed), nil)
		if sock != nil {
			t.Fatal("socket on a closed port")
		}
		if !domain.IsKind(err, domain.KindTransport) {
			t.Fatalf("got %v (%T), want a transport error", err, err)
		}
	})

	t.Run("scheme", func(t *testing.T) {
		t.Parallel()
		cases := []url.URL{
			{Scheme: "http", Host: "Example.COM", Path: "/notifications/ws/json/1"},
			{Scheme: "HTTP", Host: "Example.COM", Path: "/notifications/ws/json/1"},
			{Scheme: "https", Host: "Example.COM", Path: "/notifications/ws/json/1"},
			{Host: "Example.COM", Path: "/notifications/ws/json/1"},
		}
		want := []string{"ws", "ws", "wss", ""}
		for i, in := range cases {
			got := wireURL(in)
			if got.Scheme != want[i] || got.Host != "Example.COM" || got.Path != "/notifications/ws/json/1" {
				t.Fatalf("%+v -> %+v", in, got)
			}
		}
	})
}

func statusServer(t *testing.T, status int, header http.Header) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/notifications/ws/json/1" {
			http.NotFound(w, r)
			return
		}
		for name, values := range header {
			for _, value := range values {
				w.Header().Add(name, value)
			}
		}
		w.WriteHeader(status)
		_, _ = w.Write([]byte("no"))
	}))
	t.Cleanup(srv.Close)
	return srv
}
