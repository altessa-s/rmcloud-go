// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

// Package httpapi is the net/http adapter behind port.Transport, and the
// separate, unauthenticated transport that reads discovery.
//
// Redirects are followed, and the user token stays on the origin the
// request started from. The wire log masks credentials. Which host a call
// reaches is not decided here; package hosts does that.
package httpapi
