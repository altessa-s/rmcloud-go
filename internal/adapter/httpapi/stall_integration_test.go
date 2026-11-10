//go:build integration

// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

// Integration tests that stall a loopback server in each of the ways a
// request can stop moving, with the transport's bounds shortened.

package httpapi_test

import (
	"bytes"
	"context"
	"crypto/x509"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/altessa-s/rmcloud-go/internal/adapter/httpapi"
	"github.com/altessa-s/rmcloud-go/internal/app/retry"
	"github.com/altessa-s/rmcloud-go/internal/domain"
	"github.com/altessa-s/rmcloud-go/internal/port"
)

// stallSlack is how late a bound may fire on a loaded machine.
const stallSlack = 5 * time.Second

// stallProtocols are the two wires the bounds have to hold on.
var stallProtocols = []struct {
	name  string
	major int
}{
	{name: "http1", major: 1},
	{name: "http2", major: 2},
}

// stallServer is a loopback TLS server speaking HTTP/1.1 or HTTP/2, and a
// channel that is closed before the server is, so a handler parked on it
// lets the server close.
func stallServer(t *testing.T, major int, h func(w http.ResponseWriter, r *http.Request, release <-chan struct{})) *httptest.Server {
	t.Helper()
	release := make(chan struct{})
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.ProtoMajor != major {
			t.Errorf("the request arrived over HTTP/%d, want HTTP/%d", r.ProtoMajor, major)
		}
		h(w, r, release)
	}))
	srv.EnableHTTP2 = major == 2
	srv.StartTLS()
	t.Cleanup(srv.Close)
	t.Cleanup(func() { close(release) })
	return srv
}

// stallTransport trusts srv and takes the shortened bounds in opts.
func stallTransport(t *testing.T, srv *httptest.Server, opts ...httpapi.Option) *httpapi.Transport {
	t.Helper()
	roots := x509.NewCertPool()
	roots.AddCert(srv.Certificate())
	all := append([]httpapi.Option{
		httpapi.WithRootCAs(roots),
		httpapi.WithoutSSRFProtection(),
		httpapi.WithLogger(slog.New(slog.DiscardHandler)),
	}, opts...)
	tr := httpapi.New(all...)
	t.Cleanup(tr.CloseIdleConnections)
	return tr
}

// requireRepeated fails unless err is the transport error with reason: one
// the retry policy repeats, and not a context error the caller would take
// for its own.
func requireRepeated(t *testing.T, err error, reason domain.TransportReason) {
	t.Helper()
	got, ok := errors.AsType[*domain.TransportError](err)
	if !ok || got.Reason != reason {
		t.Fatalf("got %v (%T), want a transport error with reason %s", err, err, reason)
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("%v reads as the caller's context", err)
	}
	if !retry.ShouldRetry(got) || !retry.Default().Retries(got) {
		t.Fatalf("%v is not repeated by the retry policy", err)
	}
}

// requireBetween fails unless the time since start is at least floor, and
// not more than stallSlack past it.
func requireBetween(t *testing.T, start time.Time, floor time.Duration) {
	t.Helper()
	if got := time.Since(start); got < floor || got > floor+stallSlack {
		t.Fatalf("returned after %s, want %s or a little more", got, floor)
	}
}

// flush sends what the handler has written so far.
func flush(t *testing.T, w http.ResponseWriter) {
	t.Helper()
	if err := http.NewResponseController(w).Flush(); err != nil {
		t.Errorf("flush: %v", err)
	}
}

