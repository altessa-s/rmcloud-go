//go:build integration

// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package rmcloud_test

import (
	"context"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/altessa-s/rmcloud-go"
)

// itSlack bounds how late a real timer, a socket or a cancellation may be
// observed on a loaded machine. Lower bounds are exact: a timer never fires
// early.
const itSlack = 5 * time.Second

const (
	itUA          = "desktop/3.28.0 (macos 14.5)"
	itDeviceToken = "device-token"
	itDeviceID    = "device-1"
)

// itCloud is an in-memory reMarkable cloud on a loopback TLS server: tokens,
// a content-addressed blob store, a root with generations, check-files and
// the notification socket. Tests inject faults through fault.
type itCloud struct {
	srv *httptest.Server

	mu         sync.Mutex
	blobs      map[string][]byte
	rootHash   string
	generation int64
	userTokens map[string]bool // bearer user tokens the sync host accepts
	nextUser   string          // the token the next user/new sells
	sockets    map[*websocket.Conn]struct{}
	hits       map[string]int
	arrivals   map[string][]time.Time

	// fault, when set, sees every request first; returning true means it
	// answered and the cloud does nothing more.
	fault func(w http.ResponseWriter, r *http.Request) bool

	deviceNew atomic.Int32
	userNew   atomic.Int32

	accepted chan struct{}
}

// newITCloud starts the cloud. Its first root is an empty root index at
// generation 1, so a root read succeeds from the start.
func newITCloud(t *testing.T) *itCloud {
	t.Helper()
	c := &itCloud{
		blobs:      map[string][]byte{},
		userTokens: map[string]bool{},
		sockets:    map[*websocket.Conn]struct{}{},
		hits:       map[string]int{},
		arrivals:   map[string][]time.Time{},
		accepted:   make(chan struct{}, 16),
	}
	empty := []byte(rmcloud.SerializeRoot(nil))
	c.rootHash = c.put(empty)
	c.generation = 1
	c.nextUser = itUserToken(t, "user-1")
	c.srv = httptest.NewTLSServer(c)
	t.Cleanup(func() {
		c.mu.Lock()
		for conn := range c.sockets {
			_ = conn.CloseNow()
		}
		c.mu.Unlock()
		c.srv.Close()
	})
	return c
}

// client is a facade pinned to this cloud, trusting its certificate.
func (c *itCloud) client(t *testing.T, store rmcloud.SecretStore, opts ...rmcloud.Option) *rmcloud.Client {
	t.Helper()
	u, err := url.Parse(c.srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	roots := x509.NewCertPool()
	roots.AddCert(c.srv.Certificate())
	base := []rmcloud.Option{
		rmcloud.WithEndpoints(rmcloud.Endpoints{AuthHost: *u, SyncHost: *u}),
		rmcloud.WithRootCAs(roots),
		rmcloud.WithUserAgent(itUA),
		rmcloud.WithLogger(slog.New(slog.DiscardHandler)),
	}
	cl, err := rmcloud.New(store, append(base, opts...)...)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cl.Close() })
	return cl
}

// registered is a store holding a device token and a user token the cloud
// accepts, so a test can start at the sync calls.
func (c *itCloud) registered(t *testing.T) *itStore {
	t.Helper()
	token := itUserToken(t, "user-0")
	c.mu.Lock()
	c.userTokens[token] = true
	c.mu.Unlock()
	store := newITStore()
	store.m[rmcloud.SecretDeviceToken] = itDeviceToken
	store.m[rmcloud.SecretDeviceID] = itDeviceID
	store.m[rmcloud.SecretUserToken] = token
	return store
}

func (c *itCloud) setFault(f func(w http.ResponseWriter, r *http.Request) bool) {
	c.mu.Lock()
	c.fault = f
	c.mu.Unlock()
}

// put stores body and returns its address.
func (c *itCloud) put(body []byte) string {
	sum := sha256.Sum256(body)
	hash := hex.EncodeToString(sum[:])
	c.blobs[hash] = append([]byte(nil), body...)
	return hash
}

func (c *itCloud) count(key string) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.hits[key]
}

func (c *itCloud) times(key string) []time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]time.Time(nil), c.arrivals[key]...)
}

func (c *itCloud) root() (string, int64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.rootHash, c.generation
}

// key names a request for the counters: the method and the path, with a
// blob's hash folded into one bucket.
func key(r *http.Request) string {
	path := r.URL.Path
	if strings.HasPrefix(path, "/sync/v3/files/") {
		path = "/sync/v3/files/*"
	}
	return r.Method + " " + path
}

