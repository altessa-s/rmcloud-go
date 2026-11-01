// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package discovery

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/altessa-s/go-atlas-core/time/clock"
	"github.com/altessa-s/go-atlas-core/types/redacted"

	"github.com/altessa-s/rmcloud-go/internal/adapter/httpapi"
	"github.com/altessa-s/rmcloud-go/internal/app/auth"
	"github.com/altessa-s/rmcloud-go/internal/app/notify"
	"github.com/altessa-s/rmcloud-go/internal/app/syncstore"
	"github.com/altessa-s/rmcloud-go/internal/domain"
	"github.com/altessa-s/rmcloud-go/internal/port"
)

var (
	_ syncstore.Hosts = (*Resolver)(nil)
	_ notify.Hosts    = (*Resolver)(nil)
	_ TokenSource     = (*auth.Provider)(nil)
)

const (
	demoteSentence = "the region host did not answer; falling back to the configured sync host"
	driftSentence  = `msg="discovery publishes a different auth host" host=`
	prodWebapp     = "webapp-prod.cloud.remarkable.engineering"
)

// url.URL.String has a pointer receiver, so a function result is not addressable.
func urlText(u url.URL) string { return u.String() }

// recTrip records discovery calls. Status 0 stays 0. The hook runs after
// the mutex is released.
type recTrip struct {
	mu     sync.Mutex
	saw    []port.Request
	calls  atomic.Int32
	status int
	body   string
	err    error
	hook   func()
}

func (c *recTrip) RoundTrip(_ context.Context, req port.Request) (port.Response, error) {
	c.calls.Add(1)
	c.mu.Lock()
	c.saw = append(c.saw, req)
	status := c.status
	body := c.body
	callErr := c.err
	hook := c.hook
	c.mu.Unlock()
	if hook != nil {
		hook()
	}
	if callErr != nil {
		return port.Response{}, callErr
	}
	return port.Response{Status: status, Body: []byte(body)}, nil
}

func (c *recTrip) last() port.Request {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.saw[len(c.saw)-1]
}

type tokenFunc func(context.Context) (string, error)

func (f tokenFunc) UserToken(ctx context.Context) (redacted.RedactedString, error) {
	token, err := f(ctx)
	return redacted.RedactedString(token), err
}

// switchTokens lets one resolver change the token between calls.
type switchTokens struct {
	mu    sync.Mutex
	token string
	err   error
	calls atomic.Int32
}

func (s *switchTokens) set(token string, err error) {
	s.mu.Lock()
	s.token = token
	s.err = err
	s.mu.Unlock()
}

func (s *switchTokens) UserToken(context.Context) (redacted.RedactedString, error) {
	s.calls.Add(1)
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.err != nil {
		return "", s.err
	}
	return redacted.RedactedString(s.token), nil
}

func mustTectonic(t *testing.T, region string) url.URL {
	t.Helper()
	u, ok := domain.Tectonic(region)
	if !ok {
		t.Fatal(region)
	}
	return u
}

func mustTrustedNotifications(t *testing.T, host string) url.URL {
	t.Helper()
	u, ok := domain.TrustedHost(host)
	if !ok {
		t.Fatal(host)
	}
	return domain.NotificationsOn(u)
}

func euToken() string { return testJWT(`{"tectonic":"EU"}`) }

func fixedClock() func() time.Time {
	return newClock(time.Unix(1_700_000_000, 0)).now
}

// blockTrip stays blocked until release is closed.
// It does not select on the request context: a canceled waiter must not
// unblock the only fetch.
type blockTrip struct {
	release chan struct{}
	entered chan struct{}
	once    sync.Once
	status  int
	body    string
	calls   atomic.Int32
}

func (b *blockTrip) RoundTrip(context.Context, port.Request) (port.Response, error) {
	b.calls.Add(1)
	b.once.Do(func() { close(b.entered) })
	timer := time.NewTimer(2 * time.Second)
	defer timer.Stop()
	select {
	case <-b.release:
	case <-timer.C:
		return port.Response{}, errors.New("blockTrip timed out")
	}
	return port.Response{Status: b.status, Body: []byte(b.body)}, nil
}

