// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package porttest

import (
	"context"
	"errors"
	"testing"

	"github.com/altessa-s/rmcloud-go/internal/domain"
)

func TestSecretsRoundTrip(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := NewSecrets(map[domain.SecretName]string{
		domain.SecretDeviceToken: "device",
	})

	value, ok, err := store.Secret(ctx, domain.SecretDeviceToken)
	if err != nil || !ok || value != "device" {
		t.Fatalf("value %q ok %v err %v", value, ok, err)
	}
	_, ok, err = store.Secret(ctx, domain.SecretUserToken)
	if err != nil || ok {
		t.Fatalf("missing: ok %v err %v", ok, err)
	}

	if err := store.Save(ctx, domain.SecretUserToken, ""); err != nil {
		t.Fatal(err)
	}
	value, ok, err = store.Secret(ctx, domain.SecretUserToken)
	if err != nil || !ok || value != "" {
		t.Fatalf("empty string: %q ok %v err %v", value, ok, err)
	}

	if err := store.Forget(ctx, domain.SecretDeviceID); err != nil {
		t.Fatal(err)
	}
	if err := store.Forget(ctx, domain.SecretDeviceToken); err != nil {
		t.Fatal(err)
	}
	_, ok, err = store.Secret(ctx, domain.SecretDeviceToken)
	if err != nil || ok {
		t.Fatalf("forgotten: ok %v err %v", ok, err)
	}
}

func TestSecretsRefuseForgetKeepsTheValue(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := NewSecrets(map[domain.SecretName]string{
		domain.SecretDeviceToken: "device",
	})
	store.RefuseForget(true)
	err := store.Forget(ctx, domain.SecretDeviceToken)
	if !errors.Is(err, ErrRefused) {
		t.Fatal(err)
	}
	if _, isProtocol := domain.KindOf(err); isProtocol {
		t.Fatal("a refused delete is not a protocol error")
	}
	value, ok, getErr := store.Secret(ctx, domain.SecretDeviceToken)
	if getErr != nil || !ok || value != "device" {
		t.Fatalf("value %q ok %v err %v", value, ok, getErr)
	}
}

func TestSecretsHonourACancelledContext(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	store := NewSecrets(map[domain.SecretName]string{
		domain.SecretDeviceToken: "device",
	})
	store.RefuseForget(true)

	if _, _, err := store.Secret(ctx, domain.SecretDeviceToken); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if err := store.Save(ctx, domain.SecretUserToken, "user"); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if err := store.Forget(ctx, domain.SecretDeviceToken); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	value, ok, err := store.Secret(context.Background(), domain.SecretDeviceToken)
	if err != nil || !ok || value != "device" {
		t.Fatalf("cancelled forget changed the store: %q ok %v err %v", value, ok, err)
	}
}
