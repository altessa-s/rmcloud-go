// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package wiring_test

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/altessa-s/rmcloud-go/internal/adapter/fs"
	"github.com/altessa-s/rmcloud-go/internal/domain"
	"github.com/altessa-s/rmcloud-go/internal/port"
	"github.com/altessa-s/rmcloud-go/internal/porttest"
	"github.com/altessa-s/rmcloud-go/internal/wiring"
)

const testUA = "desktop/3.28.0 (macos 1)"

// syncServer answers the v4 root read and counts what it served.
type syncServer struct {
	*httptest.Server

	hits atomic.Int32
	mu   sync.Mutex
	uas  []string
}

func newSyncServer(t *testing.T) *syncServer {
	t.Helper()
	s := &syncServer{}
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.hits.Add(1)
		s.mu.Lock()
		s.uas = append(s.uas, r.Header.Get("User-Agent"))
		s.mu.Unlock()
		switch r.URL.Path {
		case "/sync/v4/root":
			_, _ = io.WriteString(w, `{"hash":"abc","generation":3}`)
		case "/notifications/ws/json/1":
			conn, err := websocket.Accept(w, r, nil)
			if err != nil {
				return
			}
			// Hold the socket until the client closes it.
			_, _, _ = conn.Read(context.WithoutCancel(r.Context()))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(s.Close)
	return s
}

func (s *syncServer) userAgents() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.uas...)
}

func (s *syncServer) endpoints(t *testing.T) domain.Endpoints {
	t.Helper()
	u := mustURL(t, s.URL)
	return domain.Endpoints{AuthHost: u, SyncHost: u}
}

// pinnedHosts is a resolver that sends everything to one URL.
type pinnedHosts struct{ to url.URL }

func (p pinnedHosts) Endpoints(context.Context) domain.Endpoints {
	return domain.Endpoints{AuthHost: p.to, SyncHost: p.to}
}

func (pinnedHosts) SyncHostFailed() {}

func (p pinnedHosts) Notifications(context.Context) url.URL { return p.to }

// countingTripper counts the requests that pass through it.
type countingTripper struct {
	calls atomic.Int32
}

func (c *countingTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	c.calls.Add(1)
	return http.DefaultTransport.RoundTrip(req)
}

func TestBuildReturnsEveryPart(t *testing.T) {
	cases := map[string]func(t *testing.T, srv *syncServer) wiring.Config{
		"production with discovery": func(*testing.T, *syncServer) wiring.Config {
			return wiring.Config{Endpoints: domain.Production()}
		},
		"production with a resolver": func(t *testing.T, srv *syncServer) wiring.Config {
			return wiring.Config{Endpoints: domain.Production(), Resolver: pinnedHosts{to: mustURL(t, srv.URL)}}
		},
		"pinned pair": func(t *testing.T, srv *syncServer) wiring.Config {
			return wiring.Config{Endpoints: srv.endpoints(t)}
		},
		"host round tripper": func(t *testing.T, srv *syncServer) wiring.Config {
			return wiring.Config{Endpoints: srv.endpoints(t), RoundTripper: &countingTripper{}}
		},
	}
	for name, config := range cases {
		t.Run(name, func(t *testing.T) {
			cfg := config(t, newSyncServer(t))
			cfg.Secrets = porttest.NewSecrets(nil)
			cfg.Logger = slog.New(slog.DiscardHandler)
			parts := wiring.Build(cfg)
			if parts.Store == nil || parts.Account == nil || parts.Stream == nil || parts.Transport == nil {
				t.Fatalf("%+v", parts)
			}
			// Build does no I/O: nothing to stop, nothing idle to close.
			parts.Stream.Stop()
			parts.Transport.CloseIdleConnections()
		})
	}
}

