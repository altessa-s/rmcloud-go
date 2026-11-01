// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package domain

import (
	"errors"
	"fmt"
	"math"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestErrorKindAndPayload(t *testing.T) {
	wrapped := fmt.Errorf("call: %w", HTTP(404, "/sync/v3/root"))
	if !IsKind(wrapped, KindHTTP) {
		t.Fatal("IsKind matches the kind through a wrap")
	}
	if IsKind(wrapped, KindTransport) || errors.Is(wrapped, ErrNotRegistered) {
		t.Fatal("a different kind must not match")
	}
	got, ok := errors.AsType[*HTTPError](wrapped)
	if !ok || *got != (HTTPError{Status: 404, Path: "/sync/v3/root"}) {
		t.Fatalf("payload = %#v", got)
	}
	if kind, ok := KindOf(errors.New("plain")); ok {
		t.Fatalf("a plain error has kind %q", kind)
	}
	if !errors.Is(fmt.Errorf("x: %w", ErrNotRegistered), ErrNotRegistered) {
		t.Fatal("a sentinel matches through a wrap")
	}
	if !sameError(wrapped, HTTP(404, "/sync/v3/root")) || sameError(wrapped, HTTP(404, "/sync/v4/root")) {
		t.Fatal("the path is part of the payload")
	}
}

func TestEveryKindHasAType(t *testing.T) {
	all := []Error{
		ErrNotRegistered, ErrInvalidOneTimeCode, ErrUnauthorized, ErrAccountWithoutTablet, ErrNoRootYet,
		UnsupportedSchema("9"), MalformedIndex("l", "r"), MalformedResponse("w"), HashMismatch("a", "b"),
		MissingHash("n"), RateLimited(nil), NotOnThisPlan("d"), RootConflict(412), HTTP(500, "/p"),
		WriteRejected(500, "d"), Transport(ReasonTimedOut), UnsupportedPlatform("s"),
		NothingToAsk("c"), OversizedResponse(2, 1),
	}
	seen := map[Kind]bool{}
	for _, e := range all {
		if seen[e.Kind()] {
			t.Fatalf("kind %q twice", e.Kind())
		}
		seen[e.Kind()] = true
		if !strings.HasPrefix(e.Error(), "rmcloud: ") {
			t.Fatalf("%q", e.Error())
		}
	}
	if len(seen) != 19 {
		t.Fatalf("kinds = %d", len(seen))
	}
	if RateLimited(new(time.Duration(0))).Error() != "rmcloud: rate limited, retry after 0s" || RateLimited(nil).Error() != "rmcloud: rate limited" {
		t.Fatal("a missing delay is not a zero delay")
	}
}

func TestFromHTTP(t *testing.T) {
	for _, status := range []int{200, 201, 202, 204, 206} {
		if got := FromHTTP(status, "https://h/sync/v3/root", "", nil, nil); got != nil {
			t.Fatalf("status %d = %#v", status, got)
		}
	}
	if got := FromHTTP(401, "https://h/token", "", nil, ErrInvalidOneTimeCode); got != ErrInvalidOneTimeCode {
		t.Fatalf("401 = %#v", got)
	}
	if got := FromHTTP(403, "https://h/token", "", nil, nil); got != ErrUnauthorized {
		t.Fatalf("403 = %#v", got)
	}
	if got := FromHTTP(402, "https://h/sync", "", []byte("  need connect  "), nil); !sameError(got, NotOnThisPlan("need connect")) {
		t.Fatalf("402 = %#v", got)
	}
	if got := FromHTTP(429, "https://h/sync?token=secret", " 1.5 ", nil, nil); !sameError(got, RateLimited(new(1500*time.Millisecond))) {
		t.Fatalf("429 = %#v", got)
	}
	got := FromHTTP(404, "https://h/sync/v3/files/abc?x=1", "", nil, nil)
	if !sameError(got, HTTP(404, "/sync/v3/files/abc")) {
		t.Fatalf("404 = %#v", got)
	}
	if !IsDocumentUnavailable(got) {
		t.Fatal("http 404 is document-local")
	}
	if IsDocumentUnavailable(HTTP(500, "/x")) || IsDocumentUnavailable(NotOnThisPlan("x")) || IsDocumentUnavailable(errors.New("x")) {
		t.Fatal("500, plan, and foreign errors are not document-local")
	}
	if !IsDocumentUnavailable(UnsupportedSchema("7")) || !IsDocumentUnavailable(MalformedIndex("", "empty index")) {
		t.Fatal("schema and index errors are document-local")
	}
}

// sameError reports whether err's chain holds an error equal to want.
func sameError(err, want error) bool {
	for e := err; e != nil; e = errors.Unwrap(e) {
		if reflect.DeepEqual(e, want) {
			return true
		}
	}
	return false
}

func TestParseRetryAfter(t *testing.T) {
	cases := []struct {
		header string
		want   *time.Duration
	}{
		{" 1.5 ", new(1500 * time.Millisecond)},
		{"+2", new(2 * time.Second)},
		{"1e1", new(10 * time.Second)},
		{"0", new(time.Duration(0))},
		{"-1", new(-time.Second)},
		{"Wed, 21 Oct 2015 07:28:00 GMT", nil},
		{"", nil},
		{"   ", nil},
		{"NaN", nil},
		{"inf", nil},
		{"-Infinity", nil},
		{"1e300", new(time.Duration(math.MaxInt64))},
		{"-1e300", new(time.Duration(math.MinInt64))},
	}
	for _, tc := range cases {
		got := ParseRetryAfter(tc.header)
		if !reflect.DeepEqual(got, tc.want) {
			t.Errorf("ParseRetryAfter(%q) = %v, want %v", tc.header, got, tc.want)
		}
	}
}

func TestDetailOf(t *testing.T) {
	if got := DetailOf([]byte("  hi \n")); got != "hi" {
		t.Fatalf("trim = %q", got)
	}
	raw := []byte{0xff, 'a'}
	got := DetailOf(raw)
	if !strings.Contains(got, "\uFFFD") || !strings.Contains(got, "a") {
		t.Fatalf("invalid utf-8 = %q", got)
	}
	long := bytesRepeat('x', DetailLimit+40)
	got = DetailOf(long)
	if len(got) != DetailLimit {
		t.Fatalf("len = %d", len(got))
	}
	if DetailOf(nil) != "" {
		t.Fatal("nil body is empty")
	}
}

func TestIsHostUnreachable(t *testing.T) {
	for _, reason := range []TransportReason{ReasonCannotFindHost, ReasonCannotConnectToHost, ReasonDNSLookupFailed} {
		if !IsHostUnreachable(fmt.Errorf("wrap: %w", Transport(reason))) {
			t.Fatalf("%s should mean the host is not there", reason)
		}
	}
	for _, reason := range []TransportReason{ReasonNotConnectedToInternet, ReasonNetworkConnectionLost, ReasonTimedOut} {
		if IsHostUnreachable(Transport(reason)) {
			t.Fatalf("%s is not host-not-there", reason)
		}
	}
	if IsHostUnreachable(ErrNotRegistered) {
		t.Fatal("a protocol error is not a missing host")
	}
}

func TestOversizedText(t *testing.T) {
	got := OversizedResponse(12, 10).Error()
	if got != "rmcloud: oversized response: 12 bytes over limit 10" {
		t.Fatalf("text = %q", got)
	}
}

func bytesRepeat(b byte, n int) []byte {
	out := make([]byte, n)
	for i := range out {
		out[i] = b
	}
	return out
}
