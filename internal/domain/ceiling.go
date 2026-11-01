// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package domain

// Ceiling is the most bytes one download may accept.
type Ceiling struct {
	Limit int64
}

// LimitFor is the ceiling a declared size selects. A declared size above
// zero is the ceiling. Zero and negative mean the size was not declared, and
// the ceiling is [BlobSizeLimit].
func LimitFor(declared int64) int64 {
	if declared > 0 {
		return declared
	}
	return BlobSizeLimit
}

// Refusal reports an oversized response when the larger of written and
// expected exceeds Limit. A negative expected means the length was not
// declared and does not raise the number. Equality at the limit is allowed.
func (c Ceiling) Refusal(written, expected int64) *OversizedResponseError {
	over := max(written, expected)
	if over > c.Limit {
		return OversizedResponse(over, c.Limit)
	}
	return nil
}
