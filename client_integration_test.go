//go:build integration

// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

// Integration tests that drive the public client against an emulated cloud on
// a loopback TLS server, through the real HTTP transport, the real socket
// dialer, the real filesystem and the system clock.

package rmcloud_test

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/altessa-s/rmcloud-go"
)

func TestIntegration_SessionEndToEnd(t *testing.T) {
	t.Parallel()

	cloud := newITCloud(t)
	store := newITStore()
	c := cloud.client(t, store)
	ctx := t.Context()

	registered, err := c.IsRegistered(ctx)
	if err != nil || registered {
		t.Fatalf("registered %v err %v", registered, err)
	}
	if err := c.RegisterDevice(ctx, "zzzzzzzz"); err == nil {
		t.Fatal("a code the cloud refuses registered the device")
	}
	if err := c.RegisterDevice(ctx, "abcdefgh"); err != nil {
		t.Fatal(err)
	}
	if store.get(rmcloud.SecretDeviceToken) != itDeviceToken {
		t.Fatal("the device token was not stored")
	}
	plan, err := c.Plan(ctx)
	if err != nil || plan == nil || !plan.IsConnect() {
		t.Fatalf("plan %#v err %v", plan, err)
	}
	if cloud.userNew.Load() != 1 || store.get(rmcloud.SecretUserToken) == "" {
		t.Fatalf("user/new %d", cloud.userNew.Load())
	}

	notes, on := itNotes()
	c.StartNotifications(ctx, on)
	nextNote(t, notes, rmcloud.NotificationConnected)
	cloud.waitSocket(t)

	// A document: one page blob and its index.
	page := []byte("page bytes")
	pageHash := rmcloud.BlobHash(page)
	docIndex := []byte(rmcloud.SerializeDocument([]rmcloud.Entry{
		{Hash: pageHash, Type: rmcloud.FileType, DocumentID: "doc-1.rm", Size: int64(len(page))},
	}, "doc-1"))
	docHash := rmcloud.BlobHash(docIndex)

	missing, err := c.MissingFiles(ctx, []string{pageHash, docHash})
	if err != nil || len(missing) != 2 {
		t.Fatalf("missing %v err %v", missing, err)
	}
	if got, err := c.Upload(ctx, page, "doc-1.rm"); err != nil || got != pageHash {
		t.Fatalf("upload page %s %v", got, err)
	}
	pagePath := t.TempDir() + "/doc-1.docSchema"
	if err := os.WriteFile(pagePath, docIndex, 0o600); err != nil {
		t.Fatal(err)
	}
	var sent atomic.Int64
	if got, err := c.UploadFile(ctx, pagePath, "doc-1.docSchema", func(done, _ int64) { sent.Store(done) }); err != nil || got != docHash {
		t.Fatalf("upload index %s %v", got, err)
	}
	if sent.Load() != int64(len(docIndex)) {
		t.Fatalf("upload progress %d of %d", sent.Load(), len(docIndex))
	}
	missing, err = c.MissingFiles(ctx, []string{pageHash, docHash})
	if err != nil || len(missing) != 0 {
		t.Fatalf("missing after upload %v err %v", missing, err)
	}

	rootIndex := []byte(rmcloud.SerializeRoot([]rmcloud.Entry{
		{Hash: docHash, Type: rmcloud.DocumentType, DocumentID: "doc-1", Subfiles: 1, Size: int64(len(page))},
	}))
	rootHash, err := c.Upload(ctx, rootIndex, rmcloud.RootIndexName)
	if err != nil {
		t.Fatal(err)
	}

	before, err := c.Root(ctx)
	if err != nil || before.Generation != 1 {
		t.Fatalf("root %+v err %v", before, err)
	}
	gen, err := c.PutRoot(ctx, rootHash, before.Generation, rmcloud.Broadcast(true))
	if err != nil || gen != 2 {
		t.Fatalf("gen %d err %v", gen, err)
	}
	event := nextNote(t, notes, rmcloud.NotificationEvent)
	if !event.SyncComplete || !event.FromThisDevice || event.DocumentID != "doc-1" || event.VisibleName != "Notes" {
		t.Fatalf("%#v", event)
	}

	// A writer that read generation 1 lost the race.
	_, err = c.PutRoot(ctx, before.Hash, before.Generation)
	if conflict, ok := errors.AsType[*rmcloud.RootConflictError](err); !ok || conflict.Status != http.StatusPreconditionFailed {
		t.Fatalf("stale write: %v", err)
	}
	if hash, gen := cloud.root(); hash != rootHash || gen != 2 {
		t.Fatalf("the stale write moved the root to %s@%d", hash, gen)
	}
	if cloud.count("PUT /sync/v3/root") != 2 {
		t.Fatalf("a conflict was retried: %d writes", cloud.count("PUT /sync/v3/root"))
	}

	root, index, err := c.RootIndex(ctx)
	if err != nil || root.Hash != rootHash || root.Generation != 2 {
		t.Fatalf("root %+v err %v", root, err)
	}
	if len(index.Entries) != 1 || index.Entries[0].Hash != docHash || !index.Entries[0].IsDocument() {
		t.Fatalf("%#v", index)
	}
	doc, err := c.Index(ctx, docHash, index.Entries[0].IndexName())
	if err != nil || len(doc.Entries) != 1 || doc.Entries[0].Hash != pageHash {
		t.Fatalf("%#v %v", doc, err)
	}
	body, err := c.Blob(ctx, pageHash, "doc-1.rm")
	if err != nil || !bytes.Equal(body, page) {
		t.Fatalf("%q %v", body, err)
	}
	var done, total int64
	path, err := c.BlobFile(ctx, pageHash, "doc-1.rm", rmcloud.Expecting(int64(len(page))), func(d, tot int64) { done, total = d, tot })
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Remove(path) })
	onDisk, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(onDisk, page) || done != int64(len(page)) || total != int64(len(page)) {
		t.Fatalf("%q %d/%d %v", onDisk, done, total, err)
	}
	if fileHash, err := rmcloud.FileHash(ctx, path); err != nil || fileHash != pageHash {
		t.Fatalf("FileHash %s %v", fileHash, err)
	}

	// A blob the cloud never stored, and bytes that do not match the address.
	if _, err := c.Blob(ctx, rmcloud.BlobHash([]byte("absent")), "x"); !rmcloud.IsKind(err, rmcloud.KindHTTP) || !rmcloud.IsDocumentUnavailable(err) {
		t.Fatalf("absent blob: %v", err)
	}
	cloud.setFault(func(w http.ResponseWriter, r *http.Request) bool {
		if r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, pageHash) {
			_, _ = w.Write([]byte("tampered"))
			return true
		}
		return false
	})
	if _, err := c.Blob(ctx, pageHash, "doc-1.rm"); !rmcloud.IsKind(err, rmcloud.KindHashMismatch) {
		t.Fatalf("tampered blob: %v", err)
	}
	cloud.setFault(nil)

	c.StopNotifications()
	if err := c.RevokeDevice(ctx); err != nil {
		t.Fatal(err)
	}
	if err := c.ForgetDevice(ctx); err != nil {
		t.Fatal(err)
	}
	if registered, err := c.IsRegistered(ctx); err != nil || registered {
		t.Fatalf("after forget: registered %v err %v", registered, err)
	}
}

