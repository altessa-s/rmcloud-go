// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package notify

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"slices"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/altessa-s/go-atlas-core/time/clock"
	"github.com/altessa-s/go-atlas-core/types/redacted"

	"github.com/altessa-s/rmcloud-go/internal/domain"
	"github.com/altessa-s/rmcloud-go/internal/port"
	"github.com/altessa-s/rmcloud-go/internal/porttest"
)

const testUA = "desktop/3.28.0 (macos 1)"

func testJWT(payload string) string {
	return "h." + base64.RawURLEncoding.EncodeToString([]byte(payload)) + ".s"
}

func mustURL(t *testing.T, raw string) url.URL {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	return *u
}

func exampleEndpoint(t *testing.T) url.URL {
	t.Helper()
	return mustURL(t, "https://Example.COM/notifications/ws/json/1")
}

type fixedHosts struct{ endpoint url.URL }

func (h fixedHosts) Notifications(context.Context) url.URL { return h.endpoint }

// settle waits until every goroutine in the test's bubble is blocked, so a
// signal that was going to arrive has arrived.
func settle() { synctest.Wait() }

// poll checks cond each time the bubble goes idle, moving its fake clock a
// millisecond between checks. Every caller runs inside synctest.Test, so
// the deadline is fake time and the loop never waits on the wall clock.
func poll(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for synctest.Wait(); !cond(); synctest.Sleep(time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("timed out")
		}
	}
}

func pollCalls(t *testing.T, dial *scriptDial, n int) {
	t.Helper()
	poll(t, func() bool { return dial.calls() >= n })
}

func waitCh(t *testing.T, ch <-chan struct{}, what string) {
	t.Helper()
	timer := time.NewTimer(3 * time.Second)
	defer timer.Stop()
	select {
	case <-ch:
	case <-timer.C:
		t.Fatalf("timeout waiting for %s", what)
	}
}

func stopWithin(t *testing.T, s *Stream) {
	t.Helper()
	done := make(chan struct{})
	go func() {
		s.Stop()
		close(done)
	}()
	timer := time.NewTimer(3 * time.Second)
	defer timer.Stop()
	select {
	case <-done:
	case <-timer.C:
		t.Fatal("Stop timed out")
	}
}

type tape struct {
	mu   sync.Mutex
	sigs []Signal
}

func (tp *tape) add(sig Signal) {
	tp.mu.Lock()
	tp.sigs = append(tp.sigs, sig)
	tp.mu.Unlock()
}

func (tp *tape) snapshot() []Signal {
	tp.mu.Lock()
	defer tp.mu.Unlock()
	out := make([]Signal, len(tp.sigs))
	copy(out, tp.sigs)
	return out
}

func (tp *tape) wait(t *testing.T, n int) []Signal {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for synctest.Wait(); ; synctest.Sleep(time.Millisecond) {
		got := tp.snapshot()
		if len(got) >= n {
			return got
		}
		if time.Now().After(deadline) {
			t.Fatalf("got %d signals, want %d: %s", len(got), n, formatSignals(got))
		}
	}
}

func kindName(k SignalKind) string {
	switch k {
	case SignalConnected:
		return "connected"
	case SignalEvent:
		return "event"
	case SignalUnreadable:
		return "unreadable"
	case SignalDropped:
		return "dropped"
	default:
		return fmt.Sprintf("kind(%d)", int(k))
	}
}

func formatSignals(sigs []Signal) string {
	var b strings.Builder
	for i, sig := range sigs {
		if i > 0 {
			b.WriteString("; ")
		}
		b.WriteString(kindName(sig.Kind))
		if sig.Err != nil {
			fmt.Fprintf(&b, " err=%v", sig.Err)
		}
		if sig.Kind == SignalEvent {
			fmt.Fprintf(&b, " name=%q", sig.Event.Name)
		}
	}
	return b.String()
}

func requireKinds(t *testing.T, sigs []Signal, kinds ...SignalKind) {
	t.Helper()
	if len(sigs) != len(kinds) {
		t.Fatalf("got %d signals, want %d: %s", len(sigs), len(kinds), formatSignals(sigs))
	}
	for i, kind := range kinds {
		if sigs[i].Kind != kind {
			t.Fatalf("signal %d is %s, want %s: %s", i, kindName(sigs[i].Kind), kindName(kind), formatSignals(sigs))
		}
	}
}

