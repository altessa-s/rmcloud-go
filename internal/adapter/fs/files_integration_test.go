//go:build integration

// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

// Integration tests that run the file adapter on a real temporary directory.

package fs_test

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/altessa-s/rmcloud-go/internal/adapter/fs"
	"github.com/altessa-s/rmcloud-go/internal/domain"

	iofs "io/fs"
)

func itFile(t *testing.T, body []byte) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "blob.bin")
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestIntegration_DigestMatchesTheBytesOnDisk(t *testing.T) {
	t.Parallel()

	// Several hash chunks, with a short last one.
	body := bytes.Repeat([]byte("rmcloud "), 3<<17+3)
	path := itFile(t, body)

	got, err := fs.Files{}.Digest(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	want, err := domain.DigestReader(t.Context(), bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	if got != want || got.SHA256 != domain.BlobHash(body) || got.CRC32C != domain.CRC32C(body) {
		t.Fatalf("got %+v want %+v", got, want)
	}

	empty, err := fs.Files{}.Digest(t.Context(), itFile(t, nil))
	if err != nil || empty.SHA256 != domain.BlobHash(nil) {
		t.Fatalf("%+v %v", empty, err)
	}
}

func TestIntegration_DigestFailures(t *testing.T) {
	t.Parallel()

	if _, err := (fs.Files{}).Digest(t.Context(), filepath.Join(t.TempDir(), "absent")); !errors.Is(err, iofs.ErrNotExist) {
		t.Fatalf("missing file: %v", err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := (fs.Files{}).Digest(ctx, itFile(t, []byte("x"))); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled: %v", err)
	}
	if _, err := (fs.Files{}).Digest(t.Context(), t.TempDir()); err == nil {
		t.Fatal("a directory has a digest")
	}
}

func TestIntegration_HeadReadsAtMostN(t *testing.T) {
	t.Parallel()

	path := itFile(t, []byte("0123456789"))
	for n, want := range map[int]string{0: "", 4: "0123", 10: "0123456789", 64: "0123456789"} {
		got, err := fs.Files{}.Head(path, n)
		if err != nil || string(got) != want {
			t.Errorf("Head(%d) = %q, %v; want %q", n, got, err, want)
		}
	}
	if _, err := (fs.Files{}).Head(filepath.Join(t.TempDir(), "absent"), 4); !errors.Is(err, iofs.ErrNotExist) {
		t.Fatalf("missing file: %v", err)
	}
}

func TestIntegration_RemoveIsIdempotent(t *testing.T) {
	t.Parallel()

	path := itFile(t, []byte("x"))
	if err := (fs.Files{}).Remove(path); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !errors.Is(err, iofs.ErrNotExist) {
		t.Fatalf("still there: %v", err)
	}
	if err := (fs.Files{}).Remove(path); err != nil {
		t.Fatalf("a second remove: %v", err)
	}

	// A non-empty directory is a real failure, not a missing file.
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "child"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := (fs.Files{}).Remove(dir); err == nil {
		t.Fatal("a non-empty directory was removed")
	}
}

func TestIntegration_HostVersion(t *testing.T) {
	t.Parallel()

	v := fs.HostVersion()
	if runtime.GOOS != "darwin" {
		if v != "" {
			t.Fatalf("%q off macOS", v)
		}
		return
	}
	if v == "" || domain.SystemVersion(v) != v {
		t.Fatalf("%q", v)
	}
}
