// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package httpapi

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

func TestParseLevel(t *testing.T) {
	t.Parallel()

	cases := []struct {
		raw  string
		want Level
	}{
		{"headers", LevelHeaders},
		{"1", LevelHeaders},
		{"true", LevelHeaders},
		{"yes", LevelHeaders},
		{"on", LevelHeaders},
		{"HEADERS", LevelHeaders},
		{"bodies", LevelBodies},
		{"body", LevelBodies},
		{"full", LevelBodies},
		{"2", LevelBodies},
		{"BODY", LevelBodies},
		{"", LevelOff},
		{"no", LevelOff},
		{"verbose", LevelOff},
		{" headers", LevelOff},
		{"headers ", LevelOff},
		{"off", LevelOff},
	}
	for _, tc := range cases {
		if got := ParseLevel(tc.raw); got != tc.want {
			t.Errorf("ParseLevel(%q) = %s, want %s", tc.raw, got, tc.want)
		}
	}
	if LevelOff.String() != "off" || LevelHeaders.String() != "headers" || LevelBodies.String() != "bodies" || Level(9).String() != "off" {
		t.Fatalf("names off=%s headers=%s bodies=%s unknown=%s", LevelOff, LevelHeaders, LevelBodies, Level(9))
	}
}

func TestWireAnnounce(t *testing.T) {
	t.Parallel()

	u := mustURL(t, "https://host.example/sync/v4/root")
	ew := &errWriter{}
	w := &wire{level: LevelHeaders, writer: ew}
	w.sent(http.MethodGet, &u, nil, nil, true)
	w.sent(http.MethodGet, &u, nil, nil, true)
	if n := strings.Count(ew.String(), "wire log on (headers): credentials are masked, query strings dropped"); n != 1 {
		t.Fatalf("announce count %d\n%s", n, ew.String())
	}
	if strings.Count(ew.String(), "→ GET ") != 2 {
		t.Fatalf("requests\n%s", ew.String())
	}

	var off bytes.Buffer
	wo := &wire{level: LevelOff, writer: &off}
	wo.sent(http.MethodGet, &u, http.Header{"Accept": {"*/*"}}, []byte("x"), false)
	wo.received(200, http.MethodGet, &u, nil, []byte("x"), false, 1)
	wo.redirected(302, &u)
	wo.failed(http.MethodGet, &u, "boom")
	if off.Len() != 0 {
		t.Fatalf("off wrote %q", off.String())
	}

	var quiet bytes.Buffer
	wb := &wire{level: LevelBodies, writer: &quiet}
	wb.redirected(302, &u)
	wb.received(200, http.MethodPost, &u, nil, []byte("hi"), false, 2)
	wb.failed(http.MethodGet, &u, "boom")
	if strings.Contains(quiet.String(), "wire log on") || strings.Contains(quiet.String(), "shape of the library") {
		t.Fatalf("announced without sent\n%s", quiet.String())
	}
	wb.sent(http.MethodGet, &u, nil, []byte("hi"), false)
	if n := strings.Count(quiet.String(), "wire log on (bodies): credentials are masked, query strings dropped"); n != 1 {
		t.Fatalf("bodies announce %d\n%s", n, quiet.String())
	}
	if n := strings.Count(quiet.String(), "wire log: bodies include document names and the shape of the library"); n != 1 {
		t.Fatalf("bodies note %d\n%s", n, quiet.String())
	}
}

func TestSlogDebugWriter(t *testing.T) {
	buf := captureLog(t)
	if _, err := (slogDebugWriter{}).Write([]byte("alpha\n     beta\n")); err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(buf.String(), "level=DEBUG"); n != 2 {
		t.Fatalf("debug lines %d\n%s", n, buf.String())
	}
	if !strings.Contains(buf.String(), "alpha") || !strings.Contains(buf.String(), "beta") {
		t.Fatalf("lines\n%s", buf.String())
	}
	buf.Reset()
	if _, err := (slogDebugWriter{}).Write([]byte("\n\n")); err != nil {
		t.Fatal(err)
	}
	if buf.Len() != 0 {
		t.Fatalf("blank write logged %q", buf.String())
	}
}

