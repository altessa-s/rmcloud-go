// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package rmcloud_test

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"maps"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"reflect"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/altessa-s/rmcloud-go"
)

const (
	testUA = "desktop/3.28.0 (macos 1)"

	// indexText is schema 3 with no trailing newline. DecodeIndex accepts
	// it only when the root hash is the blob hash of these bytes.
	indexText = "3\naaaa:80000000:doc-1:2:120\nbbbb:80000000:doc-2:1:40"

	syncCompleteFrame = `{"message":{"attributes":{"event":"SyncComplete","sourceDeviceID":"device-1","id":"doc-9","vissibleName":"Notes"}},"subscription":"s"}`
)

var deviceIDPattern = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

func TestNewRequiresSecrets(t *testing.T) {
	_, err := rmcloud.New(nil, rmcloud.WithUserAgent(testUA))
	if err == nil || err.Error() != "rmcloud: secret store is required" {
		t.Fatalf("%v", err)
	}
}

func TestClientSession(t *testing.T) {
	indexBody := []byte(indexText)
	rootHash := rmcloud.BlobHash(indexBody)
	blobHash := rmcloud.BlobHash([]byte("abc"))
	userToken := freshToken(t)

	svc := &service{
		indexBody: indexBody,
		rootHash:  rootHash,
		blobHash:  blobHash,
		userToken: userToken,
		accepted:  make(chan struct{}, 4),
	}
	srv := httptest.NewServer(svc)
	t.Cleanup(srv.Close)

	store := newMemStore()
	ctx := t.Context()
	c, err := rmcloud.New(store,
		rmcloud.WithResolver(nil),
		rmcloud.WithEndpoints(loopback(t, srv.URL)),
		rmcloud.WithUserAgent(testUA),
		rmcloud.WithDeviceDesc(""),
		rmcloud.WithDeviceDesc(rmcloud.DeviceDescWindows),
		rmcloud.WithDeviceDesc(rmcloud.DeviceDescMac),
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(c.StopNotifications)
	account := c

	registered, err := account.IsRegistered(ctx)
	if err != nil || registered {
		t.Fatalf("registered %v err %v", registered, err)
	}
	err = account.RegisterDevice(ctx, "short")
	if kind, ok := rmcloud.KindOf(err); !ok || kind != rmcloud.KindInvalidOneTimeCode || !errors.Is(err, rmcloud.ErrInvalidOneTimeCode) {
		t.Fatalf("%v", err)
	}
	if svc.deviceNew.Load() != 0 {
		t.Fatalf("device/new %d", svc.deviceNew.Load())
	}

	if err := account.RegisterDevice(ctx, "AbCdEfGh"); err != nil {
		t.Fatalf("%v paths %v", err, svc.paths())
	}
	if svc.deviceCode != "abcdefgh" || svc.deviceDesc != rmcloud.DeviceDescMac || !deviceIDPattern.MatchString(svc.deviceID) {
		t.Fatalf("code %q desc %q id %q", svc.deviceCode, svc.deviceDesc, svc.deviceID)
	}
	registered, err = account.IsRegistered(ctx)
	if err != nil || !registered {
		t.Fatalf("registered %v err %v", registered, err)
	}
	if got, ok := store.get(rmcloud.SecretDeviceToken); !ok || got != "device-token" {
		t.Fatalf("device token %q %v", got, ok)
	}

	plan, err := account.Plan(ctx)
	if err != nil {
		t.Fatalf("%v paths %v", err, svc.paths())
	}
	if plan == nil || !plan.IsConnect() || plan.SyncScope == nil || *plan.SyncScope != "sync:full" {
		t.Fatalf("%#v", plan)
	}
	if svc.userAuth != "Bearer device-token" || svc.userNew.Load() != 1 {
		t.Fatalf("auth %q buys %d", svc.userAuth, svc.userNew.Load())
	}
	if got, ok := store.get(rmcloud.SecretUserToken); !ok || got != userToken {
		t.Fatal("user token was not stored")
	}

	root, err := c.Root(ctx)
	if err != nil {
		t.Fatalf("%v paths %v", err, svc.paths())
	}
	if root.Hash != rootHash || root.Generation != 7 {
		t.Fatalf("%+v", root)
	}
	gotRoot, gotIndex, err := c.RootIndex(ctx)
	if err != nil {
		t.Fatalf("%v paths %v", err, svc.paths())
	}
	if gotRoot.Hash != rootHash || gotRoot.Generation != 7 {
		t.Fatalf("%+v", gotRoot)
	}
	assertDocuments(t, gotIndex)
	if svc.v4.Load() != 2 || svc.v3.Load() != 0 || svc.userNew.Load() != 1 {
		t.Fatalf("v4 %d v3 %d user %d paths %v", svc.v4.Load(), svc.v3.Load(), svc.userNew.Load(), svc.paths())
	}
	for _, ua := range svc.rootUAs {
		if ua != testUA {
			t.Fatalf("user-agent %q", ua)
		}
	}
	if len(svc.rootUAs) != 2 {
		t.Fatalf("root calls %d", len(svc.rootUAs))
	}

	idx, err := c.Index(ctx, rootHash, rmcloud.RootIndexName)
	if err != nil {
		t.Fatalf("%v paths %v", err, svc.paths())
	}
	assertDocuments(t, idx)

	blob, err := c.Blob(ctx, blobHash, "page")
	if err != nil || string(blob) != "abc" {
		t.Fatalf("%q %v", blob, err)
	}

	uploaded, err := c.Upload(ctx, []byte("doc"), "doc-1.docSchema")
	if err != nil {
		t.Fatalf("%v paths %v", err, svc.paths())
	}
	if uploaded != rmcloud.BlobHash([]byte("doc")) {
		t.Fatal(uploaded)
	}
	if svc.uploadType != rmcloud.ContentTypeIndex || string(svc.uploadBody) != "doc" || svc.uploadGoog != rmcloud.GoogHash([]byte("doc")) {
		t.Fatalf("type %q body %q goog %q", svc.uploadType, svc.uploadBody, svc.uploadGoog)
	}

	tmpUpload := t.TempDir() + "/file.pdf"
	if err := os.WriteFile(tmpUpload, []byte("pdf-bytes"), 0o600); err != nil {
		t.Fatal(err)
	}
	uploadedFileHash, err := c.UploadFile(ctx, tmpUpload, "file.pdf", nil)
	if err != nil {
		t.Fatalf("%v paths %v", err, svc.paths())
	}
	if uploadedFileHash != rmcloud.BlobHash([]byte("pdf-bytes")) {
		t.Fatal(uploadedFileHash)
	}
	if svc.uploadType != rmcloud.ContentTypeBlob || string(svc.uploadBody) != "pdf-bytes" {
		t.Fatalf("type %q body %q", svc.uploadType, svc.uploadBody)
	}

	gen, err := c.PutRoot(ctx, rootHash, 4, rmcloud.Broadcast(false), rmcloud.Broadcast(true))
	if err != nil || gen != 11 {
		t.Fatalf("gen %d err %v body %q", gen, err, svc.lastRoot())
	}
	if len(svc.rootWrites) != 1 || svc.rootWrites[0].body != `{"broadcast":true,"generation":4,"hash":"`+rootHash+`"}` {
		t.Fatalf("%q", svc.lastRoot())
	}
	if svc.rootWrites[0].contentType != rmcloud.ContentTypeJSON || svc.rootWrites[0].filename != rmcloud.RootHashName {
		t.Fatalf("%+v", svc.rootWrites[0])
	}

	// Progress runs on this goroutine, inside the download.
	var done, total int64
	path, err := c.BlobFile(ctx, blobHash, "page", rmcloud.Expecting(3), func(d, tot int64) {
		done, total = d, tot
	})
	if err != nil {
		t.Fatalf("%v paths %v", err, svc.paths())
	}
	t.Cleanup(func() { _ = os.Remove(path) })
	page, err := os.ReadFile(path)
	if err != nil || string(page) != "abc" || done != 3 || total != 3 {
		t.Fatalf("page %q progress %d/%d err %v", page, done, total, err)
	}

	_, err = c.PutRoot(ctx, "conflict", 9)
	conflict, ok := errors.AsType[*rmcloud.RootConflictError](err)
	if !ok || conflict.Status != 412 || !rmcloud.IsKind(err, rmcloud.KindRootConflict) {
		t.Fatalf("%v", err)
	}
	if svc.lastRoot() != `{"broadcast":false,"generation":9,"hash":"conflict"}` {
		t.Fatalf("%q", svc.lastRoot())
	}
	if p := svc.paths(); len(p) > 0 {
		t.Fatalf("unexpected %v", p)
	}

	c.StartNotifications(ctx, nil)
	waitAccepted(t, svc.accepted)
	c.StopNotifications()

	notes := make(chan rmcloud.Notification, 8)
	c.StartNotifications(ctx, func(n rmcloud.Notification) {
		notes <- n
	})
	connected := waitNote(t, notes)
	if connected.Kind != rmcloud.NotificationConnected || connected.SyncComplete || connected.Err != nil {
		t.Fatalf("%#v", connected)
	}
	event := waitNote(t, notes)
	if event.Kind != rmcloud.NotificationEvent || !event.SyncComplete || !event.FromThisDevice || event.Name != "SyncComplete" || event.DocumentID != "doc-9" || event.VisibleName != "Notes" || event.Err != nil {
		t.Fatalf("%#v", event)
	}
	// The raw frame comes along whole: the wrapper, not just the attributes.
	if !bytes.Contains(event.Frame, []byte(`"attributes"`)) || !bytes.Contains(event.Frame, []byte(`doc-9`)) {
		t.Fatalf("frame %q", event.Frame)
	}
	c.StopNotifications()
	c.StopNotifications()
	select {
	case extra := <-notes:
		t.Fatalf("extra %#v", extra)
	default:
	}

	if err := account.RevokeDevice(ctx); err != nil {
		t.Fatal(err)
	}
	if svc.deletes.Load() != 1 {
		t.Fatalf("deletes %d", svc.deletes.Load())
	}
	if got, ok := store.get(rmcloud.SecretDeviceToken); !ok || got != "device-token" {
		t.Fatalf("revoke dropped %q %v", got, ok)
	}
	if _, ok := store.get(rmcloud.SecretDeviceID); !ok {
		t.Fatal("revoke dropped the device id")
	}
	if _, ok := store.get(rmcloud.SecretUserToken); !ok {
		t.Fatal("revoke dropped the user token")
	}
	if err := account.ForgetDevice(ctx); err != nil {
		t.Fatal(err)
	}
	for _, name := range []rmcloud.SecretName{rmcloud.SecretDeviceToken, rmcloud.SecretDeviceID, rmcloud.SecretUserToken} {
		if _, ok := store.get(name); ok {
			t.Fatalf("%s still stored", name)
		}
	}
	if _, ok := reflect.TypeOf(c).MethodByName("UserToken"); ok {
		t.Fatal("UserToken is exported")
	}
}

func TestEmptyUserAgentOmitsHeader(t *testing.T) {
	var hits atomic.Int32
	var sawUA atomic.Bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		if _, ok := r.Header["User-Agent"]; ok {
			sawUA.Store(true)
		}
		if r.URL.Path != "/sync/v4/root" {
			http.NotFound(w, r)
			return
		}
		_, _ = io.WriteString(w, `{"hash":"abc","generation":1}`)
	}))
	t.Cleanup(srv.Close)

	store := newMemStore()
	ctx := t.Context()
	if err := store.Save(ctx, rmcloud.SecretUserToken, freshToken(t)); err != nil {
		t.Fatal(err)
	}
	c, err := rmcloud.New(store, rmcloud.WithEndpoints(loopback(t, srv.URL)),
		rmcloud.WithUserAgent(""),
	)
	if err != nil {
		t.Fatal(err)
	}
	root, err := c.Root(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if root.Hash != "abc" || root.Generation != 1 {
		t.Fatalf("%+v", root)
	}
	if hits.Load() != 1 || sawUA.Load() {
		t.Fatalf("hits %d saw %v", hits.Load(), sawUA.Load())
	}
}

