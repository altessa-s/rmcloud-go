// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package fs

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/altessa-s/rmcloud-go/internal/domain"
)

func TestFilesDigest(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "blob")
	if err := os.WriteFile(path, []byte("abc"), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := Files{}.Digest(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	if got.SHA256 != domain.BlobHash([]byte("abc")) || got.CRC32C != domain.CRC32C([]byte("abc")) {
		t.Fatalf("digest = %+v", got)
	}
	// The file is opened first, so a missing file is an os error even when
	// ctx is already done.
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := (Files{}).Digest(ctx, filepath.Join(t.TempDir(), "missing")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("missing = %v", err)
	}
	if _, err := (Files{}).Digest(ctx, path); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled = %v", err)
	}
}

func TestFilesHeadAndRemove(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "body")
	if err := os.WriteFile(path, []byte(strings.Repeat("x", 600)), 0o600); err != nil {
		t.Fatal(err)
	}
	head, err := Files{}.Head(path, 500)
	if err != nil || len(head) != 500 {
		t.Fatalf("head %d %v", len(head), err)
	}
	if _, err := (Files{}).Head(filepath.Join(t.TempDir(), "missing"), 1); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("missing head = %v", err)
	}
	if err := (Files{}).Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := (Files{}).Remove(path); err != nil {
		t.Fatalf("second remove = %v", err)
	}
}

func TestHostVersion(t *testing.T) {
	t.Parallel()
	got := HostVersion()
	if runtime.GOOS != "darwin" {
		if got != "" {
			t.Fatalf("version off darwin = %q", got)
		}
		return
	}
	if got == "" || strings.HasSuffix(got, ".0") && strings.Count(got, ".") == 2 {
		t.Fatalf("version = %q", got)
	}
}

func TestFilesRemoveReportsARealFailure(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "child"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := (Files{}).Remove(dir); err == nil {
		t.Fatal("a non-empty directory was removed")
	}
}