func TestBuildPinnedPairReachesLoopback(t *testing.T) {
	srv := newSyncServer(t)
	parts := wiring.Build(wiring.Config{
		Secrets:   signedIn(t),
		Endpoints: srv.endpoints(t),
		UserAgent: testUA,
		Logger:    slog.New(slog.DiscardHandler),
	})
	t.Cleanup(parts.Transport.CloseIdleConnections)

	root, err := parts.Store.Root(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if root.Hash != "abc" || root.Generation != 3 {
		t.Fatalf("%+v", root)
	}
	if uas := srv.userAgents(); len(uas) != 1 || uas[0] != testUA {
		t.Fatalf("user agents %q", uas)
	}
}

func TestBuildResolverCarriesSyncOnProduction(t *testing.T) {
	srv := newSyncServer(t)
	parts := wiring.Build(wiring.Config{
		Secrets:   signedIn(t),
		Endpoints: domain.Production(),
		Resolver:  pinnedHosts{to: mustURL(t, srv.URL)},
		Logger:    slog.New(slog.DiscardHandler),
	})
	t.Cleanup(parts.Transport.CloseIdleConnections)

	// A resolver turns discovery off, and with it the SSRF guard, so the
	// loopback server it names is reachable.
	if _, err := parts.Store.Root(t.Context()); err != nil {
		t.Fatal(err)
	}
	if srv.hits.Load() != 1 {
		t.Fatalf("hits %d", srv.hits.Load())
	}
}

// TestBuildSSRFGuard drives the transport directly: the guard is on only
// for the production pair with no resolver and no host round tripper.
func TestBuildSSRFGuard(t *testing.T) {
	cases := []struct {
		name    string
		config  func(t *testing.T, srv *syncServer) wiring.Config
		refused bool
	}{
		{
			name: "production with discovery refuses loopback",
			config: func(*testing.T, *syncServer) wiring.Config {
				return wiring.Config{Endpoints: domain.Production()}
			},
			refused: true,
		},
		{
			name: "production with a resolver allows loopback",
			config: func(t *testing.T, srv *syncServer) wiring.Config {
				return wiring.Config{Endpoints: domain.Production(), Resolver: pinnedHosts{to: mustURL(t, srv.URL)}}
			},
		},
		{
			name: "production with a host round tripper allows loopback",
			config: func(*testing.T, *syncServer) wiring.Config {
				return wiring.Config{Endpoints: domain.Production(), RoundTripper: &countingTripper{}}
			},
		},
		{
			name: "pinned pair allows loopback",
			config: func(t *testing.T, srv *syncServer) wiring.Config {
				return wiring.Config{Endpoints: srv.endpoints(t)}
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := newSyncServer(t)
			cfg := tc.config(t, srv)
			cfg.Secrets = porttest.NewSecrets(nil)
			cfg.Logger = slog.New(slog.DiscardHandler)
			parts := wiring.Build(cfg)
			t.Cleanup(parts.Transport.CloseIdleConnections)

			trip, ok := parts.Transport.(port.RoundTripper)
			if !ok {
				t.Fatalf("transport %T is not a round tripper", parts.Transport)
			}
			u := mustURL(t, srv.URL+"/sync/v4/root")
			resp, err := trip.RoundTrip(t.Context(), port.Request{Method: http.MethodGet, URL: u})
			if tc.refused {
				if err == nil || srv.hits.Load() != 0 {
					t.Fatalf("status %d err %v hits %d", resp.Status, err, srv.hits.Load())
				}
				return
			}
			if err != nil || resp.Status != http.StatusOK || srv.hits.Load() != 1 {
				t.Fatalf("status %d err %v hits %d", resp.Status, err, srv.hits.Load())
			}
		})
	}
}

func TestBuildHostRoundTripperCarriesEveryCall(t *testing.T) {
	srv := newSyncServer(t)
	rt := &countingTripper{}
	signals := make(chan port.Signal, 4)
	parts := wiring.Build(wiring.Config{
		Secrets:      signedIn(t),
		Endpoints:    srv.endpoints(t),
		RoundTripper: rt,
		Logger:       slog.New(slog.DiscardHandler),
		OnSignal:     func(sig port.Signal) { signals <- sig },
	})
	t.Cleanup(parts.Transport.CloseIdleConnections)

	if _, err := parts.Store.Root(t.Context()); err != nil {
		t.Fatal(err)
	}
	parts.Stream.Start(t.Context())
	t.Cleanup(parts.Stream.Stop)
	if sig := waitSignal(t, signals); sig.Kind != port.SignalConnected {
		t.Fatalf("%+v", sig)
	}
	// The root read and the notification upgrade.
	if rt.calls.Load() != 2 {
		t.Fatalf("calls through the round tripper = %d", rt.calls.Load())
	}
}

func TestBuildPooledSocketWithoutRoundTripper(t *testing.T) {
	srv := newSyncServer(t)
	signals := make(chan port.Signal, 4)
	parts := wiring.Build(wiring.Config{
		Secrets:   signedIn(t),
		Endpoints: srv.endpoints(t),
		Logger:    slog.New(slog.DiscardHandler),
		OnSignal:  func(sig port.Signal) { signals <- sig },
	})
	t.Cleanup(parts.Transport.CloseIdleConnections)

	parts.Stream.Start(t.Context())
	t.Cleanup(parts.Stream.Stop)
	if sig := waitSignal(t, signals); sig.Kind != port.SignalConnected {
		t.Fatalf("%+v", sig)
	}
}

func TestBuildReadsTheWireLogSwitch(t *testing.T) {
	cases := map[string]bool{"": false, "off": false, "headers": true, "bodies": true}
	for value, logged := range cases {
		t.Run("RMCLOUD_WIRE_LOG="+value, func(t *testing.T) {
			t.Setenv("RMCLOUD_WIRE_LOG", value)
			srv := newSyncServer(t)
			var buf bytes.Buffer
			parts := wiring.Build(wiring.Config{
				Secrets:   signedIn(t),
				Endpoints: srv.endpoints(t),
				Logger:    slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})),
			})
			t.Cleanup(parts.Transport.CloseIdleConnections)
			if _, err := parts.Store.Root(t.Context()); err != nil {
				t.Fatal(err)
			}
			if got := strings.Contains(buf.String(), "/sync/v4/root"); got != logged {
				t.Fatalf("wire logged %v, want %v: %s", got, logged, buf.String())
			}
		})
	}
}