func TestResolverSplitsAuthAndSync(t *testing.T) {
	userToken := freshToken(t)
	var authSrv struct {
		device atomic.Int32
		user   atomic.Int32
		other  atomic.Int32
		code   atomic.Value
	}
	auth := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/token/json/2/device/new":
			authSrv.device.Add(1)
			var body struct {
				Code string `json:"code"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			authSrv.code.Store(body.Code)
			_, _ = io.WriteString(w, "device-token")
		case "/token/json/2/user/new":
			authSrv.user.Add(1)
			_, _ = io.WriteString(w, userToken)
		default:
			authSrv.other.Add(1)
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(auth.Close)

	var syncSrv struct {
		root  atomic.Int32
		other atomic.Int32
	}
	syncServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/sync/v4/root" {
			syncSrv.root.Add(1)
			_, _ = io.WriteString(w, `{"hash":"abc","generation":1}`)
			return
		}
		syncSrv.other.Add(1)
		http.NotFound(w, r)
	}))
	t.Cleanup(syncServer.Close)

	syncURL := mustURL(t, syncServer.URL)
	ctx := t.Context()
	c, err := rmcloud.New(newMemStore(),
		rmcloud.WithResolver(splitHosts{sync: syncURL}),
		rmcloud.WithEndpoints(loopback(t, auth.URL)),
		rmcloud.WithUserAgent(testUA),
	)
	if err != nil {
		t.Fatal(err)
	}
	account := c
	if err := account.RegisterDevice(ctx, "abcdefgh"); err != nil {
		t.Fatal(err)
	}
	root, err := c.Root(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if root.Hash != "abc" || root.Generation != 1 {
		t.Fatalf("%+v", root)
	}
	if authSrv.device.Load() != 1 || authSrv.user.Load() != 1 || authSrv.other.Load() != 0 {
		t.Fatalf("auth device %d user %d other %d", authSrv.device.Load(), authSrv.user.Load(), authSrv.other.Load())
	}
	if syncSrv.root.Load() != 1 || syncSrv.other.Load() != 0 {
		t.Fatalf("sync root %d other %d", syncSrv.root.Load(), syncSrv.other.Load())
	}
	if authSrv.code.Load() != "abcdefgh" {
		t.Fatalf("code %v", authSrv.code.Load())
	}
}

func TestStopWithoutStartDoesNotDial(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		hits.Add(1)
	}))
	t.Cleanup(srv.Close)

	c, err := rmcloud.New(newMemStore(), rmcloud.WithEndpoints(loopback(t, srv.URL)),
		rmcloud.WithUserAgent(testUA),
	)
	if err != nil {
		t.Fatal(err)
	}
	c.StopNotifications()
	c.StopNotifications()
	if hits.Load() != 0 {
		t.Fatalf("hits %d", hits.Load())
	}
}

func assertDocuments(t *testing.T, idx rmcloud.Index) {
	t.Helper()
	if idx.Schema != rmcloud.SchemaV3 || len(idx.Entries) != 2 {
		t.Fatalf("%#v", idx)
	}
	first := idx.Entries[0]
	if !first.IsDocument() || first.Hash != "aaaa" || first.DocumentID != "doc-1" || first.IndexName() != "doc-1.docSchema" {
		t.Fatalf("%#v", first)
	}
	second := idx.Entries[1]
	if !second.IsDocument() || second.Hash != "bbbb" || second.DocumentID != "doc-2" || second.IndexName() != "doc-2.docSchema" {
		t.Fatalf("%#v", second)
	}
}

type splitHosts struct {
	sync url.URL
}

func (s splitHosts) Endpoints(context.Context) rmcloud.Endpoints {
	return rmcloud.Endpoints{AuthHost: s.sync, SyncHost: s.sync}
}

func (s splitHosts) SyncHostFailed() {}

func (s splitHosts) Notifications(context.Context) url.URL { return s.sync }

type rootPut struct {
	body        string
	contentType string
	filename    string
}

type service struct {
	mu sync.Mutex

	indexBody []byte
	rootHash  string
	blobHash  string
	userToken string

	deviceCode string
	deviceDesc string
	deviceID   string
	userAuth   string
	rootUAs    []string
	uploadType string
	uploadBody []byte
	uploadGoog string
	rootWrites []rootPut
	badPaths   []string

	deviceNew atomic.Int32
	userNew   atomic.Int32
	v4        atomic.Int32
	v3        atomic.Int32
	deletes   atomic.Int32
	upgrades  atomic.Int32

	accepted chan struct{}
}

func (s *service) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch {
	case r.Method == http.MethodPost && r.URL.Path == "/token/json/2/device/new":
		s.deviceNew.Add(1)
		var body struct {
			Code       string `json:"code"`
			DeviceDesc string `json:"deviceDesc"`
			DeviceID   string `json:"deviceID"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			s.note(err.Error())
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		s.mu.Lock()
		s.deviceCode = body.Code
		s.deviceDesc = body.DeviceDesc
		s.deviceID = body.DeviceID
		s.mu.Unlock()
		_, _ = io.WriteString(w, "device-token")
	case r.Method == http.MethodPost && r.URL.Path == "/token/json/2/user/new":
		s.userNew.Add(1)
		s.mu.Lock()
		s.userAuth = r.Header.Get("Authorization")
		token := s.userToken
		s.mu.Unlock()
		_, _ = io.WriteString(w, token)
	case r.Method == http.MethodPost && r.URL.Path == "/token/json/3/device/delete":
		s.deletes.Add(1)
		w.WriteHeader(http.StatusOK)
	case r.Method == http.MethodGet && r.URL.Path == "/sync/v4/root":
		s.v4.Add(1)
		s.mu.Lock()
		s.rootUAs = append(s.rootUAs, r.Header.Get("User-Agent"))
		hash := s.rootHash
		s.mu.Unlock()
		_, _ = io.WriteString(w, `{"hash":"`+hash+`","generation":7}`)
	case r.Method == http.MethodGet && r.URL.Path == "/sync/v3/root":
		s.v3.Add(1)
		http.NotFound(w, r)
	case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/sync/v3/files/"):
		hash := strings.TrimPrefix(r.URL.Path, "/sync/v3/files/")
		switch hash {
		case s.rootHash:
			_, _ = w.Write(s.indexBody)
		case s.blobHash:
			_, _ = io.WriteString(w, "abc")
		default:
			s.note(r.Method + " " + r.URL.Path)
			http.NotFound(w, r)
		}
	case r.Method == http.MethodPut && strings.HasPrefix(r.URL.Path, "/sync/v3/files/"):
		body, _ := io.ReadAll(r.Body)
		s.mu.Lock()
		s.uploadType = r.Header.Get("Content-Type")
		s.uploadBody = body
		s.uploadGoog = r.Header.Get("x-goog-hash")
		s.mu.Unlock()
		w.WriteHeader(http.StatusOK)
	case r.Method == http.MethodPut && r.URL.Path == "/sync/v3/root":
		body, _ := io.ReadAll(r.Body)
		put := rootPut{
			body:        string(body),
			contentType: r.Header.Get("Content-Type"),
			filename:    r.Header.Get("rm-filename"),
		}
		s.mu.Lock()
		s.rootWrites = append(s.rootWrites, put)
		s.mu.Unlock()
		if strings.Contains(put.body, `"hash":"conflict"`) {
			http.Error(w, "conflict", http.StatusPreconditionFailed)
			return
		}
		_, _ = io.WriteString(w, `{"hash":"`+s.rootHash+`","generation":11}`)
	case r.URL.Path == "/notifications/ws/json/1":
		n := s.upgrades.Add(1)
		conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
		if err != nil {
			s.note("accept " + err.Error())
			return
		}
		go func(ctx context.Context) {
			for {
				if _, _, err := conn.Read(ctx); err != nil {
					return
				}
			}
		}(context.WithoutCancel(r.Context()))
		select {
		case s.accepted <- struct{}{}:
		default:
		}
		if n == 2 {
			_ = conn.Write(r.Context(), websocket.MessageText, []byte(syncCompleteFrame))
		}
	default:
		s.note(r.Method + " " + r.URL.Path)
		http.NotFound(w, r)
	}
}

