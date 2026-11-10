// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package domain

import (
	"encoding/base64"
	"encoding/json"
	"testing"
	"time"
)

func jwtToken(t *testing.T, payload any) string {
	t.Helper()
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	enc := base64.RawURLEncoding.EncodeToString
	return enc([]byte(`{"alg":"none"}`)) + "." + enc(raw) + "." + enc([]byte("sig"))
}

func TestClaimsSplit(t *testing.T) {
	if Claims("a..c") != nil {
		t.Fatal("a..c is two parts")
	}
	if Claims("a.b.") != nil {
		t.Fatal("a trailing dot drops the empty part")
	}
	body := jwtToken(t, map[string]any{"level": "connect"})
	parts := splitJWT(body)
	dotted := "." + parts[0] + "." + parts[1] + "." + parts[2]
	got := Claims(dotted)
	if got["level"] != "connect" {
		t.Fatalf(".a.b.c payload = %#v", got)
	}
	if Claims(jwtToken(t, nil)) != nil {
		t.Fatal("null is not an object")
	}
	if Claims(jwtToken(t, []any{"a"})) != nil {
		t.Fatal("an array is not an object")
	}
	enc := base64.RawURLEncoding.EncodeToString
	if Claims("h."+enc([]byte(`{"exp":1,"exp":2}`))+".s") != nil {
		t.Fatal("a duplicate claim is not an object")
	}
}

func TestPlanFromToken(t *testing.T) {
	token := jwtToken(t, map[string]any{
		"level":  "connect",
		"scopes": "docedit sync:fox hwc",
	})
	plan := PlanFromToken(token)
	if plan == nil || !plan.IsConnect() || plan.SyncScope == nil || *plan.SyncScope != "sync:fox" {
		t.Fatalf("%#v", plan)
	}
	tortoise := jwtToken(t, map[string]any{"scopes": "sync:tortoise"})
	plan = PlanFromToken(tortoise)
	if plan == nil || plan.IsConnect() || *plan.SyncScope != "sync:tortoise" {
		t.Fatalf("%#v", plan)
	}
	tabbed := jwtToken(t, map[string]any{"scopes": "sync:fox\tsync:tortoise"})
	plan = PlanFromToken(tabbed)
	if plan.SyncScope == nil || *plan.SyncScope != "sync:fox\tsync:tortoise" {
		t.Fatalf("tab scope = %#v", plan.SyncScope)
	}
	emptyLevel := jwtToken(t, map[string]any{"level": ""})
	plan = PlanFromToken(emptyLevel)
	if plan == nil || plan.Level != nil || plan.IsConnect() {
		t.Fatalf("empty level %#v", plan)
	}
	numberLevel := jwtToken(t, map[string]any{"level": 1})
	plan = PlanFromToken(numberLevel)
	if plan == nil || plan.Level != nil {
		t.Fatalf("non-string level %#v", plan)
	}
	if PlanFromToken("not-a-jwt") != nil {
		t.Fatal("unreadable token has no plan")
	}
}

func TestDeviceIDAndExpiry(t *testing.T) {
	token := jwtToken(t, map[string]any{"device-id": "abc", "exp": 1.5})
	id := DeviceID(token)
	if id == nil || *id != "abc" {
		t.Fatalf("id = %#v", id)
	}
	if DeviceID(jwtToken(t, map[string]any{"device-id": ""})) != nil {
		t.Fatal("empty device-id is nil")
	}
	if DeviceID(jwtToken(t, map[string]any{"device-id": 4})) != nil {
		t.Fatal("non-string device-id is nil")
	}
	exp := Expiry(token)
	want := time.Unix(1, 500_000_000).UTC()
	if exp == nil || !exp.Equal(want) {
		t.Fatalf("exp = %v want %v", exp, want)
	}
	for _, huge := range []float64{1e300, -1e300} {
		if got := Expiry(jwtToken(t, map[string]any{"exp": huge})); got != nil {
			t.Fatalf("exp %g = %v, want no expiry", huge, got)
		}
	}
	neg := Expiry(jwtToken(t, map[string]any{"exp": -1.25}))
	wantNeg := time.Unix(-2, 750_000_000).UTC()
	if neg == nil || !neg.Equal(wantNeg) {
		t.Fatalf("negative exp = %v want %v", neg, wantNeg)
	}
}

