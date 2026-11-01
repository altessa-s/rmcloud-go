// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package domain

import (
	"encoding/base64"
	"encoding/binary"
	"hash/crc32"
)

// castagnoli is the reflected Castagnoli polynomial 0x82F63B78, not zip's
// CRC-32. The standard library builds that table once and hands the same one
// back on every call.
func castagnoli() *crc32.Table { return crc32.MakeTable(crc32.Castagnoli) }

// CRC32C is the Castagnoli checksum of data. A nil slice checksums as 0.
func CRC32C(data []byte) uint32 {
	return crc32.Checksum(data, castagnoli())
}

// GoogHash is the x-goog-hash value for data.
func GoogHash(data []byte) string {
	return GoogHashSum(CRC32C(data))
}

// GoogHashSum spells a checksum the way Google Cloud Storage wants it: the
// four bytes big-endian, standard base64, named crc32c.
func GoogHashSum(sum uint32) string {
	var raw [4]byte
	binary.BigEndian.PutUint32(raw[:], sum)
	return "crc32c=" + base64.StdEncoding.EncodeToString(raw[:])
}
