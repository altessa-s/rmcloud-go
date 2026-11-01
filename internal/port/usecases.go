// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package port

import (
	"context"

	"github.com/altessa-s/rmcloud-go/internal/domain"
)

// The use cases' input boundaries: what the public facade asks of the parts
// the composition root builds. The facade holds only these.

// SyncStore reads and writes the sync store.
type SyncStore interface {
	Root(ctx context.Context) (domain.Root, error)
	Index(ctx context.Context, hash, name string) (domain.Index, error)
	RootIndex(ctx context.Context) (domain.Root, domain.Index, error)
	Blob(ctx context.Context, hash, name string, verify bool) ([]byte, error)
	BlobFile(ctx context.Context, hash, name string, expecting int64, progress domain.Progress) (string, error)
	Upload(ctx context.Context, data []byte, name, contentType string) (string, error)
	UploadFile(ctx context.Context, path, name string, progress domain.Progress) (string, error)
	MissingFiles(ctx context.Context, hashes []string) ([]string, error)
	PutRoot(ctx context.Context, hash string, generation int64, broadcast bool) (int64, error)
}

// Account registers, revokes and forgets this device and reads its plan.
type Account interface {
	IsRegistered(ctx context.Context) (bool, error)
	RegisterDevice(ctx context.Context, code string) error
	RevokeDevice(ctx context.Context) error
	ForgetDevice(ctx context.Context) error
	Plan(ctx context.Context) (*domain.Plan, error)
}

// NotificationStream reads the notification socket between Start and Stop.
type NotificationStream interface {
	Start(ctx context.Context)
	Stop()
}

// IdleCloser releases idle connections.
type IdleCloser interface {
	CloseIdleConnections()
}
