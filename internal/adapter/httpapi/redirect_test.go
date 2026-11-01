// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package httpapi

import (
	"bytes"
	"errors"
	"io"
	"net/http"
	"net/url"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/altessa-s/rmcloud-go/internal/port"
)

func TestRedirectStopsAfterTen(t *testing.T) {
	t.Parallel()

	var calls atomic.Int32
	rt := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		n := calls.Add(1)
		h := http.Header{"Location": {fmtHost(n)}}
		return httpResp(r, http.StatusFound, h, ""), nil
	})
	var buf bytes.Buffer
	tr := testTransport(&buf, WithLevel(LevelHeaders), WithRoundTripper(rt))
	_, err := tr.RoundTrip(t.Context(), port.Request{URL: mustURL(t, "https://h0.example/x")})
	ue, ok := errors.AsType[*url.Error](err)
	if !ok || ue == nil || !strings.Contains(err.Error(), "stopped after 10 redirects") {
		t.Fatalf("%T %v", err, err)
	}
	if calls.Load() != 10 {
		t.Fatalf("trips %d", calls.Load())
	}
	log := buf.String()
	if strings.Count(log, "→ GET ") != 10 || strings.Count(log, "→→") != 9 {
		t.Fatalf("lines get=%d redirect=%d\n%s", strings.Count(log, "→ GET "), strings.Count(log, "→→"), log)
	}
	prefix, fail, ok := strings.Cut(log, "←!")
	if !ok || strings.Contains(prefix, "h10") || !strings.Contains(prefix, "h9.example") {
		t.Fatalf("prefix\n%s", prefix)
	}
	want := `GET https://h0.example/x — Get "https://h10.example/x": stopped after 10 redirects`
	if !strings.Contains(fail, want) {
		t.Fatalf("fail %q", fail)
	}
}

func TestRedirectTokenStaysOnOrigin(t *testing.T) {
	logs := captureLog(t)
	tr := testTransport(nil, WithLevel(LevelOff))

	start := reqWithAuth(t, "https://a.example/x", "Bearer T")
	same := hopTo(t, start, "https://a.example/y")
	must(t, tr.checkRedirect(same, []*http.Request{start}))
	if authorizationOf(same.Header) != "Bearer T" {
		t.Fatal("same origin dropped the token")
	}

	away := hopTo(t, start, "https://b.example/y")
	must(t, tr.checkRedirect(away, []*http.Request{start}))
	if authorizationOf(away.Header) != "" {
		t.Fatal("left the token on b.example")
	}

	bReq := hopTo(t, start, "https://b.example/y")
	must(t, tr.checkRedirect(bReq, []*http.Request{start}))
	back := hopTo(t, bReq, "https://a.example/z")
	must(t, tr.checkRedirect(back, []*http.Request{start, bReq}))
	if authorizationOf(back.Header) != "Bearer T" {
		t.Fatal("return hop did not restore the token")
	}

	mixed := hopTo(t, start, "https://B.Example/y")
	must(t, tr.checkRedirect(mixed, []*http.Request{start}))
	if authorizationOf(mixed.Header) != "" {
		t.Fatal("mixed-case host kept the token")
	}

	spelled := reqWithAuth(t, "https://A.Example/x", "Bearer T")
	folded := hopTo(t, spelled, "https://a.example/y")
	must(t, tr.checkRedirect(folded, []*http.Request{spelled}))
	if authorizationOf(folded.Header) != "Bearer T" {
		t.Fatal("spelling change dropped the token")
	}

	host := reqWithAuth(t, "https://host.example/x", "Bearer T")
	high := hopTo(t, host, "https://host.example:8443/y")
	must(t, tr.checkRedirect(high, []*http.Request{host}))
	if authorizationOf(high.Header) != "" {
		t.Fatal("high port kept the token")
	}
	deflt := hopTo(t, host, "https://host.example:443/y")
	must(t, tr.checkRedirect(deflt, []*http.Request{host}))
	if authorizationOf(deflt.Header) != "Bearer T" {
		t.Fatal("explicit 443 dropped the token")
	}

	plain := reqWithAuth(t, "http://a.example/x", "Bearer T")
	tls := hopTo(t, plain, "https://a.example/y")
	must(t, tr.checkRedirect(tls, []*http.Request{plain}))
	if authorizationOf(tls.Header) != "" {
		t.Fatal("scheme change kept the token")
	}

	sub := hopTo(t, start, "https://sub.a.example/y")
	must(t, tr.checkRedirect(sub, []*http.Request{start}))
	if authorizationOf(sub.Header) != "" {
		t.Fatal("subdomain kept the token")
	}

	var auths []string
	clientRT := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		auths = append(auths, authorizationOf(r.Header))
		h := make(http.Header)
		status := http.StatusOK
		switch len(auths) {
		case 1:
			h.Set("Location", "https://b.example/y")
			status = http.StatusFound
		case 2:
			h.Set("Location", "https://a.example/z")
			status = http.StatusFound
		}
		return httpResp(r, status, h, "ok"), nil
	})
	client := testTransport(nil, WithLevel(LevelOff), WithRoundTripper(clientRT))
	resp, err := client.RoundTrip(t.Context(), port.Request{
		URL:    mustURL(t, "https://a.example/x"),
		Header: port.Header{"Authorization": {"Bearer T"}},
	})
	must(t, err)
	if resp.Status != http.StatusOK {
		t.Fatalf("status %d", resp.Status)
	}
	if len(auths) != 3 || auths[0] != "Bearer T" || auths[1] != "" || auths[2] != "Bearer T" {
		t.Fatalf("auths %#v", auths)
	}

	text := logs.String()
	counts := []struct {
		phrase string
		n      int
	}{
		{"not carried\" host=b.example\n", 3},
		{"not carried\" host=a.example\n", 3},
		{"not carried\" host=B.Example\n", 1},
		{"not carried\" host=host.example\n", 1},
		{"not carried\" host=sub.a.example\n", 1},
	}
	for _, c := range counts {
		if got := strings.Count(text, c.phrase); got != c.n {
			t.Errorf("%q count %d\n%s", c.phrase, got, text)
		}
	}
}

