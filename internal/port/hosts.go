// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package port

import (
	"context"
	"net/url"

	"github.com/altessa-s/rmcloud-go/internal/domain"
)

// SyncHosts names the sync service for one call. SyncHostFailed reports
// that the host could not be reached, so a source may fall back.
type SyncHosts interface {
	Endpoints(ctx context.Context) domain.Endpoints
	SyncHostFailed()
}

// NotificationHosts names the notification socket.
type NotificationHosts interface {
	Notifications(ctx context.Context) url.URL
}

// Hosts is both: what the composition root hands every use case that needs
// a host.
type Hosts interface {
	SyncHosts
	NotificationHosts
}
