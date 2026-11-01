// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package retry

import (
	"context"

	"github.com/altessa-s/go-atlas-core/time/clock"

	coreretry "github.com/altessa-s/go-atlas-core/retry"
)

// Runner is a [Policy] built on a clock once, so a call path that retries
// often does not rebuild the core loop's options, rule and backoff on every
// call. A Runner is immutable and safe for concurrent use.
type Runner struct {
	core *coreretry.Policy
}

// On builds the policy on clk. clk is required; a nil one is a programming
// error and panics.
func (p Policy) On(clk clock.Clock) *Runner {
	if clk == nil {
		panic("retry: a policy needs a clock")
	}
	return &Runner{core: coreretry.NewPolicy(p.options(clk)...)}
}

// Run runs op until it returns nil or the policy stops. The loop is
// go-atlas-core's retry loop; the [Policy] supplies the rule and the waits.
//
// The error handed back is the last one op returned, not an error of our
// own: the caller needs the reason. A sleep that is canceled returns that
// cancellation and does not call op again. Anything that is not a
// [domain.Error], including context.Canceled from op, is not repeated, and
// a protocol error is repeated only when [Policy.Retries] says so.
func Run[T any](ctx context.Context, r *Runner, op func(context.Context) (T, error)) (T, error) {
	return collect(op, func(fn func(context.Context) error) error {
		return r.core.Do(ctx, fn)
	})
}

// collect runs op through loop and keeps the value of the call that
// succeeded. The error is the last one op returned.
func collect[T any](op func(context.Context) (T, error), loop func(func(context.Context) error) error) (T, error) {
	var value T
	err := loop(func(ctx context.Context) error {
		v, err := op(ctx)
		if err == nil {
			value = v
		}
		return err
	})
	if err != nil {
		var zero T
		return zero, err
	}
	return value, nil
}
