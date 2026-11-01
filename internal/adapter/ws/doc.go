// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

// Package ws dials the notification socket with github.com/coder/websocket.
//
// The URL the rest of the client stores is http or https. Dial
// rewrites the scheme and leaves the rest of the spelling alone.
// A refused upgrade is a protocol error, read from the status.
// Redirects are not followed, so the user token stays on the first request.
package ws
