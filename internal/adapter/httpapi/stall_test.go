// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package httpapi

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/altessa-s/rmcloud-go/internal/app/retry"
	"github.com/altessa-s/rmcloud-go/internal/domain"
	"github.com/altessa-s/rmcloud-go/internal/domaintest"
	"github.com/altessa-s/rmcloud-go/internal/port"
)

// slowBody yields chunks one-byte reads, waiting gap before each. After the
// last it ends, or with stall says nothing more until its request is
// canceled, as a connection whose peer went silent does.
type slowBody struct {
	ctx    context.Context
	chunks int
	gap    time.Duration
	stall  bool
}

func (b *slowBody) Read(p []byte) (int, error) {
	if b.chunks == 0 {
		if !b.stall {
			return 0, io.EOF
		}
		<-b.ctx.Done()
		return 0, b.ctx.Err()
	}
	select {
	case <-time.After(b.gap):
	case <-b.ctx.Done():
		return 0, b.ctx.Err()
	}
	b.chunks--
	p[0] = 'x'
	return 1, nil
}

func (*slowBody) Close() error { return nil }

// slowHost is a host round tripper whose response body is a slowBody on the
// request's context.
func slowHost(body slowBody) http.RoundTripper {
	return roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.Body != nil {
			_, _ = io.Copy(io.Discard, r.Body)
			_ = r.Body.Close()
		}
		b := body
		b.ctx = r.Context()
		resp := httpResp(r, http.StatusOK, nil, "")
		resp.Body, resp.ContentLength = &b, -1
		return resp, nil
	})
}

// mustStallTimeout fails unless err is the timeout a stalled request ends
// in: a transport error the retry policy repeats, and not a context error
// the caller would take for its own.
func mustStallTimeout(t *testing.T, err error) {
	t.Helper()
	if !domaintest.SameError(err, domain.Transport(domain.ReasonTimedOut)) {
		t.Fatalf("err = %v (%T), want a transport timeout", err, err)
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v reads as the caller's context", err)
	}
	cloud, ok := errors.AsType[domain.Error](err)
	if !ok || !retry.ShouldRetry(cloud) || !retry.Default().Retries(cloud) {
		t.Fatalf("err = %v is not retried", err)
	}
}

func TestStallBounds(t *testing.T) {
	t.Parallel()

	tr := New(WithLevel(LevelOff))
	if tr.idleReadTimeout != 45*time.Second {
		t.Fatalf("idle read timeout %s", tr.idleReadTimeout)
	}
	for name, tt := range map[string]struct {
		base    *http.Transport
		headers time.Duration
	}{
		"memory":   {tr.memBase, time.Minute},
		"upload":   {tr.upBase, 5 * time.Minute},
		"download": {tr.dlBase, time.Minute},
	} {
		if got := tt.base.ResponseHeaderTimeout; got != tt.headers {
			t.Errorf("%s: response header timeout %s, want %s", name, got, tt.headers)
		}
		h2 := tt.base.HTTP2
		if h2 == nil || h2.SendPingTimeout != 15*time.Second || h2.PingTimeout != 15*time.Second {
			t.Errorf("%s: http2 pings %+v, want a dead connection found in about 30s", name, h2)
		}
		// The guarded client dials through a clone, which has to keep both.
		clone := tt.base.Clone()
		if clone.ResponseHeaderTimeout != tt.headers || clone.HTTP2 == nil ||
			clone.HTTP2.SendPingTimeout != h2.SendPingTimeout || clone.HTTP2.PingTimeout != h2.PingTimeout {
			t.Errorf("%s: a clone lost the bounds", name)
		}
	}

	// The socket's transport is not a request: it has none of the bounds.
	if p := Pooled(nil); p.ResponseHeaderTimeout != 0 || p.HTTP2 != nil {
		t.Fatalf("pooled: header timeout %s http2 %+v", p.ResponseHeaderTimeout, p.HTTP2)
	}

	short := New(WithLevel(LevelOff),
		WithResponseHeaderTimeout(time.Second),
		WithUploadResponseHeaderTimeout(2*time.Second),
		WithIdleReadTimeout(3*time.Second),
		WithPingAfter(4*time.Second),
		WithPingTimeout(5*time.Second),
	)
	if short.memBase.ResponseHeaderTimeout != time.Second || short.dlBase.ResponseHeaderTimeout != time.Second ||
		short.upBase.ResponseHeaderTimeout != 2*time.Second || short.idleReadTimeout != 3*time.Second {
		t.Fatalf("header timeouts %s %s %s idle %s", short.memBase.ResponseHeaderTimeout,
			short.dlBase.ResponseHeaderTimeout, short.upBase.ResponseHeaderTimeout, short.idleReadTimeout)
	}
	if h2 := short.memBase.HTTP2; h2.SendPingTimeout != 4*time.Second || h2.PingTimeout != 5*time.Second {
		t.Fatalf("http2 pings %+v", h2)
	}

	// Zero would turn a bound off; it keeps the default instead.
	kept := New(WithLevel(LevelOff),
		WithResponseHeaderTimeout(0), WithUploadResponseHeaderTimeout(-1), WithIdleReadTimeout(0), WithPingAfter(0), WithPingTimeout(-1))
	if kept.memBase.ResponseHeaderTimeout != time.Minute || kept.upBase.ResponseHeaderTimeout != 5*time.Minute ||
		kept.idleReadTimeout != 45*time.Second ||
		kept.memBase.HTTP2.SendPingTimeout != 15*time.Second || kept.memBase.HTTP2.PingTimeout != 15*time.Second {
		t.Fatal("a non-positive duration replaced a default")
	}
}

