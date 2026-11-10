// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package auth

import (
	"context"
	"errors"
	"time"

	"github.com/altessa-s/go-atlas-core/types/redacted"

	"github.com/altessa-s/rmcloud-go/internal/domain"
)

// clockSkewTolerance is how far the local clock may stand from the server's
// before the provider says so in the log. A few seconds is the exchange's
// own flight; past this it is a clock that wants setting.
const clockSkewTolerance = 5 * time.Minute

// userTokenCache is the user token this process holds and the one a server
// refused. It has no lock of its own: the Provider's mu covers it. Its
// moments are the server's time, as [Provider.serverNowLocked] gives it.
type userTokenCache struct {
	has       bool
	token     redacted.RedactedString
	heldSince time.Time
	expiry    *time.Time
	// rejected is the user token a server refused. Nil means nothing was
	// refused; the empty string is a token that happened to be empty.
	rejected *redacted.RedactedString
}

func (c *userTokenCache) usable(now time.Time) (redacted.RedactedString, bool) {
	if !c.has || !domain.IsUsable(c.expiry, new(c.heldSince), now) {
		return "", false
	}
	return c.token, true
}

func (c *userTokenCache) remember(token redacted.RedactedString, now time.Time) {
	c.has = true
	c.token = token
	c.heldSince = now
	c.expiry = domain.Expiry(token.Expose())
}

func (c *userTokenCache) drop() {
	c.has = false
	c.token = ""
	c.heldSince = time.Time{}
	c.expiry = nil
}

func (c *userTokenCache) rejects(token redacted.RedactedString) bool {
	return c.rejected != nil && *c.rejected == token
}

// UserToken returns a current user token, buying one when the cache and the
// store do not have one.
//
// Callers share one buy through a go-atlas-core singleflight group. The buy
// runs under a context that no caller can cancel, bounded by the exchange
// timeout, so a caller that leaves does not abandon the others. Each caller
// waits on that buy with its own ctx and returns ctx.Err() when ctx ends
// first; a ctx that is already done wins over an answer. A cache hit does
// not consult ctx at all.
func (p *Provider) UserToken(ctx context.Context) (redacted.RedactedString, error) {
	p.mu.Lock()
	token, ok := p.cache.usable(p.serverNowLocked())
	p.mu.Unlock()
	if ok {
		return token, nil
	}

	// Another process may have written a fresh token into the store. The
	// read happens without mu: a store may be slow, or may ask the user.
	stored, ok, err := p.secret(ctx, domain.SecretUserToken)
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return "", ctxErr
		}
		p.logger().Debug("user token store read failed: buying a new token", "err", err)
		ok = false
	}

	p.mu.Lock()
	now := p.serverNowLocked()
	if token, hit := p.cache.usable(now); hit {
		p.mu.Unlock()
		return token, nil
	}
	if ok && !p.cache.rejects(stored) && domain.IsFresh(stored.Expose(), now) {
		p.cache.remember(stored, now)
		p.mu.Unlock()
		return stored, nil
	}
	session := p.session
	p.mu.Unlock()
	bought, err := p.buys.Do(ctx, session, func(work context.Context) (redacted.RedactedString, error) {
		return p.buy(work, session)
	})
	if err != nil && ctx.Err() == nil && errors.Is(err, context.DeadlineExceeded) {
		// The exchange ran out of its own time, not the caller's: a bare
		// DeadlineExceeded would read as the caller's deadline.
		return "", domain.Transport(domain.ReasonTimedOut)
	}
	return bought, err
}

// Plan reads the plan claims from the user token. A token whose claims do
// not parse returns a nil plan and a nil error: the server did not say,
// which is not the same as an account without Connect.
func (p *Provider) Plan(ctx context.Context) (*domain.Plan, error) {
	token, err := p.UserToken(ctx)
	if err != nil {
		return nil, err
	}
	return domain.PlanFromToken(token.Expose()), nil
}

// InvalidateUserToken marks refused as a token the server said no to.
//
// The cache is dropped only when it is that same token. A replacement
// bought since answers for itself, and a token another process has written
// into the store is still eligible the next time one is asked for.
func (p *Provider) InvalidateUserToken(refused redacted.RedactedString) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.cache.rejected = new(refused)
	if p.cache.has && p.cache.token == refused {
		p.cache.drop()
	}
}

