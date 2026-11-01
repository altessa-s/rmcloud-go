// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package auth

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"strings"
	"testing"

	"github.com/altessa-s/go-atlas-core/time/clock"

	"github.com/altessa-s/rmcloud-go/internal/domain"
	"github.com/altessa-s/rmcloud-go/internal/port"
	"github.com/altessa-s/rmcloud-go/internal/porttest"
)

var errStore = errors.New("store unavailable")

// brokenStore fails the calls named in fail and passes the rest through.
type brokenStore struct {
	port.SecretStore
	failSecret map[domain.SecretName]bool
	failForget map[domain.SecretName]bool
}

func (b brokenStore) Secret(ctx context.Context, which domain.SecretName) (string, bool, error) {
	if b.failSecret[which] {
		return "", false, errStore
	}
	return b.SecretStore.Secret(ctx, which)
}

func (b brokenStore) Forget(ctx context.Context, which domain.SecretName) error {
	if b.failForget[which] {
		return errStore
	}
	return b.SecretStore.Forget(ctx, which)
}

// testDeps is the three dependencies New takes, so a test can drop one.
type testDeps struct {
	Secrets port.SecretStore
	Trip    port.RoundTripper
	Clock   clock.Clock
}

func (d testDeps) build() *Provider { return New(d.Secrets, d.Trip, d.Clock) }

func TestEveryDependencyIsRequired(t *testing.T) {
	t.Parallel()
	full := func() testDeps {
		return testDeps{Secrets: porttest.NewSecrets(nil), Trip: &porttest.Transport{}, Clock: porttest.NewClock()}
	}
	for name, drop := range map[string]func(*testDeps){
		"Secrets": func(d *testDeps) { d.Secrets = nil },
		"Trip":    func(d *testDeps) { d.Trip = nil },
		"Clock":   func(d *testDeps) { d.Clock = nil },
	} {
		deps := full()
		drop(&deps)
		func() {
			defer func() {
				if r := recover(); r == nil || !strings.Contains(fmt.Sprint(r), name) {
					t.Errorf("%s: panic = %v", name, r)
				}
			}()
			deps.build()
		}()
	}
	full().build()
}

func TestEndpointsAndLoggerOptions(t *testing.T) {
	t.Parallel()
	auth, err := url.Parse("https://auth.example")
	must(t, err)
	var buf bytes.Buffer
	h := newHarness(t, nil,
		WithEndpoints(domain.Endpoints{AuthHost: *auth, SyncHost: *auth}),
		WithLogger(slog.New(slog.NewTextHandler(&buf, nil))),
	)
	h.trip.Enqueue(porttest.Reply{Body: []byte("device-token-value")})
	must(t, h.p.RegisterDevice(t.Context(), "abcdefgh"))
	if call := oneCall(t, h.trip); call.URL.Host != "auth.example" {
		t.Fatalf("host = %s", call.URL.Host)
	}
	if !strings.Contains(buf.String(), "device registered") {
		t.Fatalf("log = %s", buf.String())
	}
}

func TestStoreFailuresAreReturned(t *testing.T) {
	t.Parallel()
	initial := map[domain.SecretName]string{
		domain.SecretDeviceToken: "device",
		domain.SecretDeviceID:    "id",
	}
	t.Run("is registered", func(t *testing.T) {
		t.Parallel()
		store := brokenStore{SecretStore: porttest.NewSecrets(initial), failSecret: map[domain.SecretName]bool{domain.SecretDeviceToken: true}}
		p := open(store, &porttest.Transport{}, nowClock())
		if _, err := p.IsRegistered(t.Context()); !errors.Is(err, errStore) {
			t.Fatalf("err = %v", err)
		}
		if err := p.RevokeDevice(t.Context()); !errors.Is(err, errStore) {
			t.Fatalf("revoke err = %v", err)
		}
	})
	for _, name := range []domain.SecretName{domain.SecretDeviceToken, domain.SecretDeviceID} {
		t.Run("forget "+string(name), func(t *testing.T) {
			t.Parallel()
			store := brokenStore{SecretStore: porttest.NewSecrets(initial), failForget: map[domain.SecretName]bool{name: true}}
			p := open(store, &porttest.Transport{}, nowClock())
			if err := p.ForgetDevice(t.Context()); !errors.Is(err, errStore) {
				t.Fatalf("err = %v", err)
			}
		})
	}
	t.Run("user token read falls through to a buy", func(t *testing.T) {
		t.Parallel()
		body := freshToken(t, "bought")
		trip := &porttest.Transport{}
		trip.Enqueue(porttest.Reply{Body: []byte(body)})
		store := brokenStore{SecretStore: porttest.NewSecrets(initial), failSecret: map[domain.SecretName]bool{domain.SecretUserToken: true}}
		p := open(store, trip, nowClock())
		got, err := p.UserToken(t.Context())
		if err != nil || got.Expose() != body {
			t.Fatalf("token = %q err = %v", got, err)
		}
	})
	t.Run("device token read fails the buy", func(t *testing.T) {
		t.Parallel()
		store := brokenStore{SecretStore: porttest.NewSecrets(nil), failSecret: map[domain.SecretName]bool{domain.SecretDeviceToken: true}}
		p := open(store, &porttest.Transport{}, nowClock())
		if _, err := p.UserToken(t.Context()); !errors.Is(err, errStore) {
			t.Fatalf("err = %v", err)
		}
	})
}

func TestDeviceIDSourceFailure(t *testing.T) {
	t.Parallel()
	broken := errors.New("no entropy")
	h := newHarness(t, nil, WithID(func() (string, error) { return "", broken }))
	if err := h.p.RegisterDevice(t.Context(), "abcdefgh"); !errors.Is(err, broken) {
		t.Fatalf("err = %v", err)
	}
	if len(h.trip.Calls()) != 0 {
		t.Fatal("a request went out without a device id")
	}
}

func TestPlanWithoutRegistration(t *testing.T) {
	t.Parallel()
	h := newHarness(t, nil)
	plan, err := h.p.Plan(t.Context())
	if plan != nil || !errors.Is(err, domain.ErrNotRegistered) {
		t.Fatalf("plan = %v err = %v", plan, err)
	}
}
