// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

// Package fs is the operating system behind the ports: the filesystem and
// the host's version.
//
// The protocol packages declare what they need in package port and require
// it as a constructor argument. Only the composition root hands them the values from
// here; a test hands them the doubles from package porttest instead.
package fs
