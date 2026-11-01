// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package port

import (
	"context"

	"github.com/altessa-s/rmcloud-go/internal/domain"
)

// Files is the part of the local filesystem the sync client touches: the
// blob files it uploads and the downloads it checks and discards.
type Files interface {
	// Digest reads path once and returns its SHA-256 and CRC32C.
	Digest(ctx context.Context, path string) (domain.Digest, error)
	// Head returns at most n bytes from the start of path.
	Head(path string, n int) ([]byte, error)
	// Remove deletes path. A file that is already gone is success.
	Remove(path string) error
}
