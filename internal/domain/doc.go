// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

// Package domain holds the sync protocol's pure types.
//
// Nothing in this package opens a socket. Parsing, hashing, and the closed
// set of protocol errors live here so a test can hold them without a server.
// A new [Error] kind is a breaking change for every caller that switches on
// [Kind].
package domain
