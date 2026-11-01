// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package retry

import (
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/altessa-s/rmcloud-go/internal/domain"
)

func TestShouldRetry(t *testing.T) {
	t.Parallel()
	five := 5 * time.Second
	cases := []struct {
		name  string
		err   domain.Error
		retry bool
	}{
		{"transport", domain.Transport(domain.ReasonNotConnectedToInternet), true},
		{"rate limited", domain.RateLimited(&five), true},
		{"rate limited without a delay", domain.RateLimited(nil), true},
		{"http 500", domain.HTTP(500, "/x"), true},
		{"http 503", domain.HTTP(503, "/sync/v3/root"), true},
		{"http 599", domain.HTTP(599, "/x"), true},
		{"write rejected 502", domain.WriteRejected(502, "blob"), true},
		{"http 400", domain.HTTP(400, "/sync/v3/files/abc"), false},
		{"http 404", domain.HTTP(404, "/x"), false},
		{"http 429 is not a wait", domain.HTTP(429, "/x"), false},
		{"write rejected 409", domain.WriteRejected(409, "blob"), false},
		{"unauthorized", domain.ErrUnauthorized, false},
		{"schema", domain.UnsupportedSchema("9"), false},
		{"hash", domain.HashMismatch("a", "b"), false},
		{"not registered", domain.ErrNotRegistered, false},
		{"one-time code", domain.ErrInvalidOneTimeCode, false},
		{"no tablet", domain.ErrAccountWithoutTablet, false},
		{"no root", domain.ErrNoRootYet, false},
		{"index", domain.MalformedIndex("", "empty index"), false},
		{"response", domain.MalformedResponse("root response"), false},
		{"missing hash", domain.MissingHash("x"), false},
		{"plan", domain.NotOnThisPlan("detail"), false},
		{"conflict", domain.RootConflict(412), false},
		{"platform", domain.UnsupportedPlatform("notification socket"), false},
		{"nothing to ask", domain.NothingToAsk("check-files"), false},
		{"oversized", domain.OversizedResponse(10, 4), false},
	}
	if ShouldRetry(nil) {
		t.Fatal("a nil error is not retried")
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if ShouldRetry(tc.err) != tc.retry {
				t.Fatalf("retry = %v, want %v", !tc.retry, tc.retry)
			}
		})
	}
}

func TestBackoffGrows(t *testing.T) {
	t.Parallel()
	policy := Default()
	policy.BaseDelay = 100 * time.Millisecond
	policy.Multiplier = 3
	policy.Jitter = 0
	backoff := policy.backoff()

	// The core loop numbers repeats from zero: 0 is the wait before the second try.
	for attempt, want := range map[int]time.Duration{0: 100 * time.Millisecond, 1: 300 * time.Millisecond, 2: 900 * time.Millisecond} {
		if got := backoff(attempt, errors.New("x")); got != want {
			t.Fatalf("attempt %d: %v, want %v", attempt, got, want)
		}
	}
}

func TestBackoffJitterOnlyAdds(t *testing.T) {
	t.Parallel()
	policy := Default()
	policy.BaseDelay = time.Second
	policy.Jitter = 0.2
	backoff := policy.backoff()
	for range 100 {
		if got := backoff(0, errors.New("x")); got < time.Second || got >= 1200*time.Millisecond {
			t.Fatalf("jittered %v", got)
		}
	}
	limited := &domain.RateLimitedError{RetryAfter: 5 * time.Second, HasRetryAfter: true}
	for range 100 {
		if got := backoff(0, limited); got != 5*time.Second {
			t.Fatalf("Retry-After was spread: %v", got)
		}
	}
}

func TestBackoffHonoursRetryAfterWithinTheCeiling(t *testing.T) {
	t.Parallel()
	policy := Default()
	policy.Jitter = 0
	backoff := policy.backoff()
	after := func(d time.Duration) error {
		return fmt.Errorf("wrapped: %w", &domain.RateLimitedError{RetryAfter: d, HasRetryAfter: true})
	}
	for _, tc := range []struct {
		after, want time.Duration
	}{
		{7 * time.Second, 7 * time.Second},
		{time.Hour, 20 * time.Second},
		{0, 300 * time.Millisecond},
		{-time.Second, 300 * time.Millisecond},
	} {
		if got := backoff(0, after(tc.after)); got != tc.want {
			t.Fatalf("Retry-After %v came back as %v, want %v", tc.after, got, tc.want)
		}
	}
}

func TestNoRetryForAnOpenBreakerOrAPolicyRefusal(t *testing.T) {
	t.Parallel()
	for _, reason := range []domain.TransportReason{domain.ReasonCircuitOpen, domain.ReasonBlocked} {
		if ShouldRetry(domain.Transport(reason)) {
			t.Errorf("%s is retried", reason)
		}
	}
	if !ShouldRetry(domain.Transport(domain.ReasonNetworkConnectionLost)) {
		t.Error("a lost connection is not retried")
	}
}
