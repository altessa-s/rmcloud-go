// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package auth

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"log/slog"
	"maps"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/altessa-s/go-atlas-core/types/redacted"

	"github.com/altessa-s/rmcloud-go/internal/app/retry"
	"github.com/altessa-s/rmcloud-go/internal/domain"
	"github.com/altessa-s/rmcloud-go/internal/domaintest"
	"github.com/altessa-s/rmcloud-go/internal/port"
	"github.com/altessa-s/rmcloud-go/internal/porttest"
)

const desktopUA = "desktop/3.28.0 (macos 14.5)"

const distantExp int64 = 4102444800

var (
	desktopUAPattern = regexp.MustCompile(`desktop/[0-9.]+ \(macos [0-9]+\.[0-9]+(\.[0-9]+)?\)`)
	deviceIDPattern  = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)
)

func TestReusesTheStoredUserToken(t *testing.T) {
	t.Parallel()
	alive := freshToken(t, "alive")
	h := newHarness(t, map[domain.SecretName]string{
		domain.SecretDeviceToken: "device",
		domain.SecretUserToken:   alive,
	})
	got, err := h.p.UserToken(t.Context())
	must(t, err)
	if got.Expose() != alive {
		t.Fatalf("token = %q", got)
	}
	if calls := h.trip.Calls(); len(calls) != 0 {
		t.Fatalf("requests = %d", len(calls))
	}
}

func TestReplacesAnExpiredToken(t *testing.T) {
	t.Parallel()
	replacement := freshToken(t, "new")
	h := newHarness(t, map[domain.SecretName]string{
		domain.SecretDeviceToken: "device",
		domain.SecretUserToken:   tokenAt(t, 1000, nil),
	})
	h.trip.Enqueue(porttest.Reply{Body: []byte(replacement)})
	got, err := h.p.UserToken(t.Context())
	must(t, err)
	if got.Expose() != replacement {
		t.Fatalf("token = %q", got)
	}
	stored, ok := mustSecret(t, h.secrets, domain.SecretUserToken)
	if !ok || stored != replacement {
		t.Fatalf("stored = %q ok=%v", stored, ok)
	}
}

func TestDoesNotKeepATokenWithoutAnExpiry(t *testing.T) {
	t.Parallel()
	h := newHarness(t, map[domain.SecretName]string{
		domain.SecretDeviceToken: "device",
	})
	h.trip.Enqueue(porttest.Reply{Body: []byte("not-a-jwt")})
	got, err := h.p.UserToken(t.Context())
	must(t, err)
	if got != "not-a-jwt" {
		t.Fatalf("token = %q", got)
	}
	if _, ok := mustSecret(t, h.secrets, domain.SecretUserToken); ok {
		t.Fatal("a token without exp was stored")
	}
}

func TestRevokingTellsTheServerAndKeepsTheToken(t *testing.T) {
	t.Parallel()
	h := newHarness(t, map[domain.SecretName]string{
		domain.SecretDeviceToken: "device",
	})
	h.trip.Enqueue(porttest.Reply{Status: 204})
	must(t, h.p.RevokeDevice(t.Context()))
	call := oneCall(t, h.trip)
	if call.Method != http.MethodPost || call.URL.Path != "/token/json/3/device/delete" {
		t.Fatalf("request = %s %s", call.Method, call.URL.Path)
	}
	if got := headerOne(call.Header, domain.HeaderAuthorization); got != "Bearer device" {
		t.Fatalf("authorization = %q", got)
	}
	if call.Body == nil || len(call.Body) != 0 {
		t.Fatalf("body = %q", call.Body)
	}
	stored, ok := mustSecret(t, h.secrets, domain.SecretDeviceToken)
	if !ok || stored != "device" {
		t.Fatalf("device token = %q ok=%v", stored, ok)
	}
}

func TestRevokingARefusedTokenIsNotAFailure(t *testing.T) {
	t.Parallel()
	for _, status := range []int{401, 403} {
		h := newHarness(t, map[domain.SecretName]string{
			domain.SecretDeviceToken: "device",
		})
		h.trip.Enqueue(porttest.Reply{Status: status})
		if err := h.p.RevokeDevice(t.Context()); err != nil {
			t.Fatalf("status %d: %v", status, err)
		}
	}
}

func TestRevokingAServerErrorIsRetried(t *testing.T) {
	t.Parallel()
	h := newHarness(t, map[domain.SecretName]string{
		domain.SecretDeviceToken: "device",
	})
	h.trip.Enqueue(porttest.Reply{Status: 503})
	err := h.p.RevokeDevice(t.Context())
	mustKind(t, err, domain.HTTP(500, ""))
	if len(h.trip.Calls()) != retry.Default().MaxAttempts {
		t.Fatalf("requests = %d", len(h.trip.Calls()))
	}
	if len(h.clock.Delays()) != retry.Default().MaxAttempts-1 {
		t.Fatalf("delays = %v", h.clock.Delays())
	}
}

func TestRevokingWithoutATokenMakesNoRequest(t *testing.T) {
	t.Parallel()
	h := newHarness(t, nil)
	must(t, h.p.RevokeDevice(t.Context()))
	if calls := h.trip.Calls(); len(calls) != 0 {
		t.Fatalf("requests = %d", len(calls))
	}
}

