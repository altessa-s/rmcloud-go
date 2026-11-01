// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package domain

import (
	"cmp"
	"fmt"
	"math"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"
)

// Schema is the index file's own header. It is the digit in the file, not
// the root path's v3 or v4.
type Schema string

// The two index schemas this client reads.
const (
	SchemaV3 Schema = "3"
	SchemaV4 Schema = "4"
)

// Entry is one line of an index: hash:type:documentID:subfiles:size.
// Type is kept as it arrived. It does not say whether the record is a
// document; see [Entry.IsDocument].
type Entry struct {
	Hash       string
	Type       string
	DocumentID string
	Subfiles   int
	Size       int64
}

// Index is a parsed .docSchema file. Entries is the list that was there.
// Declared is the count a v4 file names on its count line, or nil for v3;
// it is kept so a caller can report a file that disagrees with itself.
type Index struct {
	Schema   Schema
	Entries  []Entry
	Declared *int
}

// CountMismatch reports a v4 count line that names a different number of
// records than the file carries.
func (ix Index) CountMismatch() bool {
	return ix.Declared != nil && *ix.Declared != len(ix.Entries)
}

// Addressing is which of an index's two hashes matched the address it was
// fetched by.
type Addressing int

const (
	// ByFileHash is the SHA-256 of the file's bytes.
	ByFileHash Addressing = iota
	// ByNodeHash is the SHA-256 of the children's hashes.
	ByNodeHash
)

// FileName is the identifier's last component. Empty pieces from a trailing
// or repeated slash are skipped. A name that is only slashes is returned as
// it stands.
func (e Entry) FileName() string {
	var last string
	found := false
	for part := range strings.SplitSeq(e.DocumentID, "/") {
		if part == "" {
			continue
		}
		last = part
		found = true
	}
	if !found {
		return e.DocumentID
	}
	return last
}

// FileExtension is the text after the last dot of [Entry.FileName]. A
// trailing dot has an empty extension. A dot before the last slash is not
// an extension.
func (e Entry) FileExtension() string {
	name := e.FileName()
	_, ext, _ := strings.CutLast(name, ".")
	return ext
}

// FileBaseName is [Entry.FileName] with the extension removed.
func (e Entry) FileBaseName() string {
	name := e.FileName()
	if base, _, found := strings.CutLast(name, "."); found {
		return base
	}
	return name
}

// IsDocument reports a root-index record whose name has no extension. The
// type field is not consulted: both "0" and "80000000" can mark the same
// kind of record.
func (e Entry) IsDocument() bool {
	return e.FileExtension() == ""
}

// Line is the record the way the file spells it.
func (e Entry) Line() string {
	return string(e.appendLine(make([]byte, 0, len(e.Hash)+len(e.Type)+len(e.DocumentID)+entryNumbersMax)))
}

// entryNumbersMax is room for the four separators and both integers.
const entryNumbersMax = 4 + 2*20

// appendLine appends [Entry.Line] to dst.
func (e Entry) appendLine(dst []byte) []byte {
	dst = append(dst, e.Hash...)
	dst = append(dst, ':')
	dst = append(dst, e.Type...)
	dst = append(dst, ':')
	dst = append(dst, e.DocumentID...)
	dst = append(dst, ':')
	dst = strconv.AppendInt(dst, int64(e.Subfiles), 10)
	dst = append(dst, ':')
	return strconv.AppendInt(dst, e.Size, 10)
}

// IndexName is the rm-filename of a document index. The server answers 400
// for any other suffix. The whole document identifier is used, not its last
// component.
func IndexName(documentID string) string {
	return documentID + IndexNameSuffix
}

// IndexName is [IndexName] for this record's document.
func (e Entry) IndexName() string {
	return IndexName(e.DocumentID)
}

// Entry returns the first record whose extension is ext.
func (ix Index) Entry(ext string) (Entry, bool) {
	for _, e := range ix.Entries {
		if e.FileExtension() == ext {
			return e, true
		}
	}
	return Entry{}, false
}

