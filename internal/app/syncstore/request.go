// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package syncstore

import (
	"context"
	"errors"
	"net/url"

	"github.com/altessa-s/go-atlas-core/types/redacted"

	"github.com/altessa-s/rmcloud-go/internal/app/retry"
	"github.com/altessa-s/rmcloud-go/internal/domain"
	"github.com/altessa-s/rmcloud-go/internal/port"
)

// writeDash separates a name from the service's detail. The dash is U+2014.
const writeDash = " \u2014 "

// withReauth runs op with one user token. On unauthorized it drops that
// token, fetches another, and runs op once more. The retry loop inside op
// is not part of this refresh.
func (c *Client) withReauth[T any](ctx context.Context, op func(context.Context, redacted.RedactedString) (T, error)) (T, error) {
	var zero T
	token, err := c.tokens.UserToken(ctx)
	if err != nil {
		return zero, err
	}
	value, err := op(ctx, token)
	if !errors.Is(err, domain.ErrUnauthorized) {
		return value, err
	}
	c.tokens.InvalidateUserToken(token)
	token, err = c.tokens.UserToken(ctx)
	if err != nil {
		return zero, err
	}
	return op(ctx, token)
}

// attempt repeats op under the client's policy. A host that could not be found is
// reported once the policy is spent. The error is returned, not logged as
// a failure: the caller decides what it means, and a 404 on the v4 root is
// an answer, not a fault. It is noted at debug level for the wire story.
func (c *Client) attempt[T any](ctx context.Context, u url.URL, op func(context.Context) (T, error)) (T, error) {
	value, err := retry.Run(ctx, c.retries, op)
	if err == nil {
		return value, nil
	}
	if _, ok := domain.KindOf(err); ok {
		c.logger().Debug("request failed", "path", u.Path, "err", err)
		if domain.IsHostUnreachable(err) {
			c.hosts.SyncHostFailed()
		}
	}
	return value, err
}

func (c *Client) authorizedRequest(token redacted.RedactedString, name string, u url.URL) port.Request {
	header := port.Header{}
	if c.userAgent != "" {
		header[domain.HeaderUserAgent] = []string{c.userAgent}
	}
	header[domain.HeaderAuthorization] = []string{"Bearer " + token.Expose()}
	if name != "" {
		header[domain.HeaderFilename] = []string{name}
	}
	return port.Request{URL: u, Header: header}
}

func (c *Client) authorizedData(ctx context.Context, u url.URL, name string) ([]byte, error) {
	return c.withReauth(ctx, func(ctx context.Context, token redacted.RedactedString) ([]byte, error) {
		return c.attempt(ctx, u, func(ctx context.Context) ([]byte, error) {
			return c.send(ctx, token, u, name)
		})
	})
}

func (c *Client) send(ctx context.Context, token redacted.RedactedString, u url.URL, name string) ([]byte, error) {
	req := c.authorizedRequest(token, name, u)
	req.Method = domain.MethodGet
	resp, err := c.transport.RoundTrip(ctx, req)
	if err != nil {
		return nil, err
	}
	if err := statusError(resp.Status, u, resp.Header.Get("Retry-After"), resp.Body); err != nil {
		return nil, err
	}
	return resp.Body, nil
}

func (c *Client) download(ctx context.Context, u url.URL, name string, expecting int64, progress domain.Progress) (string, error) {
	return c.withReauth(ctx, func(ctx context.Context, token redacted.RedactedString) (string, error) {
		return c.attempt(ctx, u, func(ctx context.Context) (string, error) {
			return c.downloadOnce(ctx, token, u, name, expecting, progress)
		})
	})
}

func (c *Client) downloadOnce(
	ctx context.Context, token redacted.RedactedString, u url.URL, name string, expecting int64, progress domain.Progress,
) (string, error) {
	req := c.authorizedRequest(token, name, u)
	req.Method = domain.MethodGet
	req.Limit = domain.LimitFor(expecting)
	if expecting > 0 {
		req.Expected = expecting
	} else {
		req.Expected = -1
	}
	req.Progress = progress
	resp, err := c.transport.Download(ctx, req)
	if err != nil {
		return "", err
	}
	if domain.IsSuccess(resp.Status) {
		return resp.File, nil
	}
	body := c.refusalBody(resp)
	if resp.File != "" {
		_ = c.files.Remove(resp.File)
	}
	if err := statusError(resp.Status, u, resp.Header.Get("Retry-After"), body); err != nil {
		return "", err
	}
	return "", domain.HTTP(resp.Status, u.Path)
}

// refusalBody is the start of a refused download, for the error detail.
func (c *Client) refusalBody(resp port.Response) []byte {
	if resp.File != "" {
		if head, err := c.files.Head(resp.File, domain.DetailLimit); err == nil && len(head) > 0 {
			return head
		}
	}
	if len(resp.Body) > domain.DetailLimit {
		return resp.Body[:domain.DetailLimit]
	}
	return resp.Body
}

func checkWrite(name string, u url.URL, resp port.Response) error {
	if domain.IsSuccess(resp.Status) {
		return nil
	}
	switch resp.Status {
	case domain.StatusUnauthorized, domain.StatusPaymentRequired, domain.StatusForbidden, domain.StatusTooManyRequests:
		// Not a success, so statusError always has an error to return.
		return statusError(resp.Status, u, resp.Header.Get("Retry-After"), resp.Body)
	}
	detail := domain.DetailOf(resp.Body)
	if detail == "" {
		detail = "(empty body)"
	}
	return domain.WriteRejected(resp.Status, name+writeDash+detail)
}

func statusError(status int, u url.URL, retryAfter string, body []byte) error {
	cloud := domain.FromHTTP(status, u.String(), retryAfter, body, nil)
	if cloud == nil {
		return nil
	}
	return cloud
}
