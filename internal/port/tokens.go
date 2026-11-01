// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package port

import (
	"context"

	"github.com/altessa-s/go-atlas-core/types/redacted"
)

// UserTokens supplies a user token the service will accept.
type UserTokens interface {
	UserToken(ctx context.Context) (redacted.RedactedString, error)
}

// RefreshableTokens is a [UserTokens] that can be told a token was refused.
// InvalidateUserToken drops that token, not a newer one.
type RefreshableTokens interface {
	UserTokens
	InvalidateUserToken(refused redacted.RedactedString)
}
