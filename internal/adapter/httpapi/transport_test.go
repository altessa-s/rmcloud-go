// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package httpapi

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/altessa-s/go-atlas-net/http/client"

	"github.com/altessa-s/rmcloud-go/internal/domain"
	"github.com/altessa-s/rmcloud-go/internal/domaintest"
	"github.com/altessa-s/rmcloud-go/internal/port"
)

// captureRT records the requests a Transport sends.
// Status 0 is left as 0. The hook runs after the mutex is released.
type captureRT struct {
	mu     sync.Mutex
	saw    []*http.Request
	bodies []string
	calls  atomic.Int32
	status int
	header http.Header
	body   string
	err    error
	hook   func(req *http.Request, body string)
}

func (c *captureRT) RoundTrip(req *http.Request) (*http.Response, error) {
	c.calls.Add(1)
	var body string
	if req.Body != nil {
		b, err := io.ReadAll(req.Body)
		if err != nil {
			return nil, err
		}
		body = string(b)
	}
	c.mu.Lock()
	c.saw = append(c.saw, req)
	c.bodies = append(c.bodies, body)
	status := c.status
	header := c.header
	respBody := c.body
	callErr := c.err
	hook := c.hook
	c.mu.Unlock()
	if hook != nil {
		hook(req, body)
	}
	if callErr != nil {
		return nil, callErr
	}
	return httpResp(req, status, header, respBody), nil
}

func (c *captureRT) last() *http.Request {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.saw[len(c.saw)-1]
}

type errReadCloser struct{ err error }

func (e errReadCloser) Read([]byte) (int, error) { return 0, e.err }
func (errReadCloser) Close() error               { return nil }

type gateBody struct {
	once    sync.Once
	started chan struct{}
	ctx     context.Context
}

func (g *gateBody) Read([]byte) (int, error) {
	g.once.Do(func() { close(g.started) })
	<-g.ctx.Done()
	return 0, g.ctx.Err()
}

func (*gateBody) Close() error { return nil }

func dirEmpty(t *testing.T, dir string) {
	t.Helper()
	ents, err := os.ReadDir(dir)
	must(t, err)
	if len(ents) != 0 {
		t.Fatalf("%s has %d entries", dir, len(ents))
	}
}

func waitSignal(t *testing.T, ch <-chan struct{}, what string) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(2 * time.Second):
		t.Fatal(what)
	}
}

// Header is a map, so port.Response is not comparable.
func zeroResponse(resp port.Response) bool {
	return resp.Status == 0 && resp.Header == nil && len(resp.Body) == 0 && resp.File == ""
}

func waitErr(t *testing.T, ch <-chan error) error {
	t.Helper()
	select {
	case err := <-ch:
		return err
	case <-time.After(2 * time.Second):
		t.Fatal("timed out")
	}
	return nil
}

func TestTransportClients(t *testing.T) {
	t.Parallel()
	tr := New(WithLevel(LevelOff))
	if tr.memory == nil || tr.upload == nil || tr.download == nil ||
		tr.memory == tr.download || tr.memory == tr.upload || tr.upload == tr.download {
		t.Fatalf("clients memory %p upload %p download %p", tr.memory, tr.upload, tr.download)
	}
	// No client bounds a whole request: a slow transfer that is alive is
	// the caller's to stop.
	if tr.memory.Timeout != 0 || tr.upload.Timeout != 0 || tr.download.Timeout != 0 {
		t.Fatalf("timeout memory %s upload %s download %s", tr.memory.Timeout, tr.upload.Timeout, tr.download.Timeout)
	}
	mem, up, dl := tr.memBase, tr.upBase, tr.dlBase
	if mem == nil || up == nil || dl == nil || mem == dl || mem == up || up == dl {
		t.Fatal("round trippers share a pointer")
	}
	if mem.MaxConnsPerHost != 0 || up.MaxConnsPerHost != 0 || dl.MaxConnsPerHost != 6 {
		t.Fatalf("max conns memory %d upload %d download %d", mem.MaxConnsPerHost, up.MaxConnsPerHost, dl.MaxConnsPerHost)
	}

	var memCalls, upCalls, dlCalls atomic.Int32
	tr.memory.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		memCalls.Add(1)
		return httpResp(r, 200, nil, "m"), nil
	})
	tr.upload.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		upCalls.Add(1)
		return httpResp(r, 200, nil, "u"), nil
	})
	tr.download.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		dlCalls.Add(1)
		return httpResp(r, 200, nil, "d"), nil
	})
	u := mustURL(t, "https://host.example/x")
	ctx := t.Context()
	if _, err := tr.RoundTrip(ctx, port.Request{URL: u}); err != nil {
		t.Fatal(err)
	}
	if _, err := tr.Upload(ctx, port.Request{Method: http.MethodPost, URL: u, Body: []byte("a")}); err != nil {
		t.Fatal(err)
	}
	if _, err := tr.Download(ctx, port.Request{URL: u, Dest: t.TempDir()}); err != nil {
		t.Fatal(err)
	}
	if memCalls.Load() != 1 || upCalls.Load() != 1 || dlCalls.Load() != 1 {
		t.Fatalf("memory %d upload %d download %d", memCalls.Load(), upCalls.Load(), dlCalls.Load())
	}

	silent := New(WithLevel(LevelOff), WithWireWriter(nil))
	if silent.wire.writer == nil {
		t.Fatal("nil writer replaced the default")
	}
}