func TestStalledMemoryBodyTimesOut(t *testing.T) {
	t.Parallel()
	u := mustURL(t, "https://host.example/x")
	calls := map[string]func(*Transport, context.Context) (port.Response, error){
		"round trip": func(tr *Transport, ctx context.Context) (port.Response, error) {
			return tr.RoundTrip(ctx, port.Request{URL: u})
		},
		"upload response": func(tr *Transport, ctx context.Context) (port.Response, error) {
			return tr.Upload(ctx, port.Request{Method: http.MethodPut, URL: u, Body: []byte("a")})
		},
	}
	for name, call := range calls {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				var wire bytes.Buffer
				// Two bytes arrive a second apart, then nothing.
				tr := testTransport(&wire, WithLevel(LevelHeaders),
					WithRoundTripper(slowHost(slowBody{chunks: 2, gap: time.Second, stall: true})))
				start := time.Now()
				resp, err := call(tr, t.Context())
				mustStallTimeout(t, err)
				if !zeroResponse(resp) {
					t.Fatalf("a stalled body returned %+v", resp)
				}
				// The wait is counted from the last byte, not from the headers.
				if got, want := time.Since(start), 2*time.Second+defaultIdleReadTimeout; got != want {
					t.Fatalf("gave up after %s, want %s", got, want)
				}
				if !strings.Contains(wire.String(), errIdleRead.Error()) {
					t.Fatalf("wire log does not say why:\n%s", wire.String())
				}
			})
		})
	}
}

func TestSlowButLiveMemoryBodyIsNotCut(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		// Ten minutes of body, a byte every thirty seconds: slow, and alive.
		const chunks, gap = 20, 30 * time.Second
		tr := testTransport(nil, WithRoundTripper(slowHost(slowBody{chunks: chunks, gap: gap})))
		start := time.Now()
		resp, err := tr.RoundTrip(t.Context(), port.Request{URL: mustURL(t, "https://host.example/x")})
		must(t, err)
		if string(resp.Body) != strings.Repeat("x", chunks) {
			t.Fatalf("body = %q", resp.Body)
		}
		if got := time.Since(start); got != chunks*gap {
			t.Fatalf("took %s, want %s", got, chunks*gap)
		}
		// A watch left running would cancel nothing now, but must not linger.
		time.Sleep(2 * defaultIdleReadTimeout)
	})
}

func TestCallerWinsOverAStalledMemoryBody(t *testing.T) {
	t.Parallel()
	u := mustURL(t, "https://host.example/x")
	t.Run("deadline", func(t *testing.T) {
		t.Parallel()
		synctest.Test(t, func(t *testing.T) {
			tr := testTransport(nil, WithRoundTripper(slowHost(slowBody{stall: true})))
			ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
			defer cancel()
			start := time.Now()
			_, err := tr.RoundTrip(ctx, port.Request{URL: u})
			if !errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("err = %v, want the caller's deadline", err)
			}
			if _, isProtocol := errors.AsType[domain.Error](err); isProtocol {
				t.Fatalf("the caller's deadline became %v, which the policy could repeat", err)
			}
			if got := time.Since(start); got != 10*time.Second {
				t.Fatalf("returned after %s", got)
			}
		})
	})
	t.Run("cancellation", func(t *testing.T) {
		t.Parallel()
		synctest.Test(t, func(t *testing.T) {
			tr := testTransport(nil, WithRoundTripper(slowHost(slowBody{stall: true})))
			ctx, cancel := context.WithCancel(t.Context())
			time.AfterFunc(10*time.Second, cancel)
			_, err := tr.RoundTrip(ctx, port.Request{URL: u})
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("err = %v, want the caller's cancellation", err)
			}
		})
	})
	t.Run("just before the watch", func(t *testing.T) {
		t.Parallel()
		synctest.Test(t, func(t *testing.T) {
			tr := testTransport(nil, WithRoundTripper(slowHost(slowBody{stall: true})))
			ctx, cancel := context.WithTimeout(t.Context(), defaultIdleReadTimeout-time.Nanosecond)
			defer cancel()
			_, err := tr.RoundTrip(ctx, port.Request{URL: u})
			if !errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("err = %v, want the caller's deadline", err)
			}
		})
	})
}

// A file download has no idle bound: a dead HTTP/2 connection is found by
// its ping, and a live one may be as slow as it likes.
func TestSlowDownloadIsNotCut(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		const chunks, gap = 3, 10 * time.Minute
		tr := testTransport(nil, WithRoundTripper(slowHost(slowBody{chunks: chunks, gap: gap})))
		resp, err := tr.Download(t.Context(), port.Request{URL: mustURL(t, "https://host.example/x"), Dest: t.TempDir()})
		must(t, err)
		got, err := os.ReadFile(resp.File)
		must(t, err)
		if string(got) != strings.Repeat("x", chunks) {
			t.Fatalf("file = %q", got)
		}
	})
}

func TestIdleReadTimeoutOption(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		tr := testTransport(nil, WithIdleReadTimeout(time.Second),
			WithRoundTripper(slowHost(slowBody{chunks: 1, gap: 500 * time.Millisecond, stall: true})))
		start := time.Now()
		_, err := tr.RoundTrip(t.Context(), port.Request{URL: mustURL(t, "https://host.example/x")})
		mustStallTimeout(t, err)
		if got := time.Since(start); got != 1500*time.Millisecond {
			t.Fatalf("gave up after %s", got)
		}
	})
}