func TestMaskAndHeaderOrder(t *testing.T) {
	t.Parallel()

	if maskSecret("") != "<empty>" || maskSecret("Bearer ") != "Bearer <empty>" {
		t.Fatalf("empty %q bearer %q", maskSecret(""), maskSecret("Bearer "))
	}
	one := "Bearer eyJhbGciOiJub25lIn0.one.sig"
	two := "Bearer eyJhbGciOiJub25lIn0.two.sig"
	m1 := expectMask(one)
	m2 := expectMask(two)
	if m1 == m2 || !strings.Contains(m1, "Bearer <len:") || strings.Contains(m1, "eyJ") || strings.Contains(m2, "eyJ") {
		t.Fatalf("masks %s %s", m1, m2)
	}

	h := http.Header{
		"Accept":            {"text/plain", "application/json"},
		"accept":            {"*/*"},
		"Authorization":     {one},
		"authorization":     {two},
		"Cookie":            {"sid=eyJc"},
		"rm-filename":       {"root.docSchema", "roothash"},
		"rm-token-ttl-hint": {"4653"},
		"X-Auth-Token":      {"eyJtoken"},
		"x-goog-signature":  {"eyJsigvalue"},
	}
	var b strings.Builder
	writeHeaders(&b, "→", h)
	want := strings.Join([]string{
		"  → Accept: text/plain",
		"  → Accept: application/json",
		"  → accept: */*",
		"  → Authorization: " + m1,
		"  → authorization: " + m2,
		"  → Cookie: " + expectMask("sid=eyJc"),
		"  → rm-filename: root.docSchema",
		"  → rm-filename: roothash",
		"  → rm-token-ttl-hint: 4653",
		"  → X-Auth-Token: " + expectMask("eyJtoken"),
		"  → x-goog-signature: " + expectMask("eyJsigvalue"),
		"",
	}, "\n")
	if b.String() != want {
		t.Fatalf("headers\n%s\nwant\n%s", b.String(), want)
	}
	if strings.Contains(b.String(), "eyJ") {
		t.Fatalf("secret leaked\n%s", b.String())
	}

	var buf bytes.Buffer
	u := mustURL(t, "https://host.example/x")
	w := &wire{level: LevelHeaders, writer: &buf, announced: true}
	w.sent(http.MethodGet, &u, http.Header{"Authorization": {"", "Bearer "}}, nil, true)
	got := buf.String()
	if !strings.Contains(got, "  → Authorization: <empty>") || !strings.Contains(got, "  → Authorization: Bearer <empty>") {
		t.Fatalf("empty sent\n%s", got)
	}

	buf.Reset()
	root := mustURL(t, "https://eu.tectonic.remarkable.com/sync/v4/root")
	w.sent(http.MethodGet, &root, http.Header{"rm-filename": {"roothash"}}, nil, true)
	first, _, _ := strings.Cut(buf.String(), "\n")
	if first != "→ GET https://eu.tectonic.remarkable.com/sync/v4/root" || !strings.Contains(buf.String(), "  → rm-filename: roothash") {
		t.Fatalf("golden request\n%s", buf.String())
	}
}

func TestSafeURL(t *testing.T) {
	t.Parallel()

	if safeURL(nil) != "<no url>" {
		t.Fatal(safeURL(nil))
	}
	kept := mustURL(t, "https://user:secret@host/path?a=1#frag")
	before := kept.String()
	if got := safeURL(&kept); got != "https://user:secret@host/path#frag (+1 query items)" {
		t.Fatal(got)
	}
	if kept.String() != before {
		t.Fatal("safeURL mutated the URL")
	}

	two := mustURL(t, "https://host/p?a=1&a=2")
	if got := safeURL(&two); got != "https://host/p (+2 query items)" || strings.Contains(got, "a=1") {
		t.Fatal(got)
	}
	gaps := mustURL(t, "https://host/p?a=1&&b=2")
	if got := safeURL(&gaps); !strings.HasSuffix(got, "(+2 query items)") || strings.Contains(got, "b=2") {
		t.Fatal(got)
	}
	bare := url.URL{Scheme: "https", Host: "host", Path: "/p", ForceQuery: true}
	if got := safeURL(&bare); got != "https://host/p" {
		t.Fatal(got)
	}
	opaque := url.URL{Scheme: "https", Host: "host", Opaque: "//hidden", Path: "/visible"}
	if got := safeURL(&opaque); !strings.Contains(got, "https://host/visible") || strings.Contains(got, "hidden") {
		t.Fatal(got)
	}

	var buf bytes.Buffer
	signed := mustURL(t, "https://host/p?a=1&sig=abc")
	w := &wire{level: LevelHeaders, writer: &buf, announced: true}
	w.sent(http.MethodGet, &signed, nil, nil, true)
	if strings.Contains(buf.String(), "sig=") || !strings.Contains(buf.String(), "(+2 query items)") {
		t.Fatal(buf.String())
	}
}

