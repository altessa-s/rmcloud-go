// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package fs

import (
	"syscall"

	"github.com/altessa-s/rmcloud-go/internal/domain"
)

// HostVersion is the macOS product version, with a trailing ".0" dropped.
// An unreadable sysctl is an empty string: the request then carries no
// User-Agent rather than a version we invented.
func HostVersion() string {
	raw, err := syscall.Sysctl("kern.osproductversion")
	if err != nil || raw == "" {
		return ""
	}
	return domain.SystemVersion(raw)
}
