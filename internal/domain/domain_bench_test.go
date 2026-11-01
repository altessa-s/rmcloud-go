// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package domain

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"testing"
	"time"
)

// benchSizes are the index sizes a sync meets: a large document and a root
// listing a whole library.
var benchSizes = []int{200, 5000}

// Sinks keep the compiler from discarding the benchmarked calls.
var (
	sinkString string
	sinkBool   bool
	sinkIndex  Index
	sinkError  error
)

// benchEntries is a document of n files, the shape a sync walks.
func benchEntries(n int) []Entry {
	entries := make([]Entry, n)
	for i := range entries {
		entries[i] = Entry{
			Hash:       BlobHash(fmt.Appendf(nil, "file-%d", i)),
			Type:       "0",
			DocumentID: fmt.Sprintf("doc/%d.rm", i),
			Subfiles:   0,
			Size:       int64(1024 * (i + 1)),
		}
	}
	return entries
}

// benchToken is an unsigned JWT carrying the claims the client reads.
func benchToken() string {
	enc := base64.RawURLEncoding.EncodeToString
	payload := `{"device-id":"11111111-1111-4111-8111-111111111111","exp":1893456000,` +
		`"scopes":"sync:tortoise docedit intgr screenshare hwcmail:-1 mail:-1","tectonic":"eu"}`
	return enc([]byte(`{"alg":"none"}`)) + "." + enc([]byte(payload)) + "." + enc([]byte("sig"))
}

func BenchmarkParseDocumentIndex(b *testing.B) {
	for _, n := range benchSizes {
		text := SerializeDocument(benchEntries(n), "doc")
		b.Run(fmt.Sprintf("entries=%d", n), func(b *testing.B) {
			b.ReportAllocs()
			b.SetBytes(int64(len(text)))
			for b.Loop() {
				ix, err := Parse(text)
				if err != nil {
					b.Fatal(err)
				}
				sinkIndex = ix
			}
		})
	}
}

func BenchmarkSerializeDocument(b *testing.B) {
	for _, n := range benchSizes {
		entries := benchEntries(n)
		b.Run(fmt.Sprintf("entries=%d", n), func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				sinkString = SerializeDocument(entries, "doc")
			}
		})
	}
}

func BenchmarkSerializeRoot(b *testing.B) {
	entries := benchEntries(benchSizes[len(benchSizes)-1])
	b.ReportAllocs()
	for b.Loop() {
		sinkString = SerializeRoot(entries)
	}
}

func BenchmarkDecodeIndex(b *testing.B) {
	for _, n := range benchSizes {
		data := []byte(SerializeDocument(benchEntries(n), "doc"))
		fileHash := BlobHash(data)
		ix, err := Parse(string(data))
		if err != nil {
			b.Fatal(err)
		}
		nodeHash, err := NodeHash(ix.Entries)
		if err != nil {
			b.Fatal(err)
		}
		for _, tc := range []struct {
			name, expected string
		}{
			{"file", fileHash},
			{"node", nodeHash},
		} {
			b.Run(fmt.Sprintf("entries=%d/by=%s", n, tc.name), func(b *testing.B) {
				b.ReportAllocs()
				b.SetBytes(int64(len(data)))
				for b.Loop() {
					got, _, err := DecodeIndex(data, tc.expected)
					if err != nil {
						b.Fatal(err)
					}
					sinkIndex = got
				}
			})
		}
	}
}

func BenchmarkNodeHash(b *testing.B) {
	for _, n := range benchSizes {
		entries := benchEntries(n)
		b.Run(fmt.Sprintf("entries=%d", n), func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				h, err := NodeHash(entries)
				if err != nil {
					b.Fatal(err)
				}
				sinkString = h
			}
		})
	}
}

func BenchmarkBlobHash(b *testing.B) {
	data := bytes.Repeat([]byte("x"), 1<<20)
	b.ReportAllocs()
	b.SetBytes(int64(len(data)))
	for b.Loop() {
		sinkString = BlobHash(data)
	}
}

