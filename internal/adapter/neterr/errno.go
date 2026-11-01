//go:build unix || windows

// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package neterr

import (
	"errors"
	"syscall"
)

// errnoCode reports the Winsock number for a connected-socket failure.
// Unix constants differ by OS; the numbers below are the ones the protocol
// already uses.
func errnoCode(err error) (int, bool) {
	errno, ok := errors.AsType[syscall.Errno](err)
	if !ok {
		return 0, false
	}
	switch errno { //nolint:exhaustive // only the connected-socket failures have a code
	case syscall.ECONNREFUSED:
		return wsaConnRefused, true
	case syscall.ETIMEDOUT:
		return wsaTimedOut, true
	case syscall.EHOSTUNREACH:
		return wsaHostUnreach, true
	case syscall.ENETUNREACH:
		return wsaNetUnreach, true
	case syscall.ECONNRESET:
		return wsaConnReset, true
	case syscall.ECONNABORTED, syscall.EPIPE:
		return wsaConnAborted, true
	default:
		return 0, false
	}
}
