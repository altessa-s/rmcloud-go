// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package domain

import "testing"

func TestCRC32CVectors(t *testing.T) {
	cases := []struct {
		data string
		want uint32
	}{
		{"", 0},
		{"a", 0xC1D04330},
		{"abc", 0x364B3FB7},
		{"123456789", 0xE3069283},
	}
	for _, tc := range cases {
		if got := CRC32C([]byte(tc.data)); got != tc.want {
			t.Errorf("%q = %08x, want %08x", tc.data, got, tc.want)
		}
	}
	zeros := make([]byte, 32)
	if got := CRC32C(zeros); got != 0x8A9136AA {
		t.Fatalf("zeros = %08x", got)
	}
	ones := make([]byte, 32)
	for i := range ones {
		ones[i] = 0xFF
	}
	if got := CRC32C(ones); got != 0x62A8AB43 {
		t.Fatalf("ones = %08x", got)
	}
	if CRC32C([]byte("123456789")) == 0xCBF43926 {
		t.Fatal("Castagnoli must not equal the zip CRC")
	}
	if CRC32C(nil) != 0 {
		t.Fatal("nil checksums as 0")
	}
}

func TestGoogHash(t *testing.T) {
	if got := GoogHash([]byte("123456789")); got != "crc32c=4waSgw==" {
		t.Fatalf("123456789 = %s", got)
	}
	if got := GoogHash(nil); got != "crc32c=AAAAAA==" {
		t.Fatalf("empty = %s", got)
	}
	if got := GoogHashSum(0xE3069283); got != "crc32c=4waSgw==" {
		t.Fatalf("sum = %s", got)
	}
}
