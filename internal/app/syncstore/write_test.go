// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package syncstore

import (
	"bytes"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/altessa-s/rmcloud-go/internal/app/retry"
	"github.com/altessa-s/rmcloud-go/internal/domain"
	"github.com/altessa-s/rmcloud-go/internal/port"
	"github.com/altessa-s/rmcloud-go/internal/porttest"
)

func uploadOnce(t *testing.T, file bool, data []byte, name, contentType string, replies ...porttest.Reply) (string, *rig, error) {
	t.Helper()
	r := newRig(t, nil)
	r.trip.Enqueue(replies...)
	if file {
		path := writeTempFile(t, name, data)
		hash, err := r.client.UploadFile(t.Context(), path, name, nil)
		return hash, r, err
	}
	hash, err := r.client.Upload(t.Context(), data, name, contentType)
	return hash, r, err
}

func TestRootWriteRepeatsAfterRateLimit(t *testing.T) {
	t.Parallel()
	r := newRig(t, nil)
	r.trip.Enqueue(
		porttest.Reply{Body: rootBody("aa", 41, "")},
		porttest.Reply{Status: 429, Header: port.Header{"Retry-After": {"0"}}},
		porttest.Reply{Body: rootBody("aa", 42, "")},
	)
	_, err := r.client.Root(t.Context())
	must(t, err)
	gen, err := r.client.PutRoot(t.Context(), "aa", 41, true)
	must(t, err)
	if gen != 42 {
		t.Fatal(gen)
	}
	wrote := puts(r.trip.Calls())
	if len(wrote) != 2 {
		t.Fatal(len(wrote))
	}
	wantPaths(t, wrote, "/sync/v3/root", "/sync/v3/root")
	wantDelays(t, r.sleeper.Delays(), []time.Duration{fastPolicy.BaseDelay})
	if r.hosts.reads != 2 {
		t.Fatal(r.hosts.reads)
	}
}

func TestRootWriteHonorsAPositiveRetryAfter(t *testing.T) {
	t.Parallel()
	r := newRig(t, nil)
	r.trip.Enqueue(
		porttest.Reply{Status: 429, Header: port.Header{"Retry-After": {"1"}}},
		porttest.Reply{Body: rootBody("aa", 2, "")},
	)
	gen, err := r.client.PutRoot(t.Context(), "aa", 1, true)
	must(t, err)
	if gen != 2 {
		t.Fatal(gen)
	}
	if len(puts(r.trip.Calls())) != 2 {
		t.Fatal(len(puts(r.trip.Calls())))
	}
	wantDelays(t, r.sleeper.Delays(), []time.Duration{time.Second})
	if r.hosts.reads != 1 {
		t.Fatal(r.hosts.reads)
	}
}

func TestRootWriteStopsAtOneAttempt(t *testing.T) {
	t.Parallel()
	r := newRig(t, nil, WithPolicy(retry.Policy{MaxAttempts: 1}))
	r.trip.Enqueue(porttest.Reply{Status: 429, Header: port.Header{"Retry-After": {"0"}}})
	_, err := r.client.PutRoot(t.Context(), "aa", 1, true)
	wantEqual(t, err, domain.RateLimited(domain.ParseRetryAfter("0")))
	if len(puts(r.trip.Calls())) != 1 {
		t.Fatal(len(puts(r.trip.Calls())))
	}
	wantDelays(t, r.sleeper.Delays(), nil)
}

func TestRootWriteUsesTwoDelays(t *testing.T) {
	t.Parallel()
	r := newRig(t, nil)
	r.trip.Enqueue(
		porttest.Reply{Status: 429, Header: port.Header{"Retry-After": {"0"}}},
		porttest.Reply{Status: 429, Header: port.Header{"Retry-After": {"0"}}},
		porttest.Reply{Status: 429, Header: port.Header{"Retry-After": {"0"}}},
	)
	_, err := r.client.PutRoot(t.Context(), "aa", 1, true)
	wantEqual(t, err, domain.RateLimited(domain.ParseRetryAfter("0")))
	if len(puts(r.trip.Calls())) != 3 {
		t.Fatal(len(puts(r.trip.Calls())))
	}
	wantDelays(t, r.sleeper.Delays(), []time.Duration{
		10 * time.Millisecond,
		30 * time.Millisecond,
	})
}

