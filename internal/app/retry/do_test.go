// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package retry

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/altessa-s/go-atlas-core/time/clock"

	"github.com/altessa-s/rmcloud-go/internal/domain"
	"github.com/altessa-s/rmcloud-go/internal/domaintest"
	"github.com/altessa-s/rmcloud-go/internal/porttest"
)

func TestDoSucceedsOnTheSecondAttempt(t *testing.T) {
	t.Parallel()
	sleeper := porttest.NewClock()
	calls := 0
	got, err := Run(context.Background(), Default().On(sleeper), func(context.Context) (string, error) {
		calls++
		if calls == 1 {
			return "", domain.Transport(domain.ReasonNotConnectedToInternet)
		}
		return "done", nil
	})
	if err != nil || got != "done" || calls != 2 {
		t.Fatalf("got %q err %v calls %d", got, err, calls)
	}
	if len(sleeper.Delays()) != 1 {
		t.Fatalf("delays %v", sleeper.Delays())
	}
}

func TestDoReturnsTheLastError(t *testing.T) {
	t.Parallel()
	sleeper := porttest.NewClock()
	policy := Default()
	policy.Jitter = 0
	calls := 0
	_, err := Run(context.Background(), policy.On(sleeper), func(context.Context) (int, error) {
		calls++
		return 0, domain.HTTP(503, "/x")
	})
	// Equal checks the path. Is matches the kind alone, so a 503 for another
	// path would pass it.
	if !domaintest.SameError(err, domain.HTTP(503, "/x")) {
		t.Fatalf("%v", err)
	}
	if calls != 3 || len(sleeper.Delays()) != 2 {
		t.Fatalf("calls %d delays %v", calls, sleeper.Delays())
	}
}

func TestDoDoesNotSleepOnAPermanentFailure(t *testing.T) {
	t.Parallel()
	sleeper := porttest.NewClock()
	calls := 0
	_, err := Run(context.Background(), Default().On(sleeper), func(context.Context) (int, error) {
		calls++
		return 0, domain.ErrUnauthorized
	})
	if !domaintest.SameError(err, domain.ErrUnauthorized) {
		t.Fatalf("%v", err)
	}
	if calls != 1 || len(sleeper.Delays()) != 0 {
		t.Fatalf("calls %d delays %v", calls, sleeper.Delays())
	}
}

func TestDoIgnoresAForeignError(t *testing.T) {
	t.Parallel()
	sleeper := porttest.NewClock()
	want := errors.New("nope")
	calls := 0
	_, err := Run(context.Background(), Default().On(sleeper), func(context.Context) (int, error) {
		calls++
		return 0, want
	})
	if !errors.Is(err, want) || calls != 1 || len(sleeper.Delays()) != 0 {
		t.Fatalf("err %v calls %d delays %v", err, calls, sleeper.Delays())
	}
}

func TestDoRetriesAWrappedProtocolError(t *testing.T) {
	t.Parallel()
	sleeper := porttest.NewClock()
	policy := Default()
	policy.MaxAttempts = 2
	policy.Jitter = 0
	calls := 0
	_, err := Run(context.Background(), policy.On(sleeper), func(context.Context) (int, error) {
		calls++
		return 0, fmt.Errorf("dial: %w", domain.Transport(domain.ReasonTimedOut))
	})
	if !domaintest.SameError(err, domain.Transport(domain.ReasonTimedOut)) {
		t.Fatalf("%v", err)
	}
	if calls != 2 || len(sleeper.Delays()) != 1 {
		t.Fatalf("calls %d delays %v", calls, sleeper.Delays())
	}
}

func TestDoUsesTheServerDelay(t *testing.T) {
	t.Parallel()
	sleeper := porttest.NewClock()
	wait := 5 * time.Second
	calls := 0
	_, err := Run(context.Background(), Default().On(sleeper), func(context.Context) (string, error) {
		calls++
		if calls == 1 {
			return "", domain.RateLimited(&wait)
		}
		return "ok", nil
	})
	if err != nil || calls != 2 {
		t.Fatalf("err %v calls %d", err, calls)
	}
	delays := sleeper.Delays()
	if len(delays) != 1 || delays[0] != 5*time.Second {
		t.Fatalf("delays %v", delays)
	}
}

func TestDoStopsWhenTheSleepIsCancelled(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	sleeper := cancelOnSleep{Clock: clock.Real(), cancel: cancel}
	calls := 0
	_, err := Run(ctx, Default().On(sleeper), func(context.Context) (int, error) {
		calls++
		return 0, domain.Transport(domain.ReasonNetworkConnectionLost)
	})
	if !errors.Is(err, context.Canceled) || calls != 1 {
		t.Fatalf("err %v calls %d", err, calls)
	}
}

func TestDoFirstSuccessDoesNotSleep(t *testing.T) {
	t.Parallel()
	sleeper := porttest.NewClock()
	got, err := Run(context.Background(), Default().On(sleeper), func(context.Context) (int, error) {
		return 7, nil
	})
	if err != nil || got != 7 || len(sleeper.Delays()) != 0 {
		t.Fatalf("got %d err %v delays %v", got, err, sleeper.Delays())
	}
}

type cancelOnSleep struct {
	clock.Clock

	cancel context.CancelFunc
}

func (s cancelOnSleep) Sleep(ctx context.Context, _ time.Duration) error {
	s.cancel()
	return ctx.Err()
}

func TestDoFollowsThePolicyRule(t *testing.T) {
	t.Parallel()
	sleeper := porttest.NewClock()
	policy := Default()
	policy.Retryable = OnlyRateLimits
	calls := 0
	_, err := Run(t.Context(), policy.On(sleeper), func(context.Context) (int, error) {
		calls++
		if calls == 1 {
			return 0, domain.RateLimited(nil)
		}
		return 0, domain.HTTP(503, "/x")
	})
	if !domain.IsKind(err, domain.KindHTTP) || calls != 2 || len(sleeper.Delays()) != 1 {
		t.Fatalf("err %v calls %d delays %v", err, calls, sleeper.Delays())
	}
}

func TestOnNeedsAClock(t *testing.T) {
	t.Parallel()
	defer func() {
		if recover() == nil {
			t.Fatal("On(nil) did not panic")
		}
	}()
	Default().On(nil)
}

func TestRunnerIsReusable(t *testing.T) {
	t.Parallel()
	runner := Default().On(instantClock{})
	for i := range 3 {
		calls := 0
		got, err := Run(t.Context(), runner, func(context.Context) (int, error) {
			calls++
			if calls == 1 {
				return 0, domain.Transport(domain.ReasonTimedOut)
			}
			return i, nil
		})
		if err != nil || got != i || calls != 2 {
			t.Fatalf("run %d: %d %v after %d calls", i, got, err, calls)
		}
	}
}
