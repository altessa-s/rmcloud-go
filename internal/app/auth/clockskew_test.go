// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package auth

import (
	"bytes"
	"context"
	"log/slog"
	"maps"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/altessa-s/go-atlas-core/types/redacted"

	"github.com/altessa-s/rmcloud-go/internal/domain"
	"github.com/altessa-s/rmcloud-go/internal/porttest"
)

// A token is judged fresh by its own exp, and exp is the server's clock. On
// a machine whose clock runs hours ahead, a token good for three hours reads
// as dead on arrival. These tests hold the provider to the server's time,
// which the token's iat gives.

// skewServerNow is the server's clock in these tests, in Unix seconds.
const skewServerNow int64 = 1_794_313_113

const skewTokenLife = 3 * time.Hour

// issuedAt is a token the server issued at skewServerNow plus after, good
// for skewTokenLife by the server's clock.
func issuedAt(t *testing.T, after time.Duration, jti string) string {
	t.Helper()
	iat := skewServerNow + int64(after/time.Second)
	return tokenAt(t, iat+int64(skewTokenLife/time.Second), map[string]any{"iat": iat, "jti": jti})
}

// skewHarness is a registered provider whose clock stands ahead of the
// server's by ahead, logging into the returned buffer.
func skewHarness(t *testing.T, ahead time.Duration, initial map[domain.SecretName]string) (*harness, *bytes.Buffer) {
	t.Helper()
	secrets := map[domain.SecretName]string{domain.SecretDeviceToken: "device"}
	maps.Copy(secrets, initial)
	var buf bytes.Buffer
	log := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	clk := porttest.NewClockAt(time.Unix(skewServerNow, 0).Add(ahead))
	return newHarnessAt(t, clk, secrets, WithLogger(log)), &buf
}

func measuredSkew(p *Provider) *time.Duration {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.skew
}

func mustUserToken(t *testing.T, p *Provider, want string) {
	t.Helper()
	got, err := p.UserToken(t.Context())
	must(t, err)
	if got.Expose() != want {
		t.Fatalf("token = %q, want %q", got, want)
	}
}

func TestClockAheadKeepsTheBoughtToken(t *testing.T) {
	t.Parallel()
	h, _ := skewHarness(t, 6*time.Hour, nil)
	bought := issuedAt(t, 0, "bought")
	h.trip.Enqueue(porttest.Reply{Body: []byte(bought)})

	mustUserToken(t, h.p, bought)
	// In the store, where the next process finds it.
	if stored, ok := mustSecret(t, h.secrets, domain.SecretUserToken); !ok || stored != bought {
		t.Fatalf("stored = %q ok=%v", stored, ok)
	}
	// And in hand for the next request rather than bought again.
	mustUserToken(t, h.p, bought)
	if n := len(h.trip.Calls()); n != 1 {
		t.Fatalf("requests = %d: the token was bought again", n)
	}
	if skew := measuredSkew(h.p); skew == nil || *skew != 6*time.Hour {
		t.Fatalf("skew = %v", skew)
	}
}

func TestClockBehindIsJudgedInTheServersTime(t *testing.T) {
	t.Parallel()
	h, _ := skewHarness(t, -2*time.Hour, nil)
	bought := issuedAt(t, 0, "bought")
	h.trip.Enqueue(porttest.Reply{Body: []byte(bought)})

	mustUserToken(t, h.p, bought)
	mustUserToken(t, h.p, bought)
	if n := len(h.trip.Calls()); n != 1 {
		t.Fatalf("requests = %d", n)
	}
	if stored, ok := mustSecret(t, h.secrets, domain.SecretUserToken); !ok || stored != bought {
		t.Fatalf("stored = %q ok=%v", stored, ok)
	}
	if skew := measuredSkew(h.p); skew == nil || *skew != -2*time.Hour {
		t.Fatalf("skew = %v", skew)
	}

	// By the local clock the token has five hours left, by the server's
	// three. Past the server's three it is replaced, not handed out.
	h.clock.Advance(skewTokenLife)
	next := issuedAt(t, skewTokenLife, "next")
	h.trip.Enqueue(porttest.Reply{Body: []byte(next)})
	mustUserToken(t, h.p, next)
	if n := len(h.trip.Calls()); n != 2 {
		t.Fatalf("requests = %d: a token past the server's exp was reused", n)
	}
}