func TestZeroPolicyStopsARateLimit(t *testing.T) {
	t.Parallel()
	r := newRig(t, nil, WithPolicy(retry.Policy{}))
	r.trip.Enqueue(porttest.Reply{Status: 429, Header: port.Header{"Retry-After": {"0"}}})
	_, err := r.client.PutRoot(t.Context(), "aa", 1, true)
	wantEqual(t, err, domain.RateLimited(domain.ParseRetryAfter("0")))
	if len(puts(r.trip.Calls())) != 1 {
		t.Fatal(len(puts(r.trip.Calls())))
	}
}

func TestRootIsWrittenToV3WhateverTheReadFound(t *testing.T) {
	t.Parallel()
	r := newRig(t, nil)
	r.trip.Enqueue(
		porttest.Reply{Body: rootBody("old", 42, "")},
		porttest.Reply{Body: rootBody("newhash", 43, "")},
	)
	_, err := r.client.Root(t.Context())
	must(t, err)
	gen, err := r.client.PutRoot(t.Context(), "newhash", 43, true)
	must(t, err)
	if gen != 43 {
		t.Fatal(gen)
	}
	wrote := puts(r.trip.Calls())
	if len(wrote) != 1 || wrote[0].URL.Path != "/sync/v3/root" {
		t.Fatalf("%d %s", len(wrote), wrote[0].URL.Path)
	}
	wantFilename(t, wrote, domain.RootHashName)
	contentType, ok := headerOne(wrote[0].Header, "Content-Type")
	if !ok || contentType != domain.ContentTypeJSON {
		t.Fatalf("content type %q", contentType)
	}
	if string(wrote[0].Body) != `{"broadcast":true,"generation":43,"hash":"newhash"}` {
		t.Fatalf("%s", wrote[0].Body)
	}
	for key := range wrote[0].Header {
		if strings.EqualFold(key, domain.HeaderGoogHash) {
			t.Fatalf("goog hash %v", wrote[0].Header[key])
		}
	}
}

func TestRootWriteNeedsNoPriorRead(t *testing.T) {
	t.Parallel()
	r := newRig(t, nil)
	r.trip.Enqueue(porttest.Reply{Body: rootBody("newhash", 9, "")})
	gen, err := r.client.PutRoot(t.Context(), "newhash", 8, true)
	must(t, err)
	if gen != 9 {
		t.Fatal(gen)
	}
	wrote := puts(r.trip.Calls())
	if len(wrote) != 1 || wrote[0].URL.Path != "/sync/v3/root" {
		t.Fatal(wrote)
	}
	if r.hosts.reads != 1 {
		t.Fatal(r.hosts.reads)
	}
}

func TestRootWriteDoesNotRepeatAfterServerError(t *testing.T) {
	t.Parallel()
	r := newRig(t, nil)
	r.trip.Enqueue(porttest.Reply{Status: 503})
	_, err := r.client.PutRoot(t.Context(), "aa", 1, true)
	wantEqual(t, err, domain.WriteRejected(503, domain.RootHashName+writeDash+"(empty body)"))
	if len(puts(r.trip.Calls())) != 1 {
		t.Fatal(len(puts(r.trip.Calls())))
	}
	wantDelays(t, r.sleeper.Delays(), nil)
}

func TestRootWriteKeepsAServerDetail(t *testing.T) {
	t.Parallel()
	r := newRig(t, nil)
	r.trip.Enqueue(porttest.Reply{Status: 503, Body: []byte("temporarily unavailable")})
	_, err := r.client.PutRoot(t.Context(), "aa", 1, true)
	wantEqual(t, err, domain.WriteRejected(503, domain.RootHashName+writeDash+"temporarily unavailable"))
	if len(puts(r.trip.Calls())) != 1 {
		t.Fatal(len(puts(r.trip.Calls())))
	}
	wantDelays(t, r.sleeper.Delays(), nil)
}