func TestForgettingClearsBoth(t *testing.T) {
	t.Parallel()
	alive := freshToken(t, "cached")
	h := newHarness(t, map[domain.SecretName]string{
		domain.SecretDeviceToken: "device",
		domain.SecretDeviceID:    "device-id",
		domain.SecretUserToken:   alive,
	})
	got, err := h.p.UserToken(t.Context())
	must(t, err)
	if got.Expose() != alive {
		t.Fatalf("token = %q", got)
	}
	registered, err := h.p.IsRegistered(t.Context())
	must(t, err)
	if !registered {
		t.Fatal("expected a registration")
	}
	must(t, h.p.ForgetDevice(t.Context()))
	for _, name := range []domain.SecretName{domain.SecretDeviceToken, domain.SecretDeviceID, domain.SecretUserToken} {
		if _, ok := mustSecret(t, h.secrets, name); ok {
			t.Fatalf("%s still stored", name)
		}
	}
	registered, err = h.p.IsRegistered(t.Context())
	must(t, err)
	if registered {
		t.Fatal("still registered")
	}
	_, err = h.p.UserToken(t.Context())
	mustKind(t, err, domain.ErrNotRegistered)
}

func TestLateUserTokenIsDropped(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		h := newHarness(t, map[domain.SecretName]string{
			domain.SecretDeviceToken: "device",
		})
		g, letGo := hold(t, h.trip)
		h.p.trip = g
		h.trip.Enqueue(porttest.Reply{Body: []byte(freshToken(t, "late"))})

		errc := make(chan error, 1)
		go func() {
			_, err := h.p.UserToken(context.Background())
			errc <- err
		}()
		until(t, "the buy to leave", func() bool { return len(h.trip.Calls()) == 1 })
		must(t, h.p.ForgetDevice(context.Background()))
		letGo()
		mustKind(t, <-errc, domain.ErrNotRegistered)
		if _, ok := mustSecret(t, h.secrets, domain.SecretUserToken); ok {
			t.Fatal("late user token was stored")
		}
	})
}

func TestLateDeviceTokenIsDropped(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		h := newHarness(t, nil)
		g, letGo := hold(t, h.trip)
		h.p.trip = g
		h.trip.Enqueue(porttest.Reply{Body: []byte("device-token-value")})

		errc := make(chan error, 1)
		go func() {
			errc <- h.p.RegisterDevice(context.Background(), "abcdefgh")
		}()
		until(t, "registration to leave", func() bool { return len(h.trip.Calls()) == 1 })
		must(t, h.p.ForgetDevice(context.Background()))
		letGo()
		mustKind(t, <-errc, domain.ErrNotRegistered)
		for _, name := range []domain.SecretName{domain.SecretDeviceToken, domain.SecretDeviceID} {
			if _, ok := mustSecret(t, h.secrets, name); ok {
				t.Fatalf("%s was stored after forget", name)
			}
		}
	})
}

func TestCrossProcessDisconnectDropsTheBoughtToken(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		planted := freshToken(t, "planted")
		replacement := freshToken(t, "replacement")
		h := newHarness(t, map[domain.SecretName]string{
			domain.SecretDeviceToken: "device",
		})
		h.p.InvalidateUserToken(redacted.RedactedString(planted))
		g, letGo := hold(t, h.trip)
		h.p.trip = g
		h.trip.Enqueue(
			porttest.Reply{Body: []byte(freshToken(t, "dropped"))},
			porttest.Reply{Body: []byte(replacement)},
		)

		errc := make(chan error, 1)
		go func() {
			_, err := h.p.UserToken(context.Background())
			errc <- err
		}()
		until(t, "the buy to leave", func() bool { return len(h.trip.Calls()) == 1 })
		must(t, h.secrets.Forget(context.Background(), domain.SecretDeviceToken))
		letGo()
		mustKind(t, <-errc, domain.ErrNotRegistered)
		if _, ok := mustSecret(t, h.secrets, domain.SecretUserToken); ok {
			t.Fatal("dropped user token was stored")
		}

		must(t, h.secrets.Save(context.Background(), domain.SecretDeviceToken, "device"))
		must(t, h.secrets.Save(context.Background(), domain.SecretUserToken, planted))
		got, err := h.p.UserToken(context.Background())
		must(t, err)
		if got.Expose() != replacement {
			t.Fatalf("token = %q", got)
		}
		if len(h.trip.Calls()) != 2 {
			t.Fatalf("requests = %d", len(h.trip.Calls()))
		}
	})
}

func TestReadsAnAccountWithoutATablet(t *testing.T) {
	t.Parallel()
	h := newHarness(t, map[domain.SecretName]string{
		domain.SecretDeviceToken: "device",
	})
	h.trip.Enqueue(porttest.Reply{Status: 400, Body: []byte(`{"message":"no tablet"}`)})
	_, err := h.p.UserToken(t.Context())
	mustKind(t, err, domain.ErrAccountWithoutTablet)
	if len(h.trip.Calls()) != 1 {
		t.Fatalf("requests = %d", len(h.trip.Calls()))
	}
	if len(h.clock.Delays()) != 0 {
		t.Fatalf("delays = %v", h.clock.Delays())
	}
}

