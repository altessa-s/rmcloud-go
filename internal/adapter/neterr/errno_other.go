//go:build !unix && !windows

// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package neterr

func errnoCode(error) (int, bool) { return 0, false }