func requireURL(t *testing.T, got, want url.URL) {
	t.Helper()
	g := got.String()
	w := want.String()
	if g != w {
		t.Fatalf("url %s, want %s", g, w)
	}
}

func requireAuth(t *testing.T, h port.Header, token, ua string) {
	t.Helper()
	got, ok := h[domain.HeaderAuthorization]
	if !ok || len(got) != 1 || got[0] != "Bearer "+token {
		t.Fatalf("authorization %v", h[domain.HeaderAuthorization])
	}
	if ua == "" {
		if _, present := h[domain.HeaderUserAgent]; present {
			t.Fatalf("user-agent %v", h[domain.HeaderUserAgent])
		}
		return
	}
	got, ok = h[domain.HeaderUserAgent]
	if !ok || len(got) != 1 || got[0] != ua {
		t.Fatalf("user-agent %v", h[domain.HeaderUserAgent])
	}
}

type memTokens struct {
	mu      sync.Mutex
	seq     []string
	i       int
	refused []string
}

func (m *memTokens) UserToken(context.Context) (redacted.RedactedString, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.seq) == 0 {
		return "", nil
	}
	if m.i >= len(m.seq) {
		return redacted.RedactedString(m.seq[len(m.seq)-1]), nil
	}
	tok := m.seq[m.i]
	m.i++
	return redacted.RedactedString(tok), nil
}

func (m *memTokens) InvalidateUserToken(refused redacted.RedactedString) {
	m.mu.Lock()
	m.refused = append(m.refused, refused.Expose())
	m.mu.Unlock()
}

func (m *memTokens) refusedTokens() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]string, len(m.refused))
	copy(out, m.refused)
	return out
}

type dialRec struct {
	url    url.URL
	header port.Header
	sock   *recSock
	err    error
}

type dialSnap struct {
	urls    []url.URL
	headers []port.Header
	socks   []*recSock
	errs    []error
}

type scriptDial struct {
	mu   sync.Mutex
	next int
	fn   func(ctx context.Context, call int) (port.Socket, error)
	recs []dialRec
}

func newScriptDial(fn func(ctx context.Context, call int) (port.Socket, error)) *scriptDial {
	return &scriptDial{next: 1, fn: fn}
}

func (d *scriptDial) Dial(ctx context.Context, endpoint url.URL, header port.Header) (port.Socket, error) {
	d.mu.Lock()
	call := d.next
	d.next++
	fn := d.fn
	d.mu.Unlock()

	sock, err := fn(ctx, call)

	copied := copyHeader(header)
	var rec *recSock
	if s, ok := sock.(*recSock); ok {
		rec = s
	}
	d.mu.Lock()
	d.recs = append(d.recs, dialRec{url: endpoint, header: copied, sock: rec, err: err})
	d.mu.Unlock()
	if err != nil {
		return nil, err
	}
	return sock, nil
}

func (d *scriptDial) calls() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return len(d.recs)
}

func (d *scriptDial) snapshot() dialSnap {
	d.mu.Lock()
	defer d.mu.Unlock()
	snap := dialSnap{
		urls:    make([]url.URL, len(d.recs)),
		headers: make([]port.Header, len(d.recs)),
		socks:   make([]*recSock, len(d.recs)),
		errs:    make([]error, len(d.recs)),
	}
	for i, rec := range d.recs {
		snap.urls[i] = rec.url
		snap.headers[i] = copyHeader(rec.header)
		snap.socks[i] = rec.sock
		snap.errs[i] = rec.err
	}
	return snap
}

func copyHeader(header port.Header) port.Header {
	out := make(port.Header, len(header))
	for key, values := range header {
		out[key] = append([]string(nil), values...)
	}
	return out
}

// errSocketClosed is what a closed test socket returns once its frames are gone.
var errSocketClosed = errors.New("socket closed")

type recSock struct {
	mu           sync.Mutex
	frames       []port.Frame
	i            int
	closed       bool
	aborted      bool
	pings        int
	immediate    error
	pingErrAfter int
	pingHangs    bool
	wake         chan struct{}
	once         sync.Once
}

func newSock(frames ...port.Frame) *recSock {
	copied := make([]port.Frame, len(frames))
	for i, frame := range frames {
		copied[i] = frame
		if frame.Binary != nil {
			copied[i].Binary = append([]byte(nil), frame.Binary...)
		}
	}
	return &recSock{frames: copied, wake: make(chan struct{})}
}

