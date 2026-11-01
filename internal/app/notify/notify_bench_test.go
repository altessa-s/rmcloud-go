// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package notify

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"
	"testing"

	"github.com/altessa-s/go-atlas-core/types/redacted"

	"github.com/altessa-s/rmcloud-go/internal/port"
	"github.com/altessa-s/rmcloud-go/internal/porttest"
)

// framesPerListen is how many frames one benchmarked listen call delivers.
const framesPerListen = 64

// Sinks keep the compiler from discarding the benchmarked calls.
var (
	sinkEvent Event
	sinkOK    bool
)

// benchSocket delivers its frames in order and then reports the end of the
// stream. Ping always succeeds; the fake clock never lets one happen.
type benchSocket struct {
	frames []port.Frame
	next   int
}

func (s *benchSocket) Receive(ctx context.Context) (port.Frame, error) {
	if err := ctx.Err(); err != nil {
		return port.Frame{}, err
	}
	if s.next == len(s.frames) {
		return port.Frame{}, io.EOF
	}
	frame := s.frames[s.next]
	s.next++
	return frame, nil
}

func (*benchSocket) Ping(context.Context) error { return nil }

func (*benchSocket) Close() error { return nil }

func (*benchSocket) Abort() error { return nil }

var _ port.Socket = (*benchSocket)(nil)

type benchHosts struct{}

func (benchHosts) Notifications(context.Context) url.URL {
	return url.URL{Scheme: "wss", Host: "bench.invalid"}
}

type benchTokens struct{}

func (benchTokens) UserToken(context.Context) (redacted.RedactedString, error) { return "user-1", nil }

func (benchTokens) InvalidateUserToken(redacted.RedactedString) {}

type benchDialer struct{}

func (benchDialer) Dial(context.Context, url.URL, port.Header) (port.Socket, error) {
	return nil, errors.New("notify: the benchmark does not dial")
}

func BenchmarkParseEvent(b *testing.B) {
	own := "device-2"
	large := `{"message":{"attributes":{"auth0UserID":"auth0|x","event":"SyncComplete","sourceDeviceID":"device-1",` +
		`"sourceDeviceDesc":"desktop-macos","id":"11111111-1111-4111-8111-111111111111","vissibleName":"Notebook",` +
		`"parent":"","bookmarked":false,"version":17,"type":"DocumentType"},"messageId":"1"},"subscription":"s"}`
	for _, tc := range []struct {
		name  string
		frame port.Frame
	}{
		{"text/sample", textFrame(sampleEnvelope)},
		{"text/large", textFrame(large)},
		{"unreadable", textFrame("not json")},
	} {
		b.Run(tc.name, func(b *testing.B) {
			b.ReportAllocs()
			b.SetBytes(int64(len(tc.frame.Text) + len(tc.frame.Binary)))
			for b.Loop() {
				sinkEvent, sinkOK = parse(tc.frame, &own)
			}
		})
	}
}

func BenchmarkListen(b *testing.B) {
	for _, tc := range []struct {
		name  string
		frame port.Frame
	}{
		{"events", textFrame(sampleEnvelope)},
		{"unreadable", textFrame("not json")},
	} {
		b.Run(fmt.Sprintf("%s/frames=%d", tc.name, framesPerListen), func(b *testing.B) {
			frames := make([]port.Frame, framesPerListen)
			for i := range frames {
				frames[i] = tc.frame
			}
			var signals int
			s := New(benchHosts{}, benchTokens{}, benchDialer{}, porttest.NewClock(), func(Signal) { signals++ })
			own := "device-2"
			ctx := b.Context()
			sock := &benchSocket{frames: frames}
			b.ReportAllocs()
			for b.Loop() {
				sock.next = 0
				if err := s.listen(ctx, sock, &own); !errors.Is(err, io.EOF) {
					b.Fatal(err)
				}
			}
			if signals == 0 {
				b.Fatal("no signal was delivered")
			}
		})
	}
}

func BenchmarkReconnectDelay(b *testing.B) {
	b.ReportAllocs()
	var failures int
	for b.Loop() {
		failures++
		sinkOK = reconnectDelay(failures%16) == 0
	}
}
