// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package domain

import (
	"errors"
	"fmt"
	"testing"

	coreerrors "github.com/altessa-s/go-atlas-core/errors"
)

func TestProtocolErrorsAreTheirCanonicalCoreError(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		err  Error
		want error
	}{
		{Transport(ReasonTimedOut), coreerrors.ErrUnavailable},
		{&RateLimitedError{}, coreerrors.ErrUnavailable},
		{&HTTPError{Status: 503}, coreerrors.ErrUnavailable},
		{&HTTPError{Status: 404}, coreerrors.ErrNotFound},
		{&WriteRejectedError{Status: 500}, coreerrors.ErrUnavailable},
		{&RootConflictError{Status: 412}, coreerrors.ErrConflict},
		{ErrNoRootYet, coreerrors.ErrNotFound},
		{ErrInvalidOneTimeCode, coreerrors.ErrInvalidArgument},
		{ErrNotRegistered, coreerrors.ErrFailedPrecondition},
		{ErrAccountWithoutTablet, coreerrors.ErrFailedPrecondition},
		{&NotOnThisPlanError{}, coreerrors.ErrFailedPrecondition},
		{&UnsupportedPlatformError{}, coreerrors.ErrFailedPrecondition},
	} {
		if !errors.Is(fmt.Errorf("wrapped: %w", tc.err), tc.want) {
			t.Errorf("%v is not %v", tc.err, tc.want)
		}
	}
	for _, err := range []Error{
		ErrUnauthorized, &HTTPError{Status: 400}, &WriteRejectedError{Status: 400}, &UnsupportedSchemaError{},
		&MalformedIndexError{}, &MalformedResponseError{}, &HashMismatchError{}, &MissingHashError{},
		&NothingToAskError{}, &OversizedResponseError{},
	} {
		for _, canon := range []error{coreerrors.ErrUnavailable, coreerrors.ErrNotFound, coreerrors.ErrConflict, coreerrors.ErrFailedPrecondition} {
			if errors.Is(err, canon) {
				t.Errorf("%v is %v", err, canon)
			}
		}
	}
	if errors.Is(ErrNotRegistered, ErrAccountWithoutTablet) {
		t.Fatal("two sentinels sharing a canonical error match each other")
	}
}