func (s *recSock) Receive(ctx context.Context) (port.Frame, error) {
	s.mu.Lock()
	// The immediate error wins over Close so a scripted failure stays visible.
	if s.immediate != nil {
		err := s.immediate
		s.mu.Unlock()
		return port.Frame{}, err
	}
	if s.i < len(s.frames) {
		frame := s.frames[s.i]
		s.i++
		s.mu.Unlock()
		return frame, nil
	}
	if s.closed {
		s.mu.Unlock()
		return port.Frame{}, errSocketClosed
	}
	s.mu.Unlock()
	select {
	case <-ctx.Done():
		return port.Frame{}, ctx.Err()
	case <-s.wake:
		return port.Frame{}, errSocketClosed
	}
}

func (s *recSock) Ping(ctx context.Context) error {
	s.mu.Lock()
	if s.pingHangs {
		// A peer that died without a close: the pong never comes.
		s.pings++
		s.mu.Unlock()
		<-ctx.Done()
		return ctx.Err()
	}
	defer s.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	s.pings++
	if s.pingErrAfter > 0 && s.pings >= s.pingErrAfter {
		return errors.New("no pong")
	}
	return nil
}

func (s *recSock) Close() error {
	s.mu.Lock()
	s.closed = true
	s.mu.Unlock()
	s.once.Do(func() { close(s.wake) })
	return nil
}

func (s *recSock) Abort() error {
	s.mu.Lock()
	s.aborted = true
	s.mu.Unlock()
	return s.Close()
}

func (s *recSock) isAborted() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.aborted
}

func (s *recSock) pingCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.pings
}

func (s *recSock) isClosed() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.closed
}

// pauses is what c recorded other than the keepalive's sleeps of ping: the
// pauses between reconnects.
func pauses(c *porttest.Clock, ping time.Duration) []time.Duration {
	return slices.DeleteFunc(c.Delays(), func(d time.Duration) bool { return d == ping })
}

// advanceUntilPings moves the bubble's clock one second at a time until sock has
// answered n pings. The count is read before each step, and a count that is
// already high enough does not move the clock again.
func advanceUntilPings(t *testing.T, sock *recSock, n int) {
	t.Helper()
	for range 100 {
		synctest.Wait()
		if sock.pingCount() >= n {
			return
		}
		time.Sleep(time.Second)
	}
	t.Fatalf("ping count %d, want %d", sock.pingCount(), n)
}

type lockedBuf struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (l *lockedBuf) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}

func (l *lockedBuf) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.String()
}

func requireLog(t *testing.T, buf *lockedBuf, parts ...string) {
	t.Helper()
	text := buf.String()
	for _, part := range parts {
		if !strings.Contains(text, part) {
			t.Errorf("log missing %q\n%s", part, text)
		}
	}
}