func TestRetriesTheUserTokenExchange(t *testing.T) {
	t.Parallel()
	replacement := freshToken(t, "retried")
	h := newHarness(t, map[domain.SecretName]string{
		domain.SecretDeviceToken: "device",
	})
	h.trip.Enqueue(
		porttest.Reply{Status: 503},
		porttest.Reply{Body: []byte(replacement)},
	)
	got, err := h.p.UserToken(t.Context())
	must(t, err)
	if got.Expose() != replacement {
		t.Fatalf("token = %q", got)
	}
	if len(h.trip.Calls()) != 2 || len(h.clock.Delays()) != 1 {
		t.Fatalf("requests = %d delays = %v", len(h.trip.Calls()), h.clock.Delays())
	}
	stored, ok := mustSecret(t, h.secrets, domain.SecretUserToken)
	if !ok || stored != replacement {
		t.Fatalf("stored = %q ok=%v", stored, ok)
	}
	call := h.trip.Calls()[0]
	if call.URL.Path != "/token/json/2/user/new" {
		t.Fatalf("path = %s", call.URL.Path)
	}
	if _, ok := call.Header["Content-Type"]; ok {
		t.Fatal("user-token exchange sent Content-Type")
	}
	if got := headerOne(call.Header, domain.HeaderAuthorization); got != "Bearer device" {
		t.Fatalf("authorization = %q", got)
	}
}

func TestRegistrationStoresDeviceToken(t *testing.T) {
	t.Parallel()
	h := newHarness(t, map[domain.SecretName]string{
		domain.SecretUserToken: "old-user",
	}, WithID(func() (string, error) {
		return "11111111-1111-4111-8111-111111111111", nil
	}))
	h.trip.Enqueue(porttest.Reply{Body: []byte("device-token-value")})
	must(t, h.p.RegisterDevice(t.Context(), "abcdefgh"))
	call := oneCall(t, h.trip)
	if call.Method != http.MethodPost || call.URL.Path != "/token/json/2/device/new" {
		t.Fatalf("request = %s %s", call.Method, call.URL.Path)
	}
	stored, ok := mustSecret(t, h.secrets, domain.SecretDeviceToken)
	if !ok || stored != "device-token-value" {
		t.Fatalf("device token = %q ok=%v", stored, ok)
	}
	id, ok := mustSecret(t, h.secrets, domain.SecretDeviceID)
	if !ok || id != "11111111-1111-4111-8111-111111111111" {
		t.Fatalf("device id = %q ok=%v", id, ok)
	}
	if _, ok := mustSecret(t, h.secrets, domain.SecretUserToken); ok {
		t.Fatal("previous user token survived registration")
	}
}

func TestRegistrationSendsTheMacLabel(t *testing.T) {
	t.Parallel()
	h := newHarness(t, nil)
	h.trip.Enqueue(porttest.Reply{Body: []byte("device-token-value")})
	must(t, h.p.RegisterDevice(t.Context(), "abcdefgh"))
	body := decodeRegistration(t, oneCall(t, h.trip).Body)
	if body.DeviceDesc != domain.DeviceDescMac || body.Code != "abcdefgh" {
		t.Fatalf("body = %+v", body)
	}
	if !deviceIDPattern.MatchString(body.DeviceID) {
		t.Fatalf("deviceID = %q", body.DeviceID)
	}
	raw := string(oneCall(t, h.trip).Body)
	if code, desc, id := strings.Index(raw, `"code"`), strings.Index(raw, `"deviceDesc"`), strings.Index(raw, `"deviceID"`); code < 0 || code >= desc || desc >= id {
		t.Fatalf("field order = %s", raw)
	}
}

func TestRegistrationLowercasesTheCode(t *testing.T) {
	t.Parallel()
	h := newHarness(t, nil)
	h.trip.Enqueue(porttest.Reply{Body: []byte("device-token-value")})
	must(t, h.p.RegisterDevice(t.Context(), "  AbCdEfGh \n"))
	body := decodeRegistration(t, oneCall(t, h.trip).Body)
	if body.Code != "abcdefgh" {
		t.Fatalf("code = %q", body.Code)
	}
}

func TestRequestsCarryTheDesktopUserAgent(t *testing.T) {
	t.Parallel()
	if !desktopUAPattern.MatchString(desktopUA) {
		t.Fatalf("fixture %q does not match the expected User-Agent form", desktopUA)
	}
	h := newHarness(t, nil)
	h.trip.Enqueue(porttest.Reply{Body: []byte("device-token-value")})
	must(t, h.p.RegisterDevice(t.Context(), "abcdefgh"))
	if got := headerOne(oneCall(t, h.trip).Header, domain.HeaderUserAgent); got != desktopUA {
		t.Fatalf("user agent = %q", got)
	}
	if got := headerOne(oneCall(t, h.trip).Header, "Content-Type"); got != domain.ContentTypeJSON {
		t.Fatalf("content type = %q", got)
	}
}

func TestEmptyUserAgentSendsNoHeader(t *testing.T) {
	t.Parallel()
	h := newHarness(t, nil, WithUserAgent(""))
	h.trip.Enqueue(porttest.Reply{Body: []byte("device-token-value")})
	must(t, h.p.RegisterDevice(t.Context(), "abcdefgh"))
	if _, ok := oneCall(t, h.trip).Header[domain.HeaderUserAgent]; ok {
		t.Fatal("empty user agent was sent")
	}
}

func TestRejectsShortOneTimeCode(t *testing.T) {
	t.Parallel()
	h := newHarness(t, nil)
	err := h.p.RegisterDevice(t.Context(), "abc")
	mustKind(t, err, domain.ErrInvalidOneTimeCode)
	if calls := h.trip.Calls(); len(calls) != 0 {
		t.Fatalf("requests = %d", len(calls))
	}
}

func TestMapsUnauthorizedToBadCode(t *testing.T) {
	t.Parallel()
	for _, status := range []int{401, 403} {
		h := newHarness(t, nil)
		h.trip.Enqueue(porttest.Reply{Status: status})
		err := h.p.RegisterDevice(t.Context(), "abcdefgh")
		if !errors.Is(err, domain.ErrInvalidOneTimeCode) {
			t.Fatalf("status %d: %v", status, err)
		}
	}
}