func TestUploadRetriesServerErrors(t *testing.T) {
	t.Parallel()
	for _, status := range []int{500, 503, 599} {
		for _, file := range []bool{false, true} {
			t.Run(fmt.Sprintf("%d/%t", status, file), func(t *testing.T) {
				t.Parallel()
				data := []byte("payload")
				hash, r, err := uploadOnce(t, file, data, "test.pdf", "image/png",
					porttest.Reply{Status: status},
					porttest.Reply{},
				)
				must(t, err)
				if hash != domain.BlobHash(data) {
					t.Fatal(hash)
				}
				wantType := "image/png"
				if file {
					wantType = domain.ContentTypeBlob
				}
				wrote := puts(r.trip.Calls())
				if len(wrote) != 2 {
					t.Fatal(len(wrote))
				}
				for _, call := range wrote {
					if !bytes.Equal(call.Body, data) {
						t.Fatalf("%q", call.Body)
					}
					goog, ok := headerOne(call.Header, domain.HeaderGoogHash)
					if !ok || goog != domain.GoogHash(data) {
						t.Fatalf("goog %q", goog)
					}
					if call.URL.Path != "/sync/v3/files/"+hash {
						t.Fatal(call.URL.Path)
					}
					contentType, ok := headerOne(call.Header, "Content-Type")
					if !ok || contentType != wantType {
						t.Fatalf("content type %q", contentType)
					}
				}
				wantDelays(t, r.sleeper.Delays(), []time.Duration{fastPolicy.BaseDelay})
			})
		}
	}
}

func TestUploadDoesNotRetryRejectedWrites(t *testing.T) {
	t.Parallel()
	for _, status := range []int{400, 422} {
		for _, file := range []bool{false, true} {
			t.Run(fmt.Sprintf("%d/%t", status, file), func(t *testing.T) {
				t.Parallel()
				body := []byte("temporarily unavailable")
				_, r, err := uploadOnce(t, file, []byte("payload"), "test.pdf", "image/png",
					porttest.Reply{Status: status, Body: body},
				)
				wantEqual(t, err, domain.WriteRejected(status, "test.pdf"+writeDash+"temporarily unavailable"))
				if len(puts(r.trip.Calls())) != 1 {
					t.Fatal(len(puts(r.trip.Calls())))
				}
				wantDelays(t, r.sleeper.Delays(), nil)
			})
		}
	}
}

func TestUploadExhaustsServerErrors(t *testing.T) {
	t.Parallel()
	r := newRig(t, nil)
	r.trip.Enqueue(
		porttest.Reply{Status: 503},
		porttest.Reply{Status: 503},
		porttest.Reply{Status: 503},
	)
	_, err := r.client.Upload(t.Context(), []byte("payload"), "test.pdf", "")
	wantEqual(t, err, domain.WriteRejected(503, "test.pdf"+writeDash+"(empty body)"))
	if len(puts(r.trip.Calls())) != 3 {
		t.Fatal(len(puts(r.trip.Calls())))
	}
	wantDelays(t, r.sleeper.Delays(), []time.Duration{
		10 * time.Millisecond,
		30 * time.Millisecond,
	})
}

func TestRootConflictIsNotARejectedWrite(t *testing.T) {
	t.Parallel()
	for _, status := range []int{409, 412} {
		t.Run(strconv.Itoa(status), func(t *testing.T) {
			t.Parallel()
			r := newRig(t, nil)
			r.trip.Enqueue(porttest.Reply{Status: status})
			_, err := r.client.PutRoot(t.Context(), "aa", 1, true)
			wantEqual(t, err, domain.RootConflict(status))
			if len(puts(r.trip.Calls())) != 1 {
				t.Fatal(len(puts(r.trip.Calls())))
			}
			if r.hosts.reads != 1 {
				t.Fatal(r.hosts.reads)
			}
		})
	}
}

