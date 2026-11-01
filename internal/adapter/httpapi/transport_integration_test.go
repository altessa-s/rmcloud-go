//go:build integration

// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

// Integration tests that run the transport against loopback TLS servers and
// write downloads to real temporary directories.

package httpapi_test

import (
	"bytes"
	"context"
	"crypto/x509"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/altessa-s/rmcloud-go/internal/adapter/httpapi"
	"github.com/altessa-s/rmcloud-go/internal/domain"
	"github.com/altessa-s/rmcloud-go/internal/port"
)

// itTLS starts a loopback TLS server and a transport that trusts it.
func itTLS(t *testing.T, h http.HandlerFunc) (*httptest.Server, *httpapi.Transport) {
	t.Helper()
	srv := httptest.NewTLSServer(h)
	t.Cleanup(srv.Close)
	roots := x509.NewCertPool()
	roots.AddCert(srv.Certificate())
	tr := httpapi.New(
		httpapi.WithRootCAs(roots),
		httpapi.WithoutSSRFProtection(),
		httpapi.WithLogger(slog.New(slog.DiscardHandler)),
	)
	t.Cleanup(tr.CloseIdleConnections)
	return srv, tr
}

func itURL(t *testing.T, raw, path string) url.URL {
	t.Helper()
	u, err := url.Parse(raw + path)
	if err != nil {
		t.Fatal(err)
	}
	return *u
}

func TestIntegration_TLSRoundTripUploadDownload(t *testing.T) {
	t.Parallel()

	payload := bytes.Repeat([]byte("0123456789abcdef"), 4096)
	var uploaded atomic.Value
	srv, tr := itTLS(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodPut:
			body, _ := io.ReadAll(r.Body)
			uploaded.Store(body)
			w.WriteHeader(http.StatusOK)
		default:
			if r.TLS == nil {
				http.Error(w, "plain", http.StatusBadRequest)
				return
			}
			w.Header().Set("X-Echo", r.Header.Get("X-Probe"))
			_, _ = w.Write(payload)
		}
	})
	ctx := t.Context()

	resp, err := tr.RoundTrip(ctx, port.Request{
		Method: http.MethodGet,
		URL:    itURL(t, srv.URL, "/memory"),
		Header: port.Header{"X-Probe": {"yes"}},
	})
	if err != nil || resp.Status != http.StatusOK || !bytes.Equal(resp.Body, payload) || resp.Header.Get("X-Echo") != "yes" {
		t.Fatalf("status %d len %d err %v", resp.Status, len(resp.Body), err)
	}

	dir := t.TempDir()
	var progress atomic.Int64
	resp, err = tr.Download(ctx, port.Request{
		Method:   http.MethodGet,
		URL:      itURL(t, srv.URL, "/file"),
		Dest:     dir,
		Expected: int64(len(payload)),
		Progress: func(done, _ int64) { progress.Store(done) },
	})
	if err != nil || resp.Status != http.StatusOK {
		t.Fatalf("status %d err %v", resp.Status, err)
	}
	if filepath.Dir(resp.File) != dir {
		t.Fatalf("the download landed in %s, not %s", resp.File, dir)
	}
	onDisk, err := os.ReadFile(resp.File)
	if err != nil || !bytes.Equal(onDisk, payload) || progress.Load() != int64(len(payload)) {
		t.Fatalf("len %d progress %d err %v", len(onDisk), progress.Load(), err)
	}
	if info, err := os.Stat(resp.File); err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("mode %v err %v", info.Mode(), err)
	}

	src := filepath.Join(dir, "upload.bin")
	if err := os.WriteFile(src, payload, 0o600); err != nil {
		t.Fatal(err)
	}
	resp, err = tr.Upload(ctx, port.Request{Method: http.MethodPut, URL: itURL(t, srv.URL, "/up"), File: src})
	if err != nil || resp.Status != http.StatusOK {
		t.Fatalf("status %d err %v", resp.Status, err)
	}
	if got, _ := uploaded.Load().([]byte); !bytes.Equal(got, payload) {
		t.Fatalf("the server received %d bytes", len(got))
	}
}