func TestIssuedAt(t *testing.T) {
	whole := IssuedAt(jwtToken(t, map[string]any{"iat": 1_700_000_000}))
	if want := time.Unix(1_700_000_000, 0); whole == nil || !whole.Equal(want) {
		t.Fatalf("iat = %v want %v", whole, want)
	}
	frac := IssuedAt(jwtToken(t, map[string]any{"iat": 1_700_000_000.5}))
	if want := time.Unix(1_700_000_000, 500_000_000); frac == nil || !frac.Equal(want) {
		t.Fatalf("fractional iat = %v want %v", frac, want)
	}
	if got := IssuedAt(jwtToken(t, map[string]any{"exp": 1})); got != nil {
		t.Fatalf("a token without iat was issued at %v", got)
	}
	if got := IssuedAt(jwtToken(t, map[string]any{"iat": "1700000000"})); got != nil {
		t.Fatalf("a string iat was read as %v", got)
	}
	if got := IssuedAt("not-a-token"); got != nil {
		t.Fatalf("an unreadable token was issued at %v", got)
	}
}

func TestIsUsable(t *testing.T) {
	moment := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	exactMargin := moment.Add(ExpiryMargin)
	if IsUsable(&exactMargin, nil, moment) {
		t.Fatal("exactly the margin is not usable")
	}
	justOver := moment.Add(ExpiryMargin + time.Nanosecond)
	if !IsUsable(&justOver, nil, moment) {
		t.Fatal("just over the margin is usable")
	}
	held := moment.Add(-UserTokenLifetime)
	if IsUsable(nil, &held, moment) {
		t.Fatal("a hold of exactly 20h is not usable")
	}
	earlier := moment.Add(-UserTokenLifetime + time.Nanosecond)
	if !IsUsable(nil, &earlier, moment) {
		t.Fatal("a hold just under 20h is usable")
	}
	if IsUsable(nil, nil, moment) {
		t.Fatal("no expiry and no hold is not usable")
	}
	staleHold := moment.Add(-time.Hour)
	if IsUsable(&exactMargin, &staleHold, moment) {
		t.Fatal("an expiry is judged on its own")
	}
	fresh := jwtToken(t, map[string]any{"exp": float64(moment.Add(ExpiryMargin + time.Second).Unix())})
	if !IsFresh(fresh, moment) {
		t.Fatal("token with room past the margin is fresh")
	}
	edge := jwtToken(t, map[string]any{"exp": float64(moment.Add(ExpiryMargin).Unix())})
	if IsFresh(edge, moment) {
		t.Fatal("exactly the margin is not fresh")
	}
	if IsFresh(jwtToken(t, map[string]any{"level": "connect"}), moment) {
		t.Fatal("no exp is not fresh")
	}
}

func splitJWT(token string) []string {
	var parts []string
	start := 0
	for i := range len(token) {
		if token[i] == '.' {
			parts = append(parts, token[start:i])
			start = i + 1
		}
	}
	return append(parts, token[start:])
}

func FuzzClaims(f *testing.F) {
	f.Add("eyJhbGciOiJub25lIn0.eyJleHAiOjE3MDAwMDAwMDAsImxldmVsIjoiY29ubmVjdCIsInNjb3BlcyI6InN5bmM6ZnVsbCJ9.c2ln")
	f.Add("a.b.c")
	f.Add("")
	f.Fuzz(func(_ *testing.T, token string) {
		_ = Claims(token)
		_ = Expiry(token)
		_ = DeviceID(token)
		_ = PlanFromToken(token)
	})
}