func TestIntegration_ServerThatNeverAnswersTimesOut(t *testing.T) {
	t.Parallel()
	const bound = 200 * time.Millisecond
	// The other header bound is far away, so each call shows which one it has.
	const far = time.Minute

	for _, proto := range stallProtocols {
		t.Run(proto.name, func(t *testing.T) {
			t.Parallel()
			var hits atomic.Int32
			srv := stallServer(t, proto.major, func(_ http.ResponseWriter, r *http.Request, release <-chan struct{}) {
				hits.Add(1)
				_, _ = bytes.NewBuffer(nil).ReadFrom(r.Body)
				select {
				case <-release:
				case <-r.Context().Done():
				}
			})
			src := filepath.Join(t.TempDir(), "upload.bin")
			if err := os.WriteFile(src, bytes.Repeat([]byte("u"), 64<<10), 0o600); err != nil {
				t.Fatal(err)
			}
			dir := t.TempDir()

			calls := []struct {
				name string
				opts []httpapi.Option
				do   func(tr *httpapi.Transport) (port.Response, error)
			}{
				{
					name: "round trip",
					opts: []httpapi.Option{httpapi.WithResponseHeaderTimeout(bound), httpapi.WithUploadResponseHeaderTimeout(far)},
					do: func(tr *httpapi.Transport) (port.Response, error) {
						return tr.RoundTrip(t.Context(), port.Request{Method: http.MethodGet, URL: itURL(t, srv.URL, "/memory")})
					},
				},
				{
					name: "download",
					opts: []httpapi.Option{httpapi.WithResponseHeaderTimeout(bound), httpapi.WithUploadResponseHeaderTimeout(far)},
					do: func(tr *httpapi.Transport) (port.Response, error) {
						return tr.Download(t.Context(), port.Request{Method: http.MethodGet, URL: itURL(t, srv.URL, "/file"), Dest: dir})
					},
				},
				{
					name: "upload",
					opts: []httpapi.Option{httpapi.WithResponseHeaderTimeout(far), httpapi.WithUploadResponseHeaderTimeout(bound)},
					do: func(tr *httpapi.Transport) (port.Response, error) {
						return tr.Upload(t.Context(), port.Request{Method: http.MethodPut, URL: itURL(t, srv.URL, "/up"), File: src})
					},
				},
			}
			for _, call := range calls {
				t.Run(call.name, func(t *testing.T) {
					tr := stallTransport(t, srv, call.opts...)
					before := hits.Load()
					start := time.Now()
					resp, err := call.do(tr)
					requireRepeated(t, err, domain.ReasonTimedOut)
					requireBetween(t, start, bound)
					if resp.Status != 0 || resp.File != "" {
						t.Fatalf("a request without an answer returned %+v", resp)
					}
					// The transport does not repeat: the use cases decide that.
					if got := hits.Load() - before; got != 1 {
						t.Fatalf("%d requests reached the server", got)
					}
				})
			}
			if entries, _ := os.ReadDir(dir); len(entries) != 0 {
				t.Fatalf("a download without an answer left %d files", len(entries))
			}
		})
	}
}

func TestIntegration_BodyThatStallsAfterHeadersTimesOut(t *testing.T) {
	t.Parallel()
	const bound = 200 * time.Millisecond

	for _, proto := range stallProtocols {
		t.Run(proto.name, func(t *testing.T) {
			t.Parallel()
			srv := stallServer(t, proto.major, func(w http.ResponseWriter, r *http.Request, release <-chan struct{}) {
				w.Header().Set("Content-Length", "1048576")
				_, _ = w.Write([]byte("first bytes"))
				flush(t, w)
				select {
				case <-release:
				case <-r.Context().Done():
				}
			})
			tr := stallTransport(t, srv, httpapi.WithIdleReadTimeout(bound))

			start := time.Now()
			resp, err := tr.RoundTrip(t.Context(), port.Request{Method: http.MethodGet, URL: itURL(t, srv.URL, "/memory")})
			requireRepeated(t, err, domain.ReasonTimedOut)
			requireBetween(t, start, bound)
			if resp.Status != 0 || len(resp.Body) != 0 {
				t.Fatalf("a stalled body returned status %d and %d bytes", resp.Status, len(resp.Body))
			}
		})
	}
}