func TestRegisterKeepsHTTP400(t *testing.T) {
	t.Parallel()
	h := newHarness(t, nil)
	h.trip.Enqueue(porttest.Reply{Status: 400, Body: []byte("no")})
	err := h.p.RegisterDevice(t.Context(), "abcdefgh")
	got, ok := errors.AsType[*domain.HTTPError](err)
	if !ok || got.Status != 400 {
		t.Fatalf("error = %v", err)
	}
	if len(h.trip.Calls()) != 1 || len(h.clock.Delays()) != 0 {
		t.Fatalf("requests = %d delays = %d", len(h.trip.Calls()), len(h.clock.Delays()))
	}
}

func TestRequiresRegistration(t *testing.T) {
	t.Parallel()
	h := newHarness(t, nil)
	_, err := h.p.UserToken(t.Context())
	mustKind(t, err, domain.ErrNotRegistered)
	if calls := h.trip.Calls(); len(calls) != 0 {
		t.Fatalf("requests = %d", len(calls))
	}
}

func TestRefreshesUserToken(t *testing.T) {
	t.Parallel()
	clock := porttest.NewClock()
	h := newHarnessAt(t, clock, map[domain.SecretName]string{
		domain.SecretDeviceToken: "device",
	})
	h.trip.Enqueue(
		porttest.Reply{Body: []byte("user-1")},
		porttest.Reply{Body: []byte("user-2")},
	)
	got, err := h.p.UserToken(t.Context())
	must(t, err)
	if got != "user-1" {
		t.Fatalf("first = %q", got)
	}
	got, err = h.p.UserToken(t.Context())
	must(t, err)
	if got != "user-1" || len(h.trip.Calls()) != 1 {
		t.Fatalf("second = %q requests = %d", got, len(h.trip.Calls()))
	}
	clock.Advance(21 * time.Hour)
	got, err = h.p.UserToken(t.Context())
	must(t, err)
	if got != "user-2" || len(h.trip.Calls()) != 2 {
		t.Fatalf("third = %q requests = %d", got, len(h.trip.Calls()))
	}
	if _, ok := mustSecret(t, h.secrets, domain.SecretUserToken); ok {
		t.Fatal("a token without exp was stored")
	}
}

func TestMemoryCacheHonoursTheTokenExpiry(t *testing.T) {
	t.Parallel()
	clock := porttest.NewClock()
	stored := tokenAt(t, 600, nil)
	replacement := freshToken(t, "replacement")
	h := newHarnessAt(t, clock, map[domain.SecretName]string{
		domain.SecretDeviceToken: "device",
		domain.SecretUserToken:   stored,
	})
	got, err := h.p.UserToken(t.Context())
	must(t, err)
	if got.Expose() != stored || len(h.trip.Calls()) != 0 {
		t.Fatalf("first = %q requests = %d", got, len(h.trip.Calls()))
	}
	clock.Advance(500 * time.Second)
	h.trip.Enqueue(porttest.Reply{Body: []byte(replacement)})
	got, err = h.p.UserToken(t.Context())
	must(t, err)
	if got.Expose() != replacement || len(h.trip.Calls()) != 1 {
		t.Fatalf("second = %q requests = %d", got, len(h.trip.Calls()))
	}
}

func TestConcurrentCallersShareOneExchange(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		body := freshToken(t, "shared")
		h := newHarness(t, map[domain.SecretName]string{
			domain.SecretDeviceToken: "device",
		})
		g, letGo := hold(t, h.trip)
		h.p.trip = g
		h.trip.Enqueue(porttest.Reply{Body: []byte(body)})

		const callers = 8
		var wg sync.WaitGroup
		tokens := make([]redacted.RedactedString, callers)
		errs := make([]error, callers)
		for i := range callers {
			wg.Go(func() {
				tokens[i], errs[i] = h.p.UserToken(context.Background())
			})
		}
		until(t, "eight readers and one request", func() bool {
			return h.reads.count(domain.SecretUserToken) == callers && len(h.trip.Calls()) == 1
		})
		letGo()
		wg.Wait()
		for i := range callers {
			must(t, errs[i])
			if tokens[i].Expose() != body {
				t.Fatalf("caller %d token = %q", i, tokens[i])
			}
		}
		if len(h.trip.Calls()) != 1 {
			t.Fatalf("requests = %d", len(h.trip.Calls()))
		}
	})
}