// lockHeld waits until the probe unlocks, so a free lock does not deadlock the caller.
func lockHeld(mu *sync.Mutex) bool {
	acquired := make(chan struct{})
	released := make(chan struct{})
	go func() {
		mu.Lock()
		close(acquired)
		mu.Unlock()
		close(released)
	}()
	wait := time.NewTimer(time.Second)
	select {
	case <-acquired:
		wait.Stop()
	case <-wait.C:
		return true
	}
	wait = time.NewTimer(time.Second)
	defer wait.Stop()
	select {
	case <-released:
		return false
	case <-wait.C:
		return true
	}
}

func TestResolverPinned(t *testing.T) {
	t.Parallel()
	cfg := domain.Production()
	r := NewResolver(cfg, clock.Real(), WithPinned(), WithTokens(&staticTokens{token: "x"}), WithDiscovery(boomTrip{}))
	if r.tokens != nil {
		t.Fatal("pinned kept a token source")
	}
	if r.clock == nil {
		t.Fatal("clock")
	}
	ctx := t.Context()
	if r.Notifications(ctx) != cfg.Notifications() {
		t.Fatal(urlText(r.Notifications(ctx)))
	}
	if r.Endpoints(ctx) != cfg {
		t.Fatal(r.Endpoints(ctx))
	}
	r.SyncHostFailed()
	if r.demoted {
		t.Fatal("pinned demoted")
	}

	plain := NewResolver(cfg, clock.Real(), WithTokens(nil), WithDiscovery(nil))
	if plain.tokens != nil || plain.trip != nil || plain.clock == nil {
		t.Fatal("nil arguments")
	}
}

func TestResolverRegion(t *testing.T) {
	t.Parallel()
	cfg := domain.Production()
	eu := euToken()
	src := &switchTokens{}
	src.set(eu, nil)
	r := NewResolver(cfg, clock.Real(), WithTokens(src), WithDiscovery(boomTrip{}))
	ctx := t.Context()

	got := r.Endpoints(ctx)
	if got.SyncHost.Hostname() != "eu.tectonic.remarkable.com" {
		t.Fatal(got.SyncHost.String())
	}
	if got.AuthHost != cfg.AuthHost {
		t.Fatal(got.AuthHost.String())
	}
	if !r.haveRegion || r.region != "eu" {
		t.Fatalf("region %q have %v", r.region, r.haveRegion)
	}

	src.set("", errors.New("no token"))
	if r.Endpoints(ctx) != cfg {
		t.Fatal(urlText(r.Endpoints(ctx).SyncHost))
	}
	if !r.haveRegion || r.region != "eu" {
		t.Fatalf("cached region %q", r.region)
	}

	src.set(eu, nil)
	r.mu.Lock()
	r.region = "nope!"
	r.mu.Unlock()
	if r.Endpoints(ctx) != cfg {
		t.Fatal(urlText(r.Endpoints(ctx).SyncHost))
	}
	if r.region != "nope!" {
		t.Fatal(r.region)
	}

	src.set(testJWT(`{"tectonic":"us"}`), nil)
	got = r.Endpoints(ctx)
	if got.SyncHost.Hostname() != "us.tectonic.remarkable.com" {
		t.Fatal(got.SyncHost.String())
	}

	num := NewResolver(cfg, clock.Real(), WithTokens(&staticTokens{token: testJWT(`{"tectonic":1}`)}), WithDiscovery(boomTrip{}))
	got = num.Endpoints(ctx)
	if got != cfg || !num.haveRegion || num.region != "" {
		t.Fatalf("region %q host %s", num.region, got.SyncHost.String())
	}

	bare := NewResolver(cfg, clock.Real(), WithDiscovery(boomTrip{}))
	if bare.Endpoints(ctx) != cfg {
		t.Fatal(urlText(bare.Endpoints(ctx).SyncHost))
	}
}