func BenchmarkDigestReader(b *testing.B) {
	data := bytes.Repeat([]byte("x"), 1<<20)
	ctx := context.Background()
	b.ReportAllocs()
	b.SetBytes(int64(len(data)))
	for b.Loop() {
		d, err := DigestReader(ctx, bytes.NewReader(data))
		if err != nil {
			b.Fatal(err)
		}
		sinkString = d.SHA256
	}
}

// BenchmarkGoogHash is the CRC32C checksum an upload sends, with its
// encoding.
func BenchmarkGoogHash(b *testing.B) {
	data := bytes.Repeat([]byte("x"), 1<<20)
	b.ReportAllocs()
	b.SetBytes(int64(len(data)))
	for b.Loop() {
		sinkString = GoogHash(data)
	}
}

func BenchmarkParseRoot(b *testing.B) {
	body := []byte(`{"hash":"` + BlobHash([]byte("root")) + `","generation":1700000000000000,` +
		`"schemaVersion":4,"missingHashes":[],"disableHTTP2":false}`)
	b.ReportAllocs()
	b.SetBytes(int64(len(body)))
	for b.Loop() {
		root, _, err := ParseRoot(body)
		if err != nil {
			b.Fatal(err)
		}
		sinkString = root.Hash
	}
}

func BenchmarkCheckFiles(b *testing.B) {
	hashes := make([]string, 200)
	for i := range hashes {
		hashes[i] = BlobHash(fmt.Appendf(nil, "file-%d", i))
	}
	b.Run("body", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			body, err := CheckFilesBody(hashes)
			if err != nil {
				b.Fatal(err)
			}
			sinkBool = len(body) == 0
		}
	})
	answer := []byte(`{"missingFiles":["` + hashes[3] + `","` + hashes[150] + `"]}`)
	b.Run("parse", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			missing, err := ParseMissingFiles(answer, hashes)
			if err != nil {
				b.Fatal(err)
			}
			sinkBool = len(missing) == 0
		}
	})
}

func BenchmarkClaims(b *testing.B) {
	token := benchToken()
	moment := time.Unix(1800000000, 0)
	b.Run("claims", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			sinkBool = Claims(token) == nil
		}
	})
	b.Run("expiry", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			sinkBool = Expiry(token) == nil
		}
	})
	b.Run("is-fresh", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			sinkBool = IsFresh(token, moment)
		}
	})
	b.Run("plan", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			sinkBool = PlanFromToken(token) == nil
		}
	})
}

func BenchmarkTrustedHost(b *testing.B) {
	for _, tc := range []struct{ name, host string }{
		{"accepted", "  Internal.Cloud.Remarkable.Com "},
		{"refused", "evil.example.com"},
	} {
		b.Run(tc.name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				_, ok := TrustedHost(tc.host)
				sinkBool = ok
			}
		})
	}
}

func BenchmarkFromHTTP(b *testing.B) {
	body := []byte(`  {"message":"the plan does not include this"}  `)
	for _, tc := range []struct {
		name   string
		status int
	}{
		{"payment-required", StatusPaymentRequired},
		{"too-many-requests", StatusTooManyRequests},
		{"server-error", 503},
	} {
		b.Run(tc.name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				sinkError = FromHTTP(tc.status, "https://host.remarkable.com/sync/v3/root?x=1", "12", body, nil)
			}
		})
	}
}

func BenchmarkClassify(b *testing.B) {
	wrapped := fmt.Errorf("read root: %w", fmt.Errorf("attempt 3: %w", HTTP(404, "/sync/v3/files/x")))
	b.Run("kind-of/wrapped", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			_, ok := KindOf(wrapped)
			sinkBool = ok
		}
	})
	b.Run("document-local", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			sinkBool = IsDocumentUnavailable(wrapped)
		}
	})
}