func TestIntegration_UnauthorizedRefreshesTheUserToken(t *testing.T) {
	t.Parallel()

	cloud := newITCloud(t)
	store := cloud.registered(t)
	stale := itUserToken(t, "revoked")
	store.m[rmcloud.SecretUserToken] = stale
	c := cloud.client(t, store)

	root, err := c.Root(t.Context())
	if err != nil || root.Generation != 1 {
		t.Fatalf("root %+v err %v", root, err)
	}
	if cloud.count("GET /sync/v4/root") != 2 || cloud.userNew.Load() != 1 {
		t.Fatalf("root reads %d, user/new %d", cloud.count("GET /sync/v4/root"), cloud.userNew.Load())
	}
	if got := store.get(rmcloud.SecretUserToken); got == stale || got == "" {
		t.Fatal("the refreshed user token was not stored")
	}
}

func TestIntegration_UnauthorizedTwiceIsFinal(t *testing.T) {
	t.Parallel()

	cloud := newITCloud(t)
	store := cloud.registered(t)
	cloud.setFault(func(w http.ResponseWriter, r *http.Request) bool {
		if r.URL.Path == "/sync/v4/root" {
			http.Error(w, "no", http.StatusUnauthorized)
			return true
		}
		return false
	})
	c := cloud.client(t, store)

	_, err := c.Root(t.Context())
	if !errors.Is(err, rmcloud.ErrUnauthorized) {
		t.Fatalf("%v", err)
	}
	// One refresh, and the refresh is not itself repeated by the retry loop.
	if cloud.count("GET /sync/v4/root") != 2 || cloud.userNew.Load() != 1 {
		t.Fatalf("root reads %d, user/new %d", cloud.count("GET /sync/v4/root"), cloud.userNew.Load())
	}
}

