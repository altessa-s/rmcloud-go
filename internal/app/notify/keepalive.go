// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package notify

import (
	"context"

	"github.com/altessa-s/rmcloud-go/internal/port"
)

// keepAlive pings every ping interval. Each ping is bounded by the ping
// timeout; a ping that fails or times out aborts the socket, which ends the
// Receive that listen is blocked in. A peer that answers no ping answers no
// close either, so the socket is not closed gracefully.
func (s *Stream) keepAlive(ctx context.Context, sock port.Socket) {
	for {
		if err := s.clock.Sleep(ctx, s.pingInterval); err != nil || ctx.Err() != nil {
			return
		}
		pctx, cancel := context.WithTimeout(ctx, s.pingTimeout)
		err := sock.Ping(pctx)
		cancel()
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			s.logger().Debug("notification socket did not answer a ping", "err", err)
			_ = sock.Abort()
			return
		}
	}
}
