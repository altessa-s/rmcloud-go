// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

// Package wiring is the composition root: the one place that knows every
// adapter and every use case and connects them. The public package reads
// its options, hands them over as a [Config], and keeps only interfaces to
// what [Build] returns.
package wiring