func TestIntegration_RevokedDeviceCannotBuyAUserToken(t *testing.T) {
	t.Parallel()

	cloud := newITCloud(t)
	store := newITStore()
	store.m[rmcloud.SecretDeviceToken] = "revoked-device"
	c := cloud.client(t, store)

	if _, err := c.Root(t.Context()); err == nil {
		t.Fatal("a revoked device read the root")
	}
	if cloud.count("GET /sync/v4/root") != 0 {
		t.Fatal("a sync call went out without a user token")
	}
}

func TestIntegration_RetryAfterIsHonored(t *testing.T) {
	t.Parallel()

	const wait = 500 * time.Millisecond
	cloud := newITCloud(t)
	var calls atomic.Int32
	cloud.setFault(func(w http.ResponseWriter, r *http.Request) bool {
		if r.URL.Path == "/sync/v4/root" && calls.Add(1) == 1 {
			w.Header().Set("Retry-After", strconv.FormatFloat(wait.Seconds(), 'f', -1, 64))
			w.WriteHeader(http.StatusTooManyRequests)
			return true
		}
		return false
	})
	c := cloud.client(t, cloud.registered(t))

	if _, err := c.Root(t.Context()); err != nil {
		t.Fatal(err)
	}
	at := cloud.times("GET /sync/v4/root")
	if len(at) != 2 {
		t.Fatalf("%d attempts", len(at))
	}
	if gap := at[1].Sub(at[0]); gap < wait || gap > wait+itSlack {
		t.Fatalf("the repeat came %s after the 429, want %s", gap, wait)
	}
}

func TestIntegration_RateLimitExhaustsAttempts(t *testing.T) {
	t.Parallel()

	const wait = 200 * time.Millisecond
	cloud := newITCloud(t)
	cloud.setFault(func(w http.ResponseWriter, r *http.Request) bool {
		if r.URL.Path == "/sync/v4/root" {
			w.Header().Set("Retry-After", "0.2")
			w.WriteHeader(http.StatusTooManyRequests)
			return true
		}
		return false
	})
	c := cloud.client(t, cloud.registered(t))

	_, err := c.Root(t.Context())
	limited, ok := errors.AsType[*rmcloud.RateLimitedError](err)
	if !ok || !limited.HasRetryAfter || limited.RetryAfter != wait {
		t.Fatalf("%v", err)
	}
	at := cloud.times("GET /sync/v4/root")
	if len(at) != 3 {
		t.Fatalf("%d attempts, want the policy's 3", len(at))
	}
	for i := 1; i < len(at); i++ {
		if gap := at[i].Sub(at[i-1]); gap < wait {
			t.Fatalf("attempt %d came %s after the previous one, want at least %s", i+1, gap, wait)
		}
	}
}

