// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package discovery

import (
	"context"
	"strings"

	"github.com/altessa-s/rmcloud-go/internal/domain"
)

func (r *Resolver) endpointsAndRegion(ctx context.Context) (domain.Endpoints, string) {
	if r.pinned {
		return r.configured, ""
	}
	r.mu.Lock()
	demoted := r.demoted
	r.mu.Unlock()
	if demoted {
		return r.configured, ""
	}
	region := r.currentRegion(ctx)
	host, ok := domain.Tectonic(region)
	if !ok {
		return r.configured, ""
	}
	ep := r.configured
	ep.SyncHost = host
	return ep, region
}

func (r *Resolver) currentRegion(ctx context.Context) string {
	if r.tokens == nil {
		return ""
	}
	token, err := r.tokens.UserToken(ctx)
	if err != nil {
		return ""
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.haveRegion && r.regionToken == token {
		return r.region
	}
	region := regionOf(token.Expose())
	r.haveRegion = true
	r.regionToken = token
	r.region = region
	return region
}

func regionOf(token string) string {
	claims := domain.Claims(token)
	if claims == nil {
		return ""
	}
	raw, ok := claims["tectonic"].(string)
	if !ok {
		return ""
	}
	if _, ok := domain.Tectonic(raw); !ok {
		return ""
	}
	return strings.ToLower(raw)
}
