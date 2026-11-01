// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package httpapi

import (
	"bytes"
	"errors"
	"io"
	"strings"
	"testing"
	"testing/iotest"
)

func TestReadLimited(t *testing.T) {
	t.Parallel()
	body := strings.Repeat("x", 5000)
	tests := []struct {
		name  string
		src   func() io.Reader
		limit int64
		hint  int64
		want  string
		err   error
	}{
		{name: "exact hint", src: func() io.Reader { return strings.NewReader(body) }, limit: 1 << 20, hint: 5000, want: body},
		{name: "no hint", src: func() io.Reader { return strings.NewReader(body) }, limit: 1 << 20, hint: -1, want: body},
		{name: "hint too small", src: func() io.Reader { return strings.NewReader(body) }, limit: 1 << 20, hint: 10, want: body},
		{name: "hint too large", src: func() io.Reader { return strings.NewReader("abc") }, limit: 1 << 20, hint: 1 << 30, want: "abc"},
		{name: "one byte reads", src: func() io.Reader { return iotest.OneByteReader(strings.NewReader(body)) }, limit: 1 << 20, hint: -1, want: body},
		{name: "empty", src: func() io.Reader { return strings.NewReader("") }, limit: 10, hint: 0, want: ""},
		{name: "at the limit", src: func() io.Reader { return strings.NewReader("0123456789") }, limit: 10, hint: -1, want: "0123456789"},
		{name: "one past the limit", src: func() io.Reader { return strings.NewReader("0123456789a") }, limit: 10, hint: 11, err: errTooBig},
		{name: "past the limit, hint lies", src: func() io.Reader { return strings.NewReader(body) }, limit: 100, hint: 3, err: errTooBig},
		{name: "data with eof", src: func() io.Reader { return iotest.DataErrReader(strings.NewReader("abc")) }, limit: 10, hint: -1, want: "abc"},
		{
			name: "read error",
			src: func() io.Reader {
				return io.MultiReader(strings.NewReader("ab"), iotest.ErrReader(io.ErrUnexpectedEOF))
			},
			limit: 10, hint: 64, want: "ab", err: io.ErrUnexpectedEOF,
		},
		{name: "no progress", src: func() io.Reader { return zeroReader{} }, limit: 10, hint: -1, err: io.ErrNoProgress},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := readLimited(tt.src(), tt.limit, tt.hint)
			if !errors.Is(err, tt.err) {
				t.Fatalf("err %v, want %v", err, tt.err)
			}
			if tt.err == nil || tt.want != "" {
				if !bytes.Equal(got, []byte(tt.want)) {
					t.Fatalf("got %d bytes, want %d", len(got), len(tt.want))
				}
			}
		})
	}
}

// An exact Content-Length is read into one allocation, with no copy.
func TestReadLimitedAllocatesOnceForAnExactHint(t *testing.T) {
	body := bytes.Repeat([]byte("y"), 1<<20)
	src := bytes.NewReader(nil)
	allocs := testing.AllocsPerRun(20, func() {
		src.Reset(body)
		got, err := readLimited(src, memoryLimit, int64(len(body)))
		if err != nil || len(got) != len(body) {
			t.Fatalf("%d %v", len(got), err)
		}
	})
	if allocs > 1 {
		t.Fatalf("%v allocations, want 1", allocs)
	}
}

// A body of unknown size grows by doubling from minGrowth, so a large body
// costs a bounded number of allocations.
func TestReadLimitedGrowsInFewSteps(t *testing.T) {
	body := bytes.Repeat([]byte("z"), 1<<20)
	src := bytes.NewReader(nil)
	allocs := testing.AllocsPerRun(10, func() {
		src.Reset(body)
		got, err := readLimited(src, memoryLimit, -1)
		if err != nil || len(got) != len(body) {
			t.Fatalf("%d %v", len(got), err)
		}
	})
	// The first buffer, the probe, and the doublings from 32 KiB to 1 MiB.
	if want := 2.0 + 6; allocs > want {
		t.Fatalf("%v allocations, want at most %v", allocs, want)
	}
}