// Parse reads an index. Schema 3 and 4 are accepted. A v4 count line is
// required and checked, and its count is kept in [Index.Declared] rather
// than enforced. Empty lines after that count line are skipped.
func Parse(text string) (Index, error) {
	lines := splitNewlines(text)
	for i, line := range lines {
		lines[i] = strings.TrimSpace(line)
	}
	if len(lines) == 0 || lines[0] == "" {
		return Index{}, MalformedIndex("", "empty index")
	}
	header := lines[0]
	var schema Schema
	switch Schema(header) {
	case SchemaV3, SchemaV4:
		schema = Schema(header)
	default:
		return Index{}, UnsupportedSchema(header)
	}
	lines = lines[1:]

	var declared *int
	if schema == SchemaV4 {
		if len(lines) == 0 {
			return Index{}, MalformedIndex("", countLineReason)
		}
		count, ok := countedRecords(lines[0])
		if !ok {
			return Index{}, MalformedIndex(lines[0], countLineReason)
		}
		lines = lines[1:]
		declared = new(count)
	}

	entries := make([]Entry, 0, len(lines))
	for _, line := range lines {
		if line == "" {
			continue
		}
		entry, err := ParseEntry(line)
		if err != nil {
			return Index{}, err
		}
		entries = append(entries, entry)
	}
	if len(entries) == 0 {
		entries = nil
	}
	return Index{Schema: schema, Entries: entries, Declared: declared}, nil
}

const countLineReason = "expected a v4 count line of the form 0:<owner>:<count>:<size>"

// countedRecords reads a v4 count line. The owner is not checked: it is "."
// for the root and a document id inside a document, and the parser is handed
// both. A number that does not fit, or a negative count or size, is not a
// count line at all.
//
// A leading '+' on a number is accepted.
func countedRecords(line string) (int, bool) {
	fields := strings.Split(line, ":")
	if len(fields) != 4 || fields[0] != FileType {
		return 0, false
	}
	count, err := strconv.ParseInt(fields[2], 10, 64)
	if err != nil || count < 0 || count > int64(math.MaxInt) {
		return 0, false
	}
	size, err := strconv.ParseInt(fields[3], 10, 64)
	if err != nil || size < 0 {
		return 0, false
	}
	return int(count), true
}

// ParseEntry reads one record. Five fields, two of them integers. The values
// are kept as they arrive: refusing one odd record would fail the whole
// index, and for the root that is the library.
func ParseEntry(line string) (Entry, error) {
	var fields [5]string
	if n := splitFields(line, fields[:]); n != len(fields) {
		return Entry{}, MalformedIndex(line, fmt.Sprintf("expected 5 fields, got %d", n))
	}
	// Leading '+' is accepted; see countedRecords.
	subfiles, err := strconv.ParseInt(fields[3], 10, 64)
	if err != nil || subfiles > math.MaxInt || subfiles < math.MinInt {
		return Entry{}, MalformedIndex(line, "subfiles field is not a number")
	}
	size, err := strconv.ParseInt(fields[4], 10, 64)
	if err != nil {
		return Entry{}, MalformedIndex(line, "size field is not a number")
	}
	return Entry{
		Hash:       fields[0],
		Type:       fields[1],
		DocumentID: fields[2],
		Subfiles:   int(subfiles),
		Size:       size,
	}, nil
}

// splitFields cuts line at each ':' into fields without allocating. It
// returns how many fields strings.Split would have produced; only the first
// len(fields) are stored.
func splitFields(line string, fields []string) int {
	n := 0
	for {
		head, rest, found := strings.Cut(line, ":")
		if n < len(fields) {
			fields[n] = head
		}
		n++
		if !found {
			return n
		}
		line = rest
	}
}

// SerializeRoot writes a root index. The result is always schema 4, with
// owner ".". Records are ordered by documentID. The input slice is not
// reordered.
func SerializeRoot(entries []Entry) string {
	return serialize(entries, ".")
}

