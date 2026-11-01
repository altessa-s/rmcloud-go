// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package syncstore

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/altessa-s/go-atlas-core/types/redacted"

	"github.com/altessa-s/rmcloud-go/internal/adapter/fs"
	"github.com/altessa-s/rmcloud-go/internal/app/auth"
	"github.com/altessa-s/rmcloud-go/internal/app/retry"
	"github.com/altessa-s/rmcloud-go/internal/domain"
	"github.com/altessa-s/rmcloud-go/internal/domaintest"
	"github.com/altessa-s/rmcloud-go/internal/port"
	"github.com/altessa-s/rmcloud-go/internal/porttest"
)

// The sync client accepts the real token provider. The assertion lives in
// the test so production does not import token.
var _ Tokens = (*auth.Provider)(nil)

var fastPolicy = retry.Policy{
	MaxAttempts: 3,
	BaseDelay:   10 * time.Millisecond,
	Multiplier:  3,
	MaxDelay:    time.Hour,
	Jitter:      0,
}

type rig struct {
	t       *testing.T
	trip    *porttest.Transport
	sleeper *porttest.Clock
	tokens  Tokens
	hosts   *countHosts
	client  *Client
}

func newRig(t *testing.T, tokens Tokens, opts ...Option) *rig {
	t.Helper()
	if tokens == nil {
		tokens = &fixedTokens{token: "user-1"}
	}
	trip := &porttest.Transport{}
	sleeper := porttest.NewClock()
	hosts := &countHosts{endpoints: domain.Production()}
	base := []Option{
		WithPolicy(fastPolicy),
		WithUserAgent("test-agent"),
		WithHosts(hosts),
	}
	all := make([]Option, 0, len(base)+len(opts))
	all = append(all, base...)
	all = append(all, opts...)
	return &rig{
		t:       t,
		trip:    trip,
		sleeper: sleeper,
		tokens:  tokens,
		hosts:   hosts,
		client:  New(tokens, trip, fs.Files{}, sleeper, all...),
	}
}

type fixedTokens struct {
	token string
	buys  int
}

func (f *fixedTokens) UserToken(context.Context) (redacted.RedactedString, error) {
	f.buys++
	return redacted.RedactedString(f.token), nil
}

func (f *fixedTokens) InvalidateUserToken(redacted.RedactedString) {}

type seqTokens struct {
	mu      sync.Mutex
	tokens  []string
	refused []string
}

func (s *seqTokens) UserToken(context.Context) (redacted.RedactedString, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.tokens) == 0 {
		return "", errors.New("rmcloud: test token exhausted")
	}
	token := s.tokens[0]
	s.tokens = s.tokens[1:]
	return redacted.RedactedString(token), nil
}

func (s *seqTokens) InvalidateUserToken(refused redacted.RedactedString) {
	s.mu.Lock()
	s.refused = append(s.refused, refused.Expose())
	s.mu.Unlock()
}

func (s *seqTokens) refusedTokens() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.refused)
}

type countHosts struct {
	endpoints domain.Endpoints
	failed    int
	reads     int
}

func (h *countHosts) Endpoints(context.Context) domain.Endpoints {
	h.reads++
	return h.endpoints
}

func (h *countHosts) SyncHostFailed() { h.failed++ }

// keepFile records the file Download produced, including an empty path when
// the ceiling refused the body before a file existed.
type keepFile struct {
	*porttest.Transport
	file string
}

func (k *keepFile) Download(ctx context.Context, req port.Request) (port.Response, error) {
	resp, err := k.Transport.Download(ctx, req)
	k.file = resp.File
	return resp, err
}

func (r *rig) watchFiles() *keepFile {
	k := &keepFile{Transport: r.trip}
	r.client.transport = k
	return k
}

var _ port.Transport = (*keepFile)(nil)

func rootBody(hash string, generation int64, extra string) []byte {
	if extra == "" {
		return fmt.Appendf(nil, `{"hash":%q,"generation":%d}`, hash, generation)
	}
	return fmt.Appendf(nil, `{"hash":%q,"generation":%d,%s}`, hash, generation, extra)
}

func headerOne(h port.Header, name string) (string, bool) {
	for key, values := range h {
		if strings.EqualFold(key, name) && len(values) > 0 {
			return values[0], true
		}
	}
	return "", false
}

