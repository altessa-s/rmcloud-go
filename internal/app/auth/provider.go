// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package auth

import (
	"log/slog"
	"sync"

	"github.com/altessa-s/go-atlas-core/runtime/singleflight"
	"github.com/altessa-s/go-atlas-core/time/clock"
	"github.com/altessa-s/go-atlas-core/types/redacted"

	"github.com/altessa-s/rmcloud-go/internal/app/retry"
	"github.com/altessa-s/rmcloud-go/internal/port"
)

// Provider buys the device token and the user token and remembers both.
//
// Methods are safe to call together. Two locks split the work:
//
//   - mu guards what this process remembers: the cache and the registration
//     session. It is never held across a
//     call to the secret store or the network, so a cache hit and an
//     invalidation never wait on a slow store.
//   - storeMu serializes the compound store writes: registering, forgetting,
//     and keeping a bought token. Each checks the session and writes as one
//     step, so a device forgotten mid-write cannot come back.
//
// storeMu is always taken before mu, never the other way round.
type Provider struct {
	options

	secrets port.SecretStore
	trip    port.RoundTripper
	clock   clock.Clock

	storeMu sync.Mutex

	mu      sync.Mutex
	session uint64
	cache   userTokenCache

	// buys shares one user-token buy per registration session among the
	// callers that want one. Forgetting the device moves the session, so
	// the next caller starts a fresh buy while the old one runs out.
	buys *singleflight.Group[uint64, redacted.RedactedString]

	// retries is the policy built on the clock once.
	retries *retry.Runner
}

// New returns a provider. A missing dependency is a programming error and
// panics here. The default User-Agent is none; the composition root sets it.
//
// secrets holds the device and user tokens, trip carries the token calls,
// and clk judges expiry and waits between retries.
func New(secrets port.SecretStore, trip port.RoundTripper, clk clock.Clock, opts ...Option) *Provider {
	switch {
	case secrets == nil:
		panic("auth: Secrets is required")
	case trip == nil:
		panic("auth: Trip is required")
	case clk == nil:
		panic("auth: Clock is required")
	}
	p := &Provider{
		options: *newOptions(opts...),
		secrets: secrets,
		trip:    trip,
		clock:   clk,
	}
	p.retries = p.policy.On(clk)
	p.buys = singleflight.New[uint64, redacted.RedactedString](singleflight.WithTimeout(p.exchangeTimeout))
	return p
}

func (p *Provider) logger() *slog.Logger { return p.log }
