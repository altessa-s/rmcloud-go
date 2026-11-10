// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

// Package auth buys and keeps the device token and the user token.
//
// Registration, revocation, and the user-token exchange live here. The user
// token is not part of the account surface a host is given: the host asks
// for a plan, and the token stays inside this process. One exchange is in
// flight at a time. A caller that leaves does not cancel it, because the
// others are still waiting on that same answer.
//
// A user token is judged by its own exp, which is the server's time. Each
// bought token's iat shows how far the local clock stands from the
// server's, and every later judgement allows for that skew, so a machine
// whose clock is hours off keeps the token it bought instead of buying
// another on every request. A skew past five minutes is logged.
package auth