func TestResolverPublishedHost(t *testing.T) {
	t.Parallel()
	t.Run("notes and matching webapp", func(t *testing.T) {
		t.Parallel()
		cfg := domain.Production()
		body := `{"notifications":"eu.notes.remarkable.com","webapp":"` + prodWebapp + `"}`
		trip := &recTrip{status: 200, body: body}
		r := NewResolver(cfg, nowFunc(fixedClock()), WithTokens(&staticTokens{token: euToken()}), WithDiscovery(trip))
		got := r.Notifications(t.Context())
		want := mustTrustedNotifications(t, "eu.notes.remarkable.com")
		if got != want {
			t.Fatalf("got %s want %s", urlText(got), urlText(want))
		}
		req := trip.last()
		if req.URL.Hostname() != "eu.tectonic.remarkable.com" {
			t.Fatal(req.URL.String())
		}
		if req.URL.Path != "/discovery/v1/endpoints" {
			t.Fatal(req.URL.Path)
		}
		if req.Header != nil {
			t.Fatal(req.Header)
		}
		if req.Method != http.MethodGet {
			t.Fatal(req.Method)
		}
	})

	t.Run("mixed case configured keeps the published host", func(t *testing.T) {
		t.Parallel()
		cfg := domain.Endpoints{
			AuthHost: domain.Production().AuthHost,
			SyncHost: mustURL(t, "https://Internal.Cloud.Remarkable.com"),
		}
		trip := &recTrip{status: 200, body: `{"notifications":"internal.cloud.remarkable.com"}`}
		r := NewResolver(cfg, nowFunc(fixedClock()), WithDiscovery(trip))
		got := r.Notifications(t.Context())
		want := mustTrustedNotifications(t, "internal.cloud.remarkable.com")
		if got != want {
			t.Fatalf("got %s want %s", urlText(got), urlText(want))
		}
		if got == cfg.Notifications() {
			t.Fatal(got.String())
		}
	})

	t.Run("europe is not the eu region", func(t *testing.T) {
		t.Parallel()
		cfg := domain.Production()
		trip := &recTrip{status: 200, body: `{"notifications":"europe.notes.remarkable.com"}`}
		r := NewResolver(cfg, clock.Real(), WithTokens(&staticTokens{token: euToken()}), WithDiscovery(trip))
		got := r.Notifications(t.Context())
		want := domain.NotificationsOn(mustTectonic(t, "eu"))
		if got != want {
			t.Fatalf("got %s want %s", urlText(got), urlText(want))
		}
	})

	t.Run("empty document is cached", func(t *testing.T) {
		t.Parallel()
		cfg := domain.Production()
		trip := &recTrip{status: 200, body: `{}`}
		r := NewResolver(cfg, nowFunc(fixedClock()), WithDiscovery(trip))
		ctx := t.Context()
		if r.Notifications(ctx) != cfg.Notifications() {
			t.Fatal(urlText(r.Notifications(ctx)))
		}
		if !r.haveAttempt || r.attempt == nil || r.attempt.notifications != nil {
			t.Fatal("empty document was not stored")
		}
		r.Notifications(ctx)
		if trip.calls.Load() != 1 {
			t.Fatal(trip.calls.Load())
		}
	})
}

func TestResolverNegativeCache(t *testing.T) {
	t.Parallel()
	notes := `{"notifications":"eu.notes.remarkable.com"}`
	pad := notes + strings.Repeat(" ", domain.DiscoveryLimit+8-len(notes))
	cases := []struct {
		name   string
		status int
		body   string
	}{
		{name: "status zero", status: 0, body: notes},
		{name: "not found", status: http.StatusNotFound, body: notes},
		{name: "empty", status: http.StatusOK, body: ""},
		{name: "null", status: http.StatusOK, body: "null"},
		{name: "not strings", status: http.StatusOK, body: `{"notifications":1}`},
		{name: "duplicate key", status: http.StatusOK, body: `{"notifications":"eu.notes.remarkable.com","notifications":"internal.cloud.remarkable.com"}`},
		{name: "not UTF-8", status: http.StatusOK, body: "{\"notifications\":\"eu.notes.remarkable.com\xff\"}"},
		{name: "over limit", status: http.StatusOK, body: pad},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			cfg := domain.Production()
			trip := &recTrip{status: tc.status, body: tc.body}
			r := NewResolver(cfg, nowFunc(fixedClock()),
				WithTokens(&staticTokens{token: euToken()}),
				WithDiscovery(trip),
			)
			ctx := t.Context()
			got := r.Notifications(ctx)
			want := domain.NotificationsOn(mustTectonic(t, "eu"))
			if got != want {
				t.Fatalf("got %s want %s", urlText(got), urlText(want))
			}
			if !r.haveAttempt || r.attempt != nil {
				t.Fatalf("have %v attempt %v", r.haveAttempt, r.attempt)
			}
			r.Notifications(ctx)
			if trip.calls.Load() != 1 {
				t.Fatal(trip.calls.Load())
			}
		})
	}
}

