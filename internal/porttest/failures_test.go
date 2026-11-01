// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package porttest

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/altessa-s/rmcloud-go/internal/domain"
	"github.com/altessa-s/rmcloud-go/internal/port"
)

func TestNewSecretsWithoutInitialValues(t *testing.T) {
	t.Parallel()
	s := NewSecrets(nil)
	ctx := t.Context()
	if _, ok, err := s.Secret(ctx, domain.SecretUserToken); ok || err != nil {
		t.Fatalf("ok %v err %v", ok, err)
	}
	if err := s.Save(ctx, domain.SecretUserToken, "token"); err != nil {
		t.Fatal(err)
	}
	if got, ok, err := s.Secret(ctx, domain.SecretUserToken); !ok || err != nil || got != "token" {
		t.Fatalf("%q ok %v err %v", got, ok, err)
	}
}

func TestUploadFailures(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "body")
	if err := os.WriteFile(path, []byte("bytes"), 0o600); err != nil {
		t.Fatal(err)
	}
	refused := errors.New("refused")
	canceled, cancel := context.WithCancel(t.Context())
	cancel()
	cases := []struct {
		name  string
		ctx   context.Context
		file  string
		reply *Reply
		want  error
		calls int
	}{
		{"canceled before reading", canceled, path, nil, context.Canceled, 0},
		{"missing file", t.Context(), filepath.Join(t.TempDir(), "missing"), nil, os.ErrNotExist, 0},
		{"exchange fails", t.Context(), path, &Reply{Err: refused}, refused, 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			transport := &Transport{}
			if tc.reply != nil {
				transport.Enqueue(*tc.reply)
			}
			progressed := false
			_, err := transport.Upload(tc.ctx, port.Request{File: tc.file, Progress: func(int64, int64) { progressed = true }})
			if !errors.Is(err, tc.want) {
				t.Fatalf("%v, want %v", err, tc.want)
			}
			if progressed || len(transport.Calls()) != tc.calls {
				t.Fatalf("progress %v calls %d", progressed, len(transport.Calls()))
			}
		})
	}
}

func TestWriteTempIntoAMissingDirectory(t *testing.T) {
	t.Parallel()
	dir := filepath.Join(t.TempDir(), "missing")
	if _, err := writeTemp(dir, []byte("body")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("%v", err)
	}
	if _, err := os.Stat(dir); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("directory created: %v", err)
	}
}
