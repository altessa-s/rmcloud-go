// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package discovery

import (
	"context"
	"io"
	"log/slog"
	"net/url"
	"testing"
	"time"

	"github.com/altessa-s/rmcloud-go/internal/domain"
	"github.com/altessa-s/rmcloud-go/internal/port"
)

// Sinks keep the compiler from discarding the benchmarked calls.
var (
	sinkEndpoints domain.Endpoints
	sinkURL       url.URL
)

// benchDiscovery is the document the published-host benchmarks read.
var benchDiscovery = []byte(`{"notifications":"eu.internal.cloud.remarkable.com",` +
	`"webapp":"web.eu.tectonic.remarkable.com"}`)

// benchResolver is a resolver with an EU token, a discovery transport that
// always answers benchDiscovery, a clock read from clk, and a silent log.
func benchResolver(clk *testClock) *Resolver {
	trip := portTripFunc(func(ctx context.Context, _ port.Request) (port.Response, error) {
		if err := ctx.Err(); err != nil {
			return port.Response{}, err
		}
		return port.Response{Status: domain.StatusOK, Body: benchDiscovery}, nil
	})
	return NewResolver(domain.Production(), nowFunc(clk.now),
		WithTokens(&staticTokens{token: euToken()}),
		WithDiscovery(trip),
		WithLogger(slog.New(slog.NewTextHandler(io.Discard, nil))))
}

func BenchmarkEndpoints(b *testing.B) {
	b.Run("pinned", func(b *testing.B) {
		r := NewResolver(domain.Production(), nowFunc(time.Now), WithPinned())
		ctx := b.Context()
		b.ReportAllocs()
		for b.Loop() {
			sinkEndpoints = r.Endpoints(ctx)
		}
	})
	b.Run("region", func(b *testing.B) {
		r := benchResolver(newClock(time.Date(2026, time.October, 31, 12, 0, 0, 0, time.UTC)))
		ctx := b.Context()
		b.ReportAllocs()
		for b.Loop() {
			sinkEndpoints = r.Endpoints(ctx)
		}
	})
}

func BenchmarkNotifications(b *testing.B) {
	b.Run("cache-hit", func(b *testing.B) {
		r := benchResolver(newClock(time.Date(2026, time.October, 31, 12, 0, 0, 0, time.UTC)))
		ctx := b.Context()
		sinkURL = r.Notifications(ctx)
		b.ReportAllocs()
		for b.Loop() {
			sinkURL = r.Notifications(ctx)
		}
	})
	// Each round moves the clock past the cache lifetime, so every call
	// fetches and parses the document again.
	b.Run("cache-miss", func(b *testing.B) {
		clk := newClock(time.Date(2026, time.October, 31, 12, 0, 0, 0, time.UTC))
		r := benchResolver(clk)
		ctx := b.Context()
		b.ReportAllocs()
		for b.Loop() {
			clk.advance(domain.DiscoveryTTL + time.Second)
			sinkURL = r.Notifications(ctx)
		}
	})
}

func BenchmarkParseDiscovery(b *testing.B) {
	b.ReportAllocs()
	b.SetBytes(int64(len(benchDiscovery)))
	for b.Loop() {
		answer, store := interpret(benchDiscovery, domain.StatusOK)
		if !store || answer == nil {
			b.Fatal("the document was not read")
		}
	}
}
