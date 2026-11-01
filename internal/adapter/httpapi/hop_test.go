// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package httpapi

import (
	"bytes"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/altessa-s/rmcloud-go/internal/port"
)

func TestSetAuthorization(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name  string
		in    http.Header
		token string
		want  http.Header
	}{
		{"replaces every spelling", http.Header{"authorization": {"a"}, "AUTHORIZATION": {"b"}, "X": {"x"}}, "Bearer c",
			http.Header{"Authorization": {"Bearer c"}, "X": {"x"}}},
		{"empty token drops it", http.Header{"Authorization": {"a"}}, "", http.Header{}},
		{"adds to a header without one", http.Header{}, "Bearer c", http.Header{"Authorization": {"Bearer c"}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			setAuthorization(tc.in, tc.token)
			if !equalHeader(tc.in, tc.want) {
				t.Fatalf("%v, want %v", tc.in, tc.want)
			}
		})
	}
	setAuthorization(nil, "Bearer c") // a nil header is left alone, not a panic
}

func TestPreviousURL(t *testing.T) {
	t.Parallel()
	first := mustParse(t, "https://a.example/one")
	last := mustParse(t, "https://b.example/two")
	withResponse := &http.Request{Response: &http.Response{Request: &http.Request{URL: first}}}
	cases := []struct {
		name string
		req  *http.Request
		via  []*http.Request
		want *url.URL
	}{
		{"response request wins", withResponse, []*http.Request{{URL: last}}, first},
		{"last hop of via", &http.Request{}, []*http.Request{{URL: first}, {URL: last}}, last},
		{"nil last hop", &http.Request{}, []*http.Request{nil}, nil},
		{"no history", nil, nil, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := previousURL(tc.req, tc.via); got != tc.want {
				t.Fatalf("%v, want %v", got, tc.want)
			}
		})
	}
}

func TestSameOriginAndPort(t *testing.T) {
	t.Parallel()
	cases := []struct {
		a, b string
		same bool
	}{
		{"https://h.example/x", "https://H.EXAMPLE:443/y", true},
		{"http://h.example/x", "http://h.example:80/y", true},
		{"https://h.example/x", "http://h.example/x", false},
		{"https://h.example/x", "https://g.example/x", false},
		{"https://h.example:8443/x", "https://h.example/x", false},
		{"wss://h.example/x", "wss://h.example:443/x", false},
	}
	for _, tc := range cases {
		if got := sameOrigin(mustParse(t, tc.a), mustParse(t, tc.b)); got != tc.same {
			t.Errorf("sameOrigin(%s, %s) = %v", tc.a, tc.b, got)
		}
	}
	if sameOrigin(nil, mustParse(t, "https://h.example")) || sameOrigin(mustParse(t, "https://h.example"), nil) {
		t.Error("a nil URL has an origin")
	}
	if portOf(nil) != "" {
		t.Error("a nil URL has a port")
	}
}

func TestFinalHop(t *testing.T) {
	t.Parallel()
	snap := mustParse(t, "https://h.example/asked")
	moved := mustParse(t, "https://h.example/moved")
	cases := []struct {
		name       string
		resp       *http.Response
		wantURL    string
		wantMethod string
	}{
		{"no response", nil, snap.String(), http.MethodPost},
		{"response without a request", &http.Response{}, snap.String(), http.MethodPost},
		{"request without a URL keeps the snapshot", &http.Response{Request: &http.Request{Method: http.MethodGet}}, snap.String(), http.MethodGet},
		{"redirected", &http.Response{Request: &http.Request{Method: http.MethodGet, URL: moved}}, moved.String(), http.MethodGet},
		{"empty method keeps the asked one", &http.Response{Request: &http.Request{URL: moved}}, moved.String(), http.MethodPost},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			u, method := finalHop(tc.resp, snap, http.MethodPost)
			if u.String() != tc.wantURL || method != tc.wantMethod {
				t.Fatalf("%s %s, want %s %s", method, u, tc.wantMethod, tc.wantURL)
			}
		})
	}
}

func TestSuppressUserAgent(t *testing.T) {
	t.Parallel()
	h := http.Header{}
	suppressUserAgent(h)
	if v, ok := h["User-Agent"]; !ok || !slices.Equal(v, []string{""}) {
		t.Fatalf("%v", h)
	}
	set := http.Header{"user-agent": {"mine"}}
	suppressUserAgent(set)
	if len(set) != 1 || !slices.Equal(set["user-agent"], []string{"mine"}) {
		t.Fatalf("%v", set)
	}
	suppressUserAgent(nil) // a nil header is left alone, not a panic
}

func TestDownloadRefusesADestItCannotOpen(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("body"))
	}))
	t.Cleanup(srv.Close)
	tr := New(WithoutSSRFProtection(), WithLogger(slog.New(slog.DiscardHandler)))
	t.Cleanup(tr.CloseIdleConnections)

	file := filepath.Join(t.TempDir(), "file")
	must(t, os.WriteFile(file, nil, 0o600))
	cases := map[string]string{
		// Stat fails with something other than "not there".
		"under a regular file": filepath.Join(file, "child"),
		// The temporary file beside it cannot be created.
		"in a missing directory": filepath.Join(t.TempDir(), "missing", "child"),
	}
	for name, dest := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			_, err := tr.Download(t.Context(), port.Request{URL: *mustParse(t, srv.URL), Dest: dest, Expected: -1})
			if err == nil {
				t.Fatal("download into an impossible dest succeeded")
			}
		})
	}
}

func TestLoggerOptionReceivesTheWireLog(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("ok"))
	}))
	t.Cleanup(srv.Close)
	var buf bytes.Buffer
	tr := New(WithoutSSRFProtection(), WithLevel(LevelHeaders), WithLogger(nil),
		WithLogger(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))))
	t.Cleanup(tr.CloseIdleConnections)

	resp, err := tr.RoundTrip(t.Context(), port.Request{URL: *mustParse(t, srv.URL+"/path")})
	must(t, err)
	if resp.Status != http.StatusOK || string(resp.Body) != "ok" {
		t.Fatalf("%d %q", resp.Status, resp.Body)
	}
	if !strings.Contains(buf.String(), "/path") {
		t.Fatalf("log = %s", buf.String())
	}
}

func equalHeader(a, b http.Header) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if !slices.Equal(v, b[k]) {
			return false
		}
	}
	return true
}

func mustParse(t *testing.T, raw string) *url.URL {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	return u
}
