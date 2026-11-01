// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package discovery

import (
	"bytes"
	"context"
	"encoding/base64"
	"log/slog"
	"net/url"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/altessa-s/go-atlas-core/types/redacted"

	"github.com/altessa-s/rmcloud-go/internal/port"
)

// captureLog installs a debug handler for the rest of the test.
// These tests do not call t.Parallel: the default logger is process-wide.
func captureLog(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(prev) })
	return &buf
}

func mustURL(t *testing.T, raw string) url.URL {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	return *u
}

func testJWT(payload string) string {
	return "h." + base64.RawURLEncoding.EncodeToString([]byte(payload)) + ".s"
}

type testClock struct {
	mu sync.Mutex
	at time.Time
}

func newClock(at time.Time) *testClock { return &testClock{at: at} }

func (c *testClock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.at
}

func (c *testClock) advance(d time.Duration) {
	c.mu.Lock()
	c.at = c.at.Add(d)
	c.mu.Unlock()
}

type staticTokens struct {
	token string
	calls atomic.Int32
}

func (s *staticTokens) UserToken(context.Context) (redacted.RedactedString, error) {
	s.calls.Add(1)
	return redacted.RedactedString(s.token), nil
}

type boomTokens struct{}

func (boomTokens) UserToken(context.Context) (redacted.RedactedString, error) {
	panic("token source called")
}

type boomTrip struct{}

func (boomTrip) RoundTrip(context.Context, port.Request) (port.Response, error) {
	panic("discovery called")
}

func waitSignal(t *testing.T, ch <-chan struct{}, what string) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(2 * time.Second):
		t.Fatal(what)
	}
}

type portTripFunc func(context.Context, port.Request) (port.Response, error)

func (f portTripFunc) RoundTrip(ctx context.Context, req port.Request) (port.Response, error) {
	return f(ctx, req)
}

// nowFunc is a clock read from a function. The resolver never sleeps.
type nowFunc func() time.Time

func (f nowFunc) Now() time.Time { return f() }

func (nowFunc) Sleep(ctx context.Context, _ time.Duration) error { return ctx.Err() }
