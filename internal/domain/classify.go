// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package domain

import (
	"errors"
)

// KindOf names the protocol error in err's chain. ok is false when there
// is none.
func KindOf(err error) (kind Kind, ok bool) {
	e, ok := errors.AsType[Error](err)
	if !ok || e == nil {
		return "", false
	}
	return e.Kind(), true
}

// IsKind reports whether err's chain holds a protocol error of kind.
func IsKind(err error, kind Kind) bool {
	got, ok := KindOf(err)
	return ok && got == kind
}

// IsDocumentUnavailable reports an error that means this document is not in the
// cloud, as opposed to a failure of the request itself.
func IsDocumentUnavailable(err error) bool {
	kind, ok := KindOf(err)
	if !ok {
		return false
	}
	switch kind { //nolint:exhaustive // every other kind is a failure of the request
	case KindUnsupportedSchema, KindMalformedIndex, KindHashMismatch, KindMissingHash:
		return true
	case KindHTTP:
		e, _ := errors.AsType[*HTTPError](err)
		return e.Status == StatusNotFound
	default:
		return false
	}
}

// IsHostUnreachable reports a transport failure whose reason says the host
// could not be found or reached. Offline and a generic transport failure do
// not qualify.
func IsHostUnreachable(err error) bool {
	e, ok := errors.AsType[*TransportError](err)
	if !ok {
		return false
	}
	switch e.Reason { //nolint:exhaustive // only these reasons mean the host is not there
	case ReasonCannotFindHost, ReasonCannotConnectToHost, ReasonDNSLookupFailed:
		return true
	default:
		return false
	}
}
