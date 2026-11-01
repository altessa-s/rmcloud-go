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
)

func TestCopyLimited(t *testing.T) {
	t.Parallel()

	t.Run("one read past a short ceiling", func(t *testing.T) {
		var last int64
		var buf bytes.Buffer
		n, err := copyLimited(&buf, strings.NewReader("abcdefghij"), 2, func(written int64) { last = written })
		if !errors.Is(err, errTooBig) || n != 3 || buf.String() != "ab" || last != 2 {
			t.Fatalf("n=%d err=%v body=%q last=%d", n, err, buf.String(), last)
		}
	})

	t.Run("limit four counts five and writes four", func(t *testing.T) {
		var last int64
		var buf bytes.Buffer
		n, err := copyLimited(&buf, strings.NewReader("abcdefghij"), 4, func(written int64) { last = written })
		if !errors.Is(err, errTooBig) || n != 5 || buf.String() != "abcd" || last != 4 {
			t.Fatalf("n=%d err=%v body=%q last=%d", n, err, buf.String(), last)
		}
	})

	t.Run("second read past a full ceiling", func(t *testing.T) {
		var buf bytes.Buffer
		n, err := copyLimited(&buf, &chunkReader{chunks: [][]byte{[]byte("abcd"), []byte("abcdefghij")}}, 4, nil)
		if !errors.Is(err, errTooBig) || n != 5 || buf.String() != "abcd" {
			t.Fatalf("n=%d err=%v body=%q", n, err, buf.String())
		}
	})

	t.Run("memory ceiling plus one", func(t *testing.T) {
		const extra = 100
		n, err := copyLimited(io.Discard, &countReader{left: int(memoryLimit) + extra}, memoryLimit, nil)
		if !errors.Is(err, errTooBig) || n != memoryLimit+1 {
			t.Fatalf("n=%d err=%v", n, err)
		}
	})

	t.Run("write error", func(t *testing.T) {
		n, err := copyLimited(failWriter{}, strings.NewReader("abcdefghij"), 4, nil)
		if !errors.Is(err, io.ErrClosedPipe) || errors.Is(err, errTooBig) || n != 0 {
			t.Fatalf("n=%d err=%v", n, err)
		}
	})

	t.Run("short write", func(t *testing.T) {
		_, err := copyLimited(shortWriter{}, strings.NewReader("abcdefghij"), 4, nil)
		if !errors.Is(err, io.ErrShortWrite) {
			t.Fatal(err)
		}
	})

	t.Run("no progress", func(t *testing.T) {
		_, err := copyLimited(io.Discard, zeroReader{}, 4, nil)
		if !errors.Is(err, io.ErrNoProgress) {
			t.Fatal(err)
		}
	})

	t.Run("immediate eof", func(t *testing.T) {
		var buf bytes.Buffer
		n, err := copyLimited(&buf, strings.NewReader(""), 4, nil)
		if err != nil || n != 0 || buf.Len() != 0 {
			t.Fatalf("n=%d err=%v", n, err)
		}
	})
}

type chunkReader struct {
	chunks [][]byte
	i      int
}

func (r *chunkReader) Read(p []byte) (int, error) {
	if r.i >= len(r.chunks) {
		return 0, io.EOF
	}
	n := copy(p, r.chunks[r.i])
	r.i++
	return n, nil
}

type countReader struct{ left int }

func (r *countReader) Read(p []byte) (int, error) {
	if r.left == 0 {
		return 0, io.EOF
	}
	n := min(len(p), r.left)
	for i := range n {
		p[i] = 'a'
	}
	r.left -= n
	return n, nil
}

type failWriter struct{}

func (failWriter) Write([]byte) (int, error) { return 0, io.ErrClosedPipe }

type shortWriter struct{}

func (shortWriter) Write(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	return len(p) - 1, nil
}

type zeroReader struct{}

func (zeroReader) Read([]byte) (int, error) { return 0, nil }