func (c *itCloud) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	k := key(r)
	c.mu.Lock()
	c.hits[k]++
	c.arrivals[k] = append(c.arrivals[k], time.Now())
	fault := c.fault
	c.mu.Unlock()
	if fault != nil && fault(w, r) {
		return
	}

	switch {
	case r.Method == http.MethodPost && r.URL.Path == "/token/json/2/device/new":
		c.deviceNew.Add(1)
		var body struct {
			Code string `json:"code"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Code != "abcdefgh" {
			http.Error(w, "bad code", http.StatusBadRequest)
			return
		}
		_, _ = io.WriteString(w, itDeviceToken)
	case r.Method == http.MethodPost && r.URL.Path == "/token/json/2/user/new":
		c.userNew.Add(1)
		if r.Header.Get("Authorization") != "Bearer "+itDeviceToken {
			http.Error(w, "unknown device", http.StatusUnauthorized)
			return
		}
		c.mu.Lock()
		token := c.nextUser
		c.userTokens[token] = true
		c.mu.Unlock()
		_, _ = io.WriteString(w, token)
	case r.Method == http.MethodPost && r.URL.Path == "/token/json/3/device/delete":
		w.WriteHeader(http.StatusOK)
	case r.URL.Path == "/notifications/ws/json/1":
		if !c.authorized(r) {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		c.acceptSocket(w, r)
	case strings.HasPrefix(r.URL.Path, "/sync/"):
		if !c.authorized(r) {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		c.serveSync(w, r)
	default:
		http.NotFound(w, r)
	}
}

func (c *itCloud) authorized(r *http.Request) bool {
	token, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	c.mu.Lock()
	defer c.mu.Unlock()
	return ok && c.userTokens[token]
}

func (c *itCloud) serveSync(w http.ResponseWriter, r *http.Request) {
	switch {
	case r.Method == http.MethodGet && r.URL.Path == "/sync/v4/root":
		hash, gen := c.root()
		_, _ = fmt.Fprintf(w, `{"hash":%q,"generation":%d}`, hash, gen)
	case r.Method == http.MethodPut && r.URL.Path == "/sync/v3/root":
		c.writeRoot(w, r)
	case r.Method == http.MethodPost && r.URL.Path == "/sync/v3/check-files":
		var body struct {
			Files []string `json:"files"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		missing := []string{}
		c.mu.Lock()
		for _, h := range body.Files {
			if _, ok := c.blobs[h]; !ok {
				missing = append(missing, h)
			}
		}
		c.mu.Unlock()
		_ = json.NewEncoder(w).Encode(map[string][]string{"missingFiles": missing})
	case strings.HasPrefix(r.URL.Path, "/sync/v3/files/"):
		c.serveBlob(w, r, strings.TrimPrefix(r.URL.Path, "/sync/v3/files/"))
	default:
		http.NotFound(w, r)
	}
}

