// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package rmcloud

import (
	"context"
	"errors"
	"slices"
	"sync"

	"github.com/altessa-s/rmcloud-go/internal/port"
	"github.com/altessa-s/rmcloud-go/internal/wiring"
)

// SecretStore is the caller's store for the three protocol secrets
// ([SecretDeviceToken], [SecretDeviceID], [SecretUserToken]).
//
//	type SecretStore interface {
//		// Secret returns the stored value. ok is false when the name is absent.
//		Secret(ctx context.Context, which SecretName) (value string, ok bool, err error)
//		// Save writes value under which, replacing whatever was there.
//		Save(ctx context.Context, which SecretName, value string) error
//		// Forget removes which. Removing a name that is not there is success.
//		Forget(ctx context.Context, which SecretName) error
//	}
type SecretStore = port.SecretStore

// Hosts is where sync and notifications are reached. Pass one to
// [WithResolver] to replace discovery.
//
//	type Hosts interface {
//		Endpoints(ctx context.Context) Endpoints
//		SyncHostFailed()
//		Notifications(ctx context.Context) url.URL
//	}
//
// Endpoints does not fetch. The worst answer is the pair the caller
// configured. SyncHostFailed tells discovery the region host did not
// answer. Notifications is the socket URL, still http or https; the dial
// rewrites the scheme.
type Hosts = port.Hosts

// Expectation is a declared download size. The zero value means the length
// is unknown.
type Expectation struct {
	size int64
}

// Expecting declares a download size. Expecting(0) means the length is unknown.
func Expecting(size int64) Expectation {
	return Expectation{size: size}
}

// RootWrite is one choice for a root write. The zero value does not broadcast.
type RootWrite struct {
	broadcast bool
}

// Broadcast asks the service to tell other devices when on is true.
// The last option passed to [Client.PutRoot] wins.
func Broadcast(on bool) RootWrite {
	return RootWrite{broadcast: on}
}

// Client is the sync client. [New] does not dial. [Client.Close] releases
// what the client holds.
type Client struct {
	sync      port.SyncStore
	tokens    port.Account
	notes     port.NotificationStream
	transport port.IdleCloser

	mu     sync.Mutex
	onNote func(Notification)
}

// New builds the token provider, the sync client, and the notification
// stream over store, which holds the device and user tokens. It does not
// dial, discover, or register, so it takes no context. A nil store is an
// error.
func New(store SecretStore, opts ...Option) (*Client, error) {
	if store == nil {
		return nil, errors.New("rmcloud: secret store is required")
	}
	// A nil option is skipped; the generated newOptions would call it.
	cfg := newOptions(slices.DeleteFunc(slices.Clone(opts), func(o Option) bool { return o == nil })...)

	ua := wiring.DefaultUserAgent()
	if cfg.userAgentSet {
		ua = cfg.userAgent
	}
	c := &Client{}
	parts := wiring.Build(wiring.Config{
		Secrets:      store,
		Endpoints:    cfg.endpoints,
		Resolver:     cfg.resolver,
		UserAgent:    ua,
		DeviceDesc:   cfg.deviceDesc,
		RoundTripper: cfg.roundTripper,
		RootCAs:      cfg.rootCAs,
		Logger:       cfg.logger,
		OnSignal:     c.deliver,
	})
	c.sync, c.tokens, c.notes, c.transport = parts.Store, parts.Account, parts.Stream, parts.Transport
	return c, nil
}

// Close stops the notification stream, waits for it, and closes idle
// connections. A call in progress is not interrupted. Close is safe to call
// more than once; the client is not used after it.
func (c *Client) Close() error {
	c.notes.Stop()
	c.transport.CloseIdleConnections()
	return nil
}

// Root reads the cloud pointer. The read tries v4, then v3.
func (c *Client) Root(ctx context.Context) (Root, error) {
	return c.sync.Root(ctx)
}

// Index reads one .docSchema file. The hash is checked while the file is decoded.
func (c *Client) Index(ctx context.Context, hash, name string) (Index, error) {
	return c.sync.Index(ctx, hash, name)
}

// RootIndex reads the root and the root index named by that root.
func (c *Client) RootIndex(ctx context.Context) (Root, Index, error) {
	return c.sync.RootIndex(ctx)
}

// Blob reads an object and checks that its hash is hash.
func (c *Client) Blob(ctx context.Context, hash, name string) ([]byte, error) {
	return c.sync.Blob(ctx, hash, name, true)
}

// BlobFile downloads an object to a temporary file the caller must remove.
// The file's hash is checked before it is returned. A zero [Expectation]
// leaves the length unknown. progress may be nil.
func (c *Client) BlobFile(ctx context.Context, hash, name string, expecting Expectation, progress Progress) (string, error) {
	size := expecting.size
	if size <= 0 {
		size = -1
	}
	return c.sync.BlobFile(ctx, hash, name, size, progress)
}

// Upload sends body in one PUT. An empty content type is chosen from name.
// The returned hash is the hash of body.
func (c *Client) Upload(ctx context.Context, body []byte, name string) (string, error) {
	return c.sync.Upload(ctx, body, name, "")
}

// UploadFile sends the file at path in one PUT. The content type is chosen
// from name. progress may be nil. The returned hash is the hash of the file.
func (c *Client) UploadFile(ctx context.Context, path, name string, progress Progress) (string, error) {
	return c.sync.UploadFile(ctx, path, name, progress)
}

// MissingFiles asks which of hashes the service does not hold, so an upload
// can skip the blobs that are already there. The answer is a subset of
// hashes, spelled as they were asked. An empty list is [NothingToAskError]
// and a hash that is not hex is [MissingHashError]; neither is sent.
func (c *Client) MissingFiles(ctx context.Context, hashes []string) ([]string, error) {
	return c.sync.MissingFiles(ctx, hashes)
}

// PutRoot writes the root pointer and returns the generation the service
// stored. A 409 or 412 is a [RootConflictError] and is not retried. The caller
// re-reads; this client does not merge the document.
func (c *Client) PutRoot(ctx context.Context, hash string, generation int64, opts ...RootWrite) (int64, error) {
	var broadcast bool
	for _, opt := range opts {
		broadcast = opt.broadcast
	}
	return c.sync.PutRoot(ctx, hash, generation, broadcast)
}
