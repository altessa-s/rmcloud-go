// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package syncstore

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/altessa-s/go-atlas-core/time/clock"
	"github.com/altessa-s/go-atlas-core/types/redacted"

	"github.com/altessa-s/rmcloud-go/internal/adapter/fs"
	"github.com/altessa-s/rmcloud-go/internal/app/retry"
	"github.com/altessa-s/rmcloud-go/internal/domain"
	"github.com/altessa-s/rmcloud-go/internal/port"
	"github.com/altessa-s/rmcloud-go/internal/porttest"
)

func TestNewPanicsWithoutADependency(t *testing.T) {
	t.Parallel()
	tokens := &fixedTokens{token: "user-1"}
	trip := &porttest.Transport{}
	files := fs.Files{}
	clk := porttest.NewClock()
	cases := []struct {
		name   string
		build  func()
		panics string
	}{
		{"tokens", func() { New(nil, trip, files, clk) }, "syncstore: Tokens is required"},
		{"transport", func() { New(tokens, nil, files, clk) }, "syncstore: Transport is required"},
		{"files", func() { New(tokens, trip, nil, clk) }, "syncstore: Files is required"},
		{"clock", func() { New(tokens, trip, files, clock.Clock(nil)) }, "syncstore: Clock is required"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			defer func() {
				if got := recover(); got != tc.panics {
					t.Fatalf("panic %v, want %q", got, tc.panics)
				}
			}()
			tc.build()
		})
	}
}

func TestDefaultHostsArePinnedProduction(t *testing.T) {
	t.Parallel()
	trip := &porttest.Transport{}
	trip.Enqueue(porttest.Reply{Err: domain.Transport(domain.ReasonCannotFindHost)})
	client := New(&fixedTokens{token: "user-1"}, trip, fs.Files{}, porttest.NewClock(), WithPolicy(retry.Policy{MaxAttempts: 1}))

	// The pinned source cannot be demoted: the failure is only returned.
	_, err := client.Blob(t.Context(), "ab", "doc.pdf", false)
	wantEqual(t, err, domain.Transport(domain.ReasonCannotFindHost))
	calls := trip.Calls()
	if len(calls) != 1 || calls[0].URL.Host != domain.Production().SyncHost.Host {
		t.Fatalf("calls %+v", calls)
	}
}

func TestSecondTokenFailureAfterUnauthorizedIsReturned(t *testing.T) {
	t.Parallel()
	tokens := &seqTokens{tokens: []string{"user-1"}}
	r := newRig(t, tokens)
	r.trip.Enqueue(porttest.Reply{Status: 401})

	_, err := r.client.Blob(t.Context(), "ab", "doc.pdf", false)
	if err == nil || errors.Is(err, domain.ErrUnauthorized) {
		t.Fatalf("%v", err)
	}
	if got := tokens.refusedTokens(); !slices.Equal(got, []string{"user-1"}) {
		t.Fatalf("refused %q", got)
	}
	if len(r.trip.Calls()) != 1 {
		t.Fatalf("calls %d", len(r.trip.Calls()))
	}
}

func TestTokenFailureStopsBeforeTheWire(t *testing.T) {
	t.Parallel()
	r := newRig(t, &seqTokens{})
	if _, err := r.client.PutRoot(t.Context(), "aa", 1, false); err == nil {
		t.Fatal("a write went out without a token")
	}
	if len(r.trip.Calls()) != 0 {
		t.Fatalf("calls %d", len(r.trip.Calls()))
	}
}

func TestRootIndexStopsAtTheFirstFailure(t *testing.T) {
	t.Parallel()
	t.Run("root", func(t *testing.T) {
		t.Parallel()
		r := newRig(t, nil, WithPolicy(retry.Policy{MaxAttempts: 1}))
		r.trip.Enqueue(porttest.Reply{Status: 400})
		_, _, err := r.client.RootIndex(t.Context())
		wantEqual(t, err, domain.HTTP(400, "/sync/v4/root"))
		wantPaths(t, r.trip.Calls(), "/sync/v4/root")
	})
	t.Run("index", func(t *testing.T) {
		t.Parallel()
		r := newRig(t, nil, WithPolicy(retry.Policy{MaxAttempts: 1}))
		r.trip.Enqueue(
			porttest.Reply{Body: rootBody("abcd", 2, "")},
			porttest.Reply{Status: 404},
		)
		_, _, err := r.client.RootIndex(t.Context())
		wantEqual(t, err, domain.HTTP(404, "/sync/v3/files/abcd"))
	})
}

// memoryDownload answers Download with the body in memory and no file, as
// a transport that refuses before writing would.
type memoryDownload struct {
	*porttest.Transport
}

func (m memoryDownload) Download(ctx context.Context, req port.Request) (port.Response, error) {
	return m.RoundTrip(ctx, req)
}

func TestRefusedDownloadDetailIsBounded(t *testing.T) {
	t.Parallel()
	body := bytes.Repeat([]byte("x"), domain.DetailLimit+64)
	trip := &porttest.Transport{}
	trip.Enqueue(porttest.Reply{Status: 402, Body: body})
	client := New(&fixedTokens{token: "user-1"}, memoryDownload{trip}, fs.Files{}, porttest.NewClock(), WithPolicy(fastPolicy))

	_, err := client.BlobFile(t.Context(), "ab", "doc.pdf", 0, nil)
	wantEqual(t, err, domain.NotOnThisPlan(domain.DetailOf(body[:domain.DetailLimit])))
}

func TestUploadFileThatCannotBeReadSendsNothing(t *testing.T) {
	t.Parallel()
	r := newRig(t, nil)
	_, err := r.client.UploadFile(t.Context(), filepath.Join(t.TempDir(), "missing"), "doc.pdf", nil)
	if !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("%v", err)
	}
	if len(r.trip.Calls()) != 0 {
		t.Fatalf("calls %d", len(r.trip.Calls()))
	}
}

func TestUploadReturnsARawTransportError(t *testing.T) {
	t.Parallel()
	r := newRig(t, nil, WithPolicy(retry.Policy{MaxAttempts: 1}))
	target := domain.Transport(domain.ReasonNotConnectedToInternet)
	r.trip.Enqueue(porttest.Reply{Err: target})
	_, err := r.client.Upload(t.Context(), []byte("doc"), "doc.pdf", "")
	wantEqual(t, err, target)
}

// failingTokens refuses every token request with err.
type failingTokens struct{ err error }

func (f failingTokens) UserToken(context.Context) (redacted.RedactedString, error) { return "", f.err }

func (failingTokens) InvalidateUserToken(redacted.RedactedString) {}

func TestMissingFilesTokenFailureIsReturned(t *testing.T) {
	t.Parallel()
	refused := errors.New("rmcloud: no token")
	r := newRig(t, failingTokens{err: refused})
	if _, err := r.client.MissingFiles(t.Context(), []string{"ab"}); !errors.Is(err, refused) {
		t.Fatalf("%v", err)
	}
	if len(r.trip.Calls()) != 0 {
		t.Fatalf("calls %d", len(r.trip.Calls()))
	}
}
