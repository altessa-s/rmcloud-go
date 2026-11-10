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

// idleWatch reads a body and cancels the request when it goes silent. The
// timer restarts with every read that brings bytes, so a body that is slow
// but still arriving is never cut. It is the reader itself, and one
// allocation: a request pays for the watch on every in-memory read.
type idleWatch struct {
	src     io.Reader
	timer   *time.Timer
	stall   context.CancelCauseFunc
	after   time.Duration
	expired atomic.Bool
}

// watchIdle starts the watch over src: stall is called with [errIdleRead]
// when no byte has arrived for after. Canceling the request's context is
// what unblocks a read on a connection that says nothing.
func watchIdle(src io.Reader, after time.Duration, stall context.CancelCauseFunc) *idleWatch {
	w := &idleWatch{src: src, stall: stall, after: after}
	w.timer = time.AfterFunc(after, w.fire)
	return w
}

func (w *idleWatch) fire() {
	w.expired.Store(true)
	w.stall(errIdleRead)
}

func (w *idleWatch) stop() { w.timer.Stop() }

func (w *idleWatch) Read(p []byte) (int, error) {
	n, err := w.src.Read(p)
	if n > 0 {
		w.timer.Reset(w.after)
	}
	return n, err
}
