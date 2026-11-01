// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package auth

import (
	"context"
	"errors"
	"net/url"
	"strings"
	"unicode/utf8"

	"github.com/altessa-s/go-atlas-core/types/redacted"

	"github.com/altessa-s/rmcloud-go/internal/app/retry"
	"github.com/altessa-s/rmcloud-go/internal/domain"
	"github.com/altessa-s/rmcloud-go/internal/port"
)

func (p *Provider) post(ctx context.Context, dest url.URL, header port.Header, body []byte, unauthorized, badRequest domain.Error) ([]byte, error) {
	return retry.Run(ctx, p.retries, func(ctx context.Context) ([]byte, error) {
		req := port.Request{
			Method: domain.MethodPost,
			URL:    dest,
			Header: header,
			Body:   body,
		}
		resp, err := p.trip.RoundTrip(ctx, req)
		if err != nil {
			return nil, err
		}
		failure := domain.FromHTTP(resp.Status, req.URL.String(), resp.Header.Get("Retry-After"), resp.Body, unauthorized)
		if failure == nil {
			return resp.Body, nil
		}
		if status, ok := errors.AsType[*domain.HTTPError](failure); ok && status.Status == domain.StatusBadRequest && badRequest != nil {
			return nil, badRequest
		}
		return nil, failure
	})
}

// headers is the one place a device token leaves its redaction.
func (p *Provider) headers(device redacted.RedactedString, jsonBody bool) port.Header {
	h := port.Header{}
	if p.userAgent != "" {
		h[domain.HeaderUserAgent] = []string{p.userAgent}
	}
	if device != "" {
		h[domain.HeaderAuthorization] = []string{"Bearer " + device.Expose()}
	}
	if jsonBody {
		h["Content-Type"] = []string{domain.ContentTypeJSON}
	}
	return h
}

func bearerToken(body []byte, what string) (redacted.RedactedString, error) {
	if !utf8.Valid(body) {
		return "", domain.MalformedResponse(what + " is empty or not UTF-8")
	}
	token := strings.TrimSpace(string(body))
	if token == "" {
		return "", domain.MalformedResponse(what + " is empty or not UTF-8")
	}
	return redacted.RedactedString(token), nil
}

// secret reads a token from the store. It is redacted from here on: fmt,
// slog and JSON print a placeholder in its place.
func (p *Provider) secret(ctx context.Context, name domain.SecretName) (redacted.RedactedString, bool, error) {
	value, ok, err := p.secrets.Secret(ctx, name)
	return redacted.RedactedString(value), ok, err
}
