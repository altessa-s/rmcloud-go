// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package domain

import (
	"errors"
	"math"
	"strings"
	"testing"
)

func TestParsesSchemaV3(t *testing.T) {
	text := "3\naaaa:80000000:doc-1:2:120\nbbbb:80000000:doc-2:1:40"
	ix, err := Parse(text)
	if err != nil {
		t.Fatal(err)
	}
	if ix.Schema != SchemaV3 || len(ix.Entries) != 2 {
		t.Fatalf("%#v", ix)
	}
	if ix.Entries[0].DocumentID != "doc-1" || ix.Entries[0].Subfiles != 2 || ix.Entries[0].Size != 120 {
		t.Fatalf("%#v", ix.Entries[0])
	}
	if ix.Entries[0].Type != DocumentType {
		t.Fatalf("type = %q", ix.Entries[0].Type)
	}
}

func TestParsesSchemaV4(t *testing.T) {
	text := "4\n0:.:2:160\naaaa:80000000:doc-1:2:120\nbbbb:80000000:doc-2:1:40"
	ix, err := Parse(text)
	if err != nil {
		t.Fatal(err)
	}
	if ix.Schema != SchemaV4 || len(ix.Entries) != 2 {
		t.Fatalf("%#v", ix)
	}
}

func TestParsesCRLFIndex(t *testing.T) {
	lines := []string{"4", "0:.:2:160", "aaaa:80000000:doc-1:2:120", "bbbb:80000000:doc-2:1:40"}
	unix := strings.Join(lines, "\n")
	windows := strings.Join(lines, "\r\n") + "\r\n"
	left, err := Parse(unix)
	if err != nil {
		t.Fatal(err)
	}
	right, err := Parse(windows)
	if err != nil {
		t.Fatal(err)
	}
	if left.Schema != right.Schema || len(left.Entries) != len(right.Entries) {
		t.Fatalf("unix %#v windows %#v", left, right)
	}
	for i := range left.Entries {
		if left.Entries[i] != right.Entries[i] {
			t.Fatalf("line %d unix %#v windows %#v", i, left.Entries[i], right.Entries[i])
		}
	}
}

func TestToleratesCountMismatch(t *testing.T) {
	text := "4\n0:.:99:160\naaaa:80000000:doc-1:2:120"
	ix, err := Parse(text)
	if err != nil {
		t.Fatal(err)
	}
	if len(ix.Entries) != 1 || ix.Entries[0].DocumentID != "doc-1" {
		t.Fatalf("%#v", ix)
	}
	if ix.Declared == nil || *ix.Declared != 99 || !ix.CountMismatch() {
		t.Fatalf("declared = %v mismatch = %v", ix.Declared, ix.CountMismatch())
	}
	v3, err := Parse("3\naaaa:0:doc:0:1")
	if err != nil {
		t.Fatal(err)
	}
	if v3.Declared != nil || v3.CountMismatch() {
		t.Fatalf("v3 declared = %v", v3.Declared)
	}
}

func TestRejectsUnknownSchema(t *testing.T) {
	_, err := Parse("7\naaaa:0:doc:0:1")
	if !sameError(err, UnsupportedSchema("7")) {
		t.Fatalf("%v", err)
	}
}

func TestRejectsShortLine(t *testing.T) {
	_, err := Parse("3\naaaa:0:doc:0")
	if !sameError(err, MalformedIndex("aaaa:0:doc:0", "expected 5 fields, got 4")) {
		t.Fatalf("%v", err)
	}
}

func TestEmptyIndex(t *testing.T) {
	for _, text := range []string{"", "   ", "\n", "\r\n"} {
		_, err := Parse(text)
		if !sameError(err, MalformedIndex("", "empty index")) {
			t.Fatalf("%q: %v", text, err)
		}
	}
}

func TestV4CountLineRequired(t *testing.T) {
	for _, text := range []string{"4", "4\n", "4\nnot-a-count"} {
		_, err := Parse(text)
		var got *MalformedIndexError
		if !errors.As(err, &got) || got.Reason != countLineReason {
			t.Fatalf("%q: %v", text, err)
		}
	}
	ix, err := Parse("3\n")
	if err != nil || len(ix.Entries) != 0 || ix.Schema != SchemaV3 {
		t.Fatalf("v3 empty = %#v %v", ix, err)
	}
}

func TestSerializesRootAsV4(t *testing.T) {
	entries := []Entry{
		{Hash: "bb", Type: FileType, DocumentID: "doc.pdf", Size: 90},
		{Hash: "aa", Type: FileType, DocumentID: "doc.metadata", Size: 10},
	}
	out := SerializeRoot(entries)
	if !strings.HasPrefix(out, "4\n0:.:2:100\n") {
		t.Fatalf("prefix = %q", out)
	}
	if !strings.Contains(out, "aa:0:doc.metadata:0:10\n") || !strings.Contains(out, "bb:0:doc.pdf:0:90\n") {
		t.Fatalf("%q", out)
	}
	meta := strings.Index(out, "doc.metadata")
	pdf := strings.Index(out, "doc.pdf")
	if meta < 0 || pdf < 0 || meta > pdf {
		t.Fatalf("order = %q", out)
	}
	if entries[0].DocumentID != "doc.pdf" {
		t.Fatal("input was reordered")
	}
}

func TestSerializesDocumentIndex(t *testing.T) {
	entries := []Entry{
		{Hash: "bb", Type: FileType, DocumentID: "doc/b.rm", Size: 5},
		{Hash: "aa", Type: FileType, DocumentID: "doc/a.metadata", Size: 7},
	}
	want := "4\n0:doc:2:12\naa:0:doc/a.metadata:0:7\nbb:0:doc/b.rm:0:5\n"
	if got := SerializeDocument(entries, "doc"); got != want {
		t.Fatalf("got %q", got)
	}
}

