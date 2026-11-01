// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package syncstore

import (
	"slices"
	"testing"

	"github.com/altessa-s/rmcloud-go/internal/domain"
	"github.com/altessa-s/rmcloud-go/internal/porttest"
)

func TestMissingFilesAsksAndReturnsTheSubset(t *testing.T) {
	t.Parallel()
	r := newRig(t, nil)
	r.trip.Enqueue(porttest.Reply{Body: []byte(`{"missingFiles":["bb"]}`)})
	got, err := r.client.MissingFiles(t.Context(), []string{"aa", "BB"})
	must(t, err)
	if !slices.Equal(got, []string{"BB"}) {
		t.Fatalf("missing %q", got)
	}
	calls := r.trip.Calls()
	if len(calls) != 1 {
		t.Fatal(len(calls))
	}
	req := calls[0]
	if req.Method != domain.MethodPost || req.URL.Path != "/sync/v3/check-files" {
		t.Fatalf("%s %s", req.Method, req.URL.Path)
	}
	if string(req.Body) != `{"files":["aa","BB"]}` || req.Header.Get("Content-Type") != domain.ContentTypeJSON {
		t.Fatalf("body %s type %q", req.Body, req.Header.Get("Content-Type"))
	}
	if req.Header.Get(domain.HeaderAuthorization) != "Bearer user-1" || req.Header.Get(domain.HeaderFilename) != "" {
		t.Fatalf("header %v", req.Header)
	}
}

func TestMissingFilesRefusesBeforeTheWire(t *testing.T) {
	t.Parallel()
	tokens := &fixedTokens{token: "user-1"}
	r := newRig(t, tokens)
	_, err := r.client.MissingFiles(t.Context(), nil)
	wantEqual(t, err, domain.NothingToAsk(domain.CheckFilesCall))
	_, err = r.client.MissingFiles(t.Context(), []string{"aa", "../token"})
	wantEqual(t, err, domain.MissingHash(domain.CheckFilesCall))
	if len(r.trip.Calls()) != 0 || tokens.buys != 0 || r.hosts.reads != 0 {
		t.Fatalf("calls %d buys %d reads %d", len(r.trip.Calls()), tokens.buys, r.hosts.reads)
	}
}

func TestMissingFilesRetriesAServerError(t *testing.T) {
	t.Parallel()
	r := newRig(t, nil)
	r.trip.Enqueue(porttest.Reply{Status: 503}, porttest.Reply{Body: []byte(`{"missingFiles":[]}`)})
	got, err := r.client.MissingFiles(t.Context(), []string{"aa"})
	must(t, err)
	if got == nil || len(got) != 0 {
		t.Fatalf("missing %#v", got)
	}
	if len(r.trip.Calls()) != 2 {
		t.Fatal(len(r.trip.Calls()))
	}
}

func TestMissingFilesDoesNotRetryABadRequest(t *testing.T) {
	t.Parallel()
	r := newRig(t, nil)
	r.trip.Enqueue(porttest.Reply{Status: 400})
	_, err := r.client.MissingFiles(t.Context(), []string{"aa"})
	wantEqual(t, err, domain.HTTP(400, "/sync/v3/check-files"))
	if len(r.trip.Calls()) != 1 {
		t.Fatal(len(r.trip.Calls()))
	}
}
