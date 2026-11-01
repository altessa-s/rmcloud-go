// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package rmcloud_test

import (
	"crypto/x509"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"slices"
	"sync/atomic"
	"testing"

	"github.com/coder/websocket"

	"github.com/altessa-s/rmcloud-go"
)

// signedInClient is a client over endpoints whose store already holds a
// fresh user token, so sync calls need no token exchange.
func signedInClient(t *testing.T, endpoints rmcloud.Endpoints, opts ...rmcloud.Option) *rmcloud.Client {
	t.Helper()
	store := newMemStore()
	if err := store.Save(t.Context(), rmcloud.SecretUserToken, freshToken(t)); err != nil {
		t.Fatal(err)
	}
	c, err := rmcloud.New(store, append([]rmcloud.Option{rmcloud.WithEndpoints(endpoints), rmcloud.WithUserAgent(testUA)}, opts...)...)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close() })
	return c
}

func TestMissingFiles(t *testing.T) {
	present := rmcloud.BlobHash([]byte("present"))
	absent := rmcloud.BlobHash([]byte("absent"))
	var (
		hits  atomic.Int32
		asked atomic.Value
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		if r.Method != http.MethodPost || r.URL.Path != "/sync/v3/check-files" {
			http.NotFound(w, r)
			return
		}
		var body struct {
			Files []string `json:"files"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		asked.Store(body.Files)
		_, _ = io.WriteString(w, `{"missingFiles":["`+absent+`"]}`)
	}))
	t.Cleanup(srv.Close)
	c := signedInClient(t, loopback(t, srv.URL))
	ctx := t.Context()

	missing, err := c.MissingFiles(ctx, []string{present, absent})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(missing, []string{absent}) {
		t.Fatalf("missing %q", missing)
	}
	if got, _ := asked.Load().([]string); !slices.Equal(got, []string{present, absent}) {
		t.Fatalf("asked %q", got)
	}

	// Refused before any request.
	_, err = c.MissingFiles(ctx, nil)
	if _, ok := errors.AsType[*rmcloud.NothingToAskError](err); !ok {
		t.Fatalf("empty list: %v", err)
	}
	_, err = c.MissingFiles(ctx, []string{"not-hex"})
	if _, ok := errors.AsType[*rmcloud.MissingHashError](err); !ok {
		t.Fatalf("bad hash: %v", err)
	}
	if hits.Load() != 1 {
		t.Fatalf("hits %d", hits.Load())
	}
}

func TestMissingFilesRefusesAnAnswerItDidNotAskFor(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"missingFiles":["`+rmcloud.BlobHash([]byte("other"))+`"]}`)
	}))
	t.Cleanup(srv.Close)
	c := signedInClient(t, loopback(t, srv.URL))

	_, err := c.MissingFiles(t.Context(), []string{rmcloud.BlobHash([]byte("asked"))})
	if _, ok := errors.AsType[*rmcloud.MalformedResponseError](err); !ok {
		t.Fatalf("%v", err)
	}
}

func TestBlobFileWithAnUnknownLength(t *testing.T) {
	hash := rmcloud.BlobHash([]byte("abc"))
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/sync/v3/files/"+hash {
			http.NotFound(w, r)
			return
		}
		_, _ = io.WriteString(w, "abc")
	}))
	t.Cleanup(srv.Close)
	c := signedInClient(t, loopback(t, srv.URL))

	path, err := c.BlobFile(t.Context(), hash, "page", rmcloud.Expectation{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Remove(path) })
	got, err := os.ReadFile(path)
	if err != nil || string(got) != "abc" {
		t.Fatalf("%q %v", got, err)
	}

	// A body that is not the named blob is refused, not handed over.
	_, err = c.BlobFile(t.Context(), rmcloud.BlobHash([]byte("other")), "page", rmcloud.Expecting(0), nil)
	if err == nil {
		t.Fatal("a missing blob was returned")
	}
}

func TestRootCAsTrustsTheHostAuthority(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/sync/v4/root" {
			http.NotFound(w, r)
			return
		}
		_, _ = io.WriteString(w, `{"hash":"abc","generation":2}`)
	}))
	t.Cleanup(srv.Close)
	pool := x509.NewCertPool()
	pool.AddCert(srv.Certificate())

	trusted := signedInClient(t, loopback(t, srv.URL), rmcloud.WithRootCAs(nil), rmcloud.WithRootCAs(pool))
	root, err := trusted.Root(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if root.Hash != "abc" || root.Generation != 2 {
		t.Fatalf("%+v", root)
	}

	// The system pool does not know the test authority.
	untrusted := signedInClient(t, loopback(t, srv.URL), rmcloud.WithLogger(nil))
	if _, err := untrusted.Root(t.Context()); err == nil {
		t.Fatal("a server signed by an unknown authority was trusted")
	}
}

func TestNotificationsReportAnUnreadableFrameAndTheDrop(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/notifications/ws/json/1" {
			http.NotFound(w, r)
			return
		}
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		_ = conn.Write(r.Context(), websocket.MessageText, []byte("not json"))
		_ = conn.Close(websocket.StatusInternalError, "going away")
	}))
	t.Cleanup(srv.Close)
	c := signedInClient(t, loopback(t, srv.URL))

	notes := make(chan rmcloud.Notification, 16)
	c.StartNotifications(t.Context(), func(n rmcloud.Notification) {
		select {
		case notes <- n:
		default:
		}
	})
	t.Cleanup(c.StopNotifications)

	if n := waitNote(t, notes); n.Kind != rmcloud.NotificationConnected {
		t.Fatalf("%#v", n)
	}
	if n := waitNote(t, notes); n.Kind != rmcloud.NotificationUnreadable || n.Err != nil || len(n.Frame) == 0 {
		t.Fatalf("%#v", n)
	}
	if n := waitNote(t, notes); n.Kind != rmcloud.NotificationDropped || n.Err == nil {
		t.Fatalf("%#v", n)
	}
}