func TestNilRoundTripper(t *testing.T) {
	t.Parallel()
	tr := testTransport(nil, WithRoundTripper(nil))
	if tr.host != nil {
		t.Fatalf("%T", tr.host)
	}
}

func TestCloseIdleConnections(t *testing.T) {
	t.Parallel()
	tr := testTransport(nil)
	tr.CloseIdleConnections()
}

func TestTransportRequestShape(t *testing.T) {
	t.Parallel()
	u := mustURL(t, "https://host.example/x")

	t.Run("invalid method", func(t *testing.T) {
		t.Parallel()
		var buf bytes.Buffer
		rt := &captureRT{status: 200, body: "ok"}
		tr := testTransport(&buf, WithLevel(LevelHeaders), WithRoundTripper(rt))
		_, err := tr.RoundTrip(t.Context(), port.Request{Method: "BAD METHOD", URL: u})
		if err == nil || !strings.Contains(err.Error(), "invalid method") {
			t.Fatalf("%v", err)
		}
		if rt.calls.Load() != 0 || buf.Len() != 0 {
			t.Fatalf("calls %d wire %q", rt.calls.Load(), buf.String())
		}
	})

	t.Run("empty method", func(t *testing.T) {
		t.Parallel()
		rt := &captureRT{status: 200}
		tr := testTransport(nil, WithRoundTripper(rt))
		if _, err := tr.RoundTrip(t.Context(), port.Request{URL: u}); err != nil {
			t.Fatal(err)
		}
		if rt.last().Method != http.MethodGet {
			t.Fatalf("method %s", rt.last().Method)
		}
	})

	t.Run("header spelling", func(t *testing.T) {
		t.Parallel()
		rt := &captureRT{status: 200}
		tr := testTransport(nil, WithRoundTripper(rt))
		_, err := tr.RoundTrip(t.Context(), port.Request{
			URL: u,
			Header: port.Header{
				"rm-filename": {"roothash"},
				"user-agent":  {"custom"},
			},
		})
		must(t, err)
		h := map[string][]string(rt.last().Header)
		if got := h["rm-filename"]; len(got) != 1 || got[0] != "roothash" {
			t.Fatalf("rm-filename %#v", h["rm-filename"])
		}
		if rt.last().Header.Get("rm-filename") != "" {
			t.Fatalf("Get saw %q", rt.last().Header.Get("rm-filename"))
		}
		if got := h["user-agent"]; len(got) != 1 || got[0] != "custom" {
			t.Fatalf("user-agent %#v", h["user-agent"])
		}
		if _, ok := h["User-Agent"]; ok {
			t.Fatalf("canonical key added: %#v", h["User-Agent"])
		}
	})

	t.Run("suppress user agent", func(t *testing.T) {
		t.Parallel()
		var buf bytes.Buffer
		rt := &captureRT{status: 200}
		tr := testTransport(&buf, WithLevel(LevelHeaders), WithRoundTripper(rt))
		must(t, func() error {
			_, err := tr.RoundTrip(t.Context(), port.Request{URL: u})
			return err
		}())
		got := rt.last().Header["User-Agent"]
		if len(got) != 1 || got[0] != "" {
			t.Fatalf("User-Agent %#v", got)
		}
		if strings.Contains(buf.String(), "User-Agent") {
			t.Fatalf("wire log showed the suppressor:\n%s", buf.String())
		}
	})

	t.Run("empty get", func(t *testing.T) {
		t.Parallel()
		rt := &captureRT{status: 200}
		tr := testTransport(nil, WithRoundTripper(rt))
		must(t, func() error {
			_, err := tr.RoundTrip(t.Context(), port.Request{Method: http.MethodGet, URL: u})
			return err
		}())
		req := rt.last()
		if req.Body != nil || req.GetBody != nil {
			t.Fatal("empty GET set a body")
		}
	})

	t.Run("empty post", func(t *testing.T) {
		t.Parallel()
		rt := &captureRT{status: 200}
		tr := testTransport(nil, WithRoundTripper(rt))
		must(t, func() error {
			_, err := tr.RoundTrip(t.Context(), port.Request{Method: http.MethodPost, URL: u})
			return err
		}())
		req := rt.last()
		if req.Body == nil || req.ContentLength != 0 || req.GetBody == nil {
			t.Fatalf("body %v len %d get %v", req.Body, req.ContentLength, req.GetBody != nil)
		}
		rc, err := req.GetBody()
		must(t, err)
		b, err := io.ReadAll(rc)
		must(t, err)
		if len(b) != 0 {
			t.Fatalf("replay %q", b)
		}
	})

	t.Run("url", func(t *testing.T) {
		t.Parallel()
		if snapshotURL(nil) != nil {
			t.Fatal("nil snapshot")
		}
		rt := &captureRT{status: 200}
		tr := testTransport(nil, WithRoundTripper(rt))
		must(t, func() error {
			_, err := tr.RoundTrip(t.Context(), port.Request{URL: u})
			return err
		}())
		req := rt.last()
		if req.RequestURI != "" {
			t.Fatalf("RequestURI %q", req.RequestURI)
		}
		if req.Host != u.Host || req.URL.Host != u.Host || strings.Contains(req.URL.String(), "placeholder.invalid") {
			t.Fatalf("host %q url %s", req.Host, req.URL)
		}
	})

	t.Run("ignores file", func(t *testing.T) {
		t.Parallel()
		rt := &captureRT{status: 200, body: "ok"}
		tr := testTransport(nil, WithRoundTripper(rt))
		resp, err := tr.RoundTrip(t.Context(), port.Request{
			URL:  u,
			Body: []byte("from-body"),
			File: filepath.Join(t.TempDir(), "missing"),
		})
		must(t, err)
		if rt.bodies[0] != "from-body" || string(resp.Body) != "ok" {
			t.Fatalf("sent %q got %q", rt.bodies[0], resp.Body)
		}
	})

	t.Run("status", func(t *testing.T) {
		t.Parallel()
		denied := &captureRT{status: 401, body: "no"}
		tr := testTransport(nil, WithRoundTripper(denied))
		resp, err := tr.RoundTrip(t.Context(), port.Request{URL: u})
		must(t, err)
		if resp.Status != 401 || string(resp.Body) != "no" {
			t.Fatalf("%d %q", resp.Status, resp.Body)
		}

		zero := &captureRT{}
		tr = testTransport(nil, WithRoundTripper(zero))
		resp, err = tr.RoundTrip(t.Context(), port.Request{URL: u})
		must(t, err)
		if resp.Status != 0 {
			t.Fatalf("status %d", resp.Status)
		}
	})

	t.Run("nil response", func(t *testing.T) {
		t.Parallel()
		var buf bytes.Buffer
		tr := testTransport(&buf, WithLevel(LevelHeaders), WithRoundTripper(roundTripFunc(func(*http.Request) (*http.Response, error) {
			return nil, nil //nolint:nilnil // the broken round tripper under test
		})))
		_, err := tr.RoundTrip(t.Context(), port.Request{URL: u})
		if !domaintest.SameError(err, domain.MalformedResponse("response is not HTTP")) {
			t.Fatalf("%v", err)
		}
		// go-atlas-net redacts a transport error it does not know; the chain
		// still names errNotHTTP, which the classification above relies on.
		log := buf.String()
		if !strings.Contains(log, "HTTP transport error") || !strings.Contains(log, "←!") {
			t.Fatalf("%s", log)
		}
	})

	t.Run("memory ceiling", func(t *testing.T) {
		t.Parallel()
		var progress atomic.Bool
		rt := &captureRT{status: 200, body: "abcdefghij"}
		tr := testTransport(nil, WithRoundTripper(rt))
		resp, err := tr.RoundTrip(t.Context(), port.Request{
			URL:   u,
			Limit: 4,
			Progress: func(int64, int64) {
				progress.Store(true)
			},
		})
		must(t, err)
		if string(resp.Body) != "abcdefghij" || progress.Load() {
			t.Fatalf("body %q progress %v", resp.Body, progress.Load())
		}
	})

	t.Run("unexpected eof", func(t *testing.T) {
		t.Parallel()
		var buf bytes.Buffer
		tr := testTransport(&buf, WithLevel(LevelHeaders), WithRoundTripper(roundTripFunc(func(r *http.Request) (*http.Response, error) {
			resp := httpResp(r, 200, nil, "")
			resp.Body = errReadCloser{err: io.ErrUnexpectedEOF}
			return resp, nil
		})))
		resp, err := tr.RoundTrip(t.Context(), port.Request{URL: u})
		// A body cut short is a lost connection, which the use cases retry.
		if !domaintest.SameError(err, domain.Transport(domain.ReasonNetworkConnectionLost)) {
			t.Fatalf("%v", err)
		}
		if !zeroResponse(resp) || !strings.Contains(buf.String(), "←!") {
			t.Fatalf("resp %+v\n%s", resp, buf.String())
		}
	})
}