func TestFilesIsTheLocalFilesystem(t *testing.T) {
	if _, ok := wiring.Files().(fs.Files); !ok {
		t.Fatalf("%T", wiring.Files())
	}
}

func TestDefaultUserAgentFollowsTheHostVersion(t *testing.T) {
	want := ""
	if version := fs.HostVersion(); version != "" {
		want = domain.UserAgent(version)
	}
	if got := wiring.DefaultUserAgent(); got != want {
		t.Fatalf("%q, want %q", got, want)
	}
}

// signedIn holds a user token valid for a day, so sync calls need no
// token exchange.
func signedIn(t *testing.T) *porttest.Secrets {
	t.Helper()
	claims, err := json.Marshal(map[string]any{
		"exp":       time.Now().Add(24 * time.Hour).Unix(),
		"level":     "connect",
		"device-id": "device-1",
		"scopes":    "sync:full",
	})
	if err != nil {
		t.Fatal(err)
	}
	enc := base64.RawURLEncoding.EncodeToString
	token := enc([]byte(`{"alg":"none"}`)) + "." + enc(claims) + "." + enc([]byte("sig"))
	return porttest.NewSecrets(map[domain.SecretName]string{domain.SecretUserToken: token})
}

func waitSignal(t *testing.T, signals <-chan port.Signal) port.Signal {
	t.Helper()
	select {
	case sig := <-signals:
		return sig
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for a signal")
	}
	return port.Signal{}
}

func mustURL(t *testing.T, raw string) url.URL {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	return *u
}