func TestCheckRedirectShared(t *testing.T) {
	t.Parallel()

	tr := testTransport(nil)
	mem := reflect.ValueOf(tr.memory.CheckRedirect).Pointer()
	dl := reflect.ValueOf(tr.download.CheckRedirect).Pointer()
	if mem == 0 || mem != dl {
		t.Fatalf("memory %x download %x", mem, dl)
	}
}

func TestRedirectRewritesMethod(t *testing.T) {
	t.Parallel()

	post := func(t *testing.T, status int) (methods, bodies []string, log string) {
		t.Helper()
		var buf bytes.Buffer
		rt := roundTripFunc(func(r *http.Request) (*http.Response, error) {
			methods = append(methods, r.Method)
			if r.Body == nil {
				bodies = append(bodies, "")
			} else {
				b, err := io.ReadAll(r.Body)
				must(t, err)
				bodies = append(bodies, string(b))
			}
			if len(methods) == 1 {
				h := http.Header{"Location": {"https://b.example/y"}}
				return httpResp(r, status, h, ""), nil
			}
			return httpResp(r, http.StatusOK, nil, "ok"), nil
		})
		tr := testTransport(&buf, WithLevel(LevelHeaders), WithRoundTripper(rt))
		resp, err := tr.RoundTrip(t.Context(), port.Request{
			Method: http.MethodPost,
			URL:    mustURL(t, "https://a.example/x"),
			Body:   []byte("payload"),
		})
		must(t, err)
		if resp.Status != http.StatusOK || string(resp.Body) != "ok" {
			t.Fatalf("status %d body %q", resp.Status, resp.Body)
		}
		return methods, bodies, buf.String()
	}

	t.Run("302 becomes GET", func(t *testing.T) {
		t.Parallel()
		methods, bodies, log := post(t, http.StatusFound)
		if len(methods) != 2 || methods[0] != http.MethodPost || methods[1] != http.MethodGet {
			t.Fatalf("methods %#v", methods)
		}
		if len(bodies) != 2 || bodies[0] != "payload" || bodies[1] != "" {
			t.Fatalf("bodies %#v", bodies)
		}
		for _, line := range []string{
			"→ POST https://a.example/x",
			"→ GET https://b.example/y",
			"← 200 GET https://b.example/y",
		} {
			if !strings.Contains(log, line) {
				t.Errorf("missing %s\n%s", line, log)
			}
		}
		if strings.Contains(log, "← 200 POST") || strings.Contains(log, "payload") {
			t.Fatalf("log\n%s", log)
		}
	})

	t.Run("307 keeps POST", func(t *testing.T) {
		t.Parallel()
		methods, bodies, log := post(t, http.StatusTemporaryRedirect)
		if len(methods) != 2 || methods[0] != http.MethodPost || methods[1] != http.MethodPost {
			t.Fatalf("methods %#v", methods)
		}
		if len(bodies) != 2 || bodies[0] != "payload" || bodies[1] != "payload" {
			t.Fatalf("bodies %#v", bodies)
		}
		if strings.Count(log, "→ POST ") != 2 || !strings.Contains(log, "← 200 POST https://b.example/y") || strings.Contains(log, "payload") {
			t.Fatalf("log\n%s", log)
		}
	})
}

func fmtHost(n int32) string {
	return "https://h" + itoa(n) + ".example/x"
}

func itoa(n int32) string {
	if n == 0 {
		return "0"
	}
	var b [12]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}

func reqWithAuth(t *testing.T, raw, token string) *http.Request {
	t.Helper()
	r, err := http.NewRequestWithContext(t.Context(), http.MethodGet, raw, nil)
	must(t, err)
	if token != "" {
		r.Header.Set("Authorization", token)
	}
	return r
}

func hopTo(t *testing.T, prev *http.Request, raw string) *http.Request {
	t.Helper()
	r, err := http.NewRequestWithContext(t.Context(), http.MethodGet, raw, nil)
	must(t, err)
	r.Response = &http.Response{Request: prev, StatusCode: http.StatusFound}
	return r
}