func TestResolverCacheTTL(t *testing.T) {
	t.Parallel()
	start := time.Unix(1_700_000_000, 0)

	t.Run("timestamp is before the fetch", func(t *testing.T) {
		t.Parallel()
		clock := newClock(start)
		trip := &recTrip{status: http.StatusOK, body: `{}`}
		trip.hook = func() { clock.advance(domain.DiscoveryTTL + time.Second) }
		r := NewResolver(domain.Production(), nowFunc(clock.now), WithDiscovery(trip))
		ctx := t.Context()
		r.Notifications(ctx)
		r.Notifications(ctx)
		if trip.calls.Load() != 2 {
			t.Fatal(trip.calls.Load())
		}
	})

	t.Run("exactly ttl is stale", func(t *testing.T) {
		t.Parallel()
		clock := newClock(start)
		trip := &recTrip{status: http.StatusOK, body: `{}`}
		r := NewResolver(domain.Production(), nowFunc(clock.now), WithDiscovery(trip))
		ctx := t.Context()
		r.Notifications(ctx)
		clock.advance(domain.DiscoveryTTL)
		r.Notifications(ctx)
		if trip.calls.Load() != 2 {
			t.Fatal(trip.calls.Load())
		}
	})

	t.Run("just under ttl is fresh", func(t *testing.T) {
		t.Parallel()
		clock := newClock(start)
		trip := &recTrip{status: http.StatusOK, body: `{}`}
		r := NewResolver(domain.Production(), nowFunc(clock.now), WithDiscovery(trip))
		ctx := t.Context()
		r.Notifications(ctx)
		clock.advance(domain.DiscoveryTTL - time.Nanosecond)
		r.Notifications(ctx)
		if trip.calls.Load() != 1 {
			t.Fatal(trip.calls.Load())
		}
	})

	t.Run("now once before the second call", func(t *testing.T) {
		t.Parallel()
		clock := newClock(start)
		var calls atomic.Int32
		r := NewResolver(domain.Production(), nowFunc(func() time.Time {
			calls.Add(1)
			return clock.now()
		}),
			WithDiscovery(&recTrip{status: http.StatusOK, body: `{}`}),
		)
		r.Notifications(t.Context())
		if calls.Load() != 1 {
			t.Fatal(calls.Load())
		}
	})

	t.Run("endpoints does not read the clock", func(t *testing.T) {
		t.Parallel()
		var calls atomic.Int32
		r := NewResolver(domain.Production(), nowFunc(func() time.Time {
			calls.Add(1)
			return start
		}),
			WithDiscovery(boomTrip{}),
		)
		r.Endpoints(t.Context())
		if calls.Load() != 0 {
			t.Fatal(calls.Load())
		}
	})

	t.Run("stale cache and a transport error", func(t *testing.T) {
		t.Parallel()
		clock := newClock(start)
		notes := `{"notifications":"eu.notes.remarkable.com"}`
		trip := &recTrip{status: http.StatusOK, body: notes}
		r := NewResolver(domain.Production(), nowFunc(clock.now),
			WithTokens(&staticTokens{token: euToken()}),
			WithDiscovery(trip),
		)
		ctx := t.Context()
		first := r.Notifications(ctx)
		if first != mustTrustedNotifications(t, "eu.notes.remarkable.com") {
			t.Fatal(first.String())
		}
		old := r.attempt
		oldAt := r.attemptAt
		clock.advance(domain.DiscoveryTTL + time.Second)
		trip.mu.Lock()
		trip.err = errors.New("down")
		trip.mu.Unlock()
		got := r.Notifications(ctx)
		want := domain.NotificationsOn(mustTectonic(t, "eu"))
		if got != want {
			t.Fatalf("got %s want %s", urlText(got), urlText(want))
		}
		if r.attempt != old || !r.attemptAt.Equal(oldAt) {
			t.Fatal("stale cache was refreshed")
		}
		if trip.calls.Load() != 2 {
			t.Fatal(trip.calls.Load())
		}
	})
}

