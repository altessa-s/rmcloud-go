// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package fs

import (
	"context"
	"errors"
	"io"
	"io/fs"
	"os"

	"github.com/altessa-s/rmcloud-go/internal/domain"
	"github.com/altessa-s/rmcloud-go/internal/port"
)

// Files is the local filesystem.
type Files struct{}

var _ port.Files = Files{}

// Digest reads the file at path once and returns both of its checksums.
// The file is opened before cancellation is consulted, so a missing file is
// an os error even when ctx is already done.
func (Files) Digest(ctx context.Context, path string) (domain.Digest, error) {
	f, err := os.Open(path) // #nosec G304 -- the caller names the file
	if err != nil {
		return domain.Digest{}, err
	}
	defer func() { _ = f.Close() }()
	return domain.DigestReader(ctx, f)
}

// Remove deletes path. A file that is already gone is success.
func (Files) Remove(path string) error {
	if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return nil
}

// Head returns at most n bytes from the start of path. A short file is
// returned whole.
func (Files) Head(path string, n int) ([]byte, error) {
	f, err := os.Open(path) // #nosec G304 -- the caller names the file
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	return io.ReadAll(io.LimitReader(f, int64(n)))
}
