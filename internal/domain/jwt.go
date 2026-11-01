// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package domain

import (
	"encoding/base64"
	"encoding/json/v2"
	"strings"
)

// Claims is the JWT payload as a JSON object. The signature is not checked:
// the token arrived from the auth host over TLS, and nothing here grants
// access. A string that is not three dot-separated parts, or whose middle
// is not a JSON object, returns nil.
//
// Empty pieces around a dot are omitted. "a..c" is
// two parts and is not a JWT. ".a.b.c" is three parts and the payload is
// the middle one.
func Claims(token string) map[string]any {
	obj := claimsOf[map[string]any](token)
	if obj == nil || *obj == nil {
		return nil
	}
	return *obj
}

// claimsOf decodes the JWT payload into a T, reading only what T names. It
// returns nil exactly when [Claims] does for a payload that is not a JSON
// object; a T that is not a map can still see a null payload, which is nil
// too. Values T does not name are validated and not kept.
func claimsOf[T any](token string) *T {
	raw := jwtPayload(token)
	if raw == nil {
		return nil
	}
	var obj *T
	if err := json.Unmarshal(raw, &obj); err != nil {
		return nil
	}
	return obj
}

// jwtPayload decodes the middle of three non-empty dot-separated parts. Both
// base64 alphabets are accepted, mixed, with or without padding.
func jwtPayload(token string) []byte {
	payload, ok := middlePart(token)
	if !ok {
		return nil
	}
	const base64Quantum = 4
	pad := (base64Quantum - len(payload)%base64Quantum) % base64Quantum
	encoded := make([]byte, len(payload), len(payload)+pad)
	for i := range len(payload) {
		switch c := payload[i]; c {
		case '-':
			encoded[i] = '+'
		case '_':
			encoded[i] = '/'
		default:
			encoded[i] = c
		}
	}
	for range pad {
		encoded = append(encoded, '=')
	}
	raw := make([]byte, base64.StdEncoding.DecodedLen(len(encoded)))
	n, err := base64.StdEncoding.Decode(raw, encoded)
	if err != nil {
		return nil
	}
	return raw[:n]
}

// middlePart splits token on dots, dropping empty pieces, and returns the
// second of exactly three.
func middlePart(token string) (string, bool) {
	var parts [3]string
	n := 0
	for piece := range strings.SplitSeq(token, ".") {
		if piece == "" {
			continue
		}
		if n == len(parts) {
			return "", false
		}
		parts[n] = piece
		n++
	}
	return parts[1], n == len(parts)
}