func TestResolverPrivateClient(t *testing.T) {
	t.Parallel()

	t.Run("success", func(t *testing.T) {
		t.Parallel()
		var hits atomic.Int32
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			hits.Add(1)
			if r.URL.Path != "/discovery/v1/endpoints" {
				t.Errorf("path %s", r.URL.Path)
			}
			if r.Header.Get("User-Agent") != "" {
				t.Errorf("user agent %q", r.Header.Get("User-Agent"))
			}
			// An empty User-Agent is omitted on the wire, so the default
			// agent must be absent rather than present as an empty value.
			if _, ok := r.Header["User-Agent"]; ok {
				t.Errorf("user-agent %v", r.Header["User-Agent"])
			}
			for name, vals := range r.Header {
				joined := name + " " + strings.Join(vals, ",")
				if strings.Contains(joined, "Go-http-client") {
					t.Errorf("default agent in %s", joined)
				}
			}
			_, _ = w.Write([]byte(`{}`))
		}))
		t.Cleanup(srv.Close)
		cfg := domain.Endpoints{
			AuthHost: domain.Production().AuthHost,
			SyncHost: mustURL(t, srv.URL),
		}
		r := NewResolver(cfg, nowFunc(fixedClock()), WithDiscovery(httpapi.NewDiscovery(nil, httpapi.WithoutSSRFProtection())))
		ctx := t.Context()
		if r.Notifications(ctx) != cfg.Notifications() {
			t.Fatal(urlText(r.Notifications(ctx)))
		}
		r.Notifications(ctx)
		if hits.Load() != 1 {
			t.Fatal(hits.Load())
		}
	})

	t.Run("huge body", func(t *testing.T) {
		t.Parallel()
		notes := `{"notifications":"eu.notes.remarkable.com"}`
		pad := notes + strings.Repeat(" ", domain.DiscoveryLimit+8-len(notes))
		var hits atomic.Int32
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			hits.Add(1)
			_, _ = w.Write([]byte(pad))
		}))
		t.Cleanup(srv.Close)
		cfg := domain.Endpoints{
			AuthHost: domain.Production().AuthHost,
			SyncHost: mustURL(t, srv.URL),
		}
		r := NewResolver(cfg, nowFunc(fixedClock()), WithDiscovery(httpapi.NewDiscovery(nil, httpapi.WithoutSSRFProtection())))
		ctx := t.Context()
		r.Notifications(ctx)
		if !r.haveAttempt || r.attempt != nil {
			t.Fatalf("have %v attempt %v", r.haveAttempt, r.attempt)
		}
		r.Notifications(ctx)
		if hits.Load() != 1 {
			t.Fatal(hits.Load())
		}
	})

	t.Run("short read is not cached", func(t *testing.T) {
		t.Parallel()
		var hits atomic.Int32
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			hits.Add(1)
			hj, ok := w.(http.Hijacker)
			if !ok {
				t.Error("no hijacker")
				return
			}
			conn, buf, err := hj.Hijack()
			if err != nil {
				t.Errorf("hijack: %v", err)
				return
			}
			defer func() { _ = conn.Close() }()
			if _, err := buf.WriteString("HTTP/1.1 200 OK\r\nContent-Length: 100\r\nConnection: close\r\n\r\nhello"); err != nil {
				t.Errorf("write: %v", err)
			}
			if err := buf.Flush(); err != nil {
				t.Errorf("flush: %v", err)
			}
		}))
		t.Cleanup(srv.Close)
		cfg := domain.Endpoints{
			AuthHost: domain.Production().AuthHost,
			SyncHost: mustURL(t, srv.URL),
		}
		r := NewResolver(cfg, nowFunc(fixedClock()), WithDiscovery(httpapi.NewDiscovery(nil, httpapi.WithoutSSRFProtection())))
		ctx := t.Context()
		r.Notifications(ctx)
		if r.haveAttempt {
			t.Fatal("stored a short read")
		}
		r.Notifications(ctx)
		if hits.Load() != 2 {
			t.Fatal(hits.Load())
		}
	})

	t.Run("redirect is not followed", func(t *testing.T) {
		t.Parallel()
		notes := `{"notifications":"eu.notes.remarkable.com"}`
		var hits1, hits2 atomic.Int32
		second := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			hits2.Add(1)
			_, _ = w.Write([]byte(notes))
		}))
		t.Cleanup(second.Close)
		first := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			hits1.Add(1)
			http.Redirect(w, r, second.URL+"/discovery/v1/endpoints", http.StatusFound)
		}))
		t.Cleanup(first.Close)
		cfg := domain.Endpoints{
			AuthHost: domain.Production().AuthHost,
			SyncHost: mustURL(t, first.URL),
		}
		r := NewResolver(cfg, nowFunc(fixedClock()), WithDiscovery(httpapi.NewDiscovery(nil, httpapi.WithoutSSRFProtection())))
		r.Notifications(t.Context())
		if hits1.Load() != 1 || hits2.Load() != 0 {
			t.Fatalf("first %d second %d", hits1.Load(), hits2.Load())
		}
		if !r.haveAttempt || r.attempt != nil {
			t.Fatalf("have %v attempt %v", r.haveAttempt, r.attempt)
		}
	})
}