func TestIntegration_RootWriteRepeatsOnlyRateLimits(t *testing.T) {
	t.Parallel()

	cloud := newITCloud(t)
	c := cloud.client(t, cloud.registered(t))
	hash, gen := cloud.root()

	var writes atomic.Int32
	cloud.setFault(func(w http.ResponseWriter, r *http.Request) bool {
		if r.Method != http.MethodPut || r.URL.Path != "/sync/v3/root" {
			return false
		}
		if writes.Add(1) == 1 {
			w.Header().Set("Retry-After", "0.1")
			w.WriteHeader(http.StatusTooManyRequests)
			return true
		}
		return false
	})
	got, err := c.PutRoot(t.Context(), hash, gen)
	if err != nil || got != gen+1 || writes.Load() != 2 {
		t.Fatalf("gen %d err %v writes %d", got, err, writes.Load())
	}

	writes.Store(0)
	cloud.setFault(func(w http.ResponseWriter, r *http.Request) bool {
		if r.Method == http.MethodPut && r.URL.Path == "/sync/v3/root" {
			writes.Add(1)
			http.Error(w, "backend down", http.StatusServiceUnavailable)
			return true
		}
		return false
	})
	_, err = c.PutRoot(t.Context(), hash, got)
	rejected, ok := errors.AsType[*rmcloud.WriteRejectedError](err)
	if !ok || rejected.Status != http.StatusServiceUnavailable {
		t.Fatalf("%v", err)
	}
	if writes.Load() != 1 {
		t.Fatalf("a root write was repeated after a 503: %d writes", writes.Load())
	}
}

func TestIntegration_ServerErrorsAreRetriedWithBackoff(t *testing.T) {
	t.Parallel()

	cloud := newITCloud(t)
	c := cloud.client(t, cloud.registered(t))
	blob := []byte("blob")
	cloud.mu.Lock()
	hash := cloud.put(blob)
	cloud.mu.Unlock()

	var calls atomic.Int32
	cloud.setFault(func(w http.ResponseWriter, r *http.Request) bool {
		if strings.HasPrefix(r.URL.Path, "/sync/v3/files/") && calls.Add(1) <= 2 {
			http.Error(w, "busy", http.StatusBadGateway)
			return true
		}
		return false
	})
	got, err := c.Blob(t.Context(), hash, "blob")
	if err != nil || !bytes.Equal(got, blob) {
		t.Fatalf("%q %v", got, err)
	}
	at := cloud.times("GET /sync/v3/files/*")
	if len(at) != 3 {
		t.Fatalf("%d attempts", len(at))
	}
	// The policy waits 300ms, then three times that, jitter only on top.
	if gap := at[1].Sub(at[0]); gap < 300*time.Millisecond {
		t.Fatalf("first repeat after %s", gap)
	}
	if gap := at[2].Sub(at[1]); gap < 900*time.Millisecond {
		t.Fatalf("second repeat after %s", gap)
	}
}

func TestIntegration_ServerErrorsExhaustRetries(t *testing.T) {
	t.Parallel()

	cloud := newITCloud(t)
	c := cloud.client(t, cloud.registered(t))
	cloud.setFault(func(w http.ResponseWriter, r *http.Request) bool {
		if strings.HasPrefix(r.URL.Path, "/sync/v3/files/") {
			http.Error(w, "down", http.StatusServiceUnavailable)
			return true
		}
		return false
	})

	start := time.Now()
	_, err := c.Blob(t.Context(), rmcloud.BlobHash([]byte("x")), "x")
	status, ok := errors.AsType[*rmcloud.HTTPError](err)
	if !ok || status.Status != http.StatusServiceUnavailable {
		t.Fatalf("%v", err)
	}
	if n := cloud.count("GET /sync/v3/files/*"); n != 3 {
		t.Fatalf("%d attempts, want 3", n)
	}
	if elapsed := time.Since(start); elapsed < 1200*time.Millisecond || elapsed > 1200*time.Millisecond+itSlack {
		t.Fatalf("gave up after %s", elapsed)
	}

	// A 4xx is final on the first answer.
	cloud.setFault(func(w http.ResponseWriter, r *http.Request) bool {
		if strings.HasPrefix(r.URL.Path, "/sync/v3/files/") {
			http.Error(w, "bad", http.StatusBadRequest)
			return true
		}
		return false
	})
	before := cloud.count("GET /sync/v3/files/*")
	if _, err := c.Blob(t.Context(), rmcloud.BlobHash([]byte("x")), "x"); !rmcloud.IsKind(err, rmcloud.KindHTTP) {
		t.Fatalf("%v", err)
	}
	if n := cloud.count("GET /sync/v3/files/*") - before; n != 1 {
		t.Fatalf("a 400 was repeated: %d attempts", n)
	}
}