func TestIntegration_SlowButLiveBodyIsNotCut(t *testing.T) {
	t.Parallel()
	// A byte every 50ms for well over the idle bound: the bound limits
	// silence, not how long a body takes.
	const (
		bound  = 500 * time.Millisecond
		gap    = 50 * time.Millisecond
		chunks = 24
	)

	for _, proto := range stallProtocols {
		t.Run(proto.name, func(t *testing.T) {
			t.Parallel()
			srv := stallServer(t, proto.major, func(w http.ResponseWriter, _ *http.Request, _ <-chan struct{}) {
				for range chunks {
					time.Sleep(gap)
					_, _ = w.Write([]byte("x"))
					flush(t, w)
				}
			})
			tr := stallTransport(t, srv, httpapi.WithIdleReadTimeout(bound), httpapi.WithResponseHeaderTimeout(bound))

			start := time.Now()
			resp, err := tr.RoundTrip(t.Context(), port.Request{Method: http.MethodGet, URL: itURL(t, srv.URL, "/memory")})
			if err != nil || resp.Status != http.StatusOK || !bytes.Equal(resp.Body, bytes.Repeat([]byte("x"), chunks)) {
				t.Fatalf("status %d len %d err %v", resp.Status, len(resp.Body), err)
			}
			if got := time.Since(start); got < chunks*gap || got < 2*bound {
				t.Fatalf("the body took %s, which does not outlast the bound", got)
			}
		})
	}
}

// A file download has no idle bound of its own, and a live server answers
// the HTTP/2 ping: a silence several times longer than every bound still
// ends in the whole file.
func TestIntegration_SilentButLiveDownloadIsNotCut(t *testing.T) {
	t.Parallel()
	const bound = 100 * time.Millisecond

	for _, proto := range stallProtocols {
		t.Run(proto.name, func(t *testing.T) {
			t.Parallel()
			srv := stallServer(t, proto.major, func(w http.ResponseWriter, _ *http.Request, _ <-chan struct{}) {
				_, _ = w.Write([]byte("first,"))
				flush(t, w)
				time.Sleep(8 * bound)
				_, _ = w.Write([]byte("last"))
			})
			tr := stallTransport(t, srv,
				httpapi.WithIdleReadTimeout(bound), httpapi.WithPingAfter(bound), httpapi.WithPingTimeout(bound))

			resp, err := tr.Download(t.Context(), port.Request{Method: http.MethodGet, URL: itURL(t, srv.URL, "/file"), Dest: t.TempDir()})
			if err != nil || resp.Status != http.StatusOK {
				t.Fatalf("status %d err %v", resp.Status, err)
			}
			if got, _ := os.ReadFile(resp.File); string(got) != "first,last" {
				t.Fatalf("the file holds %q", got)
			}
		})
	}
}

func TestIntegration_CallerWinsOverEveryBound(t *testing.T) {
	t.Parallel()
	const callers = 150 * time.Millisecond

	for _, proto := range stallProtocols {
		t.Run(proto.name, func(t *testing.T) {
			t.Parallel()
			srv := stallServer(t, proto.major, func(w http.ResponseWriter, r *http.Request, release <-chan struct{}) {
				if r.URL.Path == "/body" {
					_, _ = w.Write([]byte("first bytes"))
					flush(t, w)
				}
				select {
				case <-release:
				case <-r.Context().Done():
				}
			})
			// Every bound is far beyond the caller's patience.
			tr := stallTransport(t, srv)

			for _, path := range []string{"/headers", "/body"} {
				ctx, cancel := context.WithTimeout(t.Context(), callers)
				start := time.Now()
				_, err := tr.RoundTrip(ctx, port.Request{Method: http.MethodGet, URL: itURL(t, srv.URL, path)})
				cancel()
				if !errors.Is(err, context.DeadlineExceeded) {
					t.Fatalf("%s: got %v, want the caller's deadline", path, err)
				}
				if _, isProtocol := errors.AsType[domain.Error](err); isProtocol {
					t.Fatalf("%s: the caller's deadline became %v, which the policy could repeat", path, err)
				}
				requireBetween(t, start, callers)

				ctx, cancel = context.WithCancel(t.Context())
				stop := time.AfterFunc(callers, cancel)
				_, err = tr.RoundTrip(ctx, port.Request{Method: http.MethodGet, URL: itURL(t, srv.URL, path)})
				stop.Stop()
				cancel()
				if !errors.Is(err, context.Canceled) {
					t.Fatalf("%s: got %v, want the caller's cancellation", path, err)
				}
			}
		})
	}
}

