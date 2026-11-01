// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package domain

import (
	"errors"
	"fmt"
	"net/url"
	"strings"
	"unicode/utf8"
)

// RootVersion names which root path a read uses. A write is always v3.
type RootVersion string

// The two root paths.
const (
	RootV3 RootVersion = "v3"
	RootV4 RootVersion = "v4"
)

// Endpoints is the auth host and the sync host, plus the paths this
// protocol hangs off them. The stored URLs keep the spelling they were
// given. Scheme and host are lowercased only for the override check.
type Endpoints struct {
	AuthHost url.URL
	SyncHost url.URL
}

// Production is the service's production pair, used when nothing overrides it.
func Production() Endpoints {
	return Endpoints{
		AuthHost: mustURL("https://webapp-prod.cloud.remarkable.engineering"),
		SyncHost: mustURL("https://internal.cloud.remarkable.com"),
	}
}

// FromEnvironment applies host overrides from env. An empty value is
// ignored. RMCLOUD_HOST sets both hosts, then RMCLOUD_AUTH_HOST and
// RMCLOUD_SYNC_HOST each replace their own. This function does not read
// the process environment.
//
// A value that is refused leaves its host alone and is reported in the
// error, which joins one error per refused variable. The endpoints are
// usable either way. The error never quotes the raw value: a URL can carry
// credentials.
func FromEnvironment(env map[string]string) (Endpoints, error) {
	endpoints := Production()
	var errs []error
	apply := func(name string, set func(url.URL)) {
		u, ok, err := hostOverride(name, env[name])
		if err != nil {
			errs = append(errs, err)
		}
		if ok {
			set(u)
		}
	}
	apply("RMCLOUD_HOST", func(u url.URL) {
		endpoints.AuthHost = u
		endpoints.SyncHost = u
	})
	apply("RMCLOUD_AUTH_HOST", func(u url.URL) { endpoints.AuthHost = u })
	apply("RMCLOUD_SYNC_HOST", func(u url.URL) { endpoints.SyncHost = u })
	return endpoints, errors.Join(errs...)
}

// hostOverride accepts HTTPS anywhere and HTTP only on the loopback. The
// value may carry credentials, so a refusal never repeats it: a value that
// does not parse as a URL names only the variable, and a refused scheme or
// host names the variable and the lowercased hostname, never the userinfo,
// path, or query.
func hostOverride(name, value string) (url.URL, bool, error) {
	if value == "" {
		return url.URL{}, false, nil
	}
	parsed, err := url.Parse(value)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return url.URL{}, false, fmt.Errorf("rmcloud: ignoring %s: not a URL", name)
	}
	scheme := strings.ToLower(parsed.Scheme)
	host := strings.ToLower(parsed.Hostname())
	loopback := host == "localhost" || host == "127.0.0.1" || host == "::1"
	if scheme == "https" || (scheme == "http" && loopback) {
		return *parsed, true, nil
	}
	return url.URL{}, false, fmt.Errorf("rmcloud: ignoring %s for host %s: only HTTPS, or HTTP on the loopback", name, host)
}

// NewDeviceToken is where a one-time code becomes a device token.
func (e Endpoints) NewDeviceToken() url.URL {
	return joinPath(e.AuthHost, "token/json/2/device/new")
}

// NewUserToken is where a device token becomes a short-lived user token.
func (e Endpoints) NewUserToken() url.URL {
	return joinPath(e.AuthHost, "token/json/2/user/new")
}

// DeleteDeviceToken is where this device is taken off the account.
func (e Endpoints) DeleteDeviceToken() url.URL {
	return joinPath(e.AuthHost, "token/json/3/device/delete")
}

// Root is the read path for version. An unknown version is a programming
// error and panics.
func (e Endpoints) Root(version RootVersion) url.URL {
	switch version {
	case RootV3:
		return joinPath(e.SyncHost, "sync/v3/root")
	case RootV4:
		return joinPath(e.SyncHost, "sync/v4/root")
	default:
		panic("rmcloud: unknown root version")
	}
}

// RootWrite is the write path. It is always v3, whichever path answered
// the read.
func (e Endpoints) RootWrite() url.URL {
	return e.Root(RootV3)
}

// Blob is one content-addressed file. hash must satisfy [IsHash]; the sync
// client refuses any other before it builds this URL.
func (e Endpoints) Blob(hash string) url.URL {
	return joinPath(e.SyncHost, "sync/v3/files", hash)
}

// CheckFiles is the probe that asks which hashes the service already holds.
func (e Endpoints) CheckFiles() url.URL {
	return joinPath(e.SyncHost, "sync/v3/check-files")
}

// Discovery is the unauthenticated list of hosts. The auth host is never
// taken from that list.
func (e Endpoints) Discovery() url.URL {
	return joinPath(e.SyncHost, "discovery/v1/endpoints")
}

// Notifications is the receive-only socket path on the sync host. The
// stored URL keeps http or https. The dial converts the scheme.
func (e Endpoints) Notifications() url.URL {
	return NotificationsOn(e.SyncHost)
}

// NotificationsOn is the same socket path on a host somebody else named.
func NotificationsOn(host url.URL) url.URL {
	return joinPath(host, "notifications/ws/json/1")
}

// Tectonic is the sync host a token's tectonic claim names. The label must
// be two to eight ASCII letters after lowercasing. Digits and hyphens are
// refused: the value becomes a hostname this client sends a bearer token to.
func Tectonic(region string) (url.URL, bool) {
	label := strings.ToLower(region)
	n := utf8.RuneCountInString(label)
	if n < 2 || n > 8 {
		return url.URL{}, false
	}
	for _, r := range label {
		if r < 'a' || r > 'z' {
			return url.URL{}, false
		}
	}
	return mustURL("https://" + label + ".tectonic.remarkable.com"), true
}

// TrustedHost turns a bare hostname from a server answer into an HTTPS URL,
// or refuses it. The name is trimmed and lowercased. It must be one to 253
// runes, contain no slash, colon, or at-sign, and use only ASCII letters,
// digits, dots, and hyphens. It must end in .remarkable.com or
// .remarkable.engineering. The hostname of the URL we build must be the
// name we accepted.
func TrustedHost(value string) (url.URL, bool) {
	name := strings.ToLower(strings.TrimSpace(value))
	n := utf8.RuneCountInString(name)
	if n < 1 || n > 253 {
		return url.URL{}, false
	}
	if strings.ContainsAny(name, "/:@") {
		return url.URL{}, false
	}
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '.', r == '-':
		default:
			return url.URL{}, false
		}
	}
	if !strings.HasSuffix(name, ".remarkable.com") && !strings.HasSuffix(name, ".remarkable.engineering") {
		return url.URL{}, false
	}
	parsed, err := url.Parse("https://" + name)
	if err != nil || parsed.Hostname() != name {
		return url.URL{}, false
	}
	return *parsed, true
}

// RegistrationPage is the web page where the account holder gets a one-time code.
func RegistrationPage() url.URL {
	return mustURL("https://my.remarkable.com/device/desktop/connect")
}

func joinPath(base url.URL, elem ...string) url.URL {
	// JoinPath treats an empty path as relative and drops the leading slash.
	// A host with no path is still an absolute URL, and RequestURI needs the slash.
	if base.Path == "" {
		base.Path = "/"
	}
	// #nosec G125 -- every element is a constant or a hash checked by IsHash.
	joined := base.JoinPath(elem...)
	return *joined
}

func mustURL(text string) url.URL {
	parsed, err := url.Parse(text)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		panic("rmcloud: not a URL: " + text)
	}
	return *parsed
}