func TestResolverSingleFlightCancel(t *testing.T) {
	log := captureLog(t)
	cfg := domain.Production()
	body := `{"notifications":"eu.notes.remarkable.com","webapp":"Other.Remarkable.Engineering"}`
	release := make(chan struct{})
	entered := make(chan struct{})
	trip := &blockTrip{release: release, entered: entered, status: http.StatusOK, body: body}
	tok := &staticTokens{token: euToken()}
	r := NewResolver(cfg, clock.Real(), WithTokens(tok), WithDiscovery(trip))
	ctx := t.Context()

	ownerDone := make(chan url.URL, 1)
	go func() {
		ownerDone <- r.Notifications(ctx)
	}()
	waitSignal(t, entered, "owner did not enter discovery")

	waiterDone := make(chan url.URL, 1)
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	go func() {
		waiterDone <- r.Notifications(cancelled)
	}()
	var waiter url.URL
	select {
	case waiter = <-waiterDone:
	case <-time.After(2 * time.Second):
		t.Fatal("waiter")
	}
	close(release)
	var owner url.URL
	select {
	case owner = <-ownerDone:
	case <-time.After(2 * time.Second):
		t.Fatal("owner")
	}

	wantWaiter := domain.NotificationsOn(mustTectonic(t, "eu"))
	wantOwner := mustTrustedNotifications(t, "eu.notes.remarkable.com")
	if waiter != wantWaiter {
		t.Fatalf("waiter %s", urlText(waiter))
	}
	if owner != wantOwner {
		t.Fatalf("owner %s", urlText(owner))
	}
	if r.Notifications(ctx) != wantOwner {
		t.Fatal(urlText(r.Notifications(ctx)))
	}
	if trip.calls.Load() != 1 {
		t.Fatal(trip.calls.Load())
	}
	if tok.calls.Load() != 3 {
		t.Fatal(tok.calls.Load())
	}
	if strings.Count(log.String(), driftSentence) != 1 {
		t.Fatal(log.String())
	}
}

