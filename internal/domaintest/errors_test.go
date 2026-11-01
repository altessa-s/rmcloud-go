// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package domaintest_test

import (
	"errors"
	"fmt"
	"testing"

	"github.com/altessa-s/rmcloud-go/internal/domain"
	"github.com/altessa-s/rmcloud-go/internal/domaintest"
)

func TestSameError(t *testing.T) {
	mismatch := &domain.HashMismatchError{Expected: "aa", Actual: "bb"}
	cases := []struct {
		name string
		err  error
		want error
		same bool
	}{
		{"same sentinel", domain.ErrNotRegistered, domain.ErrNotRegistered, true},
		{"wrapped sentinel", fmt.Errorf("read: %w", domain.ErrNoRootYet), domain.ErrNoRootYet, true},
		{"equal payload, other pointer", mismatch, &domain.HashMismatchError{Expected: "aa", Actual: "bb"}, true},
		{"wrapped twice", fmt.Errorf("a: %w", fmt.Errorf("b: %w", mismatch)), mismatch, true},
		{"other sentinel", domain.ErrNotRegistered, domain.ErrUnauthorized, false},
		{"same type, other payload", mismatch, &domain.HashMismatchError{Expected: "aa", Actual: "cc"}, false},
		{"same kind, other type", &domain.HTTPError{Status: 500}, &domain.RootConflictError{Status: 500}, false},
		{"plain error", errors.New("rmcloud: not registered"), domain.ErrNotRegistered, false},
		{"nil error", nil, domain.ErrNotRegistered, false},
		{"joined errors are not unwrapped", errors.Join(domain.ErrNoRootYet), domain.ErrNoRootYet, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := domaintest.SameError(tc.err, tc.want); got != tc.same {
				t.Fatalf("SameError(%v, %v) = %v, want %v", tc.err, tc.want, got, tc.same)
			}
		})
	}
}

func TestSameKind(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want domain.Error
		same bool
	}{
		{"same kind, other payload", &domain.HashMismatchError{Expected: "aa"}, &domain.HashMismatchError{Expected: "bb"}, true},
		{"wrapped", fmt.Errorf("read: %w", &domain.RateLimitedError{}), &domain.RateLimitedError{}, true},
		{"sentinel", domain.ErrUnauthorized, domain.ErrUnauthorized, true},
		{"other kind", domain.ErrUnauthorized, domain.ErrNotRegistered, false},
		{"plain error", errors.New("boom"), domain.ErrUnauthorized, false},
		{"nil want", domain.ErrUnauthorized, nil, false},
		{"nil error", nil, domain.ErrUnauthorized, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := domaintest.SameKind(tc.err, tc.want); got != tc.same {
				t.Fatalf("SameKind(%v, %v) = %v, want %v", tc.err, tc.want, got, tc.same)
			}
		})
	}
}
