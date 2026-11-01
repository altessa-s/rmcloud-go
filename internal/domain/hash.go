// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package domain

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"hash/crc32"
	"io"
)

// BlobHash is the SHA-256 of a blob's bytes, lowercase hex.
func BlobHash(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// IsHash reports whether s can address a blob: one or more ASCII hex digits.
// Anything else, such as "..", would change the path a token is sent to.
func IsHash(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		switch {
		case r >= '0' && r <= '9', r >= 'a' && r <= 'f', r >= 'A' && r <= 'F':
		default:
			return false
		}
	}
	return true
}

// Digest is a blob's two checksums: the SHA-256 address, lowercase hex, and
// the CRC32C the storage service checks on upload.
type Digest struct {
	SHA256 string
	CRC32C uint32
}

// GoogHash is the x-goog-hash value for this digest.
func (d Digest) GoogHash() string { return GoogHashSum(d.CRC32C) }

// DigestReader reads r to the end in [HashChunk] pieces and returns both
// checksums from that one pass. ctx is checked before every read, including
// the first, and its error is returned as it is.
func DigestReader(ctx context.Context, r io.Reader) (Digest, error) {
	sha := sha256.New()
	var crc uint32
	table := castagnoli()
	buf := make([]byte, HashChunk)
	for {
		if err := ctx.Err(); err != nil {
			return Digest{}, err
		}
		n, err := r.Read(buf)
		if n > 0 {
			_, _ = sha.Write(buf[:n])
			crc = crc32.Update(crc, table, buf[:n])
		}
		if errors.Is(err, io.EOF) {
			return Digest{SHA256: hex.EncodeToString(sha.Sum(nil)), CRC32C: crc}, nil
		}
		if err != nil {
			return Digest{}, err
		}
	}
}

// NodeHash is the SHA-256 of the children's hashes. Each hash is decoded
// from hex and fed as raw bytes, in documentID order. The input slice is
// not reordered. An empty hash is empty input and is valid hex. Anything
// else that is not hex is a malformed index.
func NodeHash(entries []Entry) (string, error) {
	h := sha256.New()
	for _, entry := range orderedEntries(entries) {
		raw, err := hex.DecodeString(entry.Hash)
		if err != nil {
			return "", MalformedIndex(entry.Line(), "hash is not hex")
		}
		_, _ = h.Write(raw)
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
