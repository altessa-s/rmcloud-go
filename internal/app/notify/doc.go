// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

// Package notify reads the account's push channel.
//
// The channel is receive-only. A frame means the account changed; the
// caller re-reads the root. Only SyncComplete says the documents changed.
// The socket is kept open with a ping, and a dropped socket is dialed
// again after a pause. A platform without a socket is reported once.
package notify
