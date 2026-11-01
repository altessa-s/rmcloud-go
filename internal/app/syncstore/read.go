// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package syncstore

import (
	"context"
	"errors"

	"github.com/altessa-s/rmcloud-go/internal/domain"
)

// Root reads the root pointer. A version that answered before is tried
// first. A 404 drops that version and tries the other path. When both
// paths are missing, the error is no root yet.
func (c *Client) Root(ctx context.Context) (domain.Root, error) {
	if version, ok := c.cachedVersion(); ok {
		root, err := c.rootAt(ctx, version)
		if err == nil || !isNotFound(err) {
			return root, err
		}
		c.clearVersion(version)
		for _, other := range rootReadOrder() {
			if other == version {
				continue
			}
			root, err = c.rootAt(ctx, other)
			if err == nil || !isNotFound(err) {
				return root, err
			}
		}
		return domain.Root{}, domain.ErrNoRootYet
	}
	var last error
	for _, version := range rootReadOrder() {
		root, err := c.rootAt(ctx, version)
		if err == nil || !isNotFound(err) {
			return root, err
		}
		last = err
	}
	if isNotFound(last) {
		return domain.Root{}, domain.ErrNoRootYet
	}
	return domain.Root{}, last
}

func (c *Client) rootAt(ctx context.Context, version domain.RootVersion) (domain.Root, error) {
	u := c.hosts.Endpoints(ctx).Root(version)
	body, err := c.authorizedData(ctx, u, domain.RootHashName)
	if err != nil {
		return domain.Root{}, err
	}
	root, advisory, err := domain.ParseRoot(body)
	if err != nil {
		return domain.Root{}, err
	}
	c.storeVersion(version)
	c.note(advisory)
	return root, nil
}

func isNotFound(err error) bool {
	status, ok := errors.AsType[*domain.HTTPError](err)
	return ok && status.Status == domain.StatusNotFound
}

// Blob reads one object. verify checks the bytes against hash. A hash that
// is empty or not hex is refused before any request.
func (c *Client) Blob(ctx context.Context, hash, name string, verify bool) ([]byte, error) {
	if !domain.IsHash(hash) {
		return nil, domain.MissingHash(name)
	}
	u := c.hosts.Endpoints(ctx).Blob(hash)
	body, err := c.authorizedData(ctx, u, name)
	if err != nil {
		return nil, err
	}
	if verify {
		actual := domain.BlobHash(body)
		if actual != hash {
			return nil, domain.HashMismatch(hash, actual)
		}
	}
	return body, nil
}

// BlobFile downloads one object to a temporary file. The caller owns a
// file that checks out. A failure removes the file.
func (c *Client) BlobFile(ctx context.Context, hash, name string, expecting int64, progress domain.Progress) (string, error) {
	if !domain.IsHash(hash) {
		return "", domain.MissingHash(name)
	}
	u := c.hosts.Endpoints(ctx).Blob(hash)
	path, err := c.download(ctx, u, name, expecting, progress)
	if err != nil {
		return "", err
	}
	digest, err := c.files.Digest(ctx, path)
	if err != nil {
		_ = c.files.Remove(path)
		return "", err
	}
	if digest.SHA256 != hash {
		_ = c.files.Remove(path)
		return "", domain.HashMismatch(hash, digest.SHA256)
	}
	return path, nil
}

// Index reads an index by the hash the caller was given. The blob hash is
// not checked first: an index may be addressed by its node hash. An index
// whose count line disagrees with its records is kept and logged.
func (c *Client) Index(ctx context.Context, hash, name string) (domain.Index, error) {
	body, err := c.Blob(ctx, hash, name, false)
	if err != nil {
		return domain.Index{}, err
	}
	index, by, err := domain.DecodeIndex(body, hash)
	if err != nil {
		return domain.Index{}, err
	}
	if by == domain.ByNodeHash {
		c.logger().Debug("index is addressed by its node hash", "name", name)
	}
	if index.CountMismatch() {
		c.logger().Warn("index count line disagrees with its records",
			"name", name, "declared", *index.Declared, "records", len(index.Entries))
	}
	return index, nil
}

// RootIndex reads the root pointer and the index that pointer names.
func (c *Client) RootIndex(ctx context.Context) (domain.Root, domain.Index, error) {
	root, err := c.Root(ctx)
	if err != nil {
		return domain.Root{}, domain.Index{}, err
	}
	index, err := c.Index(ctx, root.Hash, domain.RootIndexName)
	if err != nil {
		return domain.Root{}, domain.Index{}, err
	}
	return root, index, nil
}
