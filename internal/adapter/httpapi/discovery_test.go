// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package httpapi

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/altessa-s/rmcloud-go/internal/domain"
	"github.com/altessa-s/rmcloud-go/internal/domaintest"
	"github.com/altessa-s/rmcloud-go/internal/port"
)

func TestDiscoverySendsNothingOfItsOwn(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, ok := r.Header["User-Agent"]; ok {
			t.Errorf("user agent %v", r.Header["User-Agent"])
		}
		if r.Header.Get("Authorization") != "" {
			t.Error("authorization sent")
		}
		_, _ = w.Write([]byte(`{"notifications":"eu.notes.remarkable.com"}`))
	}))
	t.Cleanup(srv.Close)
	resp, err := NewDiscovery(nil, WithoutSSRFProtection()).RoundTrip(t.Context(), port.Request{URL: mustURL(t, srv.URL)})
	must(t, err)
	if resp.Status != http.StatusOK || !strings.Contains(string(resp.Body), "eu.notes") {
		t.Fatalf("%d %q", resp.Status, resp.Body)
	}
}

func TestDiscoveryDoesNotFollowARedirect(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "https://elsewhere.example/", http.StatusFound)
	}))
	t.Cleanup(srv.Close)
	resp, err := NewDiscovery(srv.Client().Transport).RoundTrip(t.Context(), port.Request{URL: mustURL(t, srv.URL)})
	must(t, err)
	if resp.Status != http.StatusFound {
		t.Fatalf("status %d", resp.Status)
	}
}

func TestDiscoveryReadsOneBytePastTheLimit(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(strings.Repeat(" ", domain.DiscoveryLimit+100)))
	}))
	t.Cleanup(srv.Close)
	resp, err := NewDiscovery(nil, WithoutSSRFProtection()).RoundTrip(t.Context(), port.Request{URL: mustURL(t, srv.URL)})
	must(t, err)
	if len(resp.Body) != domain.DiscoveryLimit+1 {
		t.Fatalf("body %d", len(resp.Body))
	}
}

func TestDiscoveryClassifiesAFailure(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.NotFoundHandler())
	u := mustURL(t, srv.URL)
	srv.Close()
	_, err := NewDiscovery(nil, WithoutSSRFProtection()).RoundTrip(t.Context(), port.Request{URL: u})
	if !domaintest.SameError(err, domain.Transport(domain.ReasonCannotConnectToHost)) {
		t.Fatalf("%v", err)
	}
}
