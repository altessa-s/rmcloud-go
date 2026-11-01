// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package porttest

import (
	"context"
	"sync"
	"time"

	"github.com/altessa-s/go-atlas-core/time/clock"
)

// Clock records each Sleep and hands it to the clock beneath. A canceled
// context is reported before the duration is recorded, matching a wait that
// never starts.
type Clock struct {
	clock.Clock

	fake   *clock.Fake
	mu     sync.Mutex
	delays []time.Duration
}

var _ clock.Clock = (*Clock)(nil)

// NewClock returns a clock sitting at 1970-01-01T00:00:00Z. Sleep returns
// at once and moves the clock forward by the requested duration.
func NewClock() *Clock {
	return NewClockAt(time.Unix(0, 0).UTC())
}

// NewClockAt is [NewClock] starting at now.
func NewClockAt(now time.Time) *Clock {
	fake := clock.NewFake(now)
	return &Clock{Clock: fake, fake: fake}
}

// Recording records the sleeps of c, which really waits. Inside a
// synctest bubble, [clock.Real] waits on the bubble's fake time.
func Recording(c clock.Clock) *Clock {
	return &Clock{Clock: c}
}

// Sleep records d, then waits on the clock beneath.
func (c *Clock) Sleep(ctx context.Context, d time.Duration) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	c.mu.Lock()
	c.delays = append(c.delays, d)
	c.mu.Unlock()
	return c.Clock.Sleep(ctx, d)
}

// Advance moves a [NewClock] clock forward by d. A [Recording] clock moves
// with the time beneath it; Advance on one panics.
func (c *Clock) Advance(d time.Duration) {
	if c.fake == nil {
		panic("porttest: Advance on a recording clock")
	}
	c.fake.Advance(d)
}

// Delays returns a copy of the recorded waits.
func (c *Clock) Delays() []time.Duration {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]time.Duration(nil), c.delays...)
}
