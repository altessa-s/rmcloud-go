// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

// Package httpapi is the net/http adapter behind port.Transport, and the
// separate, unauthenticated transport that reads discovery.
//
// Redirects are followed, and the user token stays on the origin the
// request started from. The wire log masks credentials. Which host a call
// reaches is not decided here; package hosts does that.
//
// No request has a deadline of its own. A request that stopped moving is
// given up on: response headers that never come, an in-memory body that
// stalls, an HTTP/2 connection that leaves a ping unanswered. Each fails
// as a transport error the retry policy repeats, and the caller's context
// ending is returned as it is.
package httpapi
