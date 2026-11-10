// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package httpapi

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/altessa-s/rmcloud-go/internal/domain"
	"github.com/altessa-s/rmcloud-go/internal/port"
)

// do sends httpReq. The response body is the caller's to close: readMemory
// and Download both do.
func (t *Transport) do(
	ctx context.Context, client *http.Client, httpReq *http.Request, body []byte, skipBody bool,
) (*http.Response, *url.URL, string, error) {
	method := httpReq.Method
	snap := snapshotURL(httpReq.URL)
	t.wire.sent(method, snap, httpReq.Header, body, skipBody)
	suppressUserAgent(httpReq.Header)
	resp, err := client.Do(httpReq)
	if err != nil {
		if resp != nil && resp.Body != nil {
			_ = resp.Body.Close()
		}
		t.wire.failed(method, snap, err.Error())
		return nil, snap, method, classify(ctx, err)
	}
	if resp != nil && resp.Body == nil {
		resp.Body = io.NopCloser(bytes.NewReader(nil))
	}
	return resp, snap, method, nil
}

// readMemory reads the body into memory. stall cancels the request, whose
// context is a child of ctx: a body that brings no byte for the idle read
// timeout is canceled through it and fails as a timeout. ctx is the
// caller's, and its end is returned as it is.
func (t *Transport) readMemory(
	ctx context.Context, resp *http.Response, snap *url.URL, method string, stall context.CancelCauseFunc,
) (port.Response, error) {
	if resp.Body == nil {
		resp.Body = io.NopCloser(bytes.NewReader(nil))
	}
	defer func() { _ = resp.Body.Close() }()
	watch := watchIdle(resp.Body, t.idleReadTimeout, stall)
	defer watch.stop()
	body, err := readLimited(watch, memoryLimit, resp.ContentLength)
	if errors.Is(err, errTooBig) {
		// The count is the ceiling plus one, as copyLimited reports it.
		n := memoryLimit + 1
		finalURL, finalMethod := finalHop(resp, snap, method)
		t.logger().Debug("oversized response", "bytes", n)
		t.wire.oversized(resp.StatusCode, finalMethod, finalURL, resp.Header, memoryLimit)
		return port.Response{}, domain.OversizedResponse(n, memoryLimit)
	}
	if err != nil {
		if watch.expired.Load() && ctx.Err() == nil {
			// The read failed because the watch canceled it. The error in
			// hand is a cancellation, which would read as the caller's.
			t.wire.failed(method, snap, errIdleRead.Error())
			return port.Response{}, domain.Transport(domain.ReasonTimedOut)
		}
		return t.fail(ctx, method, snap, err)
	}
	finalURL, finalMethod := finalHop(resp, snap, method)
	t.wire.received(resp.StatusCode, finalMethod, finalURL, resp.Header, body, false, int64(len(body)))
	return port.Response{
		Status: resp.StatusCode,
		Header: cloneHeader(resp.Header),
		Body:   body,
	}, nil
}

func (t *Transport) fail(ctx context.Context, method string, snap *url.URL, err error) (port.Response, error) {
	t.wire.failed(method, snap, err.Error())
	return port.Response{}, classify(ctx, err)
}

func newRequest(ctx context.Context, req port.Request) (*http.Request, error) {
	method := req.Method
	if method == "" {
		method = http.MethodGet
	}
	httpReq, err := newHTTPRequest(ctx, method, req.URL)
	if err != nil {
		return nil, err
	}
	if req.Header != nil {
		h := make(http.Header, len(req.Header))
		for key, values := range req.Header {
			cp := make([]string, len(values))
			copy(cp, values)
			h[key] = cp
		}
		httpReq.Header = h
	}
	return httpReq, nil
}

// newHTTPRequest builds a request for u as it stands. The URL is assigned
// rather than parsed from its string, so the spelling the caller built,
// including an escaped path, is the spelling that goes on the wire.
func newHTTPRequest(ctx context.Context, method string, u url.URL) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, method, "/", nil)
	if err != nil {
		return nil, err
	}
	req.URL = &u
	req.Host = u.Host
	return req, nil
}

func setMemoryBody(req *http.Request, body []byte) {
	method := strings.ToUpper(req.Method)
	if len(body) == 0 && method != http.MethodPost && method != http.MethodPut && method != http.MethodPatch {
		return
	}
	req.Body = io.NopCloser(bytes.NewReader(body))
	req.ContentLength = int64(len(body))
	req.GetBody = func() (io.ReadCloser, error) {
		return io.NopCloser(bytes.NewReader(body)), nil
	}
}

func suppressUserAgent(h http.Header) {
	if h == nil {
		return
	}
	for key := range h {
		if strings.EqualFold(key, "User-Agent") {
			return
		}
	}
	h["User-Agent"] = []string{""}
}

func snapshotURL(u *url.URL) *url.URL {
	if u == nil {
		return nil
	}
	return new(*u)
}

func finalHop(resp *http.Response, snap *url.URL, method string) (*url.URL, string) {
	if resp != nil && resp.Request != nil {
		m := resp.Request.Method
		if m == "" {
			m = method
		}
		if resp.Request.URL != nil {
			return snapshotURL(resp.Request.URL), m
		}
		return snap, m
	}
	return snap, method
}

func cloneHeader(h http.Header) port.Header {
	if len(h) == 0 {
		return nil
	}
	out := make(port.Header, len(h))
	for key, values := range h {
		cp := make([]string, len(values))
		copy(cp, values)
		out[key] = cp
	}
	return out
}