func TestStreamReconnectUnauthorized(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		endpoint := exampleEndpoint(t)
		sleeper := porttest.Recording(clock.Real())
		jwt := testJWT(`{"device-id":"device-1"}`)
		tok := &memTokens{seq: []string{"old-token", jwt}}
		var tp tape
		dial := newScriptDial(func(_ context.Context, call int) (port.Socket, error) {
			if call == 1 {
				return nil, domain.ErrUnauthorized
			}
			return newSock(textFrame(sampleEnvelope)), nil
		})
		s := New(fixedHosts{endpoint: endpoint}, tok, dial, sleeper, tp.add,
			WithUserAgent(testUA),
			WithPingInterval(time.Hour),
		)
		s.Start(t.Context())
		s.Start(t.Context())
		t.Cleanup(s.Stop)

		tp.wait(t, 3)
		settle()
		got := tp.snapshot()
		requireKinds(t, got, SignalDropped, SignalConnected, SignalEvent)
		if !errors.Is(got[0].Err, domain.ErrUnauthorized) {
			t.Fatalf("dropped %v", got[0].Err)
		}
		ev := got[2].Event
		if !ev.FromThisDevice {
			t.Fatal("FromThisDevice")
		}
		if !ev.IsSyncComplete() {
			t.Fatal("IsSyncComplete")
		}
		if ev.Device != "device-1" {
			t.Fatalf("device %q", ev.Device)
		}
		if !strings.Contains(ev.Frame.Text, "subscription") {
			t.Fatalf("frame %q", ev.Frame.Text)
		}
		delays := pauses(sleeper, time.Hour)
		if len(delays) != 1 || delays[0] != 2*time.Second {
			t.Fatalf("delays %v", delays)
		}
		refused := tok.refusedTokens()
		if len(refused) != 1 || refused[0] != "old-token" {
			t.Fatalf("refused %v", refused)
		}

		snap := dial.snapshot()
		if len(snap.urls) != 2 {
			t.Fatalf("calls %d", len(snap.urls))
		}
		requireURL(t, snap.urls[0], endpoint)
		requireURL(t, snap.urls[1], endpoint)
		requireAuth(t, snap.headers[0], "old-token", testUA)
		requireAuth(t, snap.headers[1], jwt, testUA)
		if snap.socks[0] != nil {
			t.Fatal("first socket")
		}
		if snap.socks[1] == nil || snap.socks[1].isClosed() {
			t.Fatal("second socket")
		}

		s.Start(t.Context())
		settle()
		if dial.calls() != 2 {
			t.Fatalf("calls %d", dial.calls())
		}
		s.Stop()
		settle()
		if len(tp.snapshot()) != 3 {
			t.Fatalf("signals %s", formatSignals(tp.snapshot()))
		}
		if dial.calls() != 2 {
			t.Fatalf("calls %d", dial.calls())
		}
		if !snap.socks[1].isClosed() {
			t.Fatal("second socket still open")
		}
	})
}

func TestStreamUnsupported(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		endpoint := exampleEndpoint(t)
		sleeper := porttest.Recording(clock.Real())
		tok := &memTokens{seq: []string{"tok"}}
		dial := newScriptDial(func(ctx context.Context, _ int) (port.Socket, error) {
			return porttest.MissingDialer{}.Dial(ctx, url.URL{}, nil)
		})
		s := New(fixedHosts{endpoint: endpoint}, tok, dial, sleeper, nil,
			WithUserAgent(""),
			WithPingInterval(time.Hour),
		)
		s.Start(t.Context())
		t.Cleanup(s.Stop)

		pollCalls(t, dial, 1)
		settle()
		if dial.calls() != 1 {
			t.Fatalf("calls %d", dial.calls())
		}
		if delays := sleeper.Delays(); len(delays) != 0 {
			t.Fatalf("delays %v", delays)
		}
		snap := dial.snapshot()
		if !domain.IsKind(snap.errs[0], domain.KindUnsupportedPlatform) {
			t.Fatalf("dial %v", snap.errs[0])
		}
		requireURL(t, snap.urls[0], endpoint)
		requireAuth(t, snap.headers[0], "tok", "")

		s.Start(t.Context())
		settle()
		if dial.calls() != 1 {
			t.Fatalf("calls %d", dial.calls())
		}
		s.Stop()
		if refused := tok.refusedTokens(); len(refused) != 0 {
			t.Fatalf("refused %v", refused)
		}
	})
}

func TestStreamPingReconnect(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		endpoint := exampleEndpoint(t)
		clk := porttest.Recording(clock.Real())
		var tp tape
		dial := newScriptDial(func(context.Context, int) (port.Socket, error) {
			return nil, errors.New("replaced below")
		})
		// The closure above is replaced below; keep the constructor call shaped
		// so the first socket is not published outside the dial snapshot.
		dial.fn = func(_ context.Context, call int) (port.Socket, error) {
			if call == 1 {
				sock := newSock()
				sock.pingErrAfter = 2
				return sock, nil
			}
			return newSock(textFrame(sampleEnvelope)), nil
		}
		s := New(fixedHosts{endpoint: endpoint}, &memTokens{seq: []string{"tok"}}, dial, clk, tp.add,
			WithUserAgent(testUA),
			WithPingInterval(time.Second),
		)
		s.Start(t.Context())
		t.Cleanup(s.Stop)

		pollCalls(t, dial, 1)
		first := dial.snapshot().socks[0]
		if first == nil {
			t.Fatal("first socket")
		}
		advanceUntilPings(t, first, 2)

		tp.wait(t, 4)
		settle()
		got := tp.snapshot()
		requireKinds(t, got, SignalConnected, SignalDropped, SignalConnected, SignalEvent)
		if got[1].Err == nil || got[1].Err.Error() != "socket closed" || strings.Contains(got[1].Err.Error(), "no pong") {
			t.Fatalf("dropped %v", got[1].Err)
		}
		if !first.isClosed() {
			t.Fatal("first socket open")
		}
		if !got[3].Event.IsSyncComplete() {
			t.Fatal("IsSyncComplete")
		}
		snap := dial.snapshot()
		if len(snap.socks) != 2 || snap.socks[1] == nil || snap.socks[1].isClosed() || snap.socks[1].pingCount() != 0 {
			t.Fatal("second socket")
		}
		s.Stop()
		settle()
		if len(tp.snapshot()) != 4 {
			t.Fatalf("signals %s", formatSignals(tp.snapshot()))
		}
		if dial.calls() != 2 {
			t.Fatalf("calls %d", dial.calls())
		}
		if !snap.socks[1].isClosed() {
			t.Fatal("second socket still open")
		}
	})
}