func (c *itCloud) serveBlob(w http.ResponseWriter, r *http.Request, hash string) {
	switch r.Method {
	case http.MethodGet:
		c.mu.Lock()
		body, ok := c.blobs[hash]
		c.mu.Unlock()
		if !ok {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write(body)
	case http.MethodPut:
		body, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if r.Header.Get("x-goog-hash") != rmcloud.GoogHash(body) {
			http.Error(w, "crc32c mismatch", http.StatusBadRequest)
			return
		}
		c.mu.Lock()
		got := c.put(body)
		c.mu.Unlock()
		if got != hash {
			http.Error(w, "address mismatch", http.StatusBadRequest)
			return
		}
		w.WriteHeader(http.StatusOK)
	default:
		http.Error(w, "method", http.StatusMethodNotAllowed)
	}
}

func (c *itCloud) writeRoot(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Broadcast  bool   `json:"broadcast"`
		Generation int64  `json:"generation"`
		Hash       string `json:"hash"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	c.mu.Lock()
	if body.Generation != c.generation {
		c.mu.Unlock()
		http.Error(w, "generation moved", http.StatusPreconditionFailed)
		return
	}
	if _, ok := c.blobs[body.Hash]; !ok {
		c.mu.Unlock()
		http.Error(w, "root index is not stored", http.StatusBadRequest)
		return
	}
	c.rootHash = body.Hash
	c.generation++
	gen := c.generation
	conns := make([]*websocket.Conn, 0, len(c.sockets))
	for conn := range c.sockets {
		conns = append(conns, conn)
	}
	c.mu.Unlock()
	if body.Broadcast {
		for _, conn := range conns {
			_ = conn.Write(context.WithoutCancel(r.Context()), websocket.MessageText, []byte(itSyncComplete("doc-1", "Notes")))
		}
	}
	_, _ = fmt.Fprintf(w, `{"hash":%q,"generation":%d}`, body.Hash, gen)
}

// acceptSocket upgrades and keeps reading, so the server answers pings and
// notices the client closing. The reader ends when either side closes.
func (c *itCloud) acceptSocket(w http.ResponseWriter, r *http.Request) {
	conn, err := websocket.Accept(w, r, nil)
	if err != nil {
		return
	}
	c.mu.Lock()
	c.sockets[conn] = struct{}{}
	c.mu.Unlock()
	select {
	case c.accepted <- struct{}{}:
	default:
	}
	ctx := context.WithoutCancel(r.Context())
	for {
		if _, _, err := conn.Read(ctx); err != nil {
			break
		}
	}
	c.mu.Lock()
	delete(c.sockets, conn)
	c.mu.Unlock()
	_ = conn.CloseNow()
}

// waitSocket waits until the server has registered a socket. The client
// reports the upgrade as soon as it reads the 101, which can be before.
func (c *itCloud) waitSocket(t *testing.T) {
	t.Helper()
	select {
	case <-c.accepted:
	case <-time.After(itSlack):
		t.Fatal("the server never registered the notification socket")
	}
}

// dropSockets closes every notification socket from the server side.
func (c *itCloud) dropSockets() int {
	c.mu.Lock()
	conns := make([]*websocket.Conn, 0, len(c.sockets))
	for conn := range c.sockets {
		conns = append(conns, conn)
	}
	c.mu.Unlock()
	for _, conn := range conns {
		_ = conn.Close(websocket.StatusGoingAway, "maintenance")
	}
	return len(conns)
}

func itSyncComplete(id, name string) string {
	return `{"message":{"attributes":{"event":"SyncComplete","sourceDeviceID":"` + itDeviceID +
		`","id":"` + id + `","vissibleName":"` + name + `"}},"subscription":"s"}`
}

// itUserToken is an unsigned user token valid for a day. nonce keeps two
// tokens apart.
func itUserToken(t *testing.T, nonce string) string {
	t.Helper()
	claims, err := json.Marshal(map[string]any{
		"exp":       time.Now().Add(24 * time.Hour).Unix(),
		"level":     "connect",
		"device-id": itDeviceID,
		"scopes":    "sync:full",
		"nonce":     nonce,
	})
	if err != nil {
		t.Fatal(err)
	}
	enc := base64.RawURLEncoding.EncodeToString
	return enc([]byte(`{"alg":"none"}`)) + "." + enc(claims) + "." + enc([]byte("sig"))
}

// itStore is a secret store in memory.
type itStore struct {
	mu sync.Mutex
	m  map[rmcloud.SecretName]string
}

func newITStore() *itStore { return &itStore{m: map[rmcloud.SecretName]string{}} }

func (s *itStore) Secret(_ context.Context, which rmcloud.SecretName) (string, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok := s.m[which]
	return v, ok, nil
}

func (s *itStore) Save(_ context.Context, which rmcloud.SecretName, value string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.m[which] = value
	return nil
}

func (s *itStore) Forget(_ context.Context, which rmcloud.SecretName) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.m, which)
	return nil
}

func (s *itStore) get(which rmcloud.SecretName) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.m[which]
}

// itNotes collects notifications on a channel.
func itNotes() (chan rmcloud.Notification, func(rmcloud.Notification)) {
	ch := make(chan rmcloud.Notification, 64)
	return ch, func(n rmcloud.Notification) {
		select {
		case ch <- n:
		default:
		}
	}
}

// nextNote waits for the next notification of kind, skipping others.
func nextNote(t *testing.T, notes <-chan rmcloud.Notification, kind rmcloud.NotificationKind) rmcloud.Notification {
	t.Helper()
	deadline := time.After(itSlack)
	for {
		select {
		case n := <-notes:
			if n.Kind == kind {
				return n
			}
		case <-deadline:
			t.Fatalf("no %s notification within %s", kind, itSlack)
			return rmcloud.Notification{}
		}
	}
}
