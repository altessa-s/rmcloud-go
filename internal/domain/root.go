// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package domain

// Root is the cloud's pointer: the hash of the root index and the generation
// that pointer was written under.
type Root struct {
	Hash       string
	Generation int64
}
