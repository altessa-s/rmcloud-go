// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package httpapi

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

// checkRedirect keeps the user token on the origin the call started from.
//
// A hop to another origin drops it. A hop back restores it. Ten hops is
// the stop, and that stop is logged by the caller as the original request
// failing, not as an eleventh hop.
func (t *Transport) checkRedirect(req *http.Request, via []*http.Request) error {
	const maxRedirects = 10
	if len(via) >= maxRedirects {
		return fmt.Errorf("stopped after %d redirects", maxRedirects)
	}
	start := via[0]
	var startURL *url.URL
	original := ""
	if start != nil {
		startURL = start.URL
		original = authorizationOf(start.Header)
	}
	hop := req.URL
	prev := previousURL(req, via)
	token := original
	if !sameOrigin(prev, hop) {
		token = ""
	}
	if token == "" && sameOrigin(startURL, hop) {
		token = original
	}
	setAuthorization(req.Header, token)
	prevAuth := ""
	if n := len(via); n > 0 && via[n-1] != nil {
		prevAuth = authorizationOf(via[n-1].Header)
	}
	if authorizationOf(req.Header) != prevAuth {
		host := "elsewhere"
		if hop != nil {
			if h := hop.Hostname(); h != "" {
				host = h
			}
		}
		t.logger().Debug("redirect leaves the origin: the token is not carried", "host", host)
	}
	if t.wire.on() {
		status := 0
		if req.Response != nil {
			status = req.Response.StatusCode
		}
		t.wire.redirected(status, hop)
		t.wire.sent(req.Method, hop, req.Header, nil, true)
	}
	return nil
}

// authorizationOf is the first Authorization value, matched in any case.
func authorizationOf(h http.Header) string {
	for key, values := range h {
		if strings.EqualFold(key, "Authorization") && len(values) > 0 {
			return values[0]
		}
	}
	return ""
}

func setAuthorization(h http.Header, token string) {
	if h == nil {
		return
	}
	for key := range h {
		if strings.EqualFold(key, "Authorization") {
			delete(h, key)
		}
	}
	if token != "" {
		h["Authorization"] = []string{token}
	}
}

func previousURL(req *http.Request, via []*http.Request) *url.URL {
	if req != nil && req.Response != nil && req.Response.Request != nil && req.Response.Request.URL != nil {
		return req.Response.Request.URL
	}
	if n := len(via); n > 0 && via[n-1] != nil {
		return via[n-1].URL
	}
	return nil
}

func sameOrigin(a, b *url.URL) bool {
	if a == nil || b == nil {
		return false
	}
	if !strings.EqualFold(a.Scheme, b.Scheme) {
		return false
	}
	if !strings.EqualFold(a.Hostname(), b.Hostname()) {
		return false
	}
	return portOf(a) == portOf(b)
}

func portOf(u *url.URL) string {
	if u == nil {
		return ""
	}
	if p := u.Port(); p != "" {
		return p
	}
	switch strings.ToLower(u.Scheme) {
	case "https":
		return "443"
	case "http":
		return "80"
	default:
		return ""
	}
}
