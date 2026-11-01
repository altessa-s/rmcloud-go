// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package domain

import (
	coreerrors "github.com/altessa-s/go-atlas-core/errors"
)

// canonical is the go-atlas-core error a protocol error also is, so a caller
// can branch on errors.Is(err, errors.ErrUnavailable) without knowing this
// module's kinds. Nil means none fits.
func canonical(e Error) error {
	switch e := e.(type) {
	case *HTTPError:
		return canonicalStatus(e.Status)
	case *WriteRejectedError:
		return canonicalStatus(e.Status)
	}
	switch e.Kind() { //nolint:exhaustive // the other kinds have no canonical error
	case KindTransport, KindRateLimited:
		return coreerrors.ErrUnavailable
	case KindRootConflict:
		return coreerrors.ErrConflict
	case KindNoRootYet:
		return coreerrors.ErrNotFound
	case KindInvalidOneTimeCode:
		return coreerrors.ErrInvalidArgument
	case KindNotRegistered, KindAccountWithoutTablet, KindNotOnThisPlan, KindUnsupportedPlatform:
		return coreerrors.ErrFailedPrecondition
	default:
		return nil
	}
}

func canonicalStatus(status int) error {
	switch {
	case status == StatusNotFound:
		return coreerrors.ErrNotFound
	case IsServerError(status):
		return coreerrors.ErrUnavailable
	default:
		return nil
	}
}

func isCanonical(e Error, target error) bool {
	c := canonical(e)
	return c != nil && target == c
}

// Is matches the error's canonical go-atlas-core error.
func (s sentinel) Is(target error) bool { return isCanonical(s, target) }

// Is matches the error's canonical go-atlas-core error.
func (e *UnsupportedSchemaError) Is(target error) bool { return isCanonical(e, target) }

// Is matches the error's canonical go-atlas-core error.
func (e *MalformedIndexError) Is(target error) bool { return isCanonical(e, target) }

// Is matches the error's canonical go-atlas-core error.
func (e *MalformedResponseError) Is(target error) bool { return isCanonical(e, target) }

// Is matches the error's canonical go-atlas-core error.
func (e *HashMismatchError) Is(target error) bool { return isCanonical(e, target) }

// Is matches the error's canonical go-atlas-core error.
func (e *MissingHashError) Is(target error) bool { return isCanonical(e, target) }

// Is matches the error's canonical go-atlas-core error.
func (e *RateLimitedError) Is(target error) bool { return isCanonical(e, target) }

// Is matches the error's canonical go-atlas-core error.
func (e *NotOnThisPlanError) Is(target error) bool { return isCanonical(e, target) }

// Is matches the error's canonical go-atlas-core error.
func (e *RootConflictError) Is(target error) bool { return isCanonical(e, target) }

// Is matches the error's canonical go-atlas-core error.
func (e *HTTPError) Is(target error) bool { return isCanonical(e, target) }

// Is matches the error's canonical go-atlas-core error.
func (e *WriteRejectedError) Is(target error) bool { return isCanonical(e, target) }

// Is matches the error's canonical go-atlas-core error.
func (e *TransportError) Is(target error) bool { return isCanonical(e, target) }

// Is matches the error's canonical go-atlas-core error.
func (e *UnsupportedPlatformError) Is(target error) bool { return isCanonical(e, target) }

// Is matches the error's canonical go-atlas-core error.
func (e *NothingToAskError) Is(target error) bool { return isCanonical(e, target) }

// Is matches the error's canonical go-atlas-core error.
func (e *OversizedResponseError) Is(target error) bool { return isCanonical(e, target) }