func TestDepartingWaiterDoesNotBreakTheExchange(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		body := freshToken(t, "stays")
		h := newHarness(t, map[domain.SecretName]string{
			domain.SecretDeviceToken: "device",
		})
		g, letGo := hold(t, h.trip)
		h.p.trip = g
		h.trip.Enqueue(porttest.Reply{Body: []byte(body)})

		starterCtx, cancel := context.WithCancel(context.Background())
		starterErr := make(chan error, 1)
		go func() {
			_, err := h.p.UserToken(starterCtx)
			starterErr <- err
		}()
		until(t, "the buy to leave", func() bool { return len(h.trip.Calls()) == 1 })

		stayTok := make(chan redacted.RedactedString, 1)
		stayErr := make(chan error, 1)
		go func() {
			tok, err := h.p.UserToken(context.Background())
			stayTok <- tok
			stayErr <- err
		}()
		until(t, "the second reader", func() bool { return h.reads.count(domain.SecretUserToken) >= 2 })
		cancel()
		// The starter leaves at once, while the buy is still held.
		if err := <-starterErr; !errors.Is(err, context.Canceled) {
			t.Fatalf("starter = %v, want its own cancellation", err)
		}
		synctest.Wait()
		if g.wasCancelled.Load() {
			t.Fatal("the buy observed the starter's cancellation")
		}
		letGo()

		if err := <-stayErr; err != nil {
			t.Fatal(err)
		}
		if got := <-stayTok; got.Expose() != body {
			t.Fatalf("stayer = %q", got)
		}
		if len(h.trip.Calls()) != 1 {
			t.Fatalf("requests = %d", len(h.trip.Calls()))
		}
		got, err := h.p.UserToken(context.Background())
		if err != nil || got.Expose() != body || len(h.trip.Calls()) != 1 {
			t.Fatalf("after = %q %v requests = %d", got, err, len(h.trip.Calls()))
		}
	})
}

func TestWaiterLeavesWhenItsContextEnds(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		h := newHarness(t, map[domain.SecretName]string{
			domain.SecretDeviceToken: "device",
		})
		g, _ := hold(t, h.trip)
		h.p.trip = g
		h.trip.Enqueue(porttest.Reply{Body: []byte(freshToken(t, "never"))})

		ctx, cancel := context.WithTimeout(t.Context(), 20*time.Millisecond)
		defer cancel()
		_, err := h.p.UserToken(ctx)
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("err = %v, want the caller's deadline", err)
		}
	})
}

func TestExchangeTimeoutBoundsASilentServer(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		h := newHarness(t, map[domain.SecretName]string{
			domain.SecretDeviceToken: "device",
		}, WithExchangeTimeout(20*time.Millisecond))
		g, _ := hold(t, h.trip)
		h.p.trip = g
		h.trip.Enqueue(porttest.Reply{Body: []byte(freshToken(t, "never"))})

		// The caller's context is alive, so the exchange's own deadline is a
		// timeout, not the caller's DeadlineExceeded.
		_, err := h.p.UserToken(context.Background())
		if !domaintest.SameError(err, domain.Transport(domain.ReasonTimedOut)) || errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("err = %v, want a transport timeout", err)
		}
		if !g.wasCancelled.Load() {
			t.Fatal("the buy was not bounded")
		}
	})
}

func TestSlowStoreDoesNotBlockTheCache(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		alive := freshToken(t, "cached")
		secrets := porttest.NewSecrets(map[domain.SecretName]string{
			domain.SecretDeviceToken: "device",
			domain.SecretUserToken:   alive,
		})
		slow := &slowStore{SecretStore: secrets, release: make(chan struct{})}
		p := open(slow, &porttest.Transport{}, nowClock())
		got, err := p.UserToken(t.Context())
		must(t, err)
		if got.Expose() != alive {
			t.Fatalf("token = %q", got)
		}

		slow.block.Store(true)
		registered := make(chan error, 1)
		go func() {
			_, err := p.IsRegistered(context.Background())
			registered <- err
		}()
		until(t, "the store to block", func() bool { return slow.waiting.Load() == 1 })

		done := make(chan struct{})
		go func() {
			defer close(done)
			if tok, err := p.UserToken(context.Background()); err != nil || tok.Expose() != alive {
				t.Errorf("cache hit = %q %v", tok, err)
			}
			p.InvalidateUserToken("someone-else")
		}()
		select {
		case <-done:
		case <-time.After(3 * time.Second):
			t.Fatal("a cache hit waited on the store")
		}
		close(slow.release)
		must(t, <-registered)
	})
}
func TestFailedExchangeIsNotRemembered(t *testing.T) {
	t.Parallel()
	body := freshToken(t, "after-failure")
	h := newHarness(t, map[domain.SecretName]string{
		domain.SecretDeviceToken: "device",
	})
	attempts := retry.Default().MaxAttempts
	for range attempts {
		h.trip.Enqueue(porttest.Reply{Status: 503})
	}
	h.trip.Enqueue(porttest.Reply{Body: []byte(body)})
	_, err := h.p.UserToken(t.Context())
	mustKind(t, err, domain.HTTP(503, ""))
	got, err := h.p.UserToken(t.Context())
	must(t, err)
	if got.Expose() != body {
		t.Fatalf("token = %q", got)
	}
	if len(h.trip.Calls()) != attempts+1 {
		t.Fatalf("requests = %d", len(h.trip.Calls()))
	}
}

func TestWaitersShareAFailedExchange(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		body := freshToken(t, "after-shared-failure")
		policy := retry.Default()
		policy.MaxAttempts = 1
		h := newHarness(t, map[domain.SecretName]string{
			domain.SecretDeviceToken: "device",
		}, WithPolicy(policy))
		g, letGo := hold(t, h.trip)
		h.p.trip = g
		h.trip.Enqueue(
			porttest.Reply{Status: 503},
			porttest.Reply{Body: []byte(body)},
		)

		firstErr := make(chan error, 1)
		go func() {
			_, err := h.p.UserToken(context.Background())
			firstErr <- err
		}()
		until(t, "the buy to leave", func() bool { return len(h.trip.Calls()) == 1 })
		secondErr := make(chan error, 1)
		go func() {
			_, err := h.p.UserToken(context.Background())
			secondErr <- err
		}()
		until(t, "the second reader", func() bool { return h.reads.count(domain.SecretUserToken) >= 2 })
		letGo()
		mustKind(t, <-firstErr, domain.HTTP(503, ""))
		mustKind(t, <-secondErr, domain.HTTP(503, ""))

		got, err := h.p.UserToken(context.Background())
		must(t, err)
		if got.Expose() != body || len(h.trip.Calls()) != 2 {
			t.Fatalf("token = %q requests = %d", got, len(h.trip.Calls()))
		}
	})
}

