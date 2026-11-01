// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package syncstore

import (
	"context"
	"encoding/json/v2"
	"net/url"

	"github.com/altessa-s/go-atlas-core/types/redacted"

	"github.com/altessa-s/rmcloud-go/internal/app/retry"
	"github.com/altessa-s/rmcloud-go/internal/domain"
	"github.com/altessa-s/rmcloud-go/internal/port"
)

// rootUpdate is the root write body. Field order is the wire order:
// encoding/json/v2 emits struct fields in declaration order, with no spaces.
type rootUpdate struct {
	Broadcast  bool   `json:"broadcast"`
	Generation int64  `json:"generation"`
	Hash       string `json:"hash"`
}

// Upload sends bytes. An empty contentType is chosen from name. The
// returned hash is the hash of those bytes.
func (c *Client) Upload(ctx context.Context, data []byte, name, contentType string) (string, error) {
	hash := domain.BlobHash(data)
	goog := domain.GoogHash(data)
	if contentType == "" {
		contentType = domain.ContentType(name)
	}
	u := c.hosts.Endpoints(ctx).Blob(hash)
	err := c.putBlob(ctx, u, name, func(token redacted.RedactedString) port.Request {
		req := c.blobPut(token, name, contentType, goog, u)
		req.Body = data
		return req
	}, c.transport.RoundTrip)
	if err != nil {
		return "", err
	}
	return hash, nil
}

// UploadFile sends a file. The content type always comes from name. The
// file is read once for both checksums before it is sent.
func (c *Client) UploadFile(ctx context.Context, path, name string, progress domain.Progress) (string, error) {
	digest, err := c.files.Digest(ctx, path)
	if err != nil {
		return "", err
	}
	hash, goog := digest.SHA256, digest.GoogHash()
	contentType := domain.ContentType(name)
	u := c.hosts.Endpoints(ctx).Blob(hash)
	err = c.putBlob(ctx, u, name, func(token redacted.RedactedString) port.Request {
		req := c.blobPut(token, name, contentType, goog, u)
		req.File = path
		req.Progress = progress
		return req
	}, c.transport.Upload)
	if err != nil {
		return "", err
	}
	return hash, nil
}

func (c *Client) blobPut(token redacted.RedactedString, name, contentType, goog string, u url.URL) port.Request {
	req := c.authorizedRequest(token, name, u)
	req.Method = domain.MethodPut
	req.Header[domain.HeaderGoogHash] = []string{goog}
	req.Header["Content-Type"] = []string{contentType}
	return req
}

// putBlob retries a blob PUT. The header map is built inside each try.
func (c *Client) putBlob(
	ctx context.Context, u url.URL, name string,
	build func(token redacted.RedactedString) port.Request,
	send func(context.Context, port.Request) (port.Response, error),
) error {
	_, err := c.withReauth(ctx, func(ctx context.Context, token redacted.RedactedString) (struct{}, error) {
		return c.attempt(ctx, u, func(ctx context.Context) (struct{}, error) {
			resp, err := send(ctx, build(token))
			if err != nil {
				return struct{}{}, err
			}
			if err := checkWrite(name, u, resp); err != nil {
				return struct{}{}, err
			}
			return struct{}{}, nil
		})
	})
	return err
}

// PutRoot writes the root pointer on the v3 path. The generation in the
// answer is the service's, not the one the caller sent. 409 and 412 are a
// conflict and are not retried. A rate limit is retried and nothing else
// is: a write that may have landed is not repeated blind. A dropped
// connection is returned as it arrived. Each attempt carries its own
// unauthorized refresh.
func (c *Client) PutRoot(ctx context.Context, hash string, generation int64, broadcast bool) (int64, error) {
	u := c.hosts.Endpoints(ctx).RootWrite()
	body, err := json.Marshal(rootUpdate{
		Broadcast:  broadcast,
		Generation: generation,
		Hash:       hash,
	})
	if err != nil {
		return 0, err
	}
	return retry.Run(ctx, c.rootRetries, func(ctx context.Context) (int64, error) {
		return c.putRootOnce(ctx, u, body)
	})
}

func (c *Client) putRootOnce(ctx context.Context, u url.URL, body []byte) (int64, error) {
	return c.withReauth(ctx, func(ctx context.Context, token redacted.RedactedString) (int64, error) {
		req := c.authorizedRequest(token, domain.RootHashName, u)
		req.Method = domain.MethodPut
		req.Header["Content-Type"] = []string{domain.ContentTypeJSON}
		req.Body = body
		resp, err := c.transport.RoundTrip(ctx, req)
		if err != nil {
			return 0, err
		}
		if resp.Status == domain.StatusConflict || resp.Status == domain.StatusPreconditionFailed {
			return 0, domain.RootConflict(resp.Status)
		}
		if rejected := checkWrite(domain.RootHashName, u, resp); rejected != nil {
			return 0, rejected
		}
		root, _, err := domain.ParseRoot(resp.Body)
		if err != nil {
			return 0, err
		}
		return root.Generation, nil
	})
}