func TestTransportUpload(t *testing.T) {
	t.Parallel()
	u := mustURL(t, "https://host.example/blob")

	t.Run("file wins", func(t *testing.T) {
		t.Parallel()
		path := filepath.Join(t.TempDir(), "payload.bin")
		must(t, os.WriteFile(path, []byte("from-file"), 0o600))
		var buf bytes.Buffer
		rt := &captureRT{status: 200, body: "ok"}
		tr := testTransport(&buf, WithLevel(LevelHeaders), WithRoundTripper(rt))
		resp, err := tr.Upload(t.Context(), port.Request{
			Method: http.MethodPut,
			URL:    u,
			File:   path,
			Body:   []byte("from-body"),
		})
		must(t, err)
		if rt.bodies[0] != "from-file" || string(resp.Body) != "ok" {
			t.Fatalf("sent %q got %q", rt.bodies[0], resp.Body)
		}
		log := buf.String()
		if strings.Contains(log, "from-file") || strings.Contains(log, "from-body") || strings.Contains(log, "payload") {
			t.Fatalf("%s", log)
		}
	})

	t.Run("file progress", func(t *testing.T) {
		t.Parallel()
		payload := bytes.Repeat([]byte("p"), 100_000)
		path := filepath.Join(t.TempDir(), "payload.bin")
		must(t, os.WriteFile(path, payload, 0o600))
		var mu sync.Mutex
		var seen [][2]int64
		rt := &captureRT{status: 200}
		tr := testTransport(nil, WithRoundTripper(rt))
		_, err := tr.Upload(t.Context(), port.Request{
			Method: http.MethodPut,
			URL:    u,
			File:   path,
			Progress: func(done, total int64) {
				mu.Lock()
				seen = append(seen, [2]int64{done, total})
				mu.Unlock()
			},
		})
		must(t, err)
		mu.Lock()
		defer mu.Unlock()
		if len(seen) == 0 {
			t.Fatal("upload progress was never reported")
		}
		last := seen[len(seen)-1]
		if last != [2]int64{int64(len(payload)), int64(len(payload))} {
			t.Fatalf("last progress %v", last)
		}
		for i := 1; i < len(seen); i++ {
			if seen[i][0] < seen[i-1][0] {
				t.Fatalf("progress went back: %v", seen)
			}
		}
	})

	t.Run("missing file", func(t *testing.T) {
		t.Parallel()
		var buf bytes.Buffer
		rt := &captureRT{status: 200}
		tr := testTransport(&buf, WithLevel(LevelHeaders), WithRoundTripper(rt))
		_, err := tr.Upload(t.Context(), port.Request{
			Method: http.MethodPut,
			URL:    u,
			File:   filepath.Join(t.TempDir(), "missing.bin"),
		})
		if !os.IsNotExist(err) || rt.calls.Load() != 0 || buf.Len() != 0 {
			t.Fatalf("err %v calls %d wire %q", err, rt.calls.Load(), buf.String())
		}
	})

	t.Run("invalid method before open", func(t *testing.T) {
		t.Parallel()
		rt := &captureRT{status: 200}
		tr := testTransport(nil, WithRoundTripper(rt))
		_, err := tr.Upload(t.Context(), port.Request{
			Method: "BAD METHOD",
			URL:    u,
			File:   filepath.Join(t.TempDir(), "missing.bin"),
		})
		if err == nil || !strings.Contains(err.Error(), "invalid method") || os.IsNotExist(err) {
			t.Fatalf("%v", err)
		}
		if rt.calls.Load() != 0 {
			t.Fatalf("calls %d", rt.calls.Load())
		}
	})

	t.Run("memory replay", func(t *testing.T) {
		t.Parallel()
		rt := &captureRT{
			status: http.StatusTemporaryRedirect,
			header: http.Header{"Location": {u.String()}},
			body:   "ok",
		}
		rt.hook = func(*http.Request, string) {
			rt.mu.Lock()
			rt.status = http.StatusOK
			rt.header = nil
			rt.mu.Unlock()
		}
		var buf bytes.Buffer
		tr := testTransport(&buf, WithLevel(LevelHeaders), WithRoundTripper(rt))
		resp, err := tr.Upload(t.Context(), port.Request{
			Method: http.MethodPut,
			URL:    u,
			Body:   []byte("payload"),
		})
		must(t, err)
		if resp.Status != 200 || string(resp.Body) != "ok" {
			t.Fatalf("%d %q", resp.Status, resp.Body)
		}
		if len(rt.bodies) != 2 || rt.bodies[0] != "payload" || rt.bodies[1] != "payload" {
			t.Fatalf("%q", rt.bodies)
		}
		for _, req := range rt.saw {
			if req.Method != http.MethodPut {
				t.Fatalf("method %s", req.Method)
			}
		}
		if strings.Contains(buf.String(), "payload") {
			t.Fatalf("%s", buf.String())
		}
	})

	t.Run("file replay", func(t *testing.T) {
		t.Parallel()
		path := filepath.Join(t.TempDir(), "payload.bin")
		const v1, v2 = "v1v1v1v1", "v2v2v2v2"
		must(t, os.WriteFile(path, []byte(v1), 0o600))
		rt := &captureRT{
			status: http.StatusTemporaryRedirect,
			header: http.Header{"Location": {u.String()}},
			body:   "ok",
		}
		rt.hook = func(_ *http.Request, body string) {
			if body == v1 {
				must(t, os.WriteFile(path, []byte(v2), 0o600))
			}
			rt.mu.Lock()
			rt.status = http.StatusOK
			rt.header = nil
			rt.mu.Unlock()
		}
		var buf bytes.Buffer
		tr := testTransport(&buf, WithLevel(LevelHeaders), WithRoundTripper(rt))
		_, err := tr.Upload(t.Context(), port.Request{Method: http.MethodPut, URL: u, File: path})
		must(t, err)
		if len(rt.bodies) != 2 || rt.bodies[0] != v1 || rt.bodies[1] != v2 {
			t.Fatalf("%q", rt.bodies)
		}
		for _, req := range rt.saw {
			if req.Method != http.MethodPut {
				t.Fatalf("method %s", req.Method)
			}
		}
		log := buf.String()
		if strings.Contains(log, "from-file") || strings.Contains(log, "from-body") || strings.Contains(log, "payload") {
			t.Fatalf("%s", log)
		}
	})
}

