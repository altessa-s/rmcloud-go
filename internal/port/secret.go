// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package port

import (
	"context"

	"github.com/altessa-s/rmcloud-go/internal/domain"
)

// SecretStore is where the three protocol secrets live. The caller provides
// it.
type SecretStore interface {
	// Secret returns the stored value. ok is false when the name is absent.
	Secret(ctx context.Context, which domain.SecretName) (value string, ok bool, err error)

	// Save writes value under which, replacing whatever was there.
	Save(ctx context.Context, which domain.SecretName, value string) error

	// Forget removes which. Removing a name that is not there is success.
	Forget(ctx context.Context, which domain.SecretName) error
}
