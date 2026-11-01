// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package auth

import (
	"context"
	"encoding/json/v2"
	"errors"
	"strings"
	"unicode/utf8"
	"uuid"

	"github.com/altessa-s/rmcloud-go/internal/domain"
)

type deviceTokenRequest struct {
	Code       string `json:"code"`
	DeviceDesc string `json:"deviceDesc"`
	DeviceID   string `json:"deviceID"`
}

// IsRegistered reports whether a device token is in the store. A missing
// token is false, not an error. A store error is returned as it is.
func (p *Provider) IsRegistered(ctx context.Context) (bool, error) {
	_, ok, err := p.secrets.Secret(ctx, domain.SecretDeviceToken)
	if err != nil {
		return false, err
	}
	return ok, nil
}

// oneTimeCodeLength is the length of the code the connect page shows.
const oneTimeCodeLength = 8

// RegisterDevice exchanges an 8-character code for a device token.
//
// The code is trimmed and lowercased before it is counted. Anything else
// is refused before a request goes out. The registration session is read
// before the request and checked again before the token is kept, so a
// device forgotten while the request was in flight does not come back.
func (p *Provider) RegisterDevice(ctx context.Context, code string) error {
	code = strings.ToLower(strings.TrimSpace(code))
	if utf8.RuneCountInString(code) != oneTimeCodeLength {
		return domain.ErrInvalidOneTimeCode
	}
	deviceID, err := p.newID()
	if err != nil {
		return err
	}
	deviceID = strings.ToLower(deviceID)

	p.mu.Lock()
	session := p.session
	p.mu.Unlock()

	payload, err := json.Marshal(deviceTokenRequest{
		Code:       code,
		DeviceDesc: p.deviceDesc,
		DeviceID:   deviceID,
	})
	if err != nil {
		return err
	}
	dest := p.endpoints.NewDeviceToken()
	raw, err := p.post(ctx, dest, p.headers("", true), payload, domain.ErrInvalidOneTimeCode, nil)
	if err != nil {
		return err
	}
	token, err := bearerToken(raw, "device token")
	if err != nil {
		return err
	}

	p.storeMu.Lock()
	defer p.storeMu.Unlock()
	if p.currentSession() != session {
		p.logger().Info("device token arrived after the device was forgotten: dropping it")
		return domain.ErrNotRegistered
	}
	// The server has already registered the device, so the pair is saved
	// whole: a cancel between the two writes would leave an id without a
	// token, and the next registration would orphan it.
	saveCtx := context.WithoutCancel(ctx)
	if err := p.secrets.Save(saveCtx, domain.SecretDeviceID, deviceID); err != nil {
		return err
	}
	if err := p.secrets.Save(saveCtx, domain.SecretDeviceToken, token.Expose()); err != nil {
		return err
	}
	// The previous account's user token, if the store still has one.
	// Failure is the store's business: the registration itself landed.
	_ = p.secrets.Forget(context.WithoutCancel(ctx), domain.SecretUserToken)
	p.mu.Lock()
	p.forgetUserLocked()
	p.mu.Unlock()
	p.logger().Info("device registered")
	return nil
}

// RevokeDevice asks the server to take this device off the account.
//
// The store is left as it was. Forgetting is a separate call, and the two
// fail differently. No device token means there is nobody to tell.
// A 401 or 403 means the server has already stopped knowing this device.
func (p *Provider) RevokeDevice(ctx context.Context) error {
	device, ok, err := p.secret(ctx, domain.SecretDeviceToken)
	if err != nil {
		return err
	}
	if !ok {
		return nil
	}
	dest := p.endpoints.DeleteDeviceToken()
	_, err = p.post(ctx, dest, p.headers(device, false), []byte{}, nil, nil)
	if err != nil {
		if errors.Is(err, domain.ErrUnauthorized) {
			p.logger().Info("device token already refused by the server: nothing left to revoke")
			return nil
		}
		return err
	}
	p.logger().Info("device token revoked at the server")
	return nil
}

// ForgetDevice drops the device token, the device id, and the user token.
//
// The device token and the device id have to go. A failure there stops the
// call. The user token is best-effort: the registration is already gone,
// and the session moves so an exchange still in flight cannot put a token
// back. That exchange is left running; it is not canceled.
func (p *Provider) ForgetDevice(ctx context.Context) error {
	p.storeMu.Lock()
	defer p.storeMu.Unlock()
	if err := p.secrets.Forget(ctx, domain.SecretDeviceToken); err != nil {
		return err
	}
	if err := p.secrets.Forget(ctx, domain.SecretDeviceID); err != nil {
		return err
	}
	_ = p.secrets.Forget(ctx, domain.SecretUserToken)
	p.mu.Lock()
	p.forgetUserLocked()
	p.mu.Unlock()
	return nil
}

// newDeviceID is a random version 4 UUID. It cannot fail; the error is
// there for sources passed to [WithID].
func newDeviceID() (string, error) {
	return uuid.NewV4().String(), nil
}