func TestIntegration_TLSUntrustedServerIsRefused(t *testing.T) {
	t.Parallel()

	var hits atomic.Int32
	srv := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { hits.Add(1) }))
	t.Cleanup(srv.Close)
	// An empty pool trusts nothing, the test certificate included.
	tr := httpapi.New(
		httpapi.WithRootCAs(x509.NewCertPool()),
		httpapi.WithoutSSRFProtection(),
		httpapi.WithLogger(slog.New(slog.DiscardHandler)),
	)
	t.Cleanup(tr.CloseIdleConnections)

	resp, err := tr.RoundTrip(t.Context(), port.Request{Method: http.MethodGet, URL: itURL(t, srv.URL, "/")})
	if resp.Status != 0 {
		t.Fatalf("status %d err %v", resp.Status, err)
	}
	requireSecureConnectionFailed(t, err)
	if hits.Load() != 0 {
		t.Fatal("a request reached a server the client does not trust")
	}
}

func TestIntegration_DownloadReplacesTheDestinationOnlyWhenWhole(t *testing.T) {
	t.Parallel()

	fresh := []byte("fresh contents")
	srv, tr := itTLS(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/big" {
			_, _ = w.Write(bytes.Repeat([]byte("z"), 1024))
			return
		}
		_, _ = w.Write(fresh)
	})
	dir := t.TempDir()
	dest := filepath.Join(dir, "page.rm")
	if err := os.WriteFile(dest, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}

	// A body past the ceiling leaves the file that was there, and no
	// partial file beside it.
	_, err := tr.Download(t.Context(), port.Request{
		Method: http.MethodGet, URL: itURL(t, srv.URL, "/big"), Dest: dest, Limit: 100,
	})
	var oversized *domain.OversizedResponseError
	if !errors.As(err, &oversized) || oversized.Limit != 100 {
		t.Fatalf("%v", err)
	}
	if got, _ := os.ReadFile(dest); string(got) != "old" {
		t.Fatalf("a failed download changed the destination to %q", got)
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 1 {
		t.Fatalf("leftovers in %s: %d entries", dir, len(entries))
	}

	resp, err := tr.Download(t.Context(), port.Request{Method: http.MethodGet, URL: itURL(t, srv.URL, "/ok"), Dest: dest})
	if err != nil || resp.File != dest {
		t.Fatalf("file %q err %v", resp.File, err)
	}
	if got, _ := os.ReadFile(dest); !bytes.Equal(got, fresh) {
		t.Fatalf("destination holds %q", got)
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 1 {
		t.Fatalf("leftovers in %s: %d entries", dir, len(entries))
	}
}

func TestIntegration_RedirectAcrossOriginsDropsTheToken(t *testing.T) {
	t.Parallel()

	var otherAuth atomic.Value
	otherAuth.Store("unset")
	other := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		otherAuth.Store(r.Header.Get("Authorization"))
		http.Redirect(w, r, r.URL.Query().Get("back"), http.StatusFound)
	}))
	t.Cleanup(other.Close)

	var finalAuth, sameAuth atomic.Value
	var srvURL string
	srv, tr := itTLS(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/start-same":
			http.Redirect(w, r, "/same", http.StatusTemporaryRedirect)
		case "/same":
			sameAuth.Store(r.Header.Get("Authorization"))
			_, _ = io.WriteString(w, "same")
		case "/start-away":
			http.Redirect(w, r, other.URL+"/away?back="+url.QueryEscape(srvURL+"/home"), http.StatusFound)
		case "/home":
			finalAuth.Store(r.Header.Get("Authorization"))
			_, _ = io.WriteString(w, "home")
		}
	})
	srvURL = srv.URL
	header := port.Header{"Authorization": {"Bearer secret"}}

	resp, err := tr.RoundTrip(t.Context(), port.Request{Method: http.MethodGet, URL: itURL(t, srv.URL, "/start-same"), Header: header})
	if err != nil || string(resp.Body) != "same" || sameAuth.Load() != "Bearer secret" {
		t.Fatalf("%q %v auth %v", resp.Body, err, sameAuth.Load())
	}

	resp, err = tr.RoundTrip(t.Context(), port.Request{Method: http.MethodGet, URL: itURL(t, srv.URL, "/start-away"), Header: header})
	if err != nil || string(resp.Body) != "home" {
		t.Fatalf("%q %v", resp.Body, err)
	}
	if otherAuth.Load() != "" {
		t.Fatalf("the token reached another origin: %q", otherAuth.Load())
	}
	if finalAuth.Load() != "Bearer secret" {
		t.Fatalf("the token was not restored on the way back: %q", finalAuth.Load())
	}
}

