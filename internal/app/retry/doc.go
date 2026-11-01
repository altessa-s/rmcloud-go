// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

// Package retry repeats a call that failed for a reason a second try can fix.
//
// The loop and the backoff are go-atlas-core's retry package. This package
// holds what the protocol adds: which errors are worth repeating and a
// server's Retry-After. The default policy makes three attempts, from
// 300ms growing threefold, a 20s ceiling, and up to a fifth of the delay
// added as spread. A root conflict is not
// repeated here. The writer re-reads the root and rebuilds the tree first.
package retry
