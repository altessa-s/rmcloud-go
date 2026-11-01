// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package syncstore

import (
	"context"
	"net/url"

	"github.com/altessa-s/go-atlas-core/types/redacted"

	"github.com/altessa-s/rmcloud-go/internal/domain"
)

// MissingFiles asks which of hashes the service does not hold. An empty
// list or a hash that is not hex is refused before any request. The
// question changes nothing, so it is retried like a read.
func (c *Client) MissingFiles(ctx context.Context, hashes []string) ([]string, error) {
	body, err := domain.CheckFilesBody(hashes)
	if err != nil {
		return nil, err
	}
	u := c.hosts.Endpoints(ctx).CheckFiles()
	return c.withReauth(ctx, func(ctx context.Context, token redacted.RedactedString) ([]string, error) {
		return c.attempt(ctx, u, func(ctx context.Context) ([]string, error) {
			return c.checkFilesOnce(ctx, token, u, body, hashes)
		})
	})
}

func (c *Client) checkFilesOnce(ctx context.Context, token redacted.RedactedString, u url.URL, body []byte, hashes []string) ([]string, error) {
	req := c.authorizedRequest(token, "", u)
	req.Method = domain.MethodPost
	req.Header["Content-Type"] = []string{domain.ContentTypeJSON}
	req.Body = body
	resp, err := c.transport.RoundTrip(ctx, req)
	if err != nil {
		return nil, err
	}
	if err := statusError(resp.Status, u, resp.Header.Get("Retry-After"), resp.Body); err != nil {
		return nil, err
	}
	return domain.ParseMissingFiles(resp.Body, hashes)
}
