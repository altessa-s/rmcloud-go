// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package retry

import (
	"errors"
	"time"

	"github.com/altessa-s/go-atlas-core/time/clock"

	"github.com/altessa-s/rmcloud-go/internal/domain"

	coreretry "github.com/altessa-s/go-atlas-core/retry"
)

// Policy decides which failures are worth a second try and how long to wait.
//
// A zero Policy has MaxAttempts 0, so the first failure is final. Call
// [Default] and change the fields a test cares about.
type Policy struct {
	// MaxAttempts counts the first try. Three means one try and two repeats.
	MaxAttempts int
	// BaseDelay is the wait before the second attempt, before jitter.
	BaseDelay time.Duration
	// Multiplier is how many times over the wait grows on each later repeat.
	Multiplier float64
	// MaxDelay caps both the formula and a server's Retry-After. An hour of
	// waiting would stall a sync that should fail and try on its own schedule.
	MaxDelay time.Duration
	// Jitter is the spread around the formula, as a fraction of the delay.
	// Several requests fail together; without a spread they come back as one burst.
	Jitter float64
	// Retryable decides which protocol errors are worth another attempt.
	// Nil is [ShouldRetry]. A call with its own rule, such as a root write
	// that may repeat a rate limit and nothing else, sets it here instead
	// of writing a second loop.
	Retryable func(domain.Error) bool
}

// Retries reports whether e is worth another attempt under this policy.
func (p Policy) Retries(e domain.Error) bool {
	if e == nil {
		return false
	}
	if p.Retryable != nil {
		return p.Retryable(e)
	}
	return ShouldRetry(e)
}

// OnlyRateLimits is a [Policy.Retryable] that repeats an explicit wait and
// nothing else.
func OnlyRateLimits(e domain.Error) bool {
	return e != nil && e.Kind() == domain.KindRateLimited
}

// Default is the protocol's retry policy: three attempts from 300ms,
// growing threefold to a 20s ceiling, with up to a fifth added as jitter.
func Default() Policy {
	const (
		attempts   = 3
		base       = 300 * time.Millisecond
		multiplier = 3
		ceiling    = 20 * time.Second
		jitter     = 0.2
	)
	return Policy{
		MaxAttempts: attempts,
		BaseDelay:   base,
		Multiplier:  multiplier,
		MaxDelay:    ceiling,
		Jitter:      jitter,
	}
}

// ShouldRetry reports whether a second try might succeed.
//
// The network, a 5xx, and an explicit wait are the only yeses. A wrong
// token, a 400, a broken schema, a missing hash, and a body that was not the
// protocol's will not be fixed by asking again. HTTP 429 retries only when
// it is [domain.KindRateLimited]; a plain 429 does not.
func ShouldRetry(e domain.Error) bool {
	switch e := e.(type) {
	case *domain.TransportError:
		// An open breaker fails fast for a while, and neither a policy
		// refusal nor a refused certificate changes; another attempt meets
		// the same answer.
		switch e.Reason { //nolint:exhaustive // every other reason is worth a second try
		case domain.ReasonCircuitOpen, domain.ReasonBlocked, domain.ReasonSecureConnectionFailed:
			return false
		default:
			return true
		}
	case *domain.RateLimitedError:
		return true
	case *domain.HTTPError:
		return domain.IsServerError(e.Status)
	case *domain.WriteRejectedError:
		return domain.IsServerError(e.Status)
	default:
		return false
	}
}

// backoff is the wait before a repeat. A server's Retry-After wins, capped
// by MaxDelay. Otherwise the wait starts at BaseDelay and grows by
// Multiplier, with up to Jitter of it added on top, all capped by MaxDelay.
// The core loop numbers its repeats from zero.
func (p Policy) backoff() coreretry.NextDelayFunc {
	grow := coreretry.Exponential(p.BaseDelay,
		coreretry.WithExponentialFactor(p.Multiplier),
		coreretry.WithExponentialJitter(p.Jitter),
		coreretry.WithExponentialMaxDelay(p.MaxDelay),
	)
	return func(attempt int, err error) time.Duration {
		if limited, ok := errors.AsType[*domain.RateLimitedError](err); ok && limited.HasRetryAfter && limited.RetryAfter > 0 {
			return min(limited.RetryAfter, p.MaxDelay)
		}
		return grow(attempt, err)
	}
}

// options is the policy as go-atlas-core retry options on clk. MaxAttempts
// counts the first try; the core counts repeats, and a negative count there
// means no cap, so a zero policy is held at one try.
func (p Policy) options(clk clock.Clock) []coreretry.Option {
	return []coreretry.Option{
		coreretry.WithMaxAttempts(max(p.MaxAttempts-1, 0)),
		coreretry.WithClock(clk),
		coreretry.WithShouldRetry(func(err error) bool {
			cloud, ok := errors.AsType[domain.Error](err)
			return ok && p.Retries(cloud)
		}),
		coreretry.WithNextDelay(p.backoff()),
		// op decides how a done context fails, as it did on the first try.
		coreretry.WithAttemptOnDoneContext(),
	}
}
