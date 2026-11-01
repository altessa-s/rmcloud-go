// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package domain

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"testing/iotest"
)

func TestBlobHash(t *testing.T) {
	if got := BlobHash(nil); got != "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855" {
		t.Fatalf("empty = %s", got)
	}
	if got := BlobHash([]byte("abc")); got != "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad" {
		t.Fatalf("abc = %s", got)
	}
}

func TestNodeHash(t *testing.T) {
	empty, err := NodeHash(nil)
	if err != nil {
		t.Fatal(err)
	}
	if empty != BlobHash(nil) {
		t.Fatalf("empty node = %s", empty)
	}
	a := []Entry{
		{Hash: "00ff", Type: FileType, DocumentID: "b"},
		{Hash: "ff00", Type: FileType, DocumentID: "a"},
	}
	b := []Entry{
		{Hash: "ff00", Type: FileType, DocumentID: "a"},
		{Hash: "00ff", Type: FileType, DocumentID: "b"},
	}
	left, err := NodeHash(a)
	if err != nil {
		t.Fatal(err)
	}
	right, err := NodeHash(b)
	if err != nil {
		t.Fatal(err)
	}
	if left != right {
		t.Fatalf("order %s vs %s", left, right)
	}
	changed, err := NodeHash([]Entry{
		{Hash: "00fe", Type: FileType, DocumentID: "b"},
		{Hash: "ff00", Type: FileType, DocumentID: "a"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if changed == left {
		t.Fatal("a different child hash must change the node hash")
	}
	_, err = NodeHash([]Entry{{Hash: "zz", Type: FileType, DocumentID: "a"}})
	var got *MalformedIndexError
	if !errors.As(err, &got) || got.Reason != "hash is not hex" {
		t.Fatalf("%v", err)
	}
	if a[0].DocumentID != "b" {
		t.Fatal("input was reordered")
	}
}

func TestDigestReaderAgreesWithMemory(t *testing.T) {
	n := 2*HashChunk + 17
	body := make([]byte, n)
	for i := range body {
		body[i] = byte(i*31 + 7)
	}
	got, err := DigestReader(t.Context(), bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	if got.SHA256 != BlobHash(body) {
		t.Fatalf("sha %s memory %s", got.SHA256, BlobHash(body))
	}
	if got.CRC32C != CRC32C(body) {
		t.Fatalf("crc %08x memory %08x", got.CRC32C, CRC32C(body))
	}
	if got.GoogHash() != GoogHash(body) {
		t.Fatalf("goog %s memory %s", got.GoogHash(), GoogHash(body))
	}
}

func TestDigestReaderCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err := DigestReader(ctx, strings.NewReader("abc"))
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v", err)
	}
}

func TestDigestReaderReturnsAReadError(t *testing.T) {
	broken := errors.New("broken")
	_, err := DigestReader(t.Context(), iotest.ErrReader(broken))
	if !errors.Is(err, broken) {
		t.Fatalf("err = %v", err)
	}
}

func TestDecodeIndex(t *testing.T) {
	badChild := []byte("3\nzz:0:doc:0:1")
	file := BlobHash(badChild)
	ix, by, err := DecodeIndex(badChild, file)
	if err != nil || len(ix.Entries) != 1 {
		t.Fatalf("file hash wins: %#v %v", ix, err)
	}
	if by != ByFileHash {
		t.Fatalf("addressing = %v, want the file hash", by)
	}

	good := []byte("3\naa:0:doc.metadata:0:10\nbb:0:doc.pdf:0:90")
	parsed, err := Parse(string(good))
	if err != nil {
		t.Fatal(err)
	}
	node, err := NodeHash(parsed.Entries)
	if err != nil {
		t.Fatal(err)
	}
	_, by, err = DecodeIndex(good, node)
	if err != nil {
		t.Fatal(err)
	}
	if by != ByNodeHash {
		t.Fatalf("addressing = %v, want the node hash", by)
	}

	_, _, err = DecodeIndex(good, strings.Repeat("f", 64))
	if !sameError(err, HashMismatch(strings.Repeat("f", 64), BlobHash(good))) {
		t.Fatalf("%v", err)
	}

	_, _, err = DecodeIndex([]byte("3\naaaa:0:doc:0"), file)
	var malformed *MalformedIndexError
	if !errors.As(err, &malformed) {
		t.Fatalf("parse should win: %v", err)
	}
	_, _, err = DecodeIndex([]byte{0xff}, file)
	if !errors.As(err, &malformed) || *malformed != *MalformedIndex("", "index is not UTF-8") {
		t.Fatalf("utf-8: %v", err)
	}
}

func TestIsHash(t *testing.T) {
	for _, ok := range []string{"ab", "DEADbeef", BlobHash(nil)} {
		if !IsHash(ok) {
			t.Errorf("%q should be a hash", ok)
		}
	}
	for _, bad := range []string{"", "..", "a/b", "zz", "ab\x00"} {
		if IsHash(bad) {
			t.Errorf("%q should not be a hash", bad)
		}
	}
}
