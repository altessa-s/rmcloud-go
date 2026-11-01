// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package rmcloud_test

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/altessa-s/rmcloud-go"
)

// Sinks keep the compiler from discarding the benchmarked calls.
var (
	sinkBenchIndex rmcloud.Index
	sinkBenchBlob  []byte
)

// benchCloud is a loopback sync store holding one root index of n entries
// and one blob. It records nothing, so the server stays the same size for
// the whole run.
type benchCloud struct {
	index     []byte
	indexHash string
	blob      []byte
	blobHash  string
}

func newBenchCloud(entries, blobSize int) *benchCloud {
	var b strings.Builder
	b.WriteString("3\n")
	for i := range entries {
		hash := rmcloud.BlobHash(fmt.Appendf(nil, "doc-%d", i))
		fmt.Fprintf(&b, "%s:80000000:doc-%d:2:%d\n", hash, i, 1024*(i+1))
	}
	index := []byte(b.String())
	blob := bytes.Repeat([]byte("x"), blobSize)
	return &benchCloud{index: index, indexHash: rmcloud.BlobHash(index), blob: blob, blobHash: rmcloud.BlobHash(blob)}
}

func (s *benchCloud) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch r.URL.Path {
	case "/sync/v4/root":
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(w, `{"hash":%q,"generation":1700000000000000,"schemaVersion":3}`, s.indexHash)
	case "/sync/v3/files/" + s.indexHash:
		_, _ = w.Write(s.index)
	case "/sync/v3/files/" + s.blobHash:
		w.Header().Set("Content-Length", strconv.Itoa(len(s.blob)))
		_, _ = w.Write(s.blob)
	default:
		http.NotFound(w, r)
	}
}

// benchClient is a registered client on a loopback cloud whose user token
// stays fresh, so no call in the loop buys a token.
func benchClient(b *testing.B, cloud *benchCloud) *rmcloud.Client {
	b.Helper()
	srv := httptest.NewServer(cloud)
	u, err := url.Parse(srv.URL)
	if err != nil {
		b.Fatal(err)
	}
	enc := base64.RawURLEncoding.EncodeToString
	payload := fmt.Sprintf(`{"exp":%d,"level":"connect","device-id":"device-1","scopes":"sync:full"}`,
		time.Now().Add(24*time.Hour).Unix())
	store := newMemStore()
	store.m[rmcloud.SecretDeviceToken] = "device-token"
	store.m[rmcloud.SecretUserToken] = enc([]byte(`{"alg":"none"}`)) + "." + enc([]byte(payload)) + "." + enc([]byte("sig"))
	c, err := rmcloud.New(store,
		rmcloud.WithResolver(nil),
		rmcloud.WithEndpoints(rmcloud.Endpoints{AuthHost: *u, SyncHost: *u}),
		rmcloud.WithUserAgent(testUA),
	)
	if err != nil {
		srv.Close()
		b.Fatal(err)
	}
	b.Cleanup(func() {
		c.StopNotifications()
		_ = c.Close()
		srv.Close()
	})
	return c
}

func BenchmarkClientRootIndex(b *testing.B) {
	for _, n := range []int{10, 1000} {
		b.Run(fmt.Sprintf("entries=%d", n), func(b *testing.B) {
			cloud := newBenchCloud(n, 1)
			c := benchClient(b, cloud)
			ctx := b.Context()
			b.ReportAllocs()
			b.SetBytes(int64(len(cloud.index)))
			for b.Loop() {
				_, ix, err := c.RootIndex(ctx)
				if err != nil {
					b.Fatal(err)
				}
				sinkBenchIndex = ix
			}
		})
	}
}

func BenchmarkClientBlob(b *testing.B) {
	for _, size := range []int{1 << 10, 1 << 20} {
		b.Run(fmt.Sprintf("bytes=%d", size), func(b *testing.B) {
			cloud := newBenchCloud(1, size)
			c := benchClient(b, cloud)
			ctx := b.Context()
			b.ReportAllocs()
			b.SetBytes(int64(size))
			for b.Loop() {
				blob, err := c.Blob(ctx, cloud.blobHash, "page.rm")
				if err != nil {
					b.Fatal(err)
				}
				sinkBenchBlob = blob
			}
		})
	}
}