// forgetUserLocked drops everything this process remembers about a user
// token and moves the registration session. The in-flight buy is left
// running; its channel stays open until that buy finishes.
func (p *Provider) forgetUserLocked() {
	p.cache.drop()
	p.cache.rejected = nil
	p.session++
}

// serverNowLocked is now as the server would say it: the local clock less
// the measured skew. A token's exp is the server's time, so this is the
// moment every token is judged at. Before a token has been bought here the
// skew is unknown and the local clock stands in. The caller holds mu.
func (p *Provider) serverNowLocked() time.Time {
	now := p.clock.Now()
	if p.skew != nil {
		now = now.Add(-*p.skew)
	}
	return now
}

// noteClockSkew reads the server's clock off a user token it has just
// issued and returns now in the server's time. The token's iat is the
// server's now, and its distance from the local clock is the skew every
// later judgement of a token allows for. A token without an iat leaves the
// measure as it was.
//
// Without the measure, a machine whose clock runs hours ahead reads every
// token as expired on arrival: none is kept, each request buys another, and
// the server ends up answering with a rate limit.
//
// A skew past [clockSkewTolerance] is logged once per measurement.
func (p *Provider) noteClockSkew(token redacted.RedactedString) time.Time {
	issued := domain.IssuedAt(token.Expose())
	p.mu.Lock()
	if issued != nil {
		p.skew = new(p.clock.Now().Sub(*issued))
	}
	now, skew := p.serverNowLocked(), p.skew
	p.mu.Unlock()
	if issued != nil && skew.Abs() > clockSkewTolerance {
		direction := "ahead"
		if *skew < 0 {
			direction = "behind"
		}
		p.logger().Warn(
			"the local clock is off the server's: tokens are judged in the server's time, and this machine's date and time want checking",
			"seconds", int64(skew.Abs()/time.Second), "direction", direction,
		)
	}
	return now
}

func (p *Provider) currentSession() uint64 {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.session
}

// buy reads the device token and exchanges it for a user token. ctx is the
// exchange's own, not a caller's. session was read under mu when this buy
// was published. Reading it after the request would accept a token for a
// registration that was forgotten and then restored with the same
// device-token text.
func (p *Provider) buy(ctx context.Context, session uint64) (redacted.RedactedString, error) {
	device, ok, err := p.secret(ctx, domain.SecretDeviceToken)
	if err != nil {
		return "", err
	}
	if !ok {
		return "", domain.ErrNotRegistered
	}
	dest := p.endpoints.NewUserToken()
	raw, err := p.post(ctx, dest, p.headers(device, false), []byte{}, nil, domain.ErrAccountWithoutTablet)
	if err != nil {
		return "", err
	}
	token, err := bearerToken(raw, "user token")
	if err != nil {
		return "", err
	}

	p.storeMu.Lock()
	defer p.storeMu.Unlock()
	if p.currentSession() != session {
		p.logger().Info("user token arrived for a registration that is gone: dropping it")
		return "", domain.ErrNotRegistered
	}
	// A store error is a mismatch, not a failed exchange. The token in
	// hand belongs to a device the store does not hold.
	stored, ok, err := p.secret(ctx, domain.SecretDeviceToken)
	if err != nil || !ok || stored != device {
		p.logger().Info("user token arrived after the account left the store: dropping it")
		return "", domain.ErrNotRegistered
	}
	// The skew first, so the token is judged, kept and cached in the
	// server's time.
	now := p.noteClockSkew(token)
	if domain.IsFresh(token.Expose(), now) {
		err = p.secrets.Save(ctx, domain.SecretUserToken, token.Expose())
	} else {
		err = p.secrets.Forget(ctx, domain.SecretUserToken)
	}
	if err != nil {
		p.logger().Error("the user token could not be kept in the store", "err", err)
	}
	p.mu.Lock()
	p.cache.remember(token, now)
	p.cache.rejected = nil
	p.mu.Unlock()
	p.logger().Debug("got user token")
	return token, nil
}