func TestLateRefusalSparesTheReplacement(t *testing.T) {
	t.Parallel()
	expired := freshToken(t, "refused")
	replacement := freshToken(t, "replacement")
	h := newHarness(t, map[domain.SecretName]string{
		domain.SecretDeviceToken: "device",
		domain.SecretUserToken:   expired,
	})
	got, err := h.p.UserToken(t.Context())
	must(t, err)
	if got.Expose() != expired || len(h.trip.Calls()) != 0 {
		t.Fatalf("first = %q requests = %d", got, len(h.trip.Calls()))
	}
	h.p.InvalidateUserToken(redacted.RedactedString(expired))
	h.trip.Enqueue(porttest.Reply{Body: []byte(replacement)})
	got, err = h.p.UserToken(t.Context())
	must(t, err)
	if got.Expose() != replacement || len(h.trip.Calls()) != 1 {
		t.Fatalf("replacement = %q requests = %d", got, len(h.trip.Calls()))
	}
	h.p.InvalidateUserToken(redacted.RedactedString(expired))
	got, err = h.p.UserToken(t.Context())
	must(t, err)
	if got.Expose() != replacement || len(h.trip.Calls()) != 1 {
		t.Fatalf("spared = %q requests = %d", got, len(h.trip.Calls()))
	}
}

func TestInvalidatingSparesATokenBoughtElsewhere(t *testing.T) {
	t.Parallel()
	refused := freshToken(t, "refused")
	elsewhere := freshToken(t, "elsewhere")
	h := newHarness(t, map[domain.SecretName]string{
		domain.SecretDeviceToken: "device",
		domain.SecretUserToken:   refused,
	})
	got, err := h.p.UserToken(t.Context())
	must(t, err)
	if got.Expose() != refused {
		t.Fatalf("token = %q", got)
	}
	h.p.InvalidateUserToken(redacted.RedactedString(refused))
	must(t, h.secrets.Save(context.Background(), domain.SecretUserToken, elsewhere))
	got, err = h.p.UserToken(t.Context())
	must(t, err)
	if got.Expose() != elsewhere || len(h.trip.Calls()) != 0 {
		t.Fatalf("token = %q requests = %d", got, len(h.trip.Calls()))
	}
}

func TestRetryAfterIsNotJittered(t *testing.T) {
	t.Parallel()
	body := freshToken(t, "after-wait")
	h := newHarness(t, map[domain.SecretName]string{
		domain.SecretDeviceToken: "device",
	})
	h.trip.Enqueue(
		porttest.Reply{Status: 429, Header: port.Header{"retry-after": {"1.5"}}},
		porttest.Reply{Body: []byte(body)},
	)
	got, err := h.p.UserToken(t.Context())
	must(t, err)
	if got.Expose() != body {
		t.Fatalf("token = %q", got)
	}
	want := time.Duration(1.5 * float64(time.Second))
	delays := h.clock.Delays()
	if len(delays) != 1 || delays[0] != want {
		t.Fatalf("delays = %v, want %s", delays, want)
	}
}

func TestWindowsDeviceDescription(t *testing.T) {
	t.Parallel()
	h := newHarness(t, nil, WithDeviceDesc(domain.DeviceDescWindows))
	h.trip.Enqueue(porttest.Reply{Body: []byte("device-token-value")})
	must(t, h.p.RegisterDevice(t.Context(), "abcdefgh"))
	body := decodeRegistration(t, oneCall(t, h.trip).Body)
	if body.DeviceDesc != "desktop-windows" {
		t.Fatalf("deviceDesc = %q", body.DeviceDesc)
	}
}

func TestEmptyAndInvalidTokenBodies(t *testing.T) {
	t.Parallel()
	h := newHarness(t, map[domain.SecretName]string{
		domain.SecretDeviceToken: "device",
	})
	h.trip.Enqueue(porttest.Reply{Body: []byte("   ")})
	_, err := h.p.UserToken(t.Context())
	mustMalformed(t, err, "user token is empty or not UTF-8")

	h.trip.Enqueue(porttest.Reply{Body: []byte{0xff}})
	_, err = h.p.UserToken(t.Context())
	mustMalformed(t, err, "user token is empty or not UTF-8")

	h.trip.Enqueue(porttest.Reply{Body: nil})
	err = h.p.RegisterDevice(t.Context(), "abcdefgh")
	mustMalformed(t, err, "device token is empty or not UTF-8")

	h.trip.Enqueue(porttest.Reply{Body: []byte{0xff, 0xfe}})
	err = h.p.RegisterDevice(t.Context(), "abcdefgh")
	mustMalformed(t, err, "device token is empty or not UTF-8")
}

func TestIsRegisteredFollowsTheDeviceToken(t *testing.T) {
	t.Parallel()
	h := newHarness(t, nil)
	ok, err := h.p.IsRegistered(t.Context())
	must(t, err)
	if ok {
		t.Fatal("registered with no device token")
	}
	must(t, h.secrets.Save(context.Background(), domain.SecretDeviceToken, "device"))
	ok, err = h.p.IsRegistered(t.Context())
	must(t, err)
	if !ok {
		t.Fatal("not registered")
	}
}