func TestPutRootRefreshesAfterUnauthorized(t *testing.T) {
	t.Parallel()
	tokens := &seqTokens{tokens: []string{"user-1", "user-2"}}
	r := newRig(t, tokens)
	r.trip.Enqueue(
		porttest.Reply{Status: 401},
		porttest.Reply{Body: rootBody("aa", 4, "")},
	)
	gen, err := r.client.PutRoot(t.Context(), "aa", 3, false)
	must(t, err)
	if gen != 4 {
		t.Fatal(gen)
	}
	wrote := puts(r.trip.Calls())
	if len(wrote) != 2 {
		t.Fatal(len(wrote))
	}
	wantBearer(t, wrote[0], "user-1")
	wantBearer(t, wrote[1], "user-2")
	if got := tokens.refusedTokens(); len(got) != 1 || got[0] != "user-1" {
		t.Fatalf("%v", got)
	}
	wantDelays(t, r.sleeper.Delays(), nil)
	for _, call := range r.trip.Calls() {
		if call.Method == http.MethodGet {
			t.Fatal("unexpected GET")
		}
	}
}

func TestPutRootReturnsARawTransportError(t *testing.T) {
	t.Parallel()
	boom := errors.New("connection reset")
	r := newRig(t, nil)
	r.trip.Enqueue(porttest.Reply{Err: boom})
	_, err := r.client.PutRoot(t.Context(), "aa", 1, true)
	if _, ok := domain.KindOf(err); ok {
		t.Fatal(err)
	}
	if !errors.Is(err, boom) {
		t.Fatal(err)
	}
	if len(r.trip.Calls()) != 1 || r.hosts.failed != 0 {
		t.Fatalf("calls %d failed %d", len(r.trip.Calls()), r.hosts.failed)
	}
}

func TestPutRootDoesNotDemoteOnHostNotThere(t *testing.T) {
	t.Parallel()
	target := domain.Transport(domain.ReasonCannotFindHost)
	r := newRig(t, nil)
	r.trip.Enqueue(porttest.Reply{Err: target})
	_, err := r.client.PutRoot(t.Context(), "aa", 1, true)
	wantEqual(t, err, target)
	if r.hosts.failed != 0 {
		t.Fatal(r.hosts.failed)
	}
}

func TestEmptyUserAgentOmitsTheHeader(t *testing.T) {
	t.Parallel()
	r := newRig(t, nil, WithUserAgent(""))
	r.trip.Enqueue(porttest.Reply{Body: rootBody("aa", 2, "")})
	_, err := r.client.PutRoot(t.Context(), "aa", 1, true)
	must(t, err)
	if _, ok := r.trip.Calls()[0].Header[domain.HeaderUserAgent]; ok {
		t.Fatal(r.trip.Calls()[0].Header[domain.HeaderUserAgent])
	}
}

func TestUploadNotOnThisPlan(t *testing.T) {
	t.Parallel()
	body := []byte("need connect")
	r := newRig(t, nil)
	r.trip.Enqueue(porttest.Reply{Status: 402, Body: body})
	_, err := r.client.Upload(t.Context(), []byte("payload"), "test.pdf", "")
	wantEqual(t, err, domain.NotOnThisPlan(domain.DetailOf(body)))
	if len(puts(r.trip.Calls())) != 1 {
		t.Fatal(len(puts(r.trip.Calls())))
	}
	wantDelays(t, r.sleeper.Delays(), nil)
}

func TestFileUploadUsesTheIndexContentType(t *testing.T) {
	t.Parallel()
	data := []byte("payload")
	hash, r, err := uploadOnce(t, true, data, domain.RootIndexName, "image/png", porttest.Reply{})
	must(t, err)
	if hash != domain.BlobHash(data) {
		t.Fatal(hash)
	}
	contentType, ok := headerOne(puts(r.trip.Calls())[0].Header, "Content-Type")
	if !ok || contentType != domain.ContentTypeIndex {
		t.Fatalf("content type %q", contentType)
	}
}

func TestEmptyContentTypeFollowsTheName(t *testing.T) {
	t.Parallel()
	data := []byte("payload")
	r := newRig(t, nil)
	r.trip.Enqueue(porttest.Reply{})
	_, err := r.client.Upload(t.Context(), data, "test.pdf", "")
	must(t, err)
	call := puts(r.trip.Calls())[0]
	contentType, ok := headerOne(call.Header, "Content-Type")
	if !ok || contentType != domain.ContentType("test.pdf") {
		t.Fatalf("content type %q", contentType)
	}
	goog, ok := headerOne(call.Header, domain.HeaderGoogHash)
	if !ok || goog != domain.GoogHash(data) {
		t.Fatalf("goog %q", goog)
	}
}
