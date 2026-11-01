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

	"github.com/altessa-s/rmcloud-go/internal/domain"
)

// Sinks keep the compiler from discarding the benchmarked calls.
var (
	sinkBool  bool
	sinkDelay time.Duration
	sinkText  string
)

// instantClock never waits and records nothing, so a benchmark measures the
// loop and not a fake clock's bookkeeping.
type instantClock struct{}

func (instantClock) Now() time.Time { return time.Unix(1800000000, 0) }

func (instantClock) Sleep(ctx context.Context, _ time.Duration) error { return ctx.Err() }

func BenchmarkShouldRetry(b *testing.B) {
	for _, tc := range []struct {
		name string
		err  domain.Error
	}{
		{"transport", domain.Transport(domain.ReasonNotConnectedToInternet)},
		{"bad-request", domain.HTTP(400, "/sync/v3/root")},
	} {
		b.Run(tc.name, func(b *testing.B) {
			policy := Default()
			b.ReportAllocs()
			for b.Loop() {
				sinkBool = policy.Retries(tc.err)
			}
		})
	}
}

func BenchmarkBackoff(b *testing.B) {
	wait := 2 * time.Second
	for _, tc := range []struct {
		name string
		err  error
	}{
		{"exponential", domain.HTTP(503, "/sync/v3/root")},
		{"retry-after", fmt.Errorf("attempt: %w", domain.RateLimited(&wait))},
	} {
		b.Run(tc.name, func(b *testing.B) {
			next := Default().backoff()
			b.ReportAllocs()
			var attempt int
			for b.Loop() {
				sinkDelay = next(attempt%3, tc.err)
				attempt++
			}
		})
	}
}

func BenchmarkRunPolicy(b *testing.B) {
	failure := domain.Transport(domain.ReasonNotConnectedToInternet)
	permanent := errors.New("not a protocol error")
	for _, tc := range []struct {
		name     string
		failures int
		err      error
	}{
		{"first-success", 0, nil},
		{"two-repeats", 2, failure},
		{"permanent", 1, permanent},
	} {
		b.Run(tc.name, func(b *testing.B) {
			ctx := b.Context()
			runner := Default().On(instantClock{})
			b.ReportAllocs()
			for b.Loop() {
				calls := 0
				got, err := Run(ctx, runner, func(context.Context) (string, error) {
					calls++
					if calls <= tc.failures {
						return "", tc.err
					}
					return "done", nil
				})
				if tc.err == permanent {
					if !errors.Is(err, permanent) {
						b.Fatal(err)
					}
					continue
				}
				if err != nil {
					b.Fatal(err)
				}
				sinkText = got
			}
		})
	}
}

// BenchmarkRun is the path the use cases take: the policy built once, then
// run per call.
func BenchmarkRun(b *testing.B) {
	runner := Default().On(instantClock{})
	ctx := b.Context()
	b.ReportAllocs()
	for b.Loop() {
		got, err := Run(ctx, runner, func(context.Context) (string, error) { return "done", nil })
		if err != nil {
			b.Fatal(err)
		}
		sinkText = got
	}
}
