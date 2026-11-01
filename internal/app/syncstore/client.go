// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package syncstore

import (
	"context"
	"log/slog"
	"strings"
	"sync"

	"github.com/altessa-s/go-atlas-core/time/clock"

	"github.com/altessa-s/rmcloud-go/internal/app/retry"
	"github.com/altessa-s/rmcloud-go/internal/domain"
	"github.com/altessa-s/rmcloud-go/internal/port"
)

// Tokens supplies the user token for each call.
type Tokens = port.RefreshableTokens

// Hosts names the sync service.
type Hosts = port.SyncHosts

// Client reads and writes the sync store.
type Client struct {
	options

	transport port.Transport
	tokens    Tokens
	clock     clock.Clock
	files     port.Files

	// retries and rootRetries are the policy built on the clock once: every
	// call but PutRoot, and PutRoot, which repeats a rate limit and nothing
	// else.
	retries     *retry.Runner
	rootRetries *retry.Runner

	mu           sync.Mutex
	rootVersion  *domain.RootVersion // path that last answered
	lastAdvisory *domain.RootAdvisory
}

// pinned is a host source that never changes and never reports a failure.
type pinned struct {
	endpoints domain.Endpoints
}

func (p pinned) Endpoints(context.Context) domain.Endpoints { return p.endpoints }

func (p pinned) SyncHostFailed() {}

// rootReadOrder is v4, then v3. A 404 is the only reason to try the next path.
func rootReadOrder() [2]domain.RootVersion {
	return [2]domain.RootVersion{domain.RootV4, domain.RootV3}
}

// productionHosts is the default host source: production, pinned.
func productionHosts() Hosts { return pinned{endpoints: domain.Production()} }

// New returns a client pinned to production until [WithHosts] says
// otherwise. A missing dependency is a programming error and panics here.
//
// tokens authorize each call, transport carries it, files reads uploads and
// checks downloads, and clk waits between retries.
func New(tokens Tokens, transport port.Transport, files port.Files, clk clock.Clock, opts ...Option) *Client {
	switch {
	case tokens == nil:
		panic("syncstore: Tokens is required")
	case transport == nil:
		panic("syncstore: Transport is required")
	case files == nil:
		panic("syncstore: Files is required")
	case clk == nil:
		panic("syncstore: Clock is required")
	}
	c := &Client{
		options:   *newOptions(opts...),
		transport: transport,
		tokens:    tokens,
		files:     files,
		clock:     clk,
	}
	rootPolicy := c.policy
	rootPolicy.Retryable = retry.OnlyRateLimits
	c.retries = c.policy.On(clk)
	c.rootRetries = rootPolicy.On(clk)
	return c
}

func (c *Client) logger() *slog.Logger { return c.log }

func (c *Client) cachedVersion() (domain.RootVersion, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.rootVersion == nil {
		return "", false
	}
	return *c.rootVersion, true
}

func (c *Client) storeVersion(version domain.RootVersion) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.rootVersion = new(version)
}

func (c *Client) clearVersion(version domain.RootVersion) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.rootVersion != nil && *c.rootVersion == version {
		c.rootVersion = nil
	}
}

// note remembers the advisory and logs what changed. The log runs after the
// lock is released. An empty remark is silent, and the advisory is kept anyway.
func (c *Client) note(advisory domain.RootAdvisory) {
	c.mu.Lock()
	var previous *domain.RootAdvisory
	if c.lastAdvisory != nil {
		previous = new(*c.lastAdvisory)
	}
	c.lastAdvisory = new(advisory)
	c.mu.Unlock()
	remarks := advisory.Remarks(previous)
	if len(remarks) == 0 {
		return
	}
	msg := "root pointer: " + strings.Join(remarks, "; ")
	if advisory.IsOrdinary() {
		c.logger().Debug(msg)
		return
	}
	c.logger().Info(msg)
}