// SerializeDocument writes one document's index. The count line names id.
func SerializeDocument(entries []Entry, id string) string {
	return serialize(entries, id)
}

func serialize(entries []Entry, owner string) string {
	ordered := orderedEntries(entries)
	// The header and count lines take no more room than two records' numbers.
	const headerLines = 2
	size := len(SchemaV4) + len(owner) + headerLines*entryNumbersMax
	for _, entry := range ordered {
		size += len(entry.Hash) + len(entry.Type) + len(entry.DocumentID) + entryNumbersMax
	}
	b := make([]byte, 0, size)
	b = append(b, SchemaV4...)
	b = append(b, "\n0:"...)
	b = append(b, owner...)
	b = append(b, ':')
	b = strconv.AppendInt(b, int64(len(ordered)), 10)
	b = append(b, ':')
	b = strconv.AppendInt(b, totalBytes(ordered), 10)
	b = append(b, '\n')
	for _, entry := range ordered {
		b = entry.appendLine(b)
		b = append(b, '\n')
	}
	return string(b)
}

func orderedEntries(entries []Entry) []Entry {
	ordered := slices.Clone(entries)
	slices.SortStableFunc(ordered, func(a, b Entry) int {
		return cmp.Compare(a.DocumentID, b.DocumentID)
	})
	return ordered
}

// totalBytes sums sizes and saturates at MaxInt64 when a sum overflows or
// underflows. The saturated step is the new total; later sizes are still
// added. A total that ends below
// zero is written as zero: a count line with a negative size does not
// parse, and the index would not read back.
func totalBytes(entries []Entry) int64 {
	var total int64
	for _, entry := range entries {
		n := entry.Size
		sum := total + n
		if (n > 0 && sum < total) || (n < 0 && sum > total) {
			total = math.MaxInt64
			continue
		}
		total = sum
	}
	return max(total, 0)
}

// DecodeIndex parses an index and holds it to the address the caller was
// given. The file hash wins. The node hash is the other address the service
// has used; a failure to compute it is a mismatch, not a hex error. A parse
// error is returned before either comparison. The [Addressing] says which
// hash matched.
func DecodeIndex(data []byte, expected string) (Index, Addressing, error) {
	if !utf8.Valid(data) {
		return Index{}, ByFileHash, MalformedIndex("", "index is not UTF-8")
	}
	fileHash := BlobHash(data)
	index, err := Parse(string(data))
	if err != nil {
		return Index{}, ByFileHash, err
	}
	if fileHash == expected {
		return index, ByFileHash, nil
	}
	if node, err := NodeHash(index.Entries); err == nil && node == expected {
		return index, ByNodeHash, nil
	}
	return Index{}, ByFileHash, HashMismatch(expected, fileHash)
}

// splitNewlines breaks text at LF, CR, VT, FF, NEL, LS, and PS, and treats
// CR LF as one break. Empty text has no lines. A trailing break keeps
// the empty line after it.
func splitNewlines(text string) []string {
	if text == "" {
		return nil
	}
	// Converting to runes replaces each invalid byte with U+FFFD; the lines
	// below are substrings, so do that up front to keep the same result.
	if !utf8.ValidString(text) {
		text = string([]rune(text))
	}
	lines := make([]string, 0, strings.Count(text, "\n")+1)
	start := 0
	for i := 0; i < len(text); {
		r, width := utf8.DecodeRuneInString(text[i:])
		switch {
		case r == '\r' && i+1 < len(text) && text[i+1] == '\n':
			lines = append(lines, text[start:i])
			i += 2
			start = i
		case isNewline(r):
			lines = append(lines, text[start:i])
			i += width
			start = i
		default:
			i += width
		}
	}
	// A trailing break leaves start at the end, which keeps the empty line.
	return append(lines, text[start:])
}

func isNewline(r rune) bool {
	switch r {
	case '\n', '\r', '\v', '\f', '\u0085', '\u2028', '\u2029':
		return true
	default:
		return false
	}
}
