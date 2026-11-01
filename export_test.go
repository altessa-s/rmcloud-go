// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package rmcloud_test

import (
	"errors"
	"fmt"
	"testing"

	"github.com/altessa-s/rmcloud-go"
)

func TestPublicSurface(t *testing.T) {
	ix, err := rmcloud.Parse("3\naaaa:80000000:doc-1:2:120\nbbbb:80000000:doc-2:1:40")
	if err != nil {
		t.Fatal(err)
	}
	if ix.Schema != rmcloud.SchemaV3 || len(ix.Entries) != 2 {
		t.Fatalf("%#v", ix)
	}
	if !ix.Entries[0].IsDocument() {
		t.Fatal("a name without an extension is a document")
	}
	wrapped := fmt.Errorf("wrap: %w", rmcloud.ErrNotRegistered)
	if !errors.Is(wrapped, rmcloud.ErrNotRegistered) {
		t.Fatal("errors.Is")
	}
	got, ok := errors.AsType[rmcloud.Error](wrapped)
	if !ok || got.Kind() != rmcloud.KindNotRegistered {
		t.Fatalf("%v %v", got, ok)
	}
	status, ok := errors.AsType[*rmcloud.HTTPError](fmt.Errorf("wrap: %w", &rmcloud.HTTPError{Status: 503, Path: "/x"}))
	if !ok || status.Status != 503 || !rmcloud.IsKind(status, rmcloud.KindHTTP) {
		t.Fatalf("%v %v", status, ok)
	}
	prod := rmcloud.Production()
	root := prod.Root(rmcloud.RootV3)
	if root.Path != "/sync/v3/root" {
		t.Fatal(root.String())
	}
	if rmcloud.BlobHash([]byte("abc")) != "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad" {
		t.Fatal("hash")
	}
	page := rmcloud.RegistrationPage()
	if page.Host != "my.remarkable.com" {
		t.Fatal(page.String())
	}
	if rmcloud.DeviceDescMac != "desktop-macos" {
		t.Fatal("constants")
	}
	if rmcloud.SecretDeviceToken != "deviceToken" || rmcloud.RootHashName != "roothash" {
		t.Fatal("names")
	}
}

func TestErrorPredicates(t *testing.T) {
	tests := []struct {
		name        string
		err         error
		local, gone bool
	}{
		{name: "not found", err: &rmcloud.HTTPError{Status: 404, Path: "/x"}, local: true},
		{name: "server error", err: &rmcloud.HTTPError{Status: 503, Path: "/x"}},
		{name: "hash mismatch", err: &rmcloud.HashMismatchError{Expected: "aa", Actual: "bb"}, local: true},
		{name: "cannot find host", err: &rmcloud.TransportError{Reason: rmcloud.ReasonCannotFindHost}, gone: true},
		{name: "cannot connect", err: &rmcloud.TransportError{Reason: rmcloud.ReasonCannotConnectToHost}, gone: true},
		{name: "offline", err: &rmcloud.TransportError{Reason: rmcloud.ReasonNotConnectedToInternet}},
		{name: "not protocol", err: errors.New("plain")},
		{name: "nil", err: nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.err
			if err != nil {
				err = fmt.Errorf("wrap: %w", err)
			}
			if got := rmcloud.IsDocumentUnavailable(err); got != tt.local {
				t.Errorf("IsDocumentUnavailable(%v) = %v, want %v", err, got, tt.local)
			}
			if got := rmcloud.IsHostUnreachable(err); got != tt.gone {
				t.Errorf("IsHostUnreachable(%v) = %v, want %v", err, got, tt.gone)
			}
		})
	}
}

func TestIndexFunctions(t *testing.T) {
	entries := []rmcloud.Entry{
		{Hash: "bb", Type: rmcloud.FileType, DocumentID: "doc.pdf", Size: 40},
		{Hash: "aa", Type: rmcloud.FileType, DocumentID: "doc.metadata", Size: 10},
	}
	text := rmcloud.SerializeDocument(entries, "doc")
	if want := "4\n0:doc:2:50\naa:0:doc.metadata:0:10\nbb:0:doc.pdf:0:40\n"; text != want {
		t.Fatalf("SerializeDocument = %q, want %q", text, want)
	}

	node, err := rmcloud.NodeHash(entries)
	if err != nil {
		t.Fatal(err)
	}
	// The children's hashes in documentID order: aa, then bb.
	if want := "d798d1fac6bd4bb1c11f50312760351013379a0ab6f0a8c0af8a506b96b2525a"; node != want {
		t.Fatalf("NodeHash = %s, want %s", node, want)
	}
	if _, err := rmcloud.NodeHash([]rmcloud.Entry{{Hash: "zz", DocumentID: "x"}}); !rmcloud.IsKind(err, rmcloud.KindMalformedIndex) {
		t.Fatalf("NodeHash of a non-hex hash: %v", err)
	}

	data := []byte(text)
	for name, expected := range map[string]string{"file hash": rmcloud.BlobHash(data), "node hash": node} {
		index, err := rmcloud.DecodeIndex(data, expected)
		if err != nil || len(index.Entries) != 2 {
			t.Fatalf("DecodeIndex by %s: %d entries, %v", name, len(index.Entries), err)
		}
	}
	if _, err := rmcloud.DecodeIndex(data, rmcloud.BlobHash([]byte("other"))); !rmcloud.IsKind(err, rmcloud.KindHashMismatch) {
		t.Fatalf("DecodeIndex at the wrong address: %v", err)
	}

	if got := rmcloud.IndexName("doc"); got != "doc.docSchema" {
		t.Fatalf("IndexName = %q", got)
	}
}

func TestWireSpellings(t *testing.T) {
	if got := rmcloud.GoogHash([]byte("123456789")); got != "crc32c=4waSgw==" {
		t.Fatalf("GoogHash = %q", got)
	}
	for name, want := range map[string]string{
		"doc.docSchema": rmcloud.ContentTypeIndex,
		"doc.pdf":       rmcloud.ContentTypeBlob,
		"doc.metadata":  rmcloud.ContentTypeBlob,
	} {
		if got := rmcloud.ContentType(name); got != want {
			t.Errorf("ContentType(%q) = %q, want %q", name, got, want)
		}
	}
}