// blackhole is a TCP relay in front of a server that can stop relaying
// without closing anything: what a network that silently drops an
// established connection looks like from the client.
type blackhole struct {
	ln     net.Listener
	target string
	silent atomic.Bool
	done   chan struct{}
	wg     sync.WaitGroup
}

func newBlackhole(t *testing.T, target string) *blackhole {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	b := &blackhole{ln: ln, target: target, done: make(chan struct{})}
	b.wg.Go(b.accept)
	t.Cleanup(func() {
		close(b.done)
		_ = ln.Close()
		b.wg.Wait()
	})
	return b
}

func (b *blackhole) accept() {
	for {
		client, err := b.ln.Accept()
		if err != nil {
			return
		}
		server, err := net.Dial("tcp", b.target)
		if err != nil {
			_ = client.Close()
			continue
		}
		b.wg.Go(func() { b.relay(server, client) })
		b.wg.Go(func() { b.relay(client, server) })
		b.wg.Go(func() {
			<-b.done
			_ = client.Close()
			_ = server.Close()
		})
	}
}

// relay copies src to dst until the relay goes silent; from then on what
// arrives is dropped and nothing is closed.
func (b *blackhole) relay(dst, src net.Conn) {
	buf := make([]byte, 32<<10)
	for {
		n, err := src.Read(buf)
		if n > 0 && !b.silent.Load() {
			if _, werr := dst.Write(buf[:n]); werr != nil {
				return
			}
		}
		if err != nil {
			return
		}
	}
}

// The wording net/http gives a connection closed by an unanswered ping is
// what package neterr recognizes it by. This test meets the real failure,
// so a Go release that words it differently fails here.
func TestIntegration_DeadHTTP2ConnectionIsFoundByItsPing(t *testing.T) {
	t.Parallel()
	const bound = 150 * time.Millisecond

	started := make(chan struct{}, 2)
	srv := stallServer(t, 2, func(w http.ResponseWriter, r *http.Request, release <-chan struct{}) {
		w.Header().Set("Content-Length", "1048576")
		_, _ = w.Write([]byte("first bytes"))
		flush(t, w)
		started <- struct{}{}
		select {
		case <-release:
		case <-r.Context().Done():
		}
	})
	target, err := url.Parse(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()

	calls := map[string]func(tr *httpapi.Transport, via string) (port.Response, error){
		// A file transfer has no bound but the ping.
		"download": func(tr *httpapi.Transport, via string) (port.Response, error) {
			return tr.Download(t.Context(), port.Request{Method: http.MethodGet, URL: itURL(t, via, "/file"), Dest: dir})
		},
		// An in-memory read whose idle bound is further away than the ping.
		"round trip": func(tr *httpapi.Transport, via string) (port.Response, error) {
			return tr.RoundTrip(t.Context(), port.Request{Method: http.MethodGet, URL: itURL(t, via, "/memory")})
		},
	}
	for name, call := range calls {
		t.Run(name, func(t *testing.T) {
			hole := newBlackhole(t, target.Host)
			tr := stallTransport(t, srv, httpapi.WithPingAfter(bound), httpapi.WithPingTimeout(bound))

			errs := make(chan error, 1)
			go func() {
				_, err := call(tr, "https://"+hole.ln.Addr().String())
				errs <- err
			}()
			select {
			case <-started:
			case <-time.After(stallSlack):
				t.Fatal("the request never reached the server")
			}
			hole.silent.Store(true)
			start := time.Now()
			select {
			case err := <-errs:
				requireRepeated(t, err, domain.ReasonNetworkConnectionLost)
				// One silence before the ping and one waiting for its answer,
				// less what had already passed when the relay went silent.
				requireBetween(t, start, bound)
			case <-time.After(2*bound + stallSlack):
				t.Fatal("a dead connection was never given up on")
			}
		})
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		t.Fatalf("a lost download left %d files", len(entries))
	}
}