func TestWireBody(t *testing.T) {
	t.Parallel()

	if got := bodyLine("←", nil, nil, []byte("ignored"), true, 0, false, LevelHeaders); got != "  ← body: 0 bytes into a file\n" {
		t.Fatalf("empty file %q", got)
	}
	if got := bodyLine("→", nil, nil, []byte("payload"), false, 0, true, LevelBodies); got != "" {
		t.Fatalf("skip %q", got)
	}
	if got := bodyLine("→", nil, nil, nil, false, 0, false, LevelBodies); got != "" {
		t.Fatalf("empty %q", got)
	}

	cred := mustURL(t, "https://auth.example/token/json/2/user/new")
	secret := []byte("header.eyJleHAiOjF9.signature")
	for _, level := range []Level{LevelHeaders, LevelBodies} {
		got := bodyLine("→", &cred, nil, secret, false, 0, false, level)
		want := fmt.Sprintf("  → body: %d bytes (credential exchange — never printed)\n", len(secret))
		if got != want || strings.Contains(got, "eyJ") {
			t.Fatalf("level %s %q", level, got)
		}
	}
	if got := bodyLine("→", &cred, nil, nil, false, 0, false, LevelBodies); got != "" {
		t.Fatalf("empty credential %q", got)
	}

	plain := http.Header{"Content-Type": {"application/json"}}
	if got := bodyLine("→", nil, plain, []byte("hello"), false, 0, false, LevelHeaders); got != "  → body: 5 bytes\n" {
		t.Fatalf("headers count %q", got)
	}
	for _, ct := range []string{"application/json", "TEXT/PLAIN", "application/xml", "text/plain; charset=UTF-8", "application/json; charset=utf-8"} {
		got := bodyLine("←", nil, http.Header{"Content-Type": {ct}}, []byte("ok"), false, 0, false, LevelBodies)
		if got != "  ← body: ok\n" {
			t.Fatalf("%s %q", ct, got)
		}
	}
	if got := bodyLine("←", nil, http.Header{"Content-Type": {"text/plain"}}, []byte("a\x00b"), false, 0, false, LevelBodies); got != "  ← body: a\x00b\n" {
		t.Fatalf("nul %q", got)
	}
	if got := bodyLine("←", nil, http.Header{"Content-Type": {"text/plain"}}, []byte("one\ntwo"), false, 0, false, LevelBodies); got != "  ← body: one\n     two\n" {
		t.Fatalf("newline %q", got)
	}

	long := bytes.Repeat([]byte("a"), 4106)
	wantLong := "  ← body: " + strings.Repeat("a", bodyLimit) + fmt.Sprintf("… (%d bytes in all)\n", len(long))
	if got := bodyLine("←", nil, http.Header{"Content-Type": {"text/plain"}}, long, false, 0, false, LevelBodies); got != wantLong {
		t.Fatalf("ellipsis len %d", len(got))
	}
	cut := append(bytes.Repeat([]byte("a"), bodyLimit-1), 0xE2, 0x82, 0xAC)
	if got := bodyLine("←", nil, http.Header{"Content-Type": {"text/plain"}}, cut, false, 0, false, LevelBodies); got != fmt.Sprintf("  ← body: %d bytes\n", len(cut)) || strings.Contains(got, "…") {
		t.Fatalf("mid-rune %q", got)
	}
	for _, ct := range []string{"application/octet-stream", "application/pdf", ""} {
		h := http.Header{}
		if ct != "" {
			h.Set("Content-Type", ct)
		} else {
			h = nil
		}
		got := bodyLine("←", nil, h, []byte("UNIQUE"), false, 0, false, LevelBodies)
		if got != "  ← body: 6 bytes\n" || strings.Contains(got, "UNIQUE") {
			t.Fatalf("%q %q", ct, got)
		}
	}

	var buf bytes.Buffer
	check := mustURL(t, "https://host/sync/v3/check-files")
	w := &wire{level: LevelBodies, writer: &buf}
	w.received(400, http.MethodPost, &check, http.Header{"Content-Type": {"application/json"}}, []byte("invalid root schema"), false, 0)
	first, _, _ := strings.Cut(buf.String(), "\n")
	if first != "← 400 POST https://host/sync/v3/check-files" || !strings.Contains(buf.String(), "invalid root schema") || strings.Contains(buf.String(), "wire log on") {
		t.Fatalf("golden response\n%s", buf.String())
	}

	buf.Reset()
	w.announced = true
	w.level = LevelHeaders
	w.sent("", &check, nil, nil, true)
	first, _, _ = strings.Cut(buf.String(), "\n")
	if first != "→ GET https://host/sync/v3/check-files" {
		t.Fatal(first)
	}

	buf.Reset()
	w.oversized(200, http.MethodGet, &check, http.Header{"Content-Type": {"text/plain"}}, 32)
	if !strings.Contains(buf.String(), "← body: more than 32 bytes — dropped") {
		t.Fatalf("oversized log\n%s", buf.String())
	}
	buf.Reset()
	w.received(200, http.MethodGet, &check, nil, nil, true, 0)
	if !strings.Contains(buf.String(), "  ← body: 0 bytes into a file") {
		t.Fatal(buf.String())
	}
}

func bodyLine(arrow string, u *url.URL, h http.Header, body []byte, intoFile bool, n int64, skip bool, level Level) string {
	var b strings.Builder
	writeBody(&b, arrow, u, h, body, intoFile, n, skip, level)
	return b.String()
}

// expectMask repeats the wire mask without calling maskSecret.
func expectMask(value string) string {
	scheme, rest, ok := strings.Cut(value, " ")
	secret := value
	prefix := ""
	if ok {
		prefix = scheme + " "
		secret = rest
	}
	if secret == "" {
		return prefix + "<empty>"
	}
	mac := hmac.New(sha256.New, maskKey())
	_, _ = mac.Write([]byte(secret))
	sum := mac.Sum(nil)
	return fmt.Sprintf("%s<len:%d:%x>", prefix, len(secret), sum[:4])
}

type errWriter struct {
	bytes.Buffer
}

func (w *errWriter) Write(p []byte) (int, error) {
	w.Buffer.Write(p)
	return len(p), errors.New("write failed")
}