func TestTransportDownload(t *testing.T) {
	t.Parallel()
	u := mustURL(t, "https://host.example/file")

	t.Run("success", func(t *testing.T) {
		t.Parallel()
		const body = "hello"
		var buf bytes.Buffer
		rt := &captureRT{status: 200, body: body}
		tr := testTransport(&buf, WithLevel(LevelHeaders), WithRoundTripper(rt))
		var done, total int64
		var calls int
		dir := t.TempDir()
		resp, err := tr.Download(t.Context(), port.Request{
			URL:      u,
			Dest:     dir,
			Expected: int64(len(body)),
			Progress: func(d, tot int64) {
				calls++
				done, total = d, tot
			},
		})
		must(t, err)
		if resp.Status != 200 {
			t.Fatalf("status %d", resp.Status)
		}
		info, err := os.Stat(resp.File)
		must(t, err)
		if info.Mode().Perm() != 0o600 {
			t.Fatalf("perm %o", info.Mode().Perm())
		}
		got, err := os.ReadFile(resp.File)
		must(t, err)
		if string(got) != body || calls == 0 || done != int64(len(body)) || total != int64(len(body)) {
			t.Fatalf("file %q progress %d (%d,%d)", got, calls, done, total)
		}
		want := "  ← body: 5 bytes into a file"
		if !strings.Contains(buf.String(), want) {
			t.Fatalf("%s", buf.String())
		}
	})

	t.Run("unknown total", func(t *testing.T) {
		t.Parallel()
		for _, expected := range []int64{0, -1} {
			rt := &captureRT{status: 200, body: "ab"}
			tr := testTransport(nil, WithRoundTripper(rt))
			var total int64
			var calls int
			_, err := tr.Download(t.Context(), port.Request{
				URL:      u,
				Dest:     t.TempDir(),
				Expected: expected,
				Progress: func(_, tot int64) {
					calls++
					total = tot
				},
			})
			must(t, err)
			if calls == 0 || total != -1 {
				t.Fatalf("expected %d calls %d total %d", expected, calls, total)
			}
		}
	})

	t.Run("not success status", func(t *testing.T) {
		t.Parallel()
		rt := &captureRT{status: 404, body: "missing"}
		tr := testTransport(nil, WithRoundTripper(rt))
		resp, err := tr.Download(t.Context(), port.Request{URL: u, Dest: t.TempDir()})
		must(t, err)
		got, readErr := os.ReadFile(resp.File)
		must(t, readErr)
		if resp.Status != 404 || string(got) != "missing" {
			t.Fatalf("%d %q", resp.Status, got)
		}
	})

	t.Run("status zero", func(t *testing.T) {
		t.Parallel()
		rt := &captureRT{body: "x"}
		tr := testTransport(nil, WithRoundTripper(rt))
		resp, err := tr.Download(t.Context(), port.Request{URL: u, Dest: t.TempDir()})
		must(t, err)
		if resp.Status != 0 {
			t.Fatalf("status %d", resp.Status)
		}
		if _, err := os.Stat(resp.File); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("refused before trip", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		named := filepath.Join(dir, "nope")
		rt := &captureRT{status: 200, body: "abcdefghij"}
		tr := testTransport(nil, WithRoundTripper(rt))
		_, err := tr.Download(t.Context(), port.Request{
			URL:      u,
			Dest:     named,
			Expected: 100,
			Limit:    10,
		})
		if !domaintest.SameError(err, domain.OversizedResponse(100, 10)) {
			t.Fatalf("%v", err)
		}
		if rt.calls.Load() != 0 {
			t.Fatalf("calls %d", rt.calls.Load())
		}
		if _, statErr := os.Stat(named); !os.IsNotExist(statErr) {
			t.Fatal(statErr)
		}
		dirEmpty(t, dir)
	})

	oversized := []struct {
		name     string
		body     string
		limit    int64
		expected int64
		bytes    int64
		ceil     int64
	}{
		{"ten past four", "abcdefghij", 4, 0, 5, 4},
		{"past two", "abcdefghij", 2, 0, 3, 2},
		{"one past fifty", strings.Repeat("x", 51), 50, 0, 51, 50},
		{"declared", "abcdefghij", 0, 4, 5, 4},
	}
	for _, tc := range oversized {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			var buf bytes.Buffer
			rt := &captureRT{status: 200, body: tc.body}
			tr := testTransport(&buf, WithLevel(LevelHeaders), WithRoundTripper(rt))
			_, err := tr.Download(t.Context(), port.Request{
				URL:      u,
				Dest:     dir,
				Limit:    tc.limit,
				Expected: tc.expected,
			})
			if !domaintest.SameError(err, domain.OversizedResponse(tc.bytes, tc.ceil)) {
				t.Fatalf("%v", err)
			}
			log := buf.String()
			if strings.Contains(log, "into a file") || !strings.Contains(log, fmt.Sprintf("← body: more than %d bytes — dropped", tc.ceil)) {
				t.Fatalf("%s", log)
			}
			dirEmpty(t, dir)
		})
	}

	t.Run("unexpected eof", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		var buf bytes.Buffer
		tr := testTransport(&buf, WithLevel(LevelHeaders), WithRoundTripper(roundTripFunc(func(r *http.Request) (*http.Response, error) {
			resp := httpResp(r, 200, nil, "")
			resp.Body = errReadCloser{err: io.ErrUnexpectedEOF}
			return resp, nil
		})))
		_, err := tr.Download(t.Context(), port.Request{URL: u, Dest: dir})
		// A body cut short is a lost connection, which the use cases retry.
		if !domaintest.SameError(err, domain.Transport(domain.ReasonNetworkConnectionLost)) {
			t.Fatalf("%v", err)
		}
		if !strings.Contains(buf.String(), "←!") {
			t.Fatalf("%s", buf.String())
		}
		dirEmpty(t, dir)
	})

	t.Run("truncates", func(t *testing.T) {
		t.Parallel()
		path := filepath.Join(t.TempDir(), "f")
		must(t, os.WriteFile(path, []byte("old-content-that-is-longer"), 0o600))
		rt := &captureRT{status: 200, body: "new"}
		tr := testTransport(nil, WithRoundTripper(rt))
		resp, err := tr.Download(t.Context(), port.Request{URL: u, Dest: path})
		must(t, err)
		got, err := os.ReadFile(resp.File)
		must(t, err)
		if string(got) != "new" || resp.File != path {
			t.Fatalf("%s %q", resp.File, got)
		}
	})

	t.Run("failure keeps the existing file", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		path := filepath.Join(dir, "f")
		must(t, os.WriteFile(path, []byte("old"), 0o600))
		tr := testTransport(nil, WithRoundTripper(roundTripFunc(func(r *http.Request) (*http.Response, error) {
			resp := httpResp(r, 200, nil, "")
			resp.Body = errReadCloser{err: io.ErrUnexpectedEOF}
			return resp, nil
		})))
		_, err := tr.Download(t.Context(), port.Request{URL: u, Dest: path})
		// A body cut short is a lost connection, which the use cases retry.
		if !domaintest.SameError(err, domain.Transport(domain.ReasonNetworkConnectionLost)) {
			t.Fatalf("%v", err)
		}
		got, err := os.ReadFile(path)
		must(t, err)
		if string(got) != "old" {
			t.Fatalf("existing file became %q", got)
		}
		entries, err := os.ReadDir(dir)
		must(t, err)
		if len(entries) != 1 {
			t.Fatalf("leftovers %v", entries)
		}
	})

	t.Run("symlink dir", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		target := filepath.Join(dir, "real")
		must(t, os.Mkdir(target, 0o700))
		link := filepath.Join(dir, "link")
		must(t, os.Symlink(target, link))
		rt := &captureRT{status: 200, body: "in"}
		tr := testTransport(nil, WithRoundTripper(rt))
		resp, err := tr.Download(t.Context(), port.Request{URL: u, Dest: link})
		must(t, err)
		got, err := filepath.EvalSymlinks(filepath.Dir(resp.File))
		must(t, err)
		want, err := filepath.EvalSymlinks(target)
		must(t, err)
		if got != want {
			t.Fatalf("%s vs %s", got, want)
		}
	})

	t.Run("unreadable parent", func(t *testing.T) {
		t.Parallel()
		parent := t.TempDir()
		t.Cleanup(func() { _ = os.Chmod(parent, 0o700) })
		must(t, os.Chmod(parent, 0))
		rt := &captureRT{status: 200, body: "x"}
		tr := testTransport(nil, WithRoundTripper(rt))
		_, err := tr.Download(t.Context(), port.Request{URL: u, Dest: filepath.Join(parent, "file")})
		if !os.IsPermission(err) || rt.calls.Load() != 0 {
			t.Fatalf("err %v calls %d", err, rt.calls.Load())
		}
	})

	t.Run("missing parent", func(t *testing.T) {
		t.Parallel()
		rt := &captureRT{status: 200}
		tr := testTransport(nil, WithRoundTripper(rt))
		_, err := tr.Download(t.Context(), port.Request{
			URL:  u,
			Dest: filepath.Join(t.TempDir(), "nope", "file"),
		})
		if !os.IsNotExist(err) || rt.calls.Load() != 0 {
			t.Fatalf("err %v calls %d", err, rt.calls.Load())
		}
	})

	t.Run("invalid method", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		rt := &captureRT{status: 200}
		tr := testTransport(nil, WithRoundTripper(rt))
		_, err := tr.Download(t.Context(), port.Request{Method: "BAD METHOD", URL: u, Dest: dir})
		if err == nil || !strings.Contains(err.Error(), "invalid method") {
			t.Fatalf("%v", err)
		}
		if rt.calls.Load() != 0 {
			t.Fatalf("calls %d", rt.calls.Load())
		}
		dirEmpty(t, dir)
	})

	t.Run("empty body", func(t *testing.T) {
		t.Parallel()
		var buf bytes.Buffer
		rt := &captureRT{status: 200}
		tr := testTransport(&buf, WithLevel(LevelHeaders), WithRoundTripper(rt))
		resp, err := tr.Download(t.Context(), port.Request{URL: u, Dest: t.TempDir()})
		must(t, err)
		info, err := os.Stat(resp.File)
		must(t, err)
		if info.Size() != 0 || !strings.Contains(buf.String(), "  ← body: 0 bytes into a file") {
			t.Fatalf("size %d\n%s", info.Size(), buf.String())
		}
	})
}