func TestResolverDeadlines(t *testing.T) {
	t.Parallel()
	start := time.Unix(1_700_000_000, 0)

	t.Run("expired parent", func(t *testing.T) {
		t.Parallel()
		var calls atomic.Int32
		trip := portTripFunc(func(context.Context, port.Request) (port.Response, error) {
			calls.Add(1)
			return port.Response{Status: http.StatusOK, Body: []byte(`{}`)}, nil
		})
		r := NewResolver(domain.Production(), nowFunc(newClock(start).now), WithDiscovery(trip))
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		got := r.Notifications(ctx)
		if got != domain.Production().Notifications() {
			t.Fatal(got.String())
		}
		if calls.Load() != 0 {
			t.Fatal(calls.Load())
		}
	})

	t.Run("three second budget", func(t *testing.T) {
		t.Parallel()
		// The bubble's clock stands still until every goroutine blocks, so
		// the budget is exact and spending it takes no wall time.
		synctest.Test(t, func(t *testing.T) {
			var calls atomic.Int32
			trip := portTripFunc(func(ctx context.Context, _ port.Request) (port.Response, error) {
				calls.Add(1)
				deadline, ok := ctx.Deadline()
				if !ok {
					t.Error("no deadline")
					return port.Response{}, errors.New("no deadline")
				}
				if remain := time.Until(deadline); remain != 3*time.Second {
					t.Errorf("remain %s", remain)
				}
				<-ctx.Done()
				return port.Response{}, ctx.Err()
			})
			r := NewResolver(domain.Production(), nowFunc(newClock(start).now), WithDiscovery(trip))
			parent, cancel := context.WithTimeout(t.Context(), time.Minute)
			defer cancel()
			begun := time.Now()
			got := r.Notifications(parent)
			elapsed := time.Since(begun)
			if got != domain.Production().Notifications() {
				t.Fatal(got.String())
			}
			if calls.Load() != 1 {
				t.Fatal(calls.Load())
			}
			if elapsed != 3*time.Second {
				t.Fatal(elapsed)
			}
		})
	})

	t.Run("a short parent ends the call", func(t *testing.T) {
		t.Parallel()
		var calls atomic.Int32
		canceled := make(chan struct{})
		trip := portTripFunc(func(ctx context.Context, _ port.Request) (port.Response, error) {
			calls.Add(1)
			<-ctx.Done()
			close(canceled)
			return port.Response{}, ctx.Err()
		})
		r := NewResolver(domain.Production(), nowFunc(newClock(start).now), WithDiscovery(trip))
		parent, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
		defer cancel()
		if got := r.Notifications(parent); got != domain.Production().Notifications() {
			t.Fatal(got.String())
		}
		// The caller was the last one waiting, so its leaving cancels the call.
		waitSignal(t, canceled, "the call outlived its last caller")
		if calls.Load() != 1 {
			t.Fatal(calls.Load())
		}
	})

	t.Run("cancel does not poison", func(t *testing.T) {
		t.Parallel()
		var calls atomic.Int32
		trip := portTripFunc(func(context.Context, port.Request) (port.Response, error) {
			calls.Add(1)
			return port.Response{Status: http.StatusOK, Body: []byte(`{}`)}, nil
		})
		r := NewResolver(domain.Production(), nowFunc(newClock(start).now), WithDiscovery(trip))
		dead, cancel := context.WithCancel(t.Context())
		cancel()
		r.Notifications(dead)
		if calls.Load() != 0 {
			t.Fatal("cancel fetched")
		}
		if r.Notifications(t.Context()) != domain.Production().Notifications() {
			t.Fatal(urlText(r.Notifications(t.Context())))
		}
		r.Notifications(t.Context())
		if calls.Load() != 1 {
			t.Fatal(calls.Load())
		}
	})
}

func TestResolverDemote(t *testing.T) {
	log := captureLog(t)
	cfg := domain.Production()
	body := `{"notifications":"eu.notes.remarkable.com","webapp":"` + prodWebapp + `"}`
	trip := &recTrip{status: http.StatusOK, body: body}
	r := NewResolver(cfg, nowFunc(fixedClock()),
		WithTokens(&staticTokens{token: euToken()}),
		WithDiscovery(trip),
	)
	ctx := t.Context()
	first := r.Notifications(ctx)
	if first != mustTrustedNotifications(t, "eu.notes.remarkable.com") {
		t.Fatal(first.String())
	}
	disc := trip.last()
	if disc.URL.Hostname() != "eu.tectonic.remarkable.com" {
		t.Fatal(disc.URL.String())
	}
	r.SyncHostFailed()
	r.SyncHostFailed()
	if r.attempt != nil || r.haveAttempt || r.attemptAt.IsZero() || !r.haveRegion || r.region != "eu" {
		t.Fatalf("attempt %v have %v at %v region %q haveRegion %v", r.attempt, r.haveAttempt, r.attemptAt, r.region, r.haveRegion)
	}
	r.tokens = boomTokens{}
	if r.Endpoints(ctx) != cfg {
		t.Fatal(urlText(r.Endpoints(ctx).SyncHost))
	}
	second := r.Notifications(ctx)
	if second != cfg.Notifications() {
		t.Fatal(second.String())
	}
	if trip.calls.Load() != 2 {
		t.Fatal(trip.calls.Load())
	}
	again := trip.last()
	if again.URL.Hostname() != "internal.cloud.remarkable.com" {
		t.Fatal(again.URL.String())
	}

	b := NewResolver(cfg, clock.Real(), WithTokens(boomTokens{}), WithDiscovery(boomTrip{}))
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		b.SyncHostFailed()
	}()
	go func() {
		defer wg.Done()
		b.SyncHostFailed()
	}()
	wg.Wait()

	pinned := NewResolver(cfg, clock.Real(), WithPinned(), WithDiscovery(boomTrip{}))
	pinned.SyncHostFailed()
	if pinned.demoted {
		t.Fatal("pinned")
	}
	if strings.Count(log.String(), demoteSentence) != 2 {
		t.Fatal(log.String())
	}
	if strings.Contains(log.String(), driftSentence) {
		t.Fatal(log.String())
	}
}

