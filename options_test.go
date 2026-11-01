// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package rmcloud_test

import (
	"bytes"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/altessa-s/rmcloud-go"
)

// countingTripper counts the requests that pass through it.
type countingTripper struct {
	calls atomic.Int32
	next  http.RoundTripper
}

func (c *countingTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	c.calls.Add(1)
	return c.next.RoundTrip(req)
}

func TestRoundTripperCarriesTheCalls(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("device-token"))
	}))
	t.Cleanup(srv.Close)
	rt := &countingTripper{next: http.DefaultTransport}
	c, err := rmcloud.New(newMemStore(), rmcloud.WithEndpoints(loopback(t, srv.URL)),
		rmcloud.WithRoundTripper(rt),
		rmcloud.WithRoundTripper(nil),
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close() })
	if err := c.RegisterDevice(t.Context(), "abcdefgh"); err != nil {
		t.Fatal(err)
	}
	if rt.calls.Load() != 1 {
		t.Fatalf("calls through the round tripper = %d", rt.calls.Load())
	}
}

func TestLoggerReceivesTheRecords(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("device-token"))
	}))
	t.Cleanup(srv.Close)
	var buf bytes.Buffer
	c, err := rmcloud.New(newMemStore(), rmcloud.WithEndpoints(loopback(t, srv.URL)),
		rmcloud.WithLogger(slog.New(slog.NewTextHandler(&buf, nil))),
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close() })
	if err := c.RegisterDevice(t.Context(), "abcdefgh"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), "device registered") {
		t.Fatalf("log = %s", buf.String())
	}
}

func TestCloseIsSafeTwiceAndWithoutStart(t *testing.T) {
	c, err := rmcloud.New(newMemStore())
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestFileHash(t *testing.T) {
	path := filepath.Join(t.TempDir(), "blob")
	if err := os.WriteFile(path, []byte("abc"), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := rmcloud.FileHash(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	if got != rmcloud.BlobHash([]byte("abc")) {
		t.Fatalf("hash = %s", got)
	}
	if _, err := rmcloud.FileHash(t.Context(), filepath.Join(t.TempDir(), "missing")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("missing file: %v", err)
	}
}

func TestFromEnvironmentReportsARefusal(t *testing.T) {
	e, err := rmcloud.FromEnvironment(map[string]string{
		"RMCLOUD_AUTH_HOST": "http://example.com",
		"RMCLOUD_SYNC_HOST": "https://sync.example",
	})
	if err == nil || !strings.Contains(err.Error(), "RMCLOUD_AUTH_HOST") {
		t.Fatalf("err = %v", err)
	}
	if e.AuthHost.Host != rmcloud.Production().AuthHost.Host || e.SyncHost.Host != "sync.example" {
		t.Fatalf("endpoints = %s %s", e.AuthHost.Host, e.SyncHost.Host)
	}
}

func TestNotificationKindNames(t *testing.T) {
	for kind, want := range map[rmcloud.NotificationKind]string{
		rmcloud.NotificationConnected:  "connected",
		rmcloud.NotificationEvent:      "event",
		rmcloud.NotificationUnreadable: "unreadable",
		rmcloud.NotificationDropped:    "dropped",
		0:                              "unknown",
	} {
		if kind.String() != want {
			t.Errorf("%d = %s, want %s", kind, kind, want)
		}
	}
}