func TestSerializePreservesType(t *testing.T) {
	entries := []Entry{{Hash: "aa", Type: DocumentType, DocumentID: "doc", Size: 1}}
	out := SerializeRoot(entries)
	if !strings.Contains(out, "aa:80000000:doc:0:1\n") {
		t.Fatalf("%q", out)
	}
}

func TestIndexTotalSaturates(t *testing.T) {
	two := []Entry{
		{Hash: "aa", Type: FileType, DocumentID: "a", Size: math.MaxInt64},
		{Hash: "bb", Type: FileType, DocumentID: "b", Size: math.MaxInt64},
	}
	if got := SerializeDocument(two, "doc"); !strings.Contains(got, "0:doc:2:9223372036854775807\n") {
		t.Fatalf("%q", got)
	}
	under := []Entry{
		{Hash: "aa", Type: FileType, DocumentID: "a", Size: math.MinInt64},
		{Hash: "bb", Type: FileType, DocumentID: "b", Size: -1},
	}
	if got := SerializeDocument(under, "doc"); !strings.Contains(got, "0:doc:2:9223372036854775807\n") {
		t.Fatalf("underflow %q", got)
	}
	noWrap := []Entry{
		{Hash: "aa", Type: FileType, DocumentID: "a", Size: math.MaxInt64},
		{Hash: "bb", Type: FileType, DocumentID: "b", Size: -1},
	}
	if got := totalBytes(noWrap); got != math.MaxInt64-1 {
		t.Fatalf("max-1 = %d", got)
	}
	step := []Entry{
		{Hash: "aa", Type: FileType, DocumentID: "a", Size: math.MaxInt64},
		{Hash: "bb", Type: FileType, DocumentID: "b", Size: 1},
		{Hash: "cc", Type: FileType, DocumentID: "c", Size: -100},
	}
	if got := totalBytes(step); got != math.MaxInt64-100 {
		t.Fatalf("max-100 = %d", got)
	}
	negative := []Entry{{Hash: "aa", Type: FileType, DocumentID: "a", Size: -10}}
	got := SerializeRoot(negative)
	if !strings.Contains(got, "0:.:1:0\n") {
		t.Fatalf("negative %q", got)
	}
	if _, err := Parse(got); err != nil {
		t.Fatalf("negative total does not read back: %v", err)
	}
}

func TestFindsEntryByExtension(t *testing.T) {
	ix, err := Parse("3\naa:0:doc.metadata:0:10\nbb:0:doc.pdf:0:90")
	if err != nil {
		t.Fatal(err)
	}
	pdf, ok := ix.Entry("pdf")
	if !ok || pdf.Hash != "bb" {
		t.Fatalf("%#v %v", pdf, ok)
	}
	if _, ok := ix.Entry("epub"); ok {
		t.Fatal("epub should be absent")
	}
}

func TestFileNameEdges(t *testing.T) {
	cases := []struct {
		id, name, ext, base string
		doc                 bool
	}{
		{"a/b/", "b", "", "b", true},
		{"/", "/", "", "/", true},
		{"file.", "file.", "", "file", true},
		{"file.tar.gz", "file.tar.gz", "gz", "file.tar", false},
		{".hidden", ".hidden", "hidden", "", false},
		{"my.doc/page.rm", "page.rm", "rm", "page", false},
	}
	for _, tc := range cases {
		e := Entry{DocumentID: tc.id}
		if e.FileName() != tc.name || e.FileExtension() != tc.ext || e.FileBaseName() != tc.base || e.IsDocument() != tc.doc {
			t.Errorf("%q -> name %q ext %q base %q doc %v", tc.id, e.FileName(), e.FileExtension(), e.FileBaseName(), e.IsDocument())
		}
	}
}

func TestIndexName(t *testing.T) {
	e := Entry{DocumentID: "folder/doc"}
	if IndexName("folder/doc") != "folder/doc.docSchema" || e.IndexName() != "folder/doc.docSchema" {
		t.Fatal(IndexName("folder/doc"))
	}
}

func TestBadNumbers(t *testing.T) {
	_, err := Parse("3\naa:0:doc:nope:1")
	var got *MalformedIndexError
	if !errors.As(err, &got) || got.Reason != "subfiles field is not a number" {
		t.Fatalf("%v", err)
	}
	_, err = Parse("3\naa:0:doc:1:nope")
	if !errors.As(err, &got) || got.Reason != "size field is not a number" {
		t.Fatalf("%v", err)
	}
}

func FuzzParse(f *testing.F) {
	f.Add("3\naaaa:80000000:doc-1:2:120\nbbbb:80000000:doc-2:1:40")
	f.Add("4\n0:.:2:160\naaaa:80000000:doc-1:2:120\nbbbb:80000000:doc-2:1:40")
	f.Add("4\r\n0:.:1:10\r\naa:0:doc.pdf:0:10\r\n")
	f.Add("3\n")
	f.Add("")
	f.Fuzz(func(t *testing.T, text string) {
		ix, err := Parse(text)
		if err != nil {
			return
		}
		serialized := SerializeRoot(ix.Entries)
		round, err := Parse(serialized)
		if err != nil {
			t.Fatalf("SerializeRoot output failed to parse: %v", err)
		}
		if len(round.Entries) != len(ix.Entries) {
			t.Fatalf("entries len %d != %d", len(round.Entries), len(ix.Entries))
		}
	})
}
