// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package porttest

import (
	"context"
	"errors"
	"slices"
	"testing"
	"testing/synctest"
	"time"

	"github.com/altessa-s/go-atlas-core/time/clock"
)

func TestClockRecordsSleepsAndMovesForward(t *testing.T) {
	t.Parallel()
	c := NewClock()
	if !c.Now().Equal(time.Unix(0, 0).UTC()) {
		t.Fatal(c.Now())
	}
	if err := c.Sleep(t.Context(), time.Second); err != nil {
		t.Fatal(err)
	}
	c.Advance(2 * time.Second)
	if !c.Now().Equal(time.Unix(3, 0).UTC()) {
		t.Fatal(c.Now())
	}
	if got := c.Delays(); !slices.Equal(got, []time.Duration{time.Second}) {
		t.Fatal(got)
	}
}

func TestClockSkipsASleepInACanceledContext(t *testing.T) {
	t.Parallel()
	c := NewClock()
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := c.Sleep(ctx, time.Second); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if len(c.Delays()) != 0 || !c.Now().Equal(time.Unix(0, 0).UTC()) {
		t.Fatal(c.Delays(), c.Now())
	}
}

func TestRecordingWaitsOnTheClockBeneath(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		c := Recording(clock.Real())
		start := time.Now()
		if err := c.Sleep(t.Context(), time.Minute); err != nil {
			t.Fatal(err)
		}
		if waited := time.Since(start); waited != time.Minute {
			t.Fatal(waited)
		}
		if got := c.Delays(); !slices.Equal(got, []time.Duration{time.Minute}) {
			t.Fatal(got)
		}
	})
}

func TestRecordingCannotAdvance(t *testing.T) {
	t.Parallel()
	defer func() {
		if recover() == nil {
			t.Fatal("Advance did not panic")
		}
	}()
	Recording(clock.Real()).Advance(time.Second)
}