// cutBody promises more bytes than it sends, then drops the connection.
func cutBody(w http.ResponseWriter) {
	w.Header().Set("Content-Length", "64")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("only ten b"))
	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}
	panic(http.ErrAbortHandler)
}

func TestIntegration_ConnectionCutMidBodyIsRetried(t *testing.T) {
	t.Parallel()

	cloud := newITCloud(t)
	c := cloud.client(t, cloud.registered(t))
	blob := []byte("the whole blob")
	cloud.mu.Lock()
	hash := cloud.put(blob)
	cloud.mu.Unlock()

	var calls atomic.Int32
	cloud.setFault(func(w http.ResponseWriter, r *http.Request) bool {
		if strings.HasPrefix(r.URL.Path, "/sync/v3/files/") && calls.Add(1) == 1 {
			cutBody(w)
		}
		return false
	})
	got, err := c.Blob(t.Context(), hash, "blob")
	if err != nil || !bytes.Equal(got, blob) {
		t.Fatalf("%q %v (attempts %d)", got, err, calls.Load())
	}
	if calls.Load() != 2 {
		t.Fatalf("%d attempts", calls.Load())
	}
}

func TestIntegration_ConnectionCutEveryTimeFails(t *testing.T) {
	t.Parallel()

	cloud := newITCloud(t)
	c := cloud.client(t, cloud.registered(t))
	cloud.setFault(func(w http.ResponseWriter, r *http.Request) bool {
		if strings.HasPrefix(r.URL.Path, "/sync/v3/files/") {
			cutBody(w)
		}
		return false
	})
	path, err := c.BlobFile(t.Context(), rmcloud.BlobHash([]byte("x")), "x", rmcloud.Expecting(0), nil)
	if err == nil {
		_ = os.Remove(path)
		t.Fatal("a truncated body was accepted")
	}
	if path != "" {
		t.Fatalf("a failed download left %s", path)
	}
}

func TestIntegration_OversizedDownloadHitsTheCeiling(t *testing.T) {
	t.Parallel()

	cloud := newITCloud(t)
	c := cloud.client(t, cloud.registered(t))
	blob := bytes.Repeat([]byte("x"), 4096)
	cloud.mu.Lock()
	hash := cloud.put(blob)
	cloud.mu.Unlock()

	path, err := c.BlobFile(t.Context(), hash, "blob", rmcloud.Expecting(int64(len(blob))), nil)
	if err != nil {
		t.Fatal(err)
	}
	_ = os.Remove(path)

	before := cloud.count("GET /sync/v3/files/*")
	path, err = c.BlobFile(t.Context(), hash, "blob", rmcloud.Expecting(100), nil)
	oversized, ok := errors.AsType[*rmcloud.OversizedResponseError](err)
	if !ok || path != "" || oversized.Limit != 100 {
		t.Fatalf("path %q err %v", path, err)
	}
	if n := cloud.count("GET /sync/v3/files/*") - before; n != 1 {
		t.Fatalf("an oversized body was fetched again: %d attempts", n)
	}
}