func TestTransportCancel(t *testing.T) {
	t.Parallel()
	u := mustURL(t, "https://host.example/x")

	t.Run("during trip", func(t *testing.T) {
		t.Parallel()
		started := make(chan struct{})
		tr := testTransport(nil, WithRoundTripper(roundTripFunc(func(r *http.Request) (*http.Response, error) {
			close(started)
			select {
			case <-r.Context().Done():
				return nil, r.Context().Err()
			case <-time.After(2 * time.Second):
				return nil, errors.New("timed out waiting for cancel")
			}
		})))
		dir := t.TempDir()
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		errc := make(chan error, 1)
		go func() {
			_, err := tr.Download(ctx, port.Request{URL: u, Dest: dir})
			errc <- err
		}()
		waitSignal(t, started, "download did not reach the trip")
		ents, err := os.ReadDir(dir)
		must(t, err)
		if len(ents) == 0 {
			t.Fatal("file was not created before the trip")
		}
		cancel()
		if err := waitErr(t, errc); !errors.Is(err, context.Canceled) {
			t.Fatalf("%v", err)
		}
		dirEmpty(t, dir)
	})

	t.Run("during read", func(t *testing.T) {
		t.Parallel()
		newTR := func(started chan struct{}) *Transport {
			return testTransport(nil, WithRoundTripper(roundTripFunc(func(r *http.Request) (*http.Response, error) {
				resp := httpResp(r, 200, nil, "")
				resp.Body = &gateBody{started: started, ctx: r.Context()}
				return resp, nil
			})))
		}

		t.Run("download", func(t *testing.T) {
			t.Parallel()
			started := make(chan struct{})
			tr := newTR(started)
			dir := t.TempDir()
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			errc := make(chan error, 1)
			go func() {
				_, err := tr.Download(ctx, port.Request{URL: u, Dest: dir})
				errc <- err
			}()
			waitSignal(t, started, "read did not start")
			cancel()
			if err := waitErr(t, errc); !errors.Is(err, context.Canceled) {
				t.Fatalf("%v", err)
			}
			dirEmpty(t, dir)
		})

		t.Run("round trip", func(t *testing.T) {
			t.Parallel()
			started := make(chan struct{})
			tr := newTR(started)
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			type result struct {
				resp port.Response
				err  error
			}
			out := make(chan result, 1)
			go func() {
				resp, err := tr.RoundTrip(ctx, port.Request{URL: u})
				out <- result{resp, err}
			}()
			waitSignal(t, started, "read did not start")
			cancel()
			select {
			case got := <-out:
				if !errors.Is(got.err, context.Canceled) || !zeroResponse(got.resp) {
					t.Fatalf("%+v %v", got.resp, got.err)
				}
			case <-time.After(2 * time.Second):
				t.Fatal("timed out")
			}
		})
	})
}

