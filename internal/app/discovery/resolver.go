// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package discovery

import (
	"context"
	"log/slog"
	"net/url"
	"sync"
	"time"

	"github.com/altessa-s/go-atlas-core/runtime/singleflight"
	"github.com/altessa-s/go-atlas-core/time/clock"
	"github.com/altessa-s/go-atlas-core/types/redacted"

	"github.com/altessa-s/rmcloud-go/internal/domain"
	"github.com/altessa-s/rmcloud-go/internal/port"
)

// TokenSource supplies the user token whose tectonic claim names a region.
// Discovery itself is unauthenticated: the token is read locally and not
// attached to the request.
type TokenSource = port.UserTokens

// Resolver turns the configured hosts into the hosts for one call.
// A pinned resolver never asks the token source and never discovers. A
// resolver without a discovery transport never discovers either: the
// region from the token still applies.
type Resolver struct {
	options

	configured domain.Endpoints
	clock      clock.Clock

	mu          sync.Mutex
	haveRegion  bool
	regionToken redacted.RedactedString
	region      string
	haveAttempt bool
	attemptAt   time.Time
	attempt     *discovered
	// discovery shares one discovery call among the callers that miss the
	// cache. It ends when every caller has left, and after discoveryTimeout.
	discovery    *singleflight.Group[string, *discovered]
	demoted      bool
	demoteLogged bool
}

// NewResolver caches discovery for an hour. Discovery never supplies the
// auth host. clock is required; a nil one panics here.
func NewResolver(configured domain.Endpoints, clk clock.Clock, opts ...Option) *Resolver {
	if clk == nil {
		panic("discovery: NewResolver needs a clock")
	}
	r := &Resolver{
		options:    *newOptions(opts...),
		configured: configured,
		clock:      clk,
		discovery: singleflight.New[string, *discovered](
			singleflight.WithLifecycle(context.Background()),
			singleflight.WithTimeout(discoveryTimeout),
		),
	}
	if r.pinned {
		r.tokens = nil
	}
	return r
}

func (r *Resolver) logger() *slog.Logger { return r.log }

// Endpoints returns the hosts for this call. It does not fail and it does
// not fetch discovery.
func (r *Resolver) Endpoints(ctx context.Context) domain.Endpoints {
	ep, _ := r.endpointsAndRegion(ctx)
	return ep
}

// SyncHostFailed drops the discovery cache and stays on the configured sync
// host. A pinned resolver ignores the call. The region parsed from the token
// is left in place.
func (r *Resolver) SyncHostFailed() {
	if r.pinned {
		return
	}
	r.mu.Lock()
	r.demoted = true
	r.haveAttempt = false
	r.attempt = nil
	first := !r.demoteLogged
	r.demoteLogged = true
	r.mu.Unlock()
	if first {
		r.logger().Warn("the region host did not answer; falling back to the configured sync host")
	}
}

// Notifications returns the published socket when it is trusted and either
// names the sync host or belongs to the active region. Otherwise it returns
// the socket on the sync host this call is using.
func (r *Resolver) Notifications(ctx context.Context) url.URL {
	if r.pinned {
		return r.configured.Notifications()
	}
	ep, region := r.endpointsAndRegion(ctx)
	answer := r.discover(ctx, ep.Discovery())
	if chosen := chooseNotifications(answer, ep, region); chosen != nil {
		return domain.NotificationsOn(*chosen)
	}
	return ep.Notifications()
}