func TestIntegration_OversizedMemoryResponse(t *testing.T) {
	t.Parallel()

	const memoryLimit = 32 << 20
	cloud := newITCloud(t)
	c := cloud.client(t, cloud.registered(t))
	chunk := bytes.Repeat([]byte("y"), 1<<20)
	cloud.setFault(func(w http.ResponseWriter, r *http.Request) bool {
		if r.URL.Path != "/sync/v4/root" {
			return false
		}
		for range memoryLimit/len(chunk) + 1 {
			if _, err := w.Write(chunk); err != nil {
				return true
			}
		}
		return true
	})
	_, err := c.Root(t.Context())
	if !rmcloud.IsKind(err, rmcloud.KindOversizedResponse) {
		t.Fatalf("%v", err)
	}
}

func TestIntegration_CancelMidRequest(t *testing.T) {
	t.Parallel()

	cloud := newITCloud(t)
	c := cloud.client(t, cloud.registered(t))
	entered := make(chan struct{}, 1)
	cloud.setFault(func(_ http.ResponseWriter, r *http.Request) bool {
		if r.URL.Path != "/sync/v4/root" {
			return false
		}
		entered <- struct{}{}
		<-r.Context().Done()
		return true
	})

	ctx, cancel := context.WithCancel(t.Context())
	errs := make(chan error, 1)
	go func() {
		_, err := c.Root(ctx)
		errs <- err
	}()
	select {
	case <-entered:
	case <-time.After(itSlack):
		t.Fatal("the request never reached the server")
	}
	cancel()
	select {
	case err := <-errs:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("%v", err)
		}
	case <-time.After(itSlack):
		t.Fatal("a canceled call kept waiting")
	}
	if n := cloud.count("GET /sync/v4/root"); n != 1 {
		t.Fatalf("a canceled call was repeated: %d", n)
	}
}

func TestIntegration_DeadlineMidRequest(t *testing.T) {
	t.Parallel()

	const deadline = 200 * time.Millisecond
	cloud := newITCloud(t)
	c := cloud.client(t, cloud.registered(t))
	cloud.setFault(func(_ http.ResponseWriter, r *http.Request) bool {
		if r.URL.Path != "/sync/v4/root" {
			return false
		}
		<-r.Context().Done()
		return true
	})

	start := time.Now()
	ctx, cancel := context.WithTimeout(t.Context(), deadline)
	defer cancel()
	_, err := c.Root(ctx)
	elapsed := time.Since(start)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("%v", err)
	}
	if elapsed < deadline || elapsed > deadline+itSlack {
		t.Fatalf("returned after %s", elapsed)
	}
}

func TestIntegration_RedirectKeepsTheTokenOnItsOrigin(t *testing.T) {
	t.Parallel()

	cloud := newITCloud(t)
	c := cloud.client(t, cloud.registered(t))
	blob := []byte("moved blob")
	cloud.mu.Lock()
	hash := cloud.put(blob)
	cloud.mu.Unlock()

	// Same origin: the token goes along.
	var sawToken atomic.Bool
	cloud.setFault(func(w http.ResponseWriter, r *http.Request) bool {
		switch r.URL.Path {
		case "/sync/v3/files/" + hash:
			http.Redirect(w, r, "/moved/"+hash, http.StatusTemporaryRedirect)
			return true
		case "/moved/" + hash:
			sawToken.Store(strings.HasPrefix(r.Header.Get("Authorization"), "Bearer "))
			_, _ = w.Write(blob)
			return true
		}
		return false
	})
	got, err := c.Blob(t.Context(), hash, "blob")
	if err != nil || !bytes.Equal(got, blob) || !sawToken.Load() {
		t.Fatalf("%q %v token %v", got, err, sawToken.Load())
	}

	// Another origin: the token stays behind, and that host's refusal is
	// the answer.
	var leaked atomic.Bool
	var hits atomic.Int32
	other := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		if r.Header.Get("Authorization") != "" {
			leaked.Store(true)
		}
		http.Error(w, "who are you", http.StatusUnauthorized)
	}))
	t.Cleanup(other.Close)
	cloud.setFault(func(w http.ResponseWriter, r *http.Request) bool {
		if r.URL.Path == "/sync/v3/files/"+hash {
			http.Redirect(w, r, other.URL+"/elsewhere/"+hash, http.StatusFound)
			return true
		}
		return false
	})
	_, err = c.Blob(t.Context(), hash, "blob")
	if !errors.Is(err, rmcloud.ErrUnauthorized) {
		t.Fatalf("%v", err)
	}
	if leaked.Load() {
		t.Fatal("the user token followed a redirect to another origin")
	}
	if hits.Load() == 0 {
		t.Fatal("the redirect was not followed")
	}
}

