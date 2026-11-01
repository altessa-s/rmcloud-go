// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package syncstore

import (
	"bytes"
	"context"
	"fmt"
	"testing"

	"github.com/altessa-s/rmcloud-go/internal/adapter/fs"
	"github.com/altessa-s/rmcloud-go/internal/domain"
	"github.com/altessa-s/rmcloud-go/internal/port"
	"github.com/altessa-s/rmcloud-go/internal/porttest"
)

// Sinks keep the compiler from discarding the benchmarked calls.
var (
	sinkRoot  domain.Root
	sinkIndex domain.Index
	sinkBytes []byte
	sinkHash  string
	sinkGen   int64
)

// staticTransport answers every request for a path with the same response
// and records nothing, so a benchmark measures the client and not a queue.
// A path it does not know is a 404.
type staticTransport struct {
	byPath map[string]port.Response
}

func (s staticTransport) RoundTrip(ctx context.Context, req port.Request) (port.Response, error) {
	if err := ctx.Err(); err != nil {
		return port.Response{}, err
	}
	resp, ok := s.byPath[req.URL.Path]
	if !ok {
		return port.Response{Status: domain.StatusNotFound}, nil
	}
	return resp, nil
}

func (s staticTransport) Download(ctx context.Context, req port.Request) (port.Response, error) {
	return s.RoundTrip(ctx, req)
}

func (s staticTransport) Upload(ctx context.Context, req port.Request) (port.Response, error) {
	return s.RoundTrip(ctx, req)
}

var _ port.Transport = staticTransport{}

// benchClient is a client over a static transport with the production hosts.
func benchClient(byPath map[string]port.Response) *Client {
	return New(&fixedTokens{token: "user-1"}, staticTransport{byPath: byPath}, fs.Files{}, porttest.NewClock(),
		WithPolicy(fastPolicy), WithUserAgent("bench-agent"))
}

// benchIndex is a document index of n entries and its file hash.
func benchIndex(n int) ([]byte, string) {
	entries := make([]domain.Entry, n)
	for i := range entries {
		entries[i] = domain.Entry{
			Hash:       domain.BlobHash(fmt.Appendf(nil, "file-%d", i)),
			Type:       domain.FileType,
			DocumentID: fmt.Sprintf("doc/%d.rm", i),
			Size:       int64(1024 * (i + 1)),
		}
	}
	text := []byte(domain.SerializeDocument(entries, "doc"))
	return text, domain.BlobHash(text)
}

func BenchmarkRoot(b *testing.B) {
	endpoints := domain.Production()
	body := rootBody(domain.BlobHash([]byte("root")), 1700000000000000, `"schemaVersion":4`)
	for _, tc := range []struct {
		name    string
		version domain.RootVersion
	}{
		{"v4", domain.RootV4},
		{"v3-fallback", domain.RootV3},
	} {
		b.Run(tc.name, func(b *testing.B) {
			c := benchClient(map[string]port.Response{
				endpoints.Root(tc.version).Path: {Status: domain.StatusOK, Body: body},
			})
			ctx := b.Context()
			b.ReportAllocs()
			for b.Loop() {
				root, err := c.Root(ctx)
				if err != nil {
					b.Fatal(err)
				}
				sinkRoot = root
			}
		})
	}
}

func BenchmarkIndex(b *testing.B) {
	endpoints := domain.Production()
	for _, n := range []int{200, 5000} {
		text, hash := benchIndex(n)
		c := benchClient(map[string]port.Response{
			endpoints.Blob(hash).Path: {Status: domain.StatusOK, Body: text},
		})
		b.Run(fmt.Sprintf("entries=%d", n), func(b *testing.B) {
			ctx := b.Context()
			b.ReportAllocs()
			b.SetBytes(int64(len(text)))
			for b.Loop() {
				ix, err := c.Index(ctx, hash, "doc.docSchema")
				if err != nil {
					b.Fatal(err)
				}
				sinkIndex = ix
			}
		})
	}
}

func BenchmarkRootIndex(b *testing.B) {
	endpoints := domain.Production()
	text, hash := benchIndex(200)
	c := benchClient(map[string]port.Response{
		endpoints.Root(domain.RootV4).Path: {Status: domain.StatusOK, Body: rootBody(hash, 1, "")},
		endpoints.Blob(hash).Path:          {Status: domain.StatusOK, Body: text},
	})
	ctx := b.Context()
	b.ReportAllocs()
	for b.Loop() {
		_, ix, err := c.RootIndex(ctx)
		if err != nil {
			b.Fatal(err)
		}
		sinkIndex = ix
	}
}

func BenchmarkBlob(b *testing.B) {
	endpoints := domain.Production()
	for _, size := range []int{1 << 10, 1 << 20} {
		data := bytes.Repeat([]byte("x"), size)
		hash := domain.BlobHash(data)
		c := benchClient(map[string]port.Response{
			endpoints.Blob(hash).Path: {Status: domain.StatusOK, Body: data},
		})
		for _, verify := range []bool{false, true} {
			b.Run(fmt.Sprintf("bytes=%d/verify=%t", size, verify), func(b *testing.B) {
				ctx := b.Context()
				b.ReportAllocs()
				b.SetBytes(int64(size))
				for b.Loop() {
					got, err := c.Blob(ctx, hash, "page.rm", verify)
					if err != nil {
						b.Fatal(err)
					}
					sinkBytes = got
				}
			})
		}
	}
}

func BenchmarkUpload(b *testing.B) {
	endpoints := domain.Production()
	for _, size := range []int{1 << 10, 1 << 20} {
		data := bytes.Repeat([]byte("x"), size)
		hash := domain.BlobHash(data)
		c := benchClient(map[string]port.Response{
			endpoints.Blob(hash).Path: {Status: domain.StatusOK},
		})
		b.Run(fmt.Sprintf("bytes=%d", size), func(b *testing.B) {
			ctx := b.Context()
			b.ReportAllocs()
			b.SetBytes(int64(size))
			for b.Loop() {
				got, err := c.Upload(ctx, data, "page.rm", "")
				if err != nil {
					b.Fatal(err)
				}
				sinkHash = got
			}
		})
	}
}

func BenchmarkPutRoot(b *testing.B) {
	endpoints := domain.Production()
	hash := domain.BlobHash([]byte("root"))
	c := benchClient(map[string]port.Response{
		endpoints.RootWrite().Path: {Status: domain.StatusOK, Body: rootBody(hash, 2, "")},
	})
	ctx := b.Context()
	b.ReportAllocs()
	for b.Loop() {
		generation, err := c.PutRoot(ctx, hash, 1, true)
		if err != nil {
			b.Fatal(err)
		}
		sinkGen = generation
	}
}

func BenchmarkMissingFiles(b *testing.B) {
	endpoints := domain.Production()
	hashes := make([]string, 200)
	for i := range hashes {
		hashes[i] = domain.BlobHash(fmt.Appendf(nil, "file-%d", i))
	}
	answer := []byte(`{"missingFiles":["` + hashes[3] + `","` + hashes[150] + `"]}`)
	c := benchClient(map[string]port.Response{
		endpoints.CheckFiles().Path: {Status: domain.StatusOK, Body: answer},
	})
	ctx := b.Context()
	b.ReportAllocs()
	for b.Loop() {
		missing, err := c.MissingFiles(ctx, hashes)
		if err != nil {
			b.Fatal(err)
		}
		sinkHash = missing[0]
	}
}