func TestTransportOversizedLog(t *testing.T) {
	log := captureLog(t)
	u := mustURL(t, "https://host.example/file")
	dir := t.TempDir()
	rt := &captureRT{status: 200, body: "abcdefghij"}
	var buf bytes.Buffer
	tr := testTransport(&buf, WithLevel(LevelHeaders), WithRoundTripper(rt))
	_, err := tr.Download(t.Context(), port.Request{URL: u, Dest: dir, Limit: 4})
	if !domaintest.SameError(err, domain.OversizedResponse(5, 4)) {
		t.Fatalf("%v", err)
	}
	if strings.Count(log.String(), `msg="oversized response" bytes=5`) != 1 {
		t.Fatalf("%s", log.String())
	}
	if strings.Contains(buf.String(), "into a file") || !strings.Contains(buf.String(), "← body: more than 4 bytes — dropped") {
		t.Fatalf("%s", buf.String())
	}
	dirEmpty(t, dir)

	before := log.Len()
	named := filepath.Join(dir, "named")
	_, err = tr.Download(t.Context(), port.Request{
		URL:      u,
		Dest:     named,
		Expected: 100,
		Limit:    10,
	})
	if !domaintest.SameError(err, domain.OversizedResponse(100, 10)) {
		t.Fatalf("%v", err)
	}
	if log.Len() != before || rt.calls.Load() != 1 {
		t.Fatalf("log %d calls %d", log.Len(), rt.calls.Load())
	}
	if _, statErr := os.Stat(named); !os.IsNotExist(statErr) {
		t.Fatal(statErr)
	}
}

