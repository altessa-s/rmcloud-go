// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package discovery

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"

	"github.com/altessa-s/go-atlas-core/time/clock"

	"github.com/altessa-s/rmcloud-go/internal/domain"
	"github.com/altessa-s/rmcloud-go/internal/port"
)

func TestRegionOf(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name  string
		token string
		want  string
	}{
		{"lowercased", testJWT(`{"tectonic":"EU"}`), "eu"},
		{"not a token", "garbage", ""},
		{"no claim", testJWT(`{"level":"connect"}`), ""},
		{"not a string", testJWT(`{"tectonic":7}`), ""},
		{"digits are refused", testJWT(`{"tectonic":"eu1"}`), ""},
		{"too short", testJWT(`{"tectonic":"e"}`), ""},
		{"too long", testJWT(`{"tectonic":"abcdefghi"}`), ""},
		{"a hostname is refused", testJWT(`{"tectonic":"evil.example"}`), ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := regionOf(tc.token); got != tc.want {
				t.Fatalf("%q, want %q", got, tc.want)
			}
		})
	}
}

func TestRefusedRegionKeepsTheConfiguredHost(t *testing.T) {
	t.Parallel()
	cfg := domain.Production()
	r := NewResolver(cfg, clock.Real(),
		WithTokens(&staticTokens{token: testJWT(`{"tectonic":"evil.example"}`)}),
		WithDiscovery(boomTrip{}),
	)
	if got := r.Endpoints(t.Context()); got != cfg {
		t.Fatalf("sync host %s", got.SyncHost.String())
	}
}

func TestInjectedLoggerHearsAFailedDiscovery(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	cfg := domain.Production()
	r := NewResolver(cfg, clock.Real(),
		WithDiscovery(portTripFunc(func(context.Context, port.Request) (port.Response, error) {
			return port.Response{}, errors.New("dial refused")
		})),
		WithLogger(nil),
		WithLogger(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))),
	)
	if got := r.Notifications(t.Context()); got != cfg.Notifications() {
		t.Fatalf("notifications %s", got.String())
	}
	if !strings.Contains(buf.String(), "discovery did not answer") {
		t.Fatalf("log = %s", buf.String())
	}
}