func TestStreamPingTimeoutReconnects(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		clk := porttest.Recording(clock.Real())
		var tp tape
		dial := newScriptDial(func(_ context.Context, call int) (port.Socket, error) {
			sock := newSock()
			sock.pingHangs = call == 1
			return sock, nil
		})
		s := New(fixedHosts{endpoint: exampleEndpoint(t)}, &memTokens{seq: []string{"tok"}}, dial, clk, tp.add,
			WithPingInterval(time.Second),
			WithPingTimeout(20*time.Millisecond),
		)
		s.Start(t.Context())
		t.Cleanup(s.Stop)

		pollCalls(t, dial, 1)
		first := dial.snapshot().socks[0]
		advanceUntilPings(t, first, 1)
		pollCalls(t, dial, 2)
		if !first.isAborted() {
			t.Fatal("the silent socket was not aborted")
		}
		tp.wait(t, 3)
		requireKinds(t, tp.snapshot()[:3], SignalConnected, SignalDropped, SignalConnected)
	})
}

func TestStreamStopBeforePing(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		clk := porttest.Recording(clock.Real())
		var tp tape
		dial := newScriptDial(func(context.Context, int) (port.Socket, error) {
			return newSock(), nil
		})
		s := New(fixedHosts{endpoint: exampleEndpoint(t)}, &memTokens{seq: []string{"tok"}}, dial, clk, tp.add,
			WithUserAgent(testUA),
			WithPingInterval(time.Second),
		)
		s.Start(t.Context())
		t.Cleanup(s.Stop)

		tp.wait(t, 1)
		sock := dial.snapshot().socks[0]
		if sock == nil || sock.pingCount() != 0 {
			t.Fatal("ping before stop")
		}
		stopWithin(t, s)
		if sock.pingCount() != 0 {
			t.Fatalf("pings %d", sock.pingCount())
		}
		time.Sleep(time.Second)
		settle()
		if sock.pingCount() != 0 {
			t.Fatalf("pings %d", sock.pingCount())
		}
		got := tp.snapshot()
		requireKinds(t, got, SignalConnected)
	})
}

func TestStreamStopDuringDial(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		started := make(chan struct{})
		release := make(chan struct{})
		var once sync.Once
		var tp tape
		dial := newScriptDial(func(ctx context.Context, call int) (port.Socket, error) {
			if call == 1 {
				close(started)
				select {
				case <-ctx.Done():
				case <-release:
				}
				return newSock(), nil
			}
			return newSock(), nil
		})
		s := New(fixedHosts{endpoint: exampleEndpoint(t)}, &memTokens{seq: []string{"tok"}}, dial, porttest.Recording(clock.Real()), tp.add,
			WithUserAgent(testUA),
			WithPingInterval(time.Hour),
		)
		t.Cleanup(func() {
			once.Do(func() { close(release) })
			s.Stop()
		})
		s.Start(t.Context())
		waitCh(t, started, "dial")
		stopWithin(t, s)

		snap := dial.snapshot()
		if len(snap.socks) != 1 || snap.socks[0] == nil || !snap.socks[0].isClosed() {
			t.Fatal("cancelled dial")
		}
		if len(tp.snapshot()) != 0 {
			t.Fatalf("signals %s", formatSignals(tp.snapshot()))
		}

		s.Start(t.Context())
		tp.wait(t, 1)
		settle()
		requireKinds(t, tp.snapshot(), SignalConnected)
		snap = dial.snapshot()
		if len(snap.socks) != 2 || snap.socks[1] == nil || snap.socks[1].isClosed() {
			t.Fatal("second socket")
		}
		s.Stop()
		settle()
		if !snap.socks[1].isClosed() {
			t.Fatal("second socket still open")
		}
	})
}