func TestUploadFileClosesOnce(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "body")
	if err := os.WriteFile(path, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	opened, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	f := &uploadFile{File: opened}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	// A plain *os.File would answer "file already closed" here.
	if err := f.Close(); err != nil {
		t.Fatalf("second close: %v", err)
	}
}

func TestDefaultTransportRefusesLoopback(t *testing.T) {
	t.Parallel()
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { hits.Add(1) }))
	t.Cleanup(srv.Close)
	tr := New(WithLevel(LevelOff))
	_, err := tr.RoundTrip(t.Context(), port.Request{URL: mustURL(t, srv.URL)})
	if !domaintest.SameError(err, domain.Transport(domain.ReasonBlocked)) {
		t.Fatalf("a loopback dial: %v", err)
	}
	if hits.Load() != 0 {
		t.Fatalf("hits %d", hits.Load())
	}
}

func TestOpenBreakerIsNotAMissingHost(t *testing.T) {
	t.Parallel()
	err := classify(t.Context(), fmt.Errorf("wrapped: %w", client.ErrCircuitBreakerOpen))
	if !domaintest.SameError(err, domain.Transport(domain.ReasonCircuitOpen)) {
		t.Fatal(err)
	}
	if domain.IsHostUnreachable(err) {
		t.Fatal("an open breaker demotes the host")
	}
}

func TestTransportTLS(t *testing.T) {
	t.Parallel()
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("ok")) }))
	t.Cleanup(srv.Close)
	u := mustURL(t, srv.URL)

	strange := testTransport(nil)
	if cfg := strange.memBase.TLSClientConfig; cfg == nil || cfg.MinVersion != tls.VersionTLS12 {
		t.Fatalf("tls %+v", cfg)
	}
	if _, err := strange.RoundTrip(t.Context(), port.Request{URL: u}); err == nil {
		t.Fatal("an unknown authority was trusted")
	}

	pool := x509.NewCertPool()
	pool.AddCert(srv.Certificate())
	trusted := testTransport(nil, WithRootCAs(pool))
	resp, err := trusted.RoundTrip(t.Context(), port.Request{URL: u})
	if err != nil || string(resp.Body) != "ok" {
		t.Fatalf("resp %q err %v", resp.Body, err)
	}
	if Pooled(pool).TLSClientConfig.RootCAs != pool {
		t.Fatal("Pooled dropped the roots")
	}
}