func TestIntegration_RedirectLoopStops(t *testing.T) {
	t.Parallel()

	var hits atomic.Int32
	srv, tr := itTLS(t, func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		http.Redirect(w, r, r.URL.Path, http.StatusFound)
	})
	_, err := tr.RoundTrip(t.Context(), port.Request{Method: http.MethodGet, URL: itURL(t, srv.URL, "/loop")})
	if err == nil {
		t.Fatal("a redirect loop ended in a response")
	}
	if n := hits.Load(); n != 10 {
		t.Fatalf("%d requests, want the 10-hop stop", n)
	}
}

func TestIntegration_CancelStopsAStalledDownload(t *testing.T) {
	t.Parallel()

	started := make(chan struct{})
	srv, tr := itTLS(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "1048576")
		_, _ = w.Write([]byte("first bytes"))
		w.(http.Flusher).Flush()
		close(started)
		<-r.Context().Done()
	})
	dir := t.TempDir()
	ctx, cancel := context.WithCancel(t.Context())
	errs := make(chan error, 1)
	go func() {
		_, err := tr.Download(ctx, port.Request{Method: http.MethodGet, URL: itURL(t, srv.URL, "/stall"), Dest: dir})
		errs <- err
	}()
	<-started
	cancel()
	select {
	case err := <-errs:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("%v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("a canceled download kept reading")
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		t.Fatalf("a canceled download left %d files", len(entries))
	}
}

func TestIntegration_DiscoveryOverTLS(t *testing.T) {
	t.Parallel()

	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "" {
			http.Error(w, "discovery is unauthenticated", http.StatusBadRequest)
			return
		}
		_, _ = io.WriteString(w, `{"hosts":[]}`)
	}))
	t.Cleanup(srv.Close)
	roots := x509.NewCertPool()
	roots.AddCert(srv.Certificate())
	d := httpapi.NewDiscovery(nil, httpapi.WithRootCAs(roots), httpapi.WithoutSSRFProtection())

	resp, err := d.RoundTrip(t.Context(), port.Request{Method: http.MethodGet, URL: itURL(t, srv.URL, "/discovery/v1/endpoints")})
	if err != nil || resp.Status != http.StatusOK || string(resp.Body) != `{"hosts":[]}` {
		t.Fatalf("status %d body %q err %v", resp.Status, resp.Body, err)
	}

	untrusted := httpapi.NewDiscovery(nil, httpapi.WithRootCAs(x509.NewCertPool()), httpapi.WithoutSSRFProtection())
	_, err = untrusted.RoundTrip(t.Context(), port.Request{Method: http.MethodGet, URL: itURL(t, srv.URL, "/")})
	requireSecureConnectionFailed(t, err)
}

// requireSecureConnectionFailed fails unless err is the protocol's name for a
// refused certificate, which the retry policy does not repeat.
func requireSecureConnectionFailed(t *testing.T, err error) {
	t.Helper()
	got, ok := errors.AsType[*domain.TransportError](err)
	if !ok || got.Reason != domain.ReasonSecureConnectionFailed {
		t.Fatalf("got %v (%T), want a transport error with reason %s", err, err, domain.ReasonSecureConnectionFailed)
	}
}