func TestPlanReadsConnectAndStaysQuietOnGarbage(t *testing.T) {
	t.Parallel()
	connect := tokenAt(t, distantExp, map[string]any{"level": "connect", "scopes": "sync:full"})
	h := newHarness(t, map[domain.SecretName]string{
		domain.SecretDeviceToken: "device",
		domain.SecretUserToken:   connect,
	})
	plan, err := h.p.Plan(t.Context())
	must(t, err)
	if plan == nil || !plan.IsConnect() || plan.SyncScope == nil || *plan.SyncScope != "sync:full" {
		t.Fatalf("plan = %+v", plan)
	}

	h = newHarness(t, map[domain.SecretName]string{
		domain.SecretDeviceToken: "device",
	})
	h.trip.Enqueue(porttest.Reply{Body: []byte("not-a-jwt")})
	plan, err = h.p.Plan(t.Context())
	must(t, err)
	if plan != nil {
		t.Fatalf("plan = %+v", plan)
	}
}

func TestCancelledContextStillUsesTheCache(t *testing.T) {
	t.Parallel()
	alive := freshToken(t, "cached")
	h := newHarness(t, map[domain.SecretName]string{
		domain.SecretDeviceToken: "device",
		domain.SecretUserToken:   alive,
	})
	if _, err := h.p.UserToken(t.Context()); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	got, err := h.p.UserToken(ctx)
	must(t, err)
	if got.Expose() != alive || len(h.trip.Calls()) != 0 {
		t.Fatalf("token = %q requests = %d err = %v", got, len(h.trip.Calls()), err)
	}
}

func TestCancelledContextBeforeJoinMakesNoRequest(t *testing.T) {
	t.Parallel()
	h := newHarness(t, map[domain.SecretName]string{
		domain.SecretDeviceToken: "device",
	})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := h.p.UserToken(ctx)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v", err)
	}
	if calls := h.trip.Calls(); len(calls) != 0 {
		t.Fatalf("requests = %d", len(calls))
	}
}

func TestSaveRefusalIsLoggedAndTheTokenIsReturned(t *testing.T) {
	buf := captureLog(t)
	secrets := porttest.NewSecrets(map[domain.SecretName]string{
		domain.SecretDeviceToken: "device",
	})
	store := saveRefuser{SecretStore: secrets, err: errors.New("disk full")}
	trip := &porttest.Transport{}
	sleeper := nowClock()
	body := freshToken(t, "unsaved")
	trip.Enqueue(porttest.Reply{Body: []byte(body)})
	p := open(store, trip, sleeper)
	got, err := p.UserToken(t.Context())
	must(t, err)
	if got.Expose() != body {
		t.Fatalf("token = %q", got)
	}
	text := buf.String()
	if !strings.Contains(text, "the user token could not be kept in the store") || !strings.Contains(text, "disk full") {
		t.Fatalf("log = %s", text)
	}
	if strings.Contains(text, body) {
		t.Fatal("log contains the user token")
	}
	if _, ok := mustSecret(t, secrets, domain.SecretUserToken); ok {
		t.Fatal("refused save still stored the token")
	}
}

func TestDeviceRegisteredLogOmitsTheToken(t *testing.T) {
	buf := captureLog(t)
	h := newHarness(t, nil)
	const secret = "device-token-value"
	h.trip.Enqueue(porttest.Reply{Body: []byte(secret)})
	must(t, h.p.RegisterDevice(t.Context(), "abcdefgh"))
	text := buf.String()
	if !strings.Contains(text, "device registered") {
		t.Fatalf("log = %s", text)
	}
	if strings.Contains(text, secret) {
		t.Fatal("log contains the device token")
	}
}

type harness struct {
	p       *Provider
	secrets *porttest.Secrets
	reads   *readCounter
	trip    *porttest.Transport
	clock   *porttest.Clock
}

// nowClock is a test clock starting at the wall clock's now. Its sleeps
// return at once.
func nowClock() *porttest.Clock { return porttest.NewClockAt(time.Now()) }

func newHarness(t *testing.T, initial map[domain.SecretName]string, opts ...Option) *harness {
	t.Helper()
	return newHarnessAt(t, nowClock(), initial, opts...)
}

func newHarnessAt(t *testing.T, clk *porttest.Clock, initial map[domain.SecretName]string, opts ...Option) *harness {
	t.Helper()
	secrets := porttest.NewSecrets(initial)
	reads := &readCounter{inner: secrets}
	trip := &porttest.Transport{}
	return &harness{
		p:       open(reads, trip, clk, opts...),
		secrets: secrets,
		reads:   reads,
		trip:    trip,
		clock:   clk,
	}
}

func open(secrets port.SecretStore, trip port.RoundTripper, clk *porttest.Clock, opts ...Option) *Provider {
	all := make([]Option, 0, 1+len(opts))
	all = append(all, WithUserAgent(desktopUA))
	all = append(all, opts...)
	return New(secrets, trip, clk, all...)
}

func freshToken(t *testing.T, jti string) string {
	t.Helper()
	return tokenAt(t, distantExp, map[string]any{"jti": jti})
}