func TestResolverDrift(t *testing.T) {
	log := captureLog(t)
	cfg := domain.Production()
	ctx := t.Context()
	clock := fixedClock()

	empty := NewResolver(cfg, nowFunc(clock), WithDiscovery(&recTrip{status: http.StatusOK, body: `{}`}))
	empty.Notifications(ctx)

	missing := NewResolver(cfg, nowFunc(clock),
		WithDiscovery(&recTrip{
			status: http.StatusNotFound,
			body:   `{"webapp":"Other.Remarkable.Engineering"}`,
		}),
	)
	missing.Notifications(ctx)

	match := NewResolver(cfg, nowFunc(clock),
		WithDiscovery(&recTrip{
			status: http.StatusOK,
			body:   `{"webapp":"` + prodWebapp + `"}`,
		}),
	)
	match.Notifications(ctx)

	trip := &recTrip{
		status: http.StatusOK,
		body:   `{"webapp":"Other.Remarkable.Engineering","notifications":"eu.notes.remarkable.com"}`,
	}
	one := NewResolver(cfg, nowFunc(clock), WithDiscovery(trip))
	one.Notifications(ctx)
	one.Notifications(ctx)
	if trip.calls.Load() != 1 {
		t.Fatal(trip.calls.Load())
	}
	auth := cfg.AuthHost.String()
	one.trip = boomTrip{}
	gotAuth := one.Endpoints(ctx).AuthHost
	if gotAuth.String() != auth {
		t.Fatal(gotAuth.String())
	}
	if strings.Count(log.String(), driftSentence) != 1 {
		t.Fatal(log.String())
	}
	if !strings.Contains(log.String(), "other.remarkable.engineering") {
		t.Fatal(log.String())
	}
}

func TestResolverLockNotHeld(t *testing.T) {
	t.Parallel()
	var r *Resolver
	var held atomic.Bool
	probe := func() {
		if lockHeld(&r.mu) {
			held.Store(true)
		}
	}
	tok := tokenFunc(func(context.Context) (string, error) {
		probe()
		return euToken(), nil
	})
	trip := portTripFunc(func(context.Context, port.Request) (port.Response, error) {
		probe()
		return port.Response{Status: http.StatusOK, Body: []byte(`{}`)}, nil
	})
	r = NewResolver(domain.Production(), clock.Real(), WithTokens(tok), WithDiscovery(trip))
	ctx := t.Context()
	done := make(chan struct{})
	go func() {
		r.Notifications(ctx)
		close(done)
	}()
	timer := time.NewTimer(3 * time.Second)
	defer timer.Stop()
	select {
	case <-done:
	case <-timer.C:
		t.Fatal("timed out")
	}
	if held.Load() {
		t.Fatal("lock held")
	}
}

func TestResolverRequiresAClock(t *testing.T) {
	t.Parallel()
	defer func() {
		if recover() == nil {
			t.Fatal("a nil clock did not panic")
		}
	}()
	NewResolver(domain.Production(), nil)
}
