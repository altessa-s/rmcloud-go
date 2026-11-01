// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package domain

import (
	"math"
	"strings"
	"time"
)

// Expiry is the token's exp claim, or nil when the token does not parse or
// has no numeric exp.
func Expiry(token string) *time.Time {
	claims := claimsOf[struct {
		Exp any `json:"exp"`
	}](token)
	if claims == nil {
		return nil
	}
	return parseExp(claims.Exp)
}

// parseExp accepts the numeric shapes a test or a decoder may hand in.
// JSON objects decoded into map[string]any spell numbers as float64.
// A fractional second is kept. A negative fraction is normalised so the
// nanosecond argument of time.Unix stays non-negative.
func parseExp(v any) *time.Time {
	var sec float64
	switch n := v.(type) {
	case float64:
		sec = n
	case float32:
		sec = float64(n)
	case int:
		sec = float64(n)
	case int64:
		sec = float64(n)
	default:
		return nil
	}
	// A float64 past the int64 range has no defined conversion, and NaN and
	// the infinities have none at all. Such an exp is no expiry.
	if math.IsNaN(sec) || sec >= math.MaxInt64 || sec <= math.MinInt64 {
		return nil
	}
	whole, frac := math.Modf(sec)
	if frac < 0 {
		whole--
		frac++
	}
	return new(time.Unix(int64(whole), int64(frac*float64(time.Second))).UTC())
}

// DeviceID is the device-id claim. Empty, missing, and non-string are nil.
func DeviceID(token string) *string {
	claims := claimsOf[struct {
		DeviceID any `json:"device-id"`
	}](token)
	if claims == nil {
		return nil
	}
	id, ok := claims.DeviceID.(string)
	if !ok || id == "" {
		return nil
	}
	return new(id)
}

// PlanFromToken reads level and the first sync: scope. A token that does
// not parse returns nil. A token that parses without a level returns a plan
// whose level is nil: that account has no Connect, and that is an answer.
// Scopes are split on a single space. A tab is not a separator.
func PlanFromToken(token string) *Plan {
	claims := claimsOf[struct {
		Level  any `json:"level"`
		Scopes any `json:"scopes"`
	}](token)
	if claims == nil {
		return nil
	}
	var level *string
	if raw, ok := claims.Level.(string); ok && raw != "" {
		level = new(raw)
	}
	var scope *string
	if raw, ok := claims.Scopes.(string); ok {
		for part := range strings.SplitSeq(raw, " ") {
			if part == "" || !strings.HasPrefix(part, "sync:") {
				continue
			}
			scope = new(part)
			break
		}
	}
	return &Plan{Level: level, SyncScope: scope}
}

// IsUsable reports whether a token already in hand is still worth handing
// out. An expiry is judged on its own: exactly [ExpiryMargin] left is not
// usable. Without an expiry, heldSince is the fallback, and a hold of
// exactly [UserTokenLifetime] is not usable.
func IsUsable(expiry, heldSince *time.Time, moment time.Time) bool {
	if expiry != nil {
		return expiry.Sub(moment) > ExpiryMargin
	}
	if heldSince == nil {
		return false
	}
	return moment.Sub(*heldSince) < UserTokenLifetime
}

// IsFresh reports whether token's own exp still clears [ExpiryMargin].
// A token with no parseable exp is not fresh.
func IsFresh(token string, moment time.Time) bool {
	expiry := Expiry(token)
	if expiry == nil {
		return false
	}
	return expiry.Sub(moment) > ExpiryMargin
}
