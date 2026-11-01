// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

// Package discovery decides which hosts one call reaches.
//
// The auth host is always the configured one. The sync host follows the
// region named in the user token. The notification socket follows the
// discovery document when that document names a trusted host for the sync
// host in use. Discovery is cached for an hour and read through a port, so
// this package makes no request of its own.
package discovery
