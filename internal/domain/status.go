// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package domain

import (
	"bytes"
	"math"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// FromHTTP maps a status onto a protocol error. A 2xx status is nil.
// unauthorized is the error used for 401 and 403; nil means [ErrUnauthorized].
func FromHTTP(status int, rawURL, retryAfter string, body []byte, unauthorized Error) Error {
	if IsSuccess(status) {
		return nil
	}
	switch status {
	case StatusUnauthorized, StatusForbidden:
		if unauthorized != nil {
			return unauthorized
		}
		return ErrUnauthorized
	case StatusPaymentRequired:
		return NotOnThisPlan(DetailOf(body))
	case StatusTooManyRequests:
		return RateLimited(ParseRetryAfter(retryAfter))
	default:
		return HTTP(status, PathOf(rawURL))
	}
}

// DetailOf is the trimmed text of an error body. The first DetailLimit bytes
// are kept, invalid UTF-8 is replaced with U+FFFD, then the text is trimmed.
// A nil body is empty.
func DetailOf(body []byte) string {
	if body == nil {
		return ""
	}
	if len(body) > DetailLimit {
		body = body[:DetailLimit]
	}
	replaced := bytes.ToValidUTF8(body, []byte("\uFFFD"))
	return strings.TrimSpace(string(replaced))
}

// ParseRetryAfter reads a Retry-After delay in seconds. HTTP-date is not
// accepted. An unparseable or empty value is nil, and so are NaN and the
// infinities, which a float parser accepts and a duration cannot hold. A
// value past the largest duration is that largest duration. Zero and
// negative values are returned as a duration so they stay distinct from
// "no header".
func ParseRetryAfter(header string) *time.Duration {
	header = strings.TrimSpace(header)
	if header == "" {
		return nil
	}
	seconds, err := strconv.ParseFloat(header, 64)
	if err != nil || math.IsNaN(seconds) || math.IsInf(seconds, 0) {
		return nil
	}
	nanos := seconds * float64(time.Second)
	switch {
	case nanos >= math.MaxInt64:
		return new(time.Duration(math.MaxInt64))
	case nanos <= math.MinInt64:
		return new(time.Duration(math.MinInt64))
	}
	return new(time.Duration(nanos))
}

// PathOf is the URL path with the query stripped. An empty or unusable URL
// contributes an empty path.
func PathOf(rawURL string) string {
	if rawURL == "" {
		return ""
	}
	u, err := url.Parse(rawURL)
	if err != nil {
		return ""
	}
	return u.Path
}
