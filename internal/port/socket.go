// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package port

import (
	"context"
	"net/url"
)

// Frame is one message off the notification socket.
//
// Textual selects the payload. An empty text frame and an empty binary frame
// are different messages, so the flag is the discriminator rather than which
// field is non-empty.
type Frame struct {
	Textual bool
	Text    string
	Binary  []byte
}

// Socket is a notification channel that has finished its handshake.
//
// The channel is receive-only apart from Ping. Close says goodbye to a peer
// that is still there and may wait for its answer; Abort drops the channel at
// once, for a peer that stopped answering. Either may be called more than
// once, after the other too, and a repeated call is not an error.
type Socket interface {
	Receive(ctx context.Context) (Frame, error)
	Ping(ctx context.Context) error
	Close() error
	Abort() error
}

// Dialer opens a notification socket.
//
// A host that has no socket returns domain.UnsupportedPlatform. That
// error is about the platform, and the retry policy does not repeat it.
type Dialer interface {
	Dial(ctx context.Context, endpoint url.URL, header Header) (Socket, error)
}