func puts(calls []port.Request) []port.Request {
	var out []port.Request
	for _, call := range calls {
		if call.Method == http.MethodPut {
			out = append(out, call)
		}
	}
	return out
}

func wantEqual(t *testing.T, err error, want domain.Error) {
	t.Helper()
	if !domaintest.SameError(err, want) {
		t.Fatalf("got %v, want %v", err, want)
	}
}

func wantPaths(t *testing.T, calls []port.Request, paths ...string) {
	t.Helper()
	got := make([]string, len(calls))
	for i, call := range calls {
		got[i] = call.URL.Path
	}
	if !slices.Equal(got, paths) {
		t.Fatalf("paths %v, want %v", got, paths)
	}
}

func wantFilename(t *testing.T, calls []port.Request, name string) {
	t.Helper()
	for i, call := range calls {
		got, ok := headerOne(call.Header, domain.HeaderFilename)
		if !ok || got != name {
			t.Fatalf("call %d filename %q, want %q", i, got, name)
		}
	}
}

func wantBearer(t *testing.T, call port.Request, token string) {
	t.Helper()
	got, ok := headerOne(call.Header, domain.HeaderAuthorization)
	if !ok || got != "Bearer "+token {
		t.Fatalf("authorization %q, want Bearer %s", got, token)
	}
}