func TestStreamCancelDuringReconnect(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		started2 := make(chan struct{})
		release := make(chan struct{})
		var once sync.Once
		tok := &memTokens{seq: []string{"tok"}}
		var tp tape
		dial := newScriptDial(func(ctx context.Context, call int) (port.Socket, error) {
			if call == 1 {
				sock := newSock()
				sock.immediate = errors.New("boom")
				return sock, nil
			}
			close(started2)
			select {
			case <-ctx.Done():
			case <-release:
			}
			return newSock(), nil
		})
		s := New(fixedHosts{endpoint: exampleEndpoint(t)}, tok, dial, porttest.Recording(clock.Real()), tp.add,
			WithUserAgent(testUA),
			WithPingInterval(time.Hour),
		)
		t.Cleanup(func() {
			once.Do(func() { close(release) })
			s.Stop()
		})
		s.Start(t.Context())

		tp.wait(t, 2)
		settle()
		got := tp.snapshot()
		requireKinds(t, got, SignalConnected, SignalDropped)
		if got[1].Err == nil || got[1].Err.Error() != "boom" {
			t.Fatalf("dropped %v", got[1].Err)
		}
		waitCh(t, started2, "second dial")
		stopWithin(t, s)
		settle()
		if len(tp.snapshot()) != 2 {
			t.Fatalf("signals %s", formatSignals(tp.snapshot()))
		}
		if refused := tok.refusedTokens(); len(refused) != 0 {
			t.Fatalf("refused %v", refused)
		}
	})
}

func TestStreamLogs(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		var buf lockedBuf
		logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))

		clk := porttest.Recording(clock.Real())
		var tp tape
		dial := newScriptDial(func(_ context.Context, call int) (port.Socket, error) {
			if call == 1 {
				sock := newSock(
					textFrame("not json"),
					textFrame(`{"message":{"attributes":{"sourceDeviceID":"other"}}}`),
					textFrame(`{"message":{"attributes":{"event":"Nope"}}}`),
				)
				sock.pingErrAfter = 1
				return sock, nil
			}
			return newSock(), nil
		})
		s := New(fixedHosts{endpoint: exampleEndpoint(t)}, &memTokens{seq: []string{"tok"}}, dial, clk, tp.add,
			WithUserAgent(testUA),
			WithPingInterval(time.Second),
			WithLogger(logger),
		)
		t.Cleanup(s.Stop)
		s.Start(t.Context())

		tp.wait(t, 4)
		settle()
		got := tp.snapshot()
		requireKinds(t, got, SignalConnected, SignalUnreadable, SignalEvent, SignalEvent)
		if got[1].Frame.Text != "not json" {
			t.Fatalf("unparsed %q", got[1].Frame.Text)
		}
		if got[2].Event.Name != "" {
			t.Fatalf("unnamed %q", got[2].Event.Name)
		}
		if got[3].Event.Name != "Nope" {
			t.Fatalf("name %q", got[3].Event.Name)
		}

		first := dial.snapshot().socks[0]
		if first == nil {
			t.Fatal("first socket")
		}
		advanceUntilPings(t, first, 1)
		deadline := time.Now().Add(3 * time.Second)
		var dropped Signal
		for {
			found := false
			for _, sig := range tp.snapshot() {
				if sig.Kind == SignalDropped {
					dropped = sig
					found = true
					break
				}
			}
			if found {
				break
			}
			if time.Now().After(deadline) {
				t.Fatalf("no dropped signal: %s", formatSignals(tp.snapshot()))
			}
			synctest.Sleep(time.Millisecond)
		}
		if dropped.Err == nil || dropped.Err.Error() != "socket closed" || strings.Contains(dropped.Err.Error(), "no pong") {
			t.Fatalf("dropped %v", dropped.Err)
		}
		requireLog(t, &buf,
			"notification socket connected",
			`msg="notification event" name=unparsed`,
			`msg="notification event" name=unnamed`,
			`msg="notification event" name=Nope`,
			"notification socket did not answer a ping",
			"err=\"no pong\"",
			"notification socket dropped",
		)
		s.Stop()
	})
}

