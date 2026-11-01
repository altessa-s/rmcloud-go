// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package domain

import (
	"encoding/base64"
	"encoding/json/v2"
	"reflect"
	"strings"
	"testing"
	"time"
)

// claimsReference is the straightforward definition the claim readers must
// keep matching: empty pieces dropped, both base64 alphabets, the whole payload
// decoded into a map.
func claimsReference(token string) map[string]any {
	parts := strings.FieldsFunc(token, func(r rune) bool { return r == '.' })
	if len(parts) != 3 {
		return nil
	}
	encoded := strings.NewReplacer("-", "+", "_", "/").Replace(parts[1])
	if pad := (4 - len(encoded)%4) % 4; pad != 0 {
		encoded += strings.Repeat("=", pad)
	}
	raw, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return nil
	}
	var obj map[string]any
	if err := json.Unmarshal(raw, &obj); err != nil || obj == nil {
		return nil
	}
	return obj
}

func planReference(token string) *Plan {
	claims := claimsReference(token)
	if claims == nil {
		return nil
	}
	plan := &Plan{}
	if raw, ok := claims["level"].(string); ok && raw != "" {
		plan.Level = new(raw)
	}
	if raw, ok := claims["scopes"].(string); ok {
		for part := range strings.SplitSeq(raw, " ") {
			if part != "" && strings.HasPrefix(part, "sync:") {
				plan.SyncScope = new(part)
				break
			}
		}
	}
	return plan
}

func deviceIDReference(token string) *string {
	if id, ok := claimsReference(token)["device-id"].(string); ok && id != "" {
		return new(id)
	}
	return nil
}

func tokenWith(payload string) string {
	return "h." + base64.RawURLEncoding.EncodeToString([]byte(payload)) + ".s"
}

var claimSeeds = []string{
	tokenWith(`{"exp":1700000000,"device-id":"d","level":"connect","scopes":"a sync:fox"}`),
	tokenWith(`{"exp":1.5,"EXP":2}`),
	tokenWith(`{"exp":"soon","device-id":7}`),
	tokenWith(`{"exp":1,"exp":2}`),
	tokenWith(`null`),
	tokenWith(`[]`),
	tokenWith(`{}`),
	tokenWith(`{"level":""}`),
	tokenWith("{\"device-id\":\"\xff\"}"),
	"h." + base64.StdEncoding.EncodeToString([]byte(`{"exp":3}`)) + ".s",
	".a.b.c",
	"a..c",
	"a.b.c.d",
	"a.@@.c",
	"",
}

func checkClaimsAgainstReference(t *testing.T, token string) {
	t.Helper()
	if got, want := Claims(token), claimsReference(token); !reflect.DeepEqual(got, want) {
		t.Fatalf("Claims(%q) = %v, want %v", token, got, want)
	}
	if got, want := PlanFromToken(token), planReference(token); !reflect.DeepEqual(got, want) {
		t.Fatalf("PlanFromToken(%q) = %+v, want %+v", token, got, want)
	}
	if got, want := DeviceID(token), deviceIDReference(token); !reflect.DeepEqual(got, want) {
		t.Fatalf("DeviceID(%q) = %v, want %v", token, got, want)
	}
	var want *time.Time
	if claims := claimsReference(token); claims != nil {
		want = parseExp(claims["exp"])
	}
	if got := Expiry(token); !reflect.DeepEqual(got, want) {
		t.Fatalf("Expiry(%q) = %v, want %v", token, got, want)
	}
}

func TestClaimsMatchReference(t *testing.T) {
	t.Parallel()
	for _, token := range claimSeeds {
		checkClaimsAgainstReference(t, token)
	}
}

func FuzzClaimsMatchReference(f *testing.F) {
	for _, token := range claimSeeds {
		f.Add(token)
	}
	f.Fuzz(func(t *testing.T, token string) {
		checkClaimsAgainstReference(t, token)
	})
}
