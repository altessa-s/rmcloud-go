// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package httpapi

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"testing"

	"github.com/altessa-s/rmcloud-go/internal/domain"
	"github.com/altessa-s/rmcloud-go/internal/port"
)

// benchBodySizes are a metadata answer and a page blob.
var benchBodySizes = []int{1 << 10, 1 << 20}

// Sinks keep the compiler from discarding the benchmarked calls.
var (
	sinkN    int64
	sinkResp port.Response
	sinkText string
)

// benchServer is a loopback server that answers every GET with a body of
// size bytes and drains every PUT. Its connections are closed when b ends,
// so no keep-alive goroutine outlives the benchmark.
func benchServer(b *testing.B, tr *Transport, size int) url.URL {
	b.Helper()
	body := bytes.Repeat([]byte("x"), size)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPut {
			_, _ = io.Copy(io.Discard, r.Body)
			w.WriteHeader(http.StatusOK)
			return
		}
		w.Header().Set("Content-Type", "application/octet-stream")
		_, _ = w.Write(body)
	}))
	b.Cleanup(func() {
		tr.CloseIdleConnections()
		srv.Close()
	})
	u, err := url.Parse(srv.URL + "/sync/v3/files/abc")
	if err != nil {
		b.Fatal(err)
	}
	return *u
}

func BenchmarkCopyLimited(b *testing.B) {
	for _, size := range []int{64 << 10, 8 << 20} {
		src := bytes.Repeat([]byte("x"), size)
		b.Run(fmt.Sprintf("bytes=%d", size), func(b *testing.B) {
			r := bytes.NewReader(src)
			var progress int64
			b.ReportAllocs()
			b.SetBytes(int64(size))
			for b.Loop() {
				r.Reset(src)
				n, err := copyLimited(io.Discard, r, int64(size), func(n int64) { progress = n })
				if err != nil {
					b.Fatal(err)
				}
				sinkN = n + progress
			}
		})
	}
}

func BenchmarkWire(b *testing.B) {
	u := &url.URL{Scheme: "https", Host: "eu.tectonic.remarkable.com", Path: "/sync/v3/root", RawQuery: "x=1"}
	header := http.Header{
		"Authorization": {"Bearer eyJhbGciOiJub25lIn0.eyJleHAiOjE4OTM0NTYwMDB9.c2ln"},
		"Content-Type":  {"application/json"},
		"User-Agent":    {"desktop/3.28.0 (macos 14.5)"},
	}
	body := []byte(`{"hash":"` + domain.BlobHash([]byte("root")) + `","generation":1700000000000000}`)
	for _, level := range []Level{LevelOff, LevelHeaders, LevelBodies} {
		b.Run(level.String(), func(b *testing.B) {
			w := &wire{level: level, writer: io.Discard}
			b.ReportAllocs()
			for b.Loop() {
				w.sent(http.MethodPut, u, header, body, false)
				w.received(http.StatusOK, http.MethodPut, u, header, body, false, int64(len(body)))
			}
		})
	}
}

func BenchmarkMaskSecret(b *testing.B) {
	token := "Bearer eyJhbGciOiJub25lIn0.eyJleHAiOjE4OTM0NTYwMDB9.c2ln"
	b.ReportAllocs()
	for b.Loop() {
		sinkText = maskSecret(token)
	}
}

// BenchmarkRoundTripInMemory measures the transport over a round tripper
// that answers at once: request building, the go-atlas-net client and
// reading the body, without a socket.
func BenchmarkRoundTripInMemory(b *testing.B) {
	for _, size := range benchBodySizes {
		body := string(bytes.Repeat([]byte("x"), size))
		tr := testTransport(nil, WithRoundTripper(roundTripFunc(func(r *http.Request) (*http.Response, error) {
			return httpResp(r, http.StatusOK, nil, body), nil
		})))
		u := url.URL{Scheme: "https", Host: "eu.tectonic.remarkable.com", Path: "/sync/v3/files/abc"}
		b.Run(fmt.Sprintf("bytes=%d", size), func(b *testing.B) {
			ctx := b.Context()
			req := port.Request{Method: http.MethodGet, URL: u, Header: port.Header{"Authorization": {"Bearer t"}}}
			b.ReportAllocs()
			b.SetBytes(int64(size))
			for b.Loop() {
				resp, err := tr.RoundTrip(ctx, req)
				if err != nil {
					b.Fatal(err)
				}
				sinkResp = resp
			}
		})
	}
}

func BenchmarkRoundTripLoopback(b *testing.B) {
	for _, size := range benchBodySizes {
		b.Run(fmt.Sprintf("bytes=%d", size), func(b *testing.B) {
			tr := testTransport(nil)
			u := benchServer(b, tr, size)
			ctx := b.Context()
			req := port.Request{Method: http.MethodGet, URL: u, Header: port.Header{"Authorization": {"Bearer t"}}}
			b.ReportAllocs()
			b.SetBytes(int64(size))
			for b.Loop() {
				resp, err := tr.RoundTrip(ctx, req)
				if err != nil || resp.Status != http.StatusOK {
					b.Fatal(resp.Status, err)
				}
				sinkResp = resp
			}
		})
	}
}

func BenchmarkDownloadLoopback(b *testing.B) {
	size := benchBodySizes[len(benchBodySizes)-1]
	tr := testTransport(nil)
	u := benchServer(b, tr, size)
	dest := filepath.Join(b.TempDir(), "blob")
	ctx := b.Context()
	req := port.Request{Method: http.MethodGet, URL: u, Dest: dest, Expected: int64(size)}
	b.ReportAllocs()
	b.SetBytes(int64(size))
	for b.Loop() {
		resp, err := tr.Download(ctx, req)
		if err != nil || resp.Status != http.StatusOK {
			b.Fatal(resp.Status, err)
		}
		sinkResp = resp
	}
}

func BenchmarkUploadLoopback(b *testing.B) {
	size := benchBodySizes[len(benchBodySizes)-1]
	tr := testTransport(nil)
	u := benchServer(b, tr, 0)
	file := filepath.Join(b.TempDir(), "page.rm")
	if err := os.WriteFile(file, bytes.Repeat([]byte("x"), size), 0o600); err != nil {
		b.Fatal(err)
	}
	ctx := b.Context()
	for _, tc := range []struct {
		name string
		req  port.Request
	}{
		{"memory", port.Request{Method: http.MethodPut, URL: u, Body: bytes.Repeat([]byte("x"), size)}},
		{"file", port.Request{Method: http.MethodPut, URL: u, File: file}},
	} {
		b.Run(tc.name, func(b *testing.B) {
			b.ReportAllocs()
			b.SetBytes(int64(size))
			for b.Loop() {
				resp, err := tr.Upload(ctx, tc.req)
				if err != nil || resp.Status != http.StatusOK {
					b.Fatal(resp.Status, err)
				}
				sinkResp = resp
			}
		})
	}
}