// testDeps is the four dependencies New takes, so a test can drop one.
type testDeps struct {
	Hosts  Hosts
	Tokens Tokens
	Dialer port.Dialer
	Clock  clock.Clock
}

func (d testDeps) build(onSignal func(Signal), opts ...Option) *Stream {
	return New(d.Hosts, d.Tokens, d.Dialer, d.Clock, onSignal, opts...)
}

// fullDeps is a complete dependency set made of doubles.
func fullDeps(t *testing.T) testDeps {
	t.Helper()
	return testDeps{
		Hosts:  fixedHosts{endpoint: exampleEndpoint(t)},
		Tokens: &memTokens{seq: []string{"tok"}},
		Dialer: newScriptDial(func(context.Context, int) (port.Socket, error) { return newSock(), nil }),
		Clock:  porttest.Recording(clock.Real()),
	}
}

func TestDefaults(t *testing.T) {
	t.Parallel()
	t.Run("ping interval", func(t *testing.T) {
		t.Parallel()
		for _, d := range []time.Duration{0, -time.Second} {
			s := fullDeps(t).build(nil, WithPingInterval(d), WithPingTimeout(d))
			if s.pingInterval != 30*time.Second || s.pingTimeout != 10*time.Second {
				t.Fatalf("%s: interval %s timeout %s", d, s.pingInterval, s.pingTimeout)
			}
		}
	})
	t.Run("every dependency is required", func(t *testing.T) {
		t.Parallel()
		for name, drop := range map[string]func(*testDeps){
			"hosts":  func(d *testDeps) { d.Hosts = nil },
			"tokens": func(d *testDeps) { d.Tokens = nil },
			"dialer": func(d *testDeps) { d.Dialer = nil },
			"clock":  func(d *testDeps) { d.Clock = nil },
		} {
			deps := fullDeps(t)
			drop(&deps)
			func() {
				defer func() {
					if r := recover(); r == nil || !strings.Contains(strings.ToLower(fmt.Sprint(r)), name) {
						t.Errorf("%s: panic = %v", name, r)
					}
				}()
				deps.build(nil)
			}()
		}
	})
	t.Run("empty user agent", func(t *testing.T) {
		t.Parallel()
		synctest.Test(t, testEmptyUserAgent)
	})
}

func testEmptyUserAgent(t *testing.T) {
	dial := newScriptDial(func(context.Context, int) (port.Socket, error) {
		return nil, domain.UnsupportedPlatform("notification socket")
	})
	deps := fullDeps(t)
	deps.Dialer = dial
	s := deps.build(nil, WithUserAgent(""), WithPingInterval(time.Hour))
	s.Start(t.Context())
	t.Cleanup(s.Stop)
	pollCalls(t, dial, 1)
	snap := dial.snapshot()
	requireURL(t, snap.urls[0], exampleEndpoint(t))
	requireAuth(t, snap.headers[0], "tok", "")
}

func TestReconnectDelay(t *testing.T) {
	t.Parallel()
	cases := []struct {
		n    int
		want time.Duration
	}{
		{0, time.Second},
		{-1, time.Second},
		{1, 2 * time.Second},
		{2, 4 * time.Second},
		{6, 60 * time.Second},
		{7, 60 * time.Second},
	}
	for _, tc := range cases {
		if got := reconnectDelay(tc.n); got != tc.want {
			t.Errorf("reconnectDelay(%d) = %s, want %s", tc.n, got, tc.want)
		}
	}
}

func TestStreamStopIsSafeBeforeStartAndTwice(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		dial := newScriptDial(func(ctx context.Context, _ int) (port.Socket, error) {
			return porttest.MissingDialer{}.Dial(ctx, url.URL{}, nil)
		})
		deps := testDeps{Hosts: fixedHosts{endpoint: exampleEndpoint(t)}, Tokens: &memTokens{seq: []string{"tok"}}, Dialer: dial, Clock: porttest.Recording(clock.Real())}
		stopWithin(t, deps.build(nil))

		s := deps.build(nil)
		s.Start(t.Context())
		settle()
		stopWithin(t, s)
		stopWithin(t, s)
	})
}
