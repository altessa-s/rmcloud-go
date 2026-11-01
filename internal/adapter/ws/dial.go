// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package ws

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"sync/atomic"

	"github.com/coder/websocket"

	"github.com/altessa-s/rmcloud-go/internal/adapter/neterr"
	"github.com/altessa-s/rmcloud-go/internal/domain"
	"github.com/altessa-s/rmcloud-go/internal/port"
)

// readLimit is the largest notification frame accepted. A pub/sub wrapper
// is a few hundred bytes.
const readLimit = 1 << 20

// Dialer opens a notification socket with github.com/coder/websocket.
// Transport carries the upgrade request; nil uses http.DefaultTransport.
type Dialer struct {
	Transport http.RoundTripper
}

// Dial opens the notification socket.
//
// Redirects are not followed, so the user token stays on the first request.
// The scheme is rewritten to ws or wss and the host spelling is left alone.
// The response body belongs to the library; Dial does not close it.
func (d Dialer) Dial(ctx context.Context, endpoint url.URL, header port.Header) (port.Socket, error) {
	copied := make(http.Header, len(header)+1)
	hasUA := false
	for name, values := range header {
		if strings.EqualFold(name, "User-Agent") {
			hasUA = true
		}
		copied[name] = append([]string(nil), values...)
	}
	if !hasUA {
		copied["User-Agent"] = []string{""}
	}
	client := &http.Client{
		Transport: d.Transport,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	wire := wireURL(endpoint)
	raw := wire.String()
	// The library owns the response body on success and failure alike.
	conn, resp, err := websocket.Dial(ctx, raw, &websocket.DialOptions{ //nolint:bodyclose // see above
		HTTPClient: client,
		HTTPHeader: copied,
	})
	if err != nil {
		if resp != nil && resp.StatusCode != http.StatusSwitchingProtocols {
			loc := endpoint.String()
			if resp.Request != nil && resp.Request.URL != nil {
				u := resp.Request.URL
				loc = u.String()
			}
			if mapped := domain.FromHTTP(resp.StatusCode, loc, resp.Header.Get("Retry-After"), nil, nil); mapped != nil {
				return nil, mapped
			}
		}
		// No answer at all: the host is named the way the HTTP transport
		// names it, so a dropped socket says why.
		return nil, neterr.Classify(ctx, err)
	}
	conn.SetReadLimit(readLimit)
	return &socket{conn: conn}, nil
}

func wireURL(u url.URL) url.URL {
	switch strings.ToLower(u.Scheme) {
	case "https":
		u.Scheme = "wss"
	case "http":
		u.Scheme = "ws"
	}
	return u
}

type socket struct {
	conn   *websocket.Conn
	closed atomic.Bool
}

func (s *socket) Receive(ctx context.Context) (port.Frame, error) {
	typ, data, err := s.conn.Read(ctx)
	if err != nil {
		return port.Frame{}, err
	}
	if typ == websocket.MessageText {
		return port.Frame{Textual: true, Text: string(data)}, nil
	}
	// Read returns a fresh slice for every message; it is not reused.
	return port.Frame{Binary: data}, nil
}

func (s *socket) Ping(ctx context.Context) error { return s.conn.Ping(ctx) }

func (s *socket) Close() error {
	if !s.closed.CompareAndSwap(false, true) {
		return nil
	}
	return s.conn.Close(websocket.StatusGoingAway, "")
}

// Abort drops the connection without the close handshake, which Close waits
// up to five seconds for and a dead peer never completes.
func (s *socket) Abort() error {
	if !s.closed.CompareAndSwap(false, true) {
		return nil
	}
	return s.conn.CloseNow()
}

var _ port.Dialer = Dialer{}