func TestIntegration_NotificationsReconnectAfterServerClose(t *testing.T) {
	t.Parallel()

	// The first reconnect waits two seconds; there is no jitter to subtract.
	const reconnect = 2 * time.Second
	cloud := newITCloud(t)
	c := cloud.client(t, cloud.registered(t))
	notes, on := itNotes()
	c.StartNotifications(t.Context(), on)
	nextNote(t, notes, rmcloud.NotificationConnected)
	cloud.waitSocket(t)

	if n := cloud.dropSockets(); n != 1 {
		t.Fatalf("%d sockets open", n)
	}
	dropped := nextNote(t, notes, rmcloud.NotificationDropped)
	droppedAt := time.Now()
	if dropped.Err == nil {
		t.Fatal("a drop without a reason")
	}
	nextNote(t, notes, rmcloud.NotificationConnected)
	cloud.waitSocket(t)
	if gap := time.Since(droppedAt); gap < reconnect-50*time.Millisecond || gap > reconnect+itSlack {
		t.Fatalf("reconnected %s after the drop, want %s", gap, reconnect)
	}

	// The new socket carries events.
	hash, gen := cloud.root()
	if _, err := c.PutRoot(t.Context(), hash, gen, rmcloud.Broadcast(true)); err != nil {
		t.Fatal(err)
	}
	if ev := nextNote(t, notes, rmcloud.NotificationEvent); !ev.SyncComplete {
		t.Fatalf("%#v", ev)
	}

	// Without broadcast, nothing is pushed.
	hash, gen = cloud.root()
	if _, err := c.PutRoot(t.Context(), hash, gen, rmcloud.Broadcast(false)); err != nil {
		t.Fatal(err)
	}
	select {
	case n := <-notes:
		t.Fatalf("unexpected %#v", n)
	case <-time.After(200 * time.Millisecond):
	}
	c.StopNotifications()
}

func TestIntegration_NotificationUpgradeRefusedRefreshesToken(t *testing.T) {
	t.Parallel()

	cloud := newITCloud(t)
	store := cloud.registered(t)
	store.m[rmcloud.SecretUserToken] = itUserToken(t, "revoked")
	c := cloud.client(t, store)
	notes, on := itNotes()
	c.StartNotifications(t.Context(), on)

	dropped := nextNote(t, notes, rmcloud.NotificationDropped)
	if !errors.Is(dropped.Err, rmcloud.ErrUnauthorized) {
		t.Fatalf("%v", dropped.Err)
	}
	nextNote(t, notes, rmcloud.NotificationConnected)
	if cloud.userNew.Load() != 1 {
		t.Fatalf("user/new %d", cloud.userNew.Load())
	}
	c.StopNotifications()
}

func TestIntegration_ClosedHostIsATransportError(t *testing.T) {
	t.Parallel()

	cloud := newITCloud(t)
	store := cloud.registered(t)
	c := cloud.client(t, store)
	if _, err := c.Root(t.Context()); err != nil {
		t.Fatal(err)
	}
	cloud.srv.Close()

	// Every attempt is refused; the per-host breaker may open before the
	// last one, which then fails fast.
	_, err := c.Root(t.Context())
	transport, ok := errors.AsType[*rmcloud.TransportError](err)
	if !ok || (transport.Reason != rmcloud.ReasonCannotConnectToHost && transport.Reason != rmcloud.ReasonCircuitOpen) {
		t.Fatalf("%v", err)
	}
	if cloud.count("GET /sync/v4/root") != 1 {
		t.Fatal("a closed server answered")
	}
}
