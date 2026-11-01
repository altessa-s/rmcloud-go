//go:build !darwin

// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package fs

// HostVersion is empty off darwin. The default User-Agent names a macOS
// version, and none is invented for another operating system.
func HostVersion() string { return "" }
