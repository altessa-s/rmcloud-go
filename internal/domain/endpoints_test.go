// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package domain

import (
	"fmt"
	"net/url"
	"strings"
	"testing"
)

// urlText calls String on an addressable copy. A URL returned from a function is not addressable, and String has a pointer receiver.
func urlText(u url.URL) string { return u.String() }

func TestProductionPaths(t *testing.T) {
	e := Production()
	if e.AuthHost.String() != "https://webapp-prod.cloud.remarkable.engineering" {
		t.Fatal(e.AuthHost.String())
	}
	if e.SyncHost.String() != "https://internal.cloud.remarkable.com" {
		t.Fatal(e.SyncHost.String())
	}
	if got := urlText(e.NewDeviceToken()); got != "https://webapp-prod.cloud.remarkable.engineering/token/json/2/device/new" {
		t.Fatal(got)
	}
	if got := urlText(e.NewUserToken()); !strings.HasSuffix(got, "/token/json/2/user/new") {
		t.Fatal(got)
	}
	if got := urlText(e.DeleteDeviceToken()); !strings.HasSuffix(got, "/token/json/3/device/delete") {
		t.Fatal(got)
	}
	root := e.Root(RootV4)
	if got := urlText(root); got != "https://internal.cloud.remarkable.com/sync/v4/root" {
		t.Fatal(got)
	}
	if root.RequestURI() != "/sync/v4/root" {
		t.Fatal(root.RequestURI())
	}
	if urlText(e.RootWrite()) != urlText(e.Root(RootV3)) {
		t.Fatal("root write is always v3")
	}
	if got := urlText(e.Blob("abc")); got != "https://internal.cloud.remarkable.com/sync/v3/files/abc" {
		t.Fatal(got)
	}
	if got := urlText(e.CheckFiles()); !strings.HasSuffix(got, "/sync/v3/check-files") {
		t.Fatal(got)
	}
	if got := urlText(e.Discovery()); !strings.HasSuffix(got, "/discovery/v1/endpoints") {
		t.Fatal(got)
	}
	if got := urlText(e.Notifications()); got != "https://internal.cloud.remarkable.com/notifications/ws/json/1" {
		t.Fatal(got)
	}
	if got := urlText(RegistrationPage()); got != "https://my.remarkable.com/device/desktop/connect" {
		t.Fatal(got)
	}
}

func TestFromEnvironment(t *testing.T) {
	e := mustEnv(t, map[string]string{
		"RMCLOUD_HOST":      "https://both.example",
		"RMCLOUD_AUTH_HOST": "https://auth.example",
		"RMCLOUD_SYNC_HOST": "",
	})
	if e.AuthHost.Host != "auth.example" || e.SyncHost.Host != "both.example" {
		t.Fatalf("auth %s sync %s", e.AuthHost.Host, e.SyncHost.Host)
	}
	e = mustEnv(t, map[string]string{
		"RMCLOUD_SYNC_HOST": "http://127.0.0.1:9",
	})
	if e.SyncHost.String() != "http://127.0.0.1:9" {
		t.Fatal(e.SyncHost.String())
	}
	e = mustEnv(t, map[string]string{"RMCLOUD_HOST": "HTTP://LOCALHOST:9/base"})
	if e.AuthHost.String() != "http://LOCALHOST:9/base" {
		t.Fatal(e.AuthHost.String())
	}
	// url.Parse lowercases the scheme. The hostname stays as written.
	if e.AuthHost.Scheme != "http" || e.AuthHost.Hostname() != "LOCALHOST" {
		t.Fatalf("case = %s %s", e.AuthHost.Scheme, e.AuthHost.Hostname())
	}
	e = mustEnv(t, map[string]string{"RMCLOUD_SYNC_HOST": "http://[::1]:9"})
	if e.SyncHost.Hostname() != "::1" {
		t.Fatal(e.SyncHost.String())
	}

	e, err := FromEnvironment(map[string]string{
		"RMCLOUD_HOST":      "http://user:secret-token@example.com/path",
		"RMCLOUD_SYNC_HOST": "https://sync.example",
	})
	if e.AuthHost.Host != Production().AuthHost.Host {
		t.Fatal("rejected override replaced the host")
	}
	if e.SyncHost.Host != "sync.example" {
		t.Fatal("a refused variable stopped the accepted one")
	}
	msg := fmt.Sprint(err)
	if !strings.Contains(msg, "ignoring RMCLOUD_HOST for host example.com: only HTTPS, or HTTP on the loopback") {
		t.Fatalf("err = %s", msg)
	}
	if strings.Contains(msg, "secret-token") || strings.Contains(msg, "user:") {
		t.Fatalf("secret leaked: %s", msg)
	}

	_, err = FromEnvironment(map[string]string{
		"RMCLOUD_AUTH_HOST": "not a url secret-value",
		"RMCLOUD_SYNC_HOST": "ftp://sync.example",
	})
	msg = fmt.Sprint(err)
	if !strings.Contains(msg, "ignoring RMCLOUD_AUTH_HOST: not a URL") || !strings.Contains(msg, "RMCLOUD_SYNC_HOST") {
		t.Fatalf("err = %s", msg)
	}
	if strings.Contains(msg, "secret-value") || strings.Contains(msg, "not a url") {
		t.Fatalf("raw value leaked: %s", msg)
	}
}

func mustEnv(t *testing.T, env map[string]string) Endpoints {
	t.Helper()
	e, err := FromEnvironment(env)
	if err != nil {
		t.Fatal(err)
	}
	return e
}

func TestUnknownRootVersionPanics(t *testing.T) {
	defer func() {
		got := recover()
		if got != "rmcloud: unknown root version" {
			t.Fatalf("panic = %v", got)
		}
	}()
	Production().Root("v9")
}

func TestTectonicAndTrustedHost(t *testing.T) {
	u, ok := Tectonic("EU")
	if !ok || u.String() != "https://eu.tectonic.remarkable.com" {
		t.Fatalf("%s %v", u.String(), ok)
	}
	for _, bad := range []string{"a", "abcdefghi", "eu1", "eu-west", ""} {
		if _, ok := Tectonic(bad); ok {
			t.Fatalf("accepted %q", bad)
		}
	}
	u, ok = TrustedHost(" eu.remarkable.com ")
	if !ok || u.Hostname() != "eu.remarkable.com" || u.Scheme != "https" {
		t.Fatalf("%s %v", u.String(), ok)
	}
	if _, ok := TrustedHost("eu.remarkable.engineering"); !ok {
		t.Fatal("engineering host")
	}
	long := strings.Repeat("a", 240) + ".remarkable.com"
	for _, bad := range []string{
		"eu.remarkable.com/path",
		"eu.remarkable.com:443",
		"user@eu.remarkable.com",
		long,
		"remarkable.com",
		"notremarkable.com",
		"foo.remarkable.com.evil.com",
		"",
	} {
		if _, ok := TrustedHost(bad); ok {
			t.Fatalf("accepted %q", bad)
		}
	}
}

func FuzzTrustedHost(f *testing.F) {
	f.Add("eu.remarkable.com")
	f.Add("eu.remarkable.engineering")
	f.Add("eu.remarkable.com:443")
	f.Add("user@eu.remarkable.com")
	f.Fuzz(func(t *testing.T, raw string) {
		u, ok := TrustedHost(raw)
		if !ok {
			return
		}
		if u.Scheme != "https" || u.User != nil || u.Path != "" {
			t.Fatalf("unexpected URL %+v from %q", u, raw)
		}
	})
}
