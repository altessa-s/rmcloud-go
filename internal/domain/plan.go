// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package domain

// Plan is the slice of a user token this client reads. A nil Level is an
// account without Connect, which is a tier and not a missing service.
type Plan struct {
	Level     *string
	SyncScope *string
}

// IsConnect reports whether Level is exactly "connect".
func (p Plan) IsConnect() bool {
	return p.Level != nil && *p.Level == "connect"
}