func (s *service) note(path string) {
	s.mu.Lock()
	s.badPaths = append(s.badPaths, path)
	s.mu.Unlock()
}

func (s *service) paths() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.badPaths...)
}

func (s *service) lastRoot() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.rootWrites) == 0 {
		return ""
	}
	return s.rootWrites[len(s.rootWrites)-1].body
}

type memStore struct {
	mu sync.Mutex
	m  map[rmcloud.SecretName]string
}

func newMemStore() *memStore {
	return &memStore{m: map[rmcloud.SecretName]string{}}
}

func (s *memStore) Secret(_ context.Context, which rmcloud.SecretName) (string, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok := s.m[which]
	return v, ok, nil
}

func freshToken(t *testing.T) string {
	t.Helper()
	return tokenAt(t, time.Now().Add(24*time.Hour).Unix(), map[string]any{
		"level":     "connect",
		"device-id": "device-1",
		"scopes":    "sync:full",
	})
}

func tokenAt(t *testing.T, exp int64, extra map[string]any) string {
	t.Helper()
	claims := map[string]any{"exp": exp}
	maps.Copy(claims, extra)
	raw, err := json.Marshal(claims)
	if err != nil {
		t.Fatal(err)
	}
	enc := base64.RawURLEncoding.EncodeToString
	return enc([]byte(`{"alg":"none"}`)) + "." + enc(raw) + "." + enc([]byte("sig"))
}

func loopback(t *testing.T, raw string) rmcloud.Endpoints {
	t.Helper()
	u := mustURL(t, raw)
	return rmcloud.Endpoints{AuthHost: u, SyncHost: u}
}

func mustURL(t *testing.T, raw string) url.URL {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	return *u
}

func waitNote(t *testing.T, notes <-chan rmcloud.Notification) rmcloud.Notification {
	t.Helper()
	select {
	case n := <-notes:
		return n
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for a notification")
	}
	return rmcloud.Notification{}
}

func waitAccepted(t *testing.T, accepted <-chan struct{}) {
	t.Helper()
	select {
	case <-accepted:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for the notification socket")
	}
}

func (s *memStore) Save(ctx context.Context, which rmcloud.SecretName, value string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.m[which] = value
	return nil
}

func (s *memStore) Forget(ctx context.Context, which rmcloud.SecretName) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.m, which)
	return nil
}

func (s *memStore) get(which rmcloud.SecretName) (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok := s.m[which]
	return v, ok
}
