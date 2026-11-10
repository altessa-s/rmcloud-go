// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package httpapi

import (
	"context"
	"errors"
	"io"
	"sync/atomic"
	"time"
)

// errIdleRead is the cause a request is canceled with when its in-memory
// body stops arriving.
var errIdleRead = errors.New("httpapi: response body stalled")

// idleWatch cancels a request whose body goes silent. The timer restarts
// with every byte, so a body that is slow but still arriving is never cut.
type idleWatch struct {
	timer   *time.Timer
	after   time.Duration
	expired atomic.Bool
}

// watchIdle starts the watch: stall is called with [errIdleRead] when no
// byte has arrived for after. Canceling the request's context is what
// unblocks a read on a connection that says nothing.
func watchIdle(after time.Duration, stall context.CancelCauseFunc) *idleWatch {
	w := &idleWatch{after: after}
	w.timer = time.AfterFunc(after, func() {
		w.expired.Store(true)
		stall(errIdleRead)
	})
	return w
}

func (w *idleWatch) stop() { w.timer.Stop() }

// idleReader reports each read that brought bytes to its watch.
type idleReader struct {
	src   io.Reader
	watch *idleWatch
}

func (r idleReader) Read(p []byte) (int, error) {
	n, err := r.src.Read(p)
	if n > 0 {
		r.watch.timer.Reset(r.watch.after)
	}
	return n, err
}