func TestCachedTokenExpiresInTheServersTime(t *testing.T) {
	t.Parallel()
	h, _ := skewHarness(t, 6*time.Hour, nil)
	bought := issuedAt(t, 0, "bought")
	h.trip.Enqueue(porttest.Reply{Body: []byte(bought)})
	mustUserToken(t, h.p, bought)

	// One second short of the margin before the server's exp: still in hand.
	h.clock.Advance(skewTokenLife - domain.ExpiryMargin - time.Second)
	mustUserToken(t, h.p, bought)
	if n := len(h.trip.Calls()); n != 1 {
		t.Fatalf("requests = %d", n)
	}

	// At the margin it is replaced.
	h.clock.Advance(time.Second)
	next := issuedAt(t, skewTokenLife-domain.ExpiryMargin, "next")
	h.trip.Enqueue(porttest.Reply{Body: []byte(next)})
	mustUserToken(t, h.p, next)
	if n := len(h.trip.Calls()); n != 2 {
		t.Fatalf("requests = %d", n)
	}
}

func TestTokenWithoutIatMeasuresNothing(t *testing.T) {
	t.Parallel()
	h, buf := skewHarness(t, 2*time.Second, nil)
	noIat := tokenAt(t, skewServerNow+int64(skewTokenLife/time.Second), nil)
	h.trip.Enqueue(porttest.Reply{Body: []byte(noIat)})
	mustUserToken(t, h.p, noIat)
	if skew := measuredSkew(h.p); skew != nil {
		t.Fatalf("skew = %v from a token without iat", *skew)
	}

	// A measure already taken is left as it was.
	h.p.InvalidateUserToken(redacted.RedactedString(noIat))
	h.clock.Advance(time.Hour)
	measured := issuedAt(t, 0, "measured")
	h.trip.Enqueue(porttest.Reply{Body: []byte(measured)})
	mustUserToken(t, h.p, measured)
	want := time.Hour + 2*time.Second
	if skew := measuredSkew(h.p); skew == nil || *skew != want {
		t.Fatalf("skew = %v, want %v", skew, want)
	}
	h.p.InvalidateUserToken(redacted.RedactedString(measured))
	h.trip.Enqueue(porttest.Reply{Body: []byte(noIat)})
	mustUserToken(t, h.p, noIat)
	if skew := measuredSkew(h.p); skew == nil || *skew != want {
		t.Fatalf("skew = %v after a token without iat, want %v", skew, want)
	}
	if n := strings.Count(buf.String(), "the local clock is off"); n != 1 {
		t.Fatalf("clock warnings = %d, want the one measurement past the tolerance:\n%s", n, buf)
	}
}

func TestStoredTokenIsJudgedWithTheSkew(t *testing.T) {
	t.Parallel()
	h, _ := skewHarness(t, 6*time.Hour, nil)
	bought := issuedAt(t, 0, "bought")
	h.trip.Enqueue(porttest.Reply{Body: []byte(bought)})
	mustUserToken(t, h.p, bought)

	// Another process wrote a token of its own into the store. By the local
	// clock it died three hours ago; by the server's it has three to live.
	theirs := issuedAt(t, 0, "theirs")
	must(t, h.secrets.Save(t.Context(), domain.SecretUserToken, theirs))
	h.p.InvalidateUserToken(redacted.RedactedString(bought))

	mustUserToken(t, h.p, theirs)
	if n := len(h.trip.Calls()); n != 1 {
		t.Fatalf("requests = %d: a stored token alive by the server's clock was replaced", n)
	}
}

