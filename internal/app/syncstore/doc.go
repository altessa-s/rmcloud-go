// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

// Package syncstore reads and writes the reMarkable sync store.
//
// The package is a client, not a host. A root read tries v4 and falls back
// to v3 only on 404. A write always uses v3. A 412 or 409 is the caller's
// conflict: this package does not merge documents.
package syncstore
