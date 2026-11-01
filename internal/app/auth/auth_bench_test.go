// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package auth

import (
	"context"
	"encoding/base64"
	"testing"
	"time"

	"github.com/altessa-s/go-atlas-core/types/redacted"

	"github.com/altessa-s/rmcloud-go/internal/domain"
	"github.com/altessa-s/rmcloud-go/internal/port"
	"github.com/altessa-s/rmcloud-go/internal/porttest"
)

// Sinks keep the compiler from discarding the benchmarked calls.
var (
	sinkToken redacted.RedactedString
	sinkPlan  *domain.Plan
)

// benchUserToken is an unsigned user token that stays fresh at benchNow.
func benchUserToken() string {
	enc := base64.RawURLEncoding.EncodeToString
	payload := `{"exp":4102444800,"device-id":"11111111-1111-4111-8111-111111111111",` +
		`"scopes":"sync:tortoise docedit intgr screenshare hwcmail:-1 mail:-1","tectonic":"eu"}`
	return enc([]byte(`{"alg":"none"}`)) + "." + enc([]byte(payload)) + "." + enc([]byte("sig"))
}

// benchNow is a fixed moment, so every run sees the same token state.
var benchNow = time.Date(2026, time.October, 31, 12, 0, 0, 0, time.UTC)

// staticTrip answers every exchange with the same user token and records
// nothing, so a benchmark measures the provider and not a queue.
type staticTrip struct {
	body []byte
}

func (s staticTrip) RoundTrip(ctx context.Context, _ port.Request) (port.Response, error) {
	if err := ctx.Err(); err != nil {
		return port.Response{}, err
	}
	return port.Response{Status: domain.StatusOK, Body: s.body}, nil
}

// benchProvider is a registered provider whose exchange returns token.
func benchProvider(token string, initial map[domain.SecretName]string) *Provider {
	secrets := porttest.NewSecrets(initial)
	return New(secrets, staticTrip{body: []byte(token)}, porttest.NewClockAt(benchNow), WithUserAgent(desktopUA))
}

func BenchmarkUserToken(b *testing.B) {
	token := benchUserToken()
	b.Run("cache-hit", func(b *testing.B) {
		p := benchProvider(token, map[domain.SecretName]string{
			domain.SecretDeviceToken: "device",
			domain.SecretUserToken:   token,
		})
		ctx := b.Context()
		b.ReportAllocs()
		for b.Loop() {
			got, err := p.UserToken(ctx)
			if err != nil {
				b.Fatal(err)
			}
			sinkToken = got
		}
	})
	// Each round refuses the token in hand, so the next call reads the store,
	// passes over the refused token, and buys a new one through the exchange.
	b.Run("exchange", func(b *testing.B) {
		p := benchProvider(token, map[domain.SecretName]string{
			domain.SecretDeviceToken: "device",
		})
		ctx := b.Context()
		b.ReportAllocs()
		for b.Loop() {
			got, err := p.UserToken(ctx)
			if err != nil {
				b.Fatal(err)
			}
			p.InvalidateUserToken(got)
			sinkToken = got
		}
	})
}

func BenchmarkPlan(b *testing.B) {
	token := benchUserToken()
	p := benchProvider(token, map[domain.SecretName]string{
		domain.SecretDeviceToken: "device",
		domain.SecretUserToken:   token,
	})
	ctx := b.Context()
	b.ReportAllocs()
	for b.Loop() {
		plan, err := p.Plan(ctx)
		if err != nil {
			b.Fatal(err)
		}
		sinkPlan = plan
	}
}

func BenchmarkUserTokenCacheUsable(b *testing.B) {
	var cache userTokenCache
	cache.remember(redacted.RedactedString(benchUserToken()), benchNow)
	moment := benchNow.Add(time.Hour)
	b.ReportAllocs()
	for b.Loop() {
		got, ok := cache.usable(moment)
		if !ok {
			b.Fatal("cached token is not usable")
		}
		sinkToken = got
	}
}