func tokenAt(t *testing.T, exp int64, extra map[string]any) string {
	t.Helper()
	claims := map[string]any{"exp": exp}
	maps.Copy(claims, extra)
	raw, err := json.Marshal(claims)
	if err != nil {
		t.Fatal(err)
	}
	enc := base64.RawURLEncoding.EncodeToString
	return enc([]byte(`{"alg":"none"}`)) + "." + enc(raw) + "." + enc([]byte("sig"))
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

// mustKind matches the kind of target and not its payload.
func mustKind(t *testing.T, err error, target domain.Error) {
	t.Helper()
	if !domaintest.SameKind(err, target) {
		t.Fatalf("error = %v, want %v", err, target)
	}
}

func mustMalformed(t *testing.T, err error, text string) {
	t.Helper()
	got, ok := errors.AsType[*domain.MalformedResponseError](err)
	if !ok || got.What != text {
		t.Fatalf("error = %v, want malformed %q", err, text)
	}
}

func mustSecret(t *testing.T, secrets *porttest.Secrets, name domain.SecretName) (string, bool) {
	t.Helper()
	value, ok, err := secrets.Secret(context.Background(), name)
	if err != nil {
		t.Fatal(err)
	}
	return value, ok
}

func oneCall(t *testing.T, trip *porttest.Transport) port.Request {
	t.Helper()
	calls := trip.Calls()
	if len(calls) != 1 {
		t.Fatalf("requests = %d", len(calls))
	}
	return calls[0]
}

func headerOne(header port.Header, name string) string {
	for key, values := range header {
		if strings.EqualFold(key, name) && len(values) > 0 {
			return values[0]
		}
	}
	return ""
}

func decodeRegistration(t *testing.T, body []byte) deviceTokenRequest {
	t.Helper()
	var req deviceTokenRequest
	if err := json.Unmarshal(body, &req); err != nil {
		t.Fatal(err)
	}
	return req
}

// until checks cond once the bubble is idle. Nothing else can make it true
// after that, so a false cond is a failure, not a reason to wait longer.
func until(t *testing.T, what string, cond func() bool) {
	t.Helper()
	synctest.Wait()
	if !cond() {
		t.Fatalf("never happened: %s", what)
	}
}

func captureLog(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(prev) })
	return &buf
}

// gate records through the scripted transport, then blocks until release.
// The buy's context is not the caller's, so cancelling a caller must not
// set wasCancelled; only the exchange's own deadline may.
type gate struct {
	base         *porttest.Transport
	release      chan struct{}
	wasCancelled atomic.Bool
}

func hold(t *testing.T, base *porttest.Transport) (*gate, func()) {
	t.Helper()
	g := &gate{base: base, release: make(chan struct{})}
	var once sync.Once
	letGo := func() { once.Do(func() { close(g.release) }) }
	t.Cleanup(letGo)
	return g, letGo
}

func (g *gate) RoundTrip(ctx context.Context, req port.Request) (port.Response, error) {
	resp, err := g.base.RoundTrip(ctx, req)
	select {
	case <-g.release:
		return resp, err
	case <-ctx.Done():
		g.wasCancelled.Store(true)
		return port.Response{}, ctx.Err()
	}
}

type readCounter struct {
	inner port.SecretStore
	mu    sync.Mutex
	n     map[domain.SecretName]int
}

func (c *readCounter) Secret(ctx context.Context, which domain.SecretName) (string, bool, error) {
	c.mu.Lock()
	if c.n == nil {
		c.n = map[domain.SecretName]int{}
	}
	c.n[which]++
	c.mu.Unlock()
	return c.inner.Secret(ctx, which)
}

func (c *readCounter) Save(ctx context.Context, which domain.SecretName, value string) error {
	return c.inner.Save(ctx, which, value)
}

func (c *readCounter) Forget(ctx context.Context, which domain.SecretName) error {
	return c.inner.Forget(ctx, which)
}

func (c *readCounter) count(which domain.SecretName) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.n[which]
}

// slowStore blocks every call once block is set, until release closes.
type slowStore struct {
	port.SecretStore
	block   atomic.Bool
	waiting atomic.Int64
	release chan struct{}
}

func (s *slowStore) Secret(ctx context.Context, which domain.SecretName) (string, bool, error) {
	if s.block.Load() {
		s.waiting.Add(1)
		<-s.release
	}
	return s.SecretStore.Secret(ctx, which)
}

type saveRefuser struct {
	port.SecretStore
	err error
}

func (s saveRefuser) Save(ctx context.Context, which domain.SecretName, value string) error {
	if which == domain.SecretUserToken {
		return s.err
	}
	return s.SecretStore.Save(ctx, which, value)
}

// cancelAfterID cancels the registration's context once the device id is
// saved, between the two writes that must land together.
type cancelAfterID struct {
	port.SecretStore
	cancel context.CancelFunc
}

func (s cancelAfterID) Save(ctx context.Context, which domain.SecretName, value string) error {
	err := s.SecretStore.Save(ctx, which, value)
	if which == domain.SecretDeviceID {
		s.cancel()
	}
	return err
}

func TestRegistrationSavesThePairDespiteACancel(t *testing.T) {
	t.Parallel()
	secrets := porttest.NewSecrets(nil)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	trip := &porttest.Transport{}
	trip.Enqueue(porttest.Reply{Body: []byte("device-token-value")})
	p := open(cancelAfterID{SecretStore: secrets, cancel: cancel}, trip, nowClock())
	must(t, p.RegisterDevice(ctx, "abcdefgh"))
	if _, ok := mustSecret(t, secrets, domain.SecretDeviceID); !ok {
		t.Fatal("device id not saved")
	}
	if stored, ok := mustSecret(t, secrets, domain.SecretDeviceToken); !ok || stored != "device-token-value" {
		t.Fatalf("device token = %q ok=%v", stored, ok)
	}
}
