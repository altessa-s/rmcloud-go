// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

// Package port is what the protocol asks of its host.
//
// The interfaces are narrow on purpose. A secret store, an HTTP exchange, a
// notification socket, and the local files are separate reasons to change.
// Time is clock.Clock from go-atlas-core.
// This package holds no implementation and does not import net/http, so a test
// can stand in for all of them.
package port
