// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package httpapi

import (
	"errors"
	"io"
)

// memoryLimit is the most bytes an in-memory response may hold.
const memoryLimit int64 = 32 << 20

// errTooBig means the body passed the ceiling. The count returned with it
// is the ceiling plus one, not the unread remainder.
var errTooBig = errors.New("httpapi: response exceeds the ceiling")

// copyLimited writes src to dst until limit.
//
// A read that crosses the ceiling writes only the bytes that fit. The
// returned count is then the ceiling plus one when the read went past it,
// and the rest of that read is not pulled from src. onWrite sees bytes
// actually written.
func copyLimited(dst io.Writer, src io.Reader, limit int64, onWrite func(int64)) (int64, error) {
	buf := make([]byte, 32<<10)
	var written int64
	for {
		nr, er := src.Read(buf)
		if nr > 0 {
			toWrite := int(min(int64(nr), max(limit-written, 0)))
			var ew error
			written, ew = writeChunk(dst, buf[:toWrite], written, onWrite)
			if ew != nil {
				return written, ew
			}
			if extra := nr - toWrite; extra > 0 {
				return min(written+int64(extra), limit+1), errTooBig
			}
		}
		if er != nil {
			if errors.Is(er, io.EOF) {
				return written, nil
			}
			return written, er
		}
		if nr == 0 {
			return written, io.ErrNoProgress
		}
	}
}

// writeChunk writes chunk to dst and returns the running total. onWrite sees
// the total whenever any byte was written, even by a failing write.
func writeChunk(dst io.Writer, chunk []byte, written int64, onWrite func(int64)) (int64, error) {
	if len(chunk) == 0 {
		return written, nil
	}
	nw, ew := dst.Write(chunk)
	if nw > 0 {
		written += int64(nw)
		if onWrite != nil {
			onWrite(written)
		}
	}
	if ew != nil {
		return written, ew
	}
	if nw != len(chunk) {
		return written, io.ErrShortWrite
	}
	return written, nil
}

// minRead is the first allocation of readLimited when the size is not known
// and the size of its probe read. Past it the buffer
// doubles, as bytes.Buffer does, so a body of unknown size is copied a
// bounded number of times.
const minRead = 512

// minGrowth is the least a body that outgrew minRead is given next: a body
// that is not small skips the doublings below it, as the 32 KiB copy buffer
// this replaced did.
const minGrowth = 32 << 10

// readLimited reads src into memory until limit. Past the ceiling it returns
// errTooBig, for which copyLimited would count the ceiling plus one. The
// body is read in place, without an intermediate buffer; a sizeHint from
// Content-Length, capped at the ceiling, sizes it up front so a body that
// arrives as announced is never copied while growing.
func readLimited(src io.Reader, limit, sizeHint int64) ([]byte, error) {
	initial := int64(minRead)
	if sizeHint >= 0 {
		// One byte past the hint lets the closing EOF arrive without a grow.
		initial = min(sizeHint, limit) + 1
	}
	buf := make([]byte, 0, initial)
	// A full buffer reads into probe first, so the EOF after a body that
	// filled it exactly costs no doubling.
	var probe []byte
	for {
		full := len(buf) == cap(buf)
		dst := buf[len(buf):cap(buf)]
		if full {
			if probe == nil {
				probe = make([]byte, minRead)
			}
			dst = probe
		}
		// Never read further than one byte past the ceiling.
		if room := limit + 1 - int64(len(buf)); int64(len(dst)) > room {
			dst = dst[:room]
		}
		nr, er := src.Read(dst)
		if full && nr > 0 {
			// Exactly double: append's growth overshoots, and a body that
			// is a power of two then needs one more doubling for its EOF.
			grown := make([]byte, len(buf)+nr, max(cap(buf)+cap(buf), minGrowth))
			copy(grown[copy(grown, buf):], probe[:nr])
			buf = grown
		} else {
			buf = buf[:len(buf)+nr]
		}
		if int64(len(buf)) > limit {
			return nil, errTooBig
		}
		if er != nil {
			if errors.Is(er, io.EOF) {
				return buf, nil
			}
			return buf, er
		}
		if nr == 0 {
			return buf, io.ErrNoProgress
		}
	}
}
