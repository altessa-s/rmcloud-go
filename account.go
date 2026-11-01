// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package rmcloud

import "context"

// IsRegistered reports whether a device token is stored.
func (c *Client) IsRegistered(ctx context.Context) (bool, error) {
	return c.tokens.IsRegistered(ctx)
}

// RegisterDevice exchanges an 8-character code for a device token.
func (c *Client) RegisterDevice(ctx context.Context, code string) error {
	return c.tokens.RegisterDevice(ctx, code)
}

// RevokeDevice tells the service this device has left. The store is kept.
func (c *Client) RevokeDevice(ctx context.Context) error {
	return c.tokens.RevokeDevice(ctx)
}

// ForgetDevice drops the device token, the device id, and the user token.
func (c *Client) ForgetDevice(ctx context.Context) error {
	return c.tokens.ForgetDevice(ctx)
}

// Plan reads the plan claim. An unparsable token is a nil plan and a nil error.
func (c *Client) Plan(ctx context.Context) (*Plan, error) {
	return c.tokens.Plan(ctx)
}