func TestDeadStoredTokenIsStillReplaced(t *testing.T) {
	t.Parallel()
	// The stored token is four hours old by the server's clock: dead by
	// either clock. One purchase, kept.
	stale := issuedAt(t, -4*time.Hour, "stale")
	h, _ := skewHarness(t, 6*time.Hour, map[domain.SecretName]string{domain.SecretUserToken: stale})
	bought := issuedAt(t, 0, "bought")
	h.trip.Enqueue(porttest.Reply{Body: []byte(bought)})

	mustUserToken(t, h.p, bought)
	if stored, ok := mustSecret(t, h.secrets, domain.SecretUserToken); !ok || stored != bought {
		t.Fatalf("stored = %q ok=%v", stored, ok)
	}
	if n := len(h.trip.Calls()); n != 1 {
		t.Fatalf("requests = %d", n)
	}
}

func TestClockSkewLog(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		ahead time.Duration
		want  []string
	}{
		{name: "hours ahead", ahead: 6 * time.Hour, want: []string{"level=WARN", "seconds=21600", "direction=ahead", "date and time"}},
		{name: "hours behind", ahead: -2 * time.Hour, want: []string{"level=WARN", "seconds=7200", "direction=behind", "date and time"}},
		{name: "just past the tolerance", ahead: clockSkewTolerance + time.Second, want: []string{"seconds=301", "direction=ahead"}},
		{name: "at the tolerance", ahead: clockSkewTolerance},
		{name: "behind within the tolerance", ahead: -clockSkewTolerance},
		{name: "agreeing clocks", ahead: 2 * time.Second},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			h, buf := skewHarness(t, tt.ahead, nil)
			bought := issuedAt(t, 0, "bought")
			h.trip.Enqueue(porttest.Reply{Body: []byte(bought)})
			mustUserToken(t, h.p, bought)
			// A token in hand measures nothing, so it says nothing either.
			mustUserToken(t, h.p, bought)

			text := buf.String()
			if n := strings.Count(text, "the local clock is off"); n != min(len(tt.want), 1) {
				t.Fatalf("clock warnings = %d:\n%s", n, text)
			}
			for _, want := range tt.want {
				if !strings.Contains(text, want) {
					t.Fatalf("log lacks %q:\n%s", want, text)
				}
			}
			if strings.Contains(text, bought) {
				t.Fatalf("log contains a token:\n%s", text)
			}
			if skew := measuredSkew(h.p); skew == nil || *skew != tt.ahead {
				t.Fatalf("skew = %v, want %v", skew, tt.ahead)
			}
		})
	}
}

// TestClockSkewIsRaceFree buys, reads and invalidates from many goroutines,
// so the skew is written by one buy while other callers judge tokens with
// it. The race detector is the assertion.
func TestClockSkewIsRaceFree(t *testing.T) {
	t.Parallel()
	const callers, rounds = 8, 25
	h, _ := skewHarness(t, 6*time.Hour, nil)
	for i := range callers * rounds {
		h.trip.Enqueue(porttest.Reply{Body: []byte(issuedAt(t, time.Duration(i)*time.Second, "race"))})
	}

	var wg sync.WaitGroup
	for range callers {
		wg.Go(func() {
			for range rounds {
				token, err := h.p.UserToken(context.Background())
				if err != nil {
					t.Errorf("UserToken: %v", err)
					return
				}
				if !domain.IsFresh(token.Expose(), time.Unix(skewServerNow, 0)) {
					t.Errorf("a token dead by the server's clock was handed out")
					return
				}
				h.p.InvalidateUserToken(token)
			}
		})
	}
	wg.Wait()
	if n := len(h.trip.Calls()); n == 0 || n > callers*rounds {
		t.Fatalf("requests = %d", n)
	}
	if skew := measuredSkew(h.p); skew == nil {
		t.Fatal("no skew was measured")
	}
}
