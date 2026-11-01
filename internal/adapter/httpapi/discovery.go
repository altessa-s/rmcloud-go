// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package httpapi

import (
	"context"
	"io"
	"net/http"

	"github.com/altessa-s/go-atlas-net/http/client"

	"github.com/altessa-s/rmcloud-go/internal/domain"
	"github.com/altessa-s/rmcloud-go/internal/port"
)

// Discovery is the transport for the unauthenticated host list.
//
// It is not [Transport]: it sends no User-Agent and no token, follows no
// redirect, and keeps no wire log. It reads at most one byte past
// [domain.DiscoveryLimit], so the caller can tell a body that was too long.
// The caller's context carries the deadline; this client has none of its own.
type Discovery struct {
	client *http.Client
}

var _ port.RoundTripper = (*Discovery)(nil)

// NewDiscovery returns the discovery transport: a go-atlas-net client with
// no retries and no redirects. A nil round tripper uses a pooled transport
// behind the SSRF guard. A host's round tripper turns the guard off, and so
// does [WithoutSSRFProtection].
func NewDiscovery(rt http.RoundTripper, opts ...Option) *Discovery {
	cfg := newOptions(opts...)
	base := []client.Option{client.WithoutRetry(), client.WithoutRedirects()}
	switch {
	case rt != nil:
		base = append(base, client.WithClient(&http.Client{Transport: nilResponseGuard{rt}}), client.WithoutSSRFProtection())
	case cfg.open:
		base = append(base, client.WithTransport(pooled(cfg.roots)), client.WithoutSSRFProtection())
	default:
		base = append(base, client.WithTransport(pooled(cfg.roots)))
	}
	return &Discovery{client: client.New(base...)}
}

// RoundTrip performs the GET. Only Method and URL are read from req.
func (d *Discovery) RoundTrip(ctx context.Context, req port.Request) (port.Response, error) {
	method := req.Method
	if method == "" {
		method = http.MethodGet
	}
	httpReq, err := newHTTPRequest(ctx, method, req.URL)
	if err != nil {
		return port.Response{}, err
	}
	httpReq.Header.Set("User-Agent", "")
	resp, err := d.client.Do(httpReq)
	if err != nil {
		return port.Response{}, classify(ctx, err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(resp.Body, domain.DiscoveryLimit+1))
	if err != nil {
		return port.Response{}, classify(ctx, err)
	}
	return port.Response{Status: resp.StatusCode, Header: cloneHeader(resp.Header), Body: body}, nil
}
