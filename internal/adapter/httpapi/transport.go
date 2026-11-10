// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package httpapi

import (
	"context"
	"crypto/x509"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/altessa-s/go-tlsutils"

	"github.com/altessa-s/go-atlas-net/http/client"

	"github.com/altessa-s/rmcloud-go/internal/port"
)

// Transport is the net/http adapter. RoundTrip, Upload and Download each
// have a client. Download's is capped at six connections per host, so a
// large file cannot occupy every slot the metadata calls need. Upload's
// waits longer for the response headers: see the bounds in options.go.
//
// No client has a deadline for a whole request. What is bounded is a
// request that stopped moving: headers that never come, an in-memory body
// that stalls, and an HTTP/2 connection that no longer answers a ping. Each
// is a transport error the retry policy repeats. The caller's context is
// the only overall limit, and its end is returned as it is.
type Transport struct {
	memory   *http.Client
	upload   *http.Client
	download *http.Client
	// memBase, upBase and dlBase are the pooled transports beneath the
	// clients when the host supplied no round tripper.
	memBase *http.Transport
	upBase  *http.Transport
	dlBase  *http.Transport
	wire    wire

	options
}

// New builds a transport. The wire log is off unless WithLevel turns it on.
//
// The clients are go-atlas-net clients without retries: the use cases
// decide what to repeat. A circuit breaker per host stops a dead host from
// being dialed again and again, transport errors have their URLs redacted,
// and, unless [WithoutSSRFProtection], a dial to a private or loopback
// address is refused.
func New(opts ...Option) *Transport {
	t := &Transport{options: *newOptions(opts...)}
	t.wire.level = t.level
	t.wire.writer = t.wireWriter
	if t.wire.writer == nil {
		t.wire.writer = slogDebugWriter{log: t.logger}
	}
	t.memBase, t.upBase, t.dlBase = t.baseTransports()
	t.memory = t.client(t.memBase)
	t.upload = t.client(t.upBase)
	t.download = t.client(t.dlBase)
	return t
}

// client is a go-atlas-net client over base, or over the host's round
// tripper when there is one. Redirects follow [Transport.checkRedirect].
func (t *Transport) client(base *http.Transport) *http.Client {
	opts := []client.Option{client.WithoutRetry(), client.WithLogger(t.logger())}
	switch {
	case t.host != nil:
		// The host's round tripper is its own; the guard cannot see its dials.
		opts = append(opts, client.WithClient(&http.Client{Transport: nilResponseGuard{t.host}}), client.WithoutSSRFProtection())
	case t.open:
		opts = append(opts, client.WithTransport(base), client.WithoutSSRFProtection())
	default:
		opts = append(opts, client.WithTransport(base))
	}
	c := client.New(opts...)
	c.CheckRedirect = t.checkRedirect
	return c
}

func (t *Transport) logger() *slog.Logger { return t.log }

// CloseIdleConnections closes the idle connections of every client. A call
// in progress is not interrupted.
func (t *Transport) CloseIdleConnections() {
	t.memory.CloseIdleConnections()
	t.upload.CloseIdleConnections()
	t.download.CloseIdleConnections()
}

// baseTransports are go-atlas-net pooled transports with go-tlsutils'
// client TLS: TLS 1.2 or later with its curated cipher suites. Each gives
// up on a request that stopped moving.
func (t *Transport) baseTransports() (memory, upload, download *http.Transport) {
	memory = t.bounded(t.responseHeaderTimeout)
	upload = t.bounded(t.uploadResponseHeaderTimeout)
	download = t.bounded(t.responseHeaderTimeout)
	download.MaxConnsPerHost = downloadConnsPerHost
	return memory, upload, download
}

// bounded is a pooled transport that waits headerTimeout for the response
// headers of a request it has written, and pings an HTTP/2 connection that
// has gone silent. net/http starts the header wait only once the request
// body is written, so the time an upload takes to send is not counted. A
// connection whose ping goes unanswered is closed, which fails every
// request on it; a slow peer that answers keeps its connection.
func (t *Transport) bounded(headerTimeout time.Duration) *http.Transport {
	tr := pooled(t.roots)
	tr.ResponseHeaderTimeout = headerTimeout
	tr.HTTP2 = &http.HTTP2Config{SendPingTimeout: t.pingAfter, PingTimeout: t.pingTimeout}
	return tr
}

// Pooled is the transport the clients start from, for a caller that needs
// the same TLS on a connection of its own, such as the notification socket.
// Nil roots use the system pool. It has none of the clients' bounds: the
// socket is long-lived and keeps itself alive.
func Pooled(roots *x509.CertPool) *http.Transport { return pooled(roots) }

func pooled(roots *x509.CertPool) *http.Transport {
	tr := client.DefaultPooledTransport()
	tr.TLSClientConfig = tlsutils.DefaultClientTLSConfig("")
	tr.TLSClientConfig.RootCAs = roots
	return tr
}

// errNotHTTP is what a round tripper that answered nothing at all becomes.
var errNotHTTP = errors.New("httpapi: round tripper returned no response and no error")

// nilResponseGuard turns a round tripper's (nil, nil) into errNotHTTP
// before net/http sees it, so the failure is recognized by identity rather
// than by the wording of net/http's own message.
type nilResponseGuard struct{ rt http.RoundTripper }

func (g nilResponseGuard) RoundTrip(req *http.Request) (*http.Response, error) {
	resp, err := g.rt.RoundTrip(req)
	if resp == nil && err == nil {
		return nil, errNotHTTP
	}
	return resp, err
}

var _ port.Transport = (*Transport)(nil)

// RoundTrip performs the call and keeps the body in memory, up to 32 MiB.
// File is ignored. A non-2xx status is still a response; the caller decides.
// A body that stops arriving fails as a timeout.
func (t *Transport) RoundTrip(ctx context.Context, req port.Request) (port.Response, error) {
	reqCtx, stall := context.WithCancelCause(ctx)
	defer stall(nil)
	httpReq, err := newRequest(reqCtx, req)
	if err != nil {
		return port.Response{}, err
	}
	setMemoryBody(httpReq, req.Body)
	resp, snap, method, err := t.do(ctx, t.memory, httpReq, req.Body, false) //nolint:bodyclose // readMemory closes it
	if err != nil {
		return port.Response{}, err
	}
	return t.readMemory(ctx, resp, snap, method, stall)
}
