// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package domain

import "testing"

func TestLimitFor(t *testing.T) {
	if LimitFor(0) != BlobSizeLimit || LimitFor(-1) != BlobSizeLimit {
		t.Fatal("undeclared size uses the blob ceiling")
	}
	if LimitFor(10) != 10 {
		t.Fatal(LimitFor(10))
	}
}

func TestCeilingRefusal(t *testing.T) {
	c := Ceiling{Limit: 10}
	if c.Refusal(10, 10) != nil {
		t.Fatal("equality is allowed")
	}
	if c.Refusal(10, -1) != nil {
		t.Fatal("a negative expected does not raise the number")
	}
	got := c.Refusal(4, 12)
	if got == nil || *got != *OversizedResponse(12, 10) {
		t.Fatalf("%#v", got)
	}
	got = c.Refusal(11, -5)
	if got == nil || *got != *OversizedResponse(11, 10) {
		t.Fatalf("%#v", got)
	}
}