func wantDelays(t *testing.T, got, want []time.Duration) {
	t.Helper()
	if !slices.Equal(got, want) {
		t.Fatalf("delays %v, want %v", got, want)
	}
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func writeTempFile(t *testing.T, name string, data []byte) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestReadsIndexByFileHash(t *testing.T) {
	t.Parallel()
	text := []byte("3\naaaa:0:doc-1:0:10\n")
	hash := domain.BlobHash(text)
	r := newRig(t, nil)
	r.trip.Enqueue(porttest.Reply{Body: text})
	got, err := r.client.Index(t.Context(), hash, domain.RootIndexName)
	must(t, err)
	if got.Schema != domain.SchemaV3 || len(got.Entries) != 1 {
		t.Fatalf("%+v", got)
	}
	want := domain.Entry{Hash: "aaaa", Type: domain.FileType, DocumentID: "doc-1", Subfiles: 0, Size: 10}
	if got.Entries[0] != want {
		t.Fatalf("%+v", got.Entries[0])
	}
	wantPaths(t, r.trip.Calls(), "/sync/v3/files/"+hash)
}

func TestReadsIndexByNodeHash(t *testing.T) {
	t.Parallel()
	text := "3\naaaa:80000000:doc-1:1:210\n"
	parsed, err := domain.Parse(text)
	must(t, err)
	node, err := domain.NodeHash(parsed.Entries)
	must(t, err)
	if node == domain.BlobHash([]byte(text)) {
		t.Fatal("node hash matched the file hash")
	}
	r := newRig(t, nil)
	r.trip.Enqueue(porttest.Reply{Body: []byte(text)})
	got, err := r.client.Index(t.Context(), node, "doc.docSchema")
	must(t, err)
	want := domain.Entry{Hash: "aaaa", Type: domain.DocumentType, DocumentID: "doc-1", Subfiles: 1, Size: 210}
	if got.Schema != domain.SchemaV3 || len(got.Entries) != 1 || got.Entries[0] != want {
		t.Fatalf("%+v", got)
	}
	wantPaths(t, r.trip.Calls(), "/sync/v3/files/"+node)
}

func TestRefusesIndexThatMatchesNeitherHash(t *testing.T) {
	t.Parallel()
	text := []byte("3\naaaa:0:doc-1:0:10\n")
	expected := strings.Repeat("f", 64)
	r := newRig(t, nil)
	r.trip.Enqueue(porttest.Reply{Body: text})
	_, err := r.client.Index(t.Context(), expected, domain.RootIndexName)
	wantEqual(t, err, domain.HashMismatch(expected, domain.BlobHash(text)))
	wantPaths(t, r.trip.Calls(), "/sync/v3/files/"+expected)
}

func TestReadsRootFromV4(t *testing.T) {
	t.Parallel()
	r := newRig(t, nil)
	r.trip.Enqueue(
		porttest.Reply{Body: rootBody("aa", 1, "")},
		porttest.Reply{Body: rootBody("aa", 1, "")},
	)
	root, err := r.client.Root(t.Context())
	must(t, err)
	if root.Hash != "aa" || root.Generation != 1 {
		t.Fatalf("%+v", root)
	}
	version, ok := r.client.cachedVersion()
	if !ok || version != domain.RootV4 {
		t.Fatalf("cached %q %v", version, ok)
	}
	root, err = r.client.Root(t.Context())
	must(t, err)
	if root.Generation != 1 {
		t.Fatal(root.Generation)
	}
	calls := r.trip.Calls()
	wantPaths(t, calls, "/sync/v4/root", "/sync/v4/root")
	wantFilename(t, calls, domain.RootHashName)
}

func TestUnsettlesTheRootPathOnA404(t *testing.T) {
	t.Parallel()
	r := newRig(t, nil)
	r.trip.Enqueue(
		porttest.Reply{Body: rootBody("aa", 1, "")},
		porttest.Reply{Status: 404},
		porttest.Reply{Body: rootBody("bb", 2, "")},
	)
	root, err := r.client.Root(t.Context())
	must(t, err)
	if root.Generation != 1 || root.Hash != "aa" {
		t.Fatalf("%+v", root)
	}
	version, ok := r.client.cachedVersion()
	if !ok || version != domain.RootV4 {
		t.Fatalf("cached %q %v", version, ok)
	}
	root, err = r.client.Root(t.Context())
	must(t, err)
	if root.Generation != 2 || root.Hash != "bb" {
		t.Fatalf("%+v", root)
	}
	version, ok = r.client.cachedVersion()
	if !ok || version != domain.RootV3 {
		t.Fatalf("cached %q %v", version, ok)
	}
	calls := r.trip.Calls()
	wantPaths(t, calls, "/sync/v4/root", "/sync/v4/root", "/sync/v3/root")
	wantFilename(t, calls, domain.RootHashName)
}

func TestFallsBackToV3Root(t *testing.T) {
	t.Parallel()
	r := newRig(t, nil)
	r.trip.Enqueue(
		porttest.Reply{Status: 404},
		porttest.Reply{Body: rootBody("aa", 7, "")},
	)
	root, err := r.client.Root(t.Context())
	must(t, err)
	if root.Generation != 7 {
		t.Fatal(root.Generation)
	}
	calls := r.trip.Calls()
	if calls[len(calls)-1].URL.Path != "/sync/v3/root" {
		t.Fatal(calls[len(calls)-1].URL.Path)
	}
	wantFilename(t, calls, domain.RootHashName)
	if r.hosts.reads != 2 {
		t.Fatal(r.hosts.reads)
	}
}

func TestBothRootsMissingIsNoRootYet(t *testing.T) {
	t.Parallel()
	r := newRig(t, nil)
	r.trip.Enqueue(porttest.Reply{Status: 404}, porttest.Reply{Status: 404})
	_, err := r.client.Root(t.Context())
	wantEqual(t, err, domain.ErrNoRootYet)
	wantPaths(t, r.trip.Calls(), "/sync/v4/root", "/sync/v3/root")
	if _, ok := r.client.cachedVersion(); ok {
		t.Fatal("cache set")
	}
}

func TestCachedMissDoesNotRestartTheOrder(t *testing.T) {
	t.Parallel()
	r := newRig(t, nil)
	r.client.storeVersion(domain.RootV4)
	r.trip.Enqueue(porttest.Reply{Status: 404}, porttest.Reply{Status: 404})
	_, err := r.client.Root(t.Context())
	wantEqual(t, err, domain.ErrNoRootYet)
	wantPaths(t, r.trip.Calls(), "/sync/v4/root", "/sync/v3/root")
	if _, ok := r.client.cachedVersion(); ok {
		t.Fatal("cache set")
	}
}

func TestRootDoesNotFallThroughOnServerError(t *testing.T) {
	t.Parallel()
	r := newRig(t, nil, WithPolicy(retry.Policy{}))
	r.trip.Enqueue(porttest.Reply{Status: 500})
	_, err := r.client.Root(t.Context())
	wantEqual(t, err, domain.HTTP(500, "/sync/v4/root"))
	if len(r.trip.Calls()) != 1 {
		t.Fatal(len(r.trip.Calls()))
	}
	if _, ok := r.client.cachedVersion(); ok {
		t.Fatal("cache set")
	}
}

func TestSettledRootServerErrorKeepsTheCache(t *testing.T) {
	t.Parallel()
	r := newRig(t, nil, WithPolicy(retry.Policy{}))
	r.trip.Enqueue(
		porttest.Reply{Body: rootBody("aa", 1, "")},
		porttest.Reply{Status: 500},
	)
	_, err := r.client.Root(t.Context())
	must(t, err)
	_, err = r.client.Root(t.Context())
	wantEqual(t, err, domain.HTTP(500, "/sync/v4/root"))
	wantPaths(t, r.trip.Calls(), "/sync/v4/root", "/sync/v4/root")
	version, ok := r.client.cachedVersion()
	if !ok || version != domain.RootV4 {
		t.Fatalf("cached %q %v", version, ok)
	}
}

func TestMalformedRootDoesNotFallThrough(t *testing.T) {
	t.Parallel()
	r := newRig(t, nil)
	r.trip.Enqueue(porttest.Reply{Body: []byte("{}")})
	_, err := r.client.Root(t.Context())
	wantEqual(t, err, domain.MalformedResponse("root response"))
	if len(r.trip.Calls()) != 1 {
		t.Fatal(len(r.trip.Calls()))
	}
	if _, ok := r.client.cachedVersion(); ok {
		t.Fatal("cache set")
	}
	if r.client.lastAdvisory != nil {
		t.Fatal("advisory stored")
	}
}

func TestClearVersionKeepsADifferentVersion(t *testing.T) {
	t.Parallel()
	c := New(&fixedTokens{}, &porttest.Transport{}, fs.Files{}, porttest.NewClock())
	c.storeVersion(domain.RootV4)
	c.clearVersion(domain.RootV3)
	version, ok := c.cachedVersion()
	if !ok || version != domain.RootV4 {
		t.Fatalf("cached %q %v", version, ok)
	}
	c.clearVersion(domain.RootV4)
	if _, ok := c.cachedVersion(); ok {
		t.Fatal("cache set")
	}
}

func TestRejectsBlobWithWrongHash(t *testing.T) {
	t.Parallel()
	body := []byte("some bytes")
	r := newRig(t, nil)
	r.trip.Enqueue(porttest.Reply{Body: body})
	_, err := r.client.Blob(t.Context(), "deadbeef", "doc.metadata", true)
	wantEqual(t, err, domain.HashMismatch("deadbeef", domain.BlobHash(body)))
}

func TestBlobWithoutHashIsRefusedBeforeTheWire(t *testing.T) {
	t.Parallel()
	tokens := &fixedTokens{token: "user-1"}
	r := newRig(t, tokens)
	_, err := r.client.Blob(t.Context(), "", "doc.metadata", true)
	wantEqual(t, err, domain.MissingHash("doc.metadata"))
	if len(r.trip.Calls()) != 0 || tokens.buys != 0 || r.hosts.reads != 0 {
		t.Fatalf("calls %d buys %d reads %d", len(r.trip.Calls()), tokens.buys, r.hosts.reads)
	}
}

func TestNonHexHashIsRefusedBeforeTheWire(t *testing.T) {
	t.Parallel()
	r := newRig(t, nil)
	for _, hash := range []string{"..", "../token/json/2/user/new", "ab/cd", "zz"} {
		if _, err := r.client.Blob(t.Context(), hash, "doc.pdf", true); !domain.IsKind(err, domain.KindMissingHash) {
			t.Fatalf("%q: %v", hash, err)
		}
		if _, err := r.client.BlobFile(t.Context(), hash, "doc.pdf", 0, nil); !domain.IsKind(err, domain.KindMissingHash) {
			t.Fatalf("%q: %v", hash, err)
		}
	}
	if len(r.trip.Calls()) != 0 {
		t.Fatalf("requests = %d", len(r.trip.Calls()))
	}
}

func TestBlobFileWithoutHashIsRefusedBeforeTheWire(t *testing.T) {
	t.Parallel()
	tokens := &fixedTokens{token: "user-1"}
	r := newRig(t, tokens)
	_, err := r.client.BlobFile(t.Context(), "", "doc.metadata", 0, nil)
	wantEqual(t, err, domain.MissingHash("doc.metadata"))
	if len(r.trip.Calls()) != 0 || tokens.buys != 0 || r.hosts.reads != 0 {
		t.Fatalf("calls %d buys %d reads %d", len(r.trip.Calls()), tokens.buys, r.hosts.reads)
	}
}

func TestDownloadsBlobToFile(t *testing.T) {
	t.Parallel()
	payload := []byte("the contents of a large original")
	hash := domain.BlobHash(payload)
	var done, total int64
	r := newRig(t, nil)
	r.trip.Enqueue(porttest.Reply{Body: payload})
	path, err := r.client.BlobFile(t.Context(), hash, "doc.pdf", int64(len(payload)), func(d, tot int64) {
		done, total = d, tot
	})
	must(t, err)
	t.Cleanup(func() { _ = os.Remove(path) })
	if done != int64(len(payload)) || total != int64(len(payload)) {
		t.Fatalf("progress %d %d", done, total)
	}
	got, err := os.ReadFile(path)
	must(t, err)
	if !bytes.Equal(got, payload) {
		t.Fatalf("%q", got)
	}
	wantFilename(t, r.trip.Calls(), "doc.pdf")
}

func TestRejectsDownloadedFileWithWrongHash(t *testing.T) {
	t.Parallel()
	body := []byte("nope")
	r := newRig(t, nil)
	files := r.watchFiles()
	r.trip.Enqueue(porttest.Reply{Body: body})
	_, err := r.client.BlobFile(t.Context(), "deadbeef", "doc.pdf", 0, nil)
	wantEqual(t, err, domain.HashMismatch("deadbeef", domain.BlobHash(body)))
	if files.file == "" {
		t.Fatal("no file")
	}
	if _, statErr := os.Stat(files.file); !os.IsNotExist(statErr) {
		t.Fatalf("file still there: %v", statErr)
	}
}

func TestDownloadNotFoundDeletesTheFile(t *testing.T) {
	t.Parallel()
	r := newRig(t, nil)
	files := r.watchFiles()
	r.trip.Enqueue(porttest.Reply{Status: 404, Body: []byte("missing")})
	_, err := r.client.BlobFile(t.Context(), "ab", "doc.pdf", 0, nil)
	wantEqual(t, err, domain.HTTP(404, "/sync/v3/files/ab"))
	if files.file == "" {
		t.Fatal("no file")
	}
	if _, statErr := os.Stat(files.file); !os.IsNotExist(statErr) {
		t.Fatalf("file still there: %v", statErr)
	}
}

func TestDownloadNotOnThisPlanKeepsTheDetail(t *testing.T) {
	t.Parallel()
	body := []byte("need connect")
	r := newRig(t, nil)
	files := r.watchFiles()
	r.trip.Enqueue(porttest.Reply{Status: 402, Body: body})
	_, err := r.client.BlobFile(t.Context(), "ab", "doc.pdf", 0, nil)
	wantEqual(t, err, domain.NotOnThisPlan(domain.DetailOf(body)))
	if files.file == "" {
		t.Fatal("no file")
	}
	if _, statErr := os.Stat(files.file); !os.IsNotExist(statErr) {
		t.Fatalf("file still there: %v", statErr)
	}
}

func TestDownloadLimits(t *testing.T) {
	t.Parallel()
	payload := []byte("payload")
	hash := domain.BlobHash(payload)
	t.Run("declared", func(t *testing.T) {
		t.Parallel()
		r := newRig(t, nil)
		r.trip.Enqueue(porttest.Reply{Body: payload})
		path, err := r.client.BlobFile(t.Context(), hash, "doc.pdf", int64(len(payload)), nil)
		must(t, err)
		t.Cleanup(func() { _ = os.Remove(path) })
		call := r.trip.Calls()[0]
		if call.Limit != int64(len(payload)) || call.Expected != int64(len(payload)) {
			t.Fatalf("limit %d expected %d", call.Limit, call.Expected)
		}
	})
	t.Run("past declared", func(t *testing.T) {
		t.Parallel()
		r := newRig(t, nil)
		files := r.watchFiles()
		r.trip.Enqueue(porttest.Reply{Body: bytes.Repeat([]byte{0x07}, 4096)})
		_, err := r.client.BlobFile(t.Context(), "ab", "doc.pdf", 10, nil)
		wantEqual(t, err, domain.OversizedResponse(4096, 10))
		if files.file != "" {
			t.Fatal(files.file)
		}
		call := r.trip.Calls()[0]
		if call.Limit != 10 || call.Expected != 10 {
			t.Fatalf("limit %d expected %d", call.Limit, call.Expected)
		}
	})
	t.Run("undeclared", func(t *testing.T) {
		t.Parallel()
		r := newRig(t, nil)
		r.trip.Enqueue(porttest.Reply{Body: payload})
		path, err := r.client.BlobFile(t.Context(), hash, "doc.pdf", 0, nil)
		must(t, err)
		t.Cleanup(func() { _ = os.Remove(path) })
		call := r.trip.Calls()[0]
		if call.Limit != domain.BlobSizeLimit || call.Expected != -1 {
			t.Fatalf("limit %d expected %d", call.Limit, call.Expected)
		}
	})
	t.Run("negative", func(t *testing.T) {
		t.Parallel()
		r := newRig(t, nil)
		r.trip.Enqueue(porttest.Reply{Body: payload})
		path, err := r.client.BlobFile(t.Context(), hash, "doc.pdf", -5, nil)
		must(t, err)
		t.Cleanup(func() { _ = os.Remove(path) })
		call := r.trip.Calls()[0]
		if call.Limit != domain.BlobSizeLimit || call.Expected != -1 {
			t.Fatalf("limit %d expected %d", call.Limit, call.Expected)
		}
	})
}

func TestClientRetriesServerError(t *testing.T) {
	t.Parallel()
	payload := []byte("payload")
	hash := domain.BlobHash(payload)
	r := newRig(t, nil)
	r.trip.Enqueue(porttest.Reply{Status: 503}, porttest.Reply{Body: payload})
	got, err := r.client.Blob(t.Context(), hash, "doc.pdf", true)
	must(t, err)
	if !bytes.Equal(got, payload) {
		t.Fatalf("%q", got)
	}
	wantDelays(t, r.sleeper.Delays(), []time.Duration{fastPolicy.BaseDelay})
	if len(r.trip.Calls()) != 2 {
		t.Fatal(len(r.trip.Calls()))
	}
}

func TestClientDoesNotRetryBadRequest(t *testing.T) {
	t.Parallel()
	r := newRig(t, nil)
	r.trip.Enqueue(porttest.Reply{Status: 400})
	_, err := r.client.Blob(t.Context(), "ab", "doc.pdf", false)
	wantEqual(t, err, domain.HTTP(400, "/sync/v3/files/ab"))
	wantDelays(t, r.sleeper.Delays(), nil)
	if len(r.trip.Calls()) != 1 {
		t.Fatal(len(r.trip.Calls()))
	}
}

func TestZeroPolicyMakesTheFirstFailureFinal(t *testing.T) {
	t.Parallel()
	r := newRig(t, nil, WithPolicy(retry.Policy{}))
	r.trip.Enqueue(porttest.Reply{Status: 503})
	_, err := r.client.Blob(t.Context(), "ab", "doc.pdf", false)
	wantEqual(t, err, domain.HTTP(503, "/sync/v3/files/ab"))
	wantDelays(t, r.sleeper.Delays(), nil)
}

func TestGetReturnsARawTransportError(t *testing.T) {
	t.Parallel()
	boom := errors.New("connection reset")
	r := newRig(t, nil)
	r.trip.Enqueue(porttest.Reply{Err: boom})
	_, err := r.client.Blob(t.Context(), "ab", "doc.pdf", false)
	if _, ok := domain.KindOf(err); ok {
		t.Fatal(err)
	}
	if !errors.Is(err, boom) {
		t.Fatal(err)
	}
	if len(r.trip.Calls()) != 1 || r.hosts.failed != 0 {
		t.Fatalf("calls %d failed %d", len(r.trip.Calls()), r.hosts.failed)
	}
}

func TestRetriesOnceAfterUnauthorized(t *testing.T) {
	t.Parallel()
	trip := &porttest.Transport{}
	sleeper := porttest.NewClockAt(time.Now())
	secrets := porttest.NewSecrets(map[domain.SecretName]string{
		domain.SecretDeviceToken: "device",
	})
	tokens := auth.New(secrets, trip, sleeper,
		auth.WithPolicy(fastPolicy),
		auth.WithUserAgent("test-agent"),
	)
	client := New(tokens, trip, fs.Files{}, sleeper,
		WithPolicy(fastPolicy),
		WithUserAgent("test-agent"),
	)
	payload := []byte("payload")
	hash := domain.BlobHash(payload)
	trip.Enqueue(
		porttest.Reply{Body: []byte("user-1")},
		porttest.Reply{Status: 401},
		porttest.Reply{Body: []byte("user-2")},
		porttest.Reply{Body: payload},
	)
	got, err := client.Blob(t.Context(), hash, "doc.pdf", true)
	must(t, err)
	if !bytes.Equal(got, payload) {
		t.Fatalf("%q", got)
	}
	calls := trip.Calls()
	if len(calls) != 4 {
		t.Fatalf("calls %d", len(calls))
	}
	if calls[0].Method != http.MethodPost || calls[0].URL.Path != "/token/json/2/user/new" {
		t.Fatalf("%s %s", calls[0].Method, calls[0].URL.Path)
	}
	if calls[1].Method != http.MethodGet {
		t.Fatal(calls[1].Method)
	}
	wantBearer(t, calls[1], "user-1")
	if calls[3].Method != http.MethodGet {
		t.Fatal(calls[3].Method)
	}
	wantBearer(t, calls[3], "user-2")
	filename, ok := headerOne(calls[3].Header, domain.HeaderFilename)
	if !ok || filename != "doc.pdf" {
		t.Fatalf("filename %q", filename)
	}
	wantDelays(t, sleeper.Delays(), nil)
}

func TestSyncHostFailedAfterTheHostCannotBeFound(t *testing.T) {
	t.Parallel()
	r := newRig(t, nil, WithPolicy(retry.Policy{MaxAttempts: 1}))
	target := domain.Transport(domain.ReasonCannotFindHost)
	r.trip.Enqueue(porttest.Reply{Err: target})
	_, err := r.client.Blob(t.Context(), "ab", "doc.pdf", false)
	wantEqual(t, err, target)
	if r.hosts.failed != 1 {
		t.Fatal(r.hosts.failed)
	}
}

func TestOfflineDoesNotDemoteTheHost(t *testing.T) {
	t.Parallel()
	r := newRig(t, nil, WithPolicy(retry.Policy{MaxAttempts: 1}))
	target := domain.Transport(domain.ReasonNotConnectedToInternet)
	r.trip.Enqueue(porttest.Reply{Err: target})
	_, err := r.client.Blob(t.Context(), "ab", "doc.pdf", false)
	wantEqual(t, err, target)
	if r.hosts.failed != 0 {
		t.Fatal(r.hosts.failed)
	}
}

func TestRootIndexReadsTheNamedIndex(t *testing.T) {
	t.Parallel()
	text := []byte("3\naaaa:0:doc-1:0:10\n")
	hash := domain.BlobHash(text)
	r := newRig(t, nil)
	r.trip.Enqueue(
		porttest.Reply{Body: rootBody(hash, 3, "")},
		porttest.Reply{Body: text},
	)
	root, index, err := r.client.RootIndex(t.Context())
	must(t, err)
	if root.Hash != hash || root.Generation != 3 || len(index.Entries) != 1 {
		t.Fatalf("%+v %+v", root, index)
	}
	calls := r.trip.Calls()
	wantPaths(t, calls, "/sync/v4/root", "/sync/v3/files/"+hash)
	rootName, ok := headerOne(calls[0].Header, domain.HeaderFilename)
	if !ok || rootName != domain.RootHashName {
		t.Fatalf("root filename %q", rootName)
	}
	indexName, ok := headerOne(calls[1].Header, domain.HeaderFilename)
	if !ok || indexName != domain.RootIndexName {
		t.Fatalf("index filename %q", indexName)
	}
}

func TestDefaultSendsNoUserAgent(t *testing.T) {
	t.Parallel()
	trip := &porttest.Transport{}
	trip.Enqueue(porttest.Reply{Body: rootBody("aa", 1, "")})
	client := New(&fixedTokens{token: "user-1"}, trip, fs.Files{}, porttest.NewClock())
	_, err := client.Root(t.Context())
	must(t, err)
	if values, present := trip.Calls()[0].Header[domain.HeaderUserAgent]; present {
		t.Fatalf("user agent %v", values)
	}
}
