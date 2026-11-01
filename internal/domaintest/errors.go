// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package domaintest

import (
	"errors"
	"reflect"

	"github.com/altessa-s/rmcloud-go/internal/domain"
)

// SameError reports whether err's chain holds an error equal to want: the
// same sentinel, or the same protocol error type with the same fields.
func SameError(err, want error) bool {
	for e := err; e != nil; e = errors.Unwrap(e) {
		if reflect.DeepEqual(e, want) { //nolint:govet // comparing payloads field by field is the point
			return true
		}
	}
	return false
}

// SameKind reports whether err's chain holds a protocol error of want's
// kind. The payload is not compared.
func SameKind(err error, want domain.Error) bool {
	return want != nil && domain.IsKind(err, want.Kind())
}
