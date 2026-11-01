// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package porttest

import (
	"context"
	"net/url"

	"github.com/altessa-s/rmcloud-go/internal/domain"
	"github.com/altessa-s/rmcloud-go/internal/port"
)

// MissingDialer is a host whose notification socket is not there.
//
// Go can open a socket on every GOOS, so only this double names the absence;
// the retry policy leaves it alone.
type MissingDialer struct{}

// Dial returns unsupportedPlatform and no socket.
func (MissingDialer) Dial(context.Context, url.URL, port.Header) (port.Socket, error) {
	return nil, domain.UnsupportedPlatform("notification socket")
}

var _ port.Dialer = MissingDialer{}
