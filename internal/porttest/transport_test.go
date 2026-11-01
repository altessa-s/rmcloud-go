// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package porttest

import (
	"context"
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"testing"

	"github.com/altessa-s/rmcloud-go/internal/domain"
	"github.com/altessa-s/rmcloud-go/internal/domaintest"
	"github.com/altessa-s/rmcloud-go/internal/port"
)

func TestTransportPlaysTheQueues(t *testing.T) {
	t.Parallel()
	transport := &Transport{}
	transport.Enqueue(Reply{Body: []byte("global")})
	transport.EnqueuePath("/sync/v3/root", Reply{Status: 201, Body: []byte("root")})

	ctx := context.Background()
	root := mustURL(t, "https://sync.example/sync/v3/root?generation=1")
	other := mustURL(t, "https://sync.example/sync/v3/files/abc")

	resp, err := transport.RoundTrip(ctx, port.Request{Method: "GET", URL: root})
	if err != nil || resp.Status != 201 || string(resp.Body) != "root" {
		t.Fatalf("path queue: %+v %v", resp, err)
	}
	resp, err = transport.RoundTrip(ctx, port.Request{Method: "GET", URL: other})
	if err != nil || resp.Status != 200 || string(resp.Body) != "global" {
		t.Fatalf("global queue: %+v %v", resp, err)
	}
	resp, err = transport.RoundTrip(ctx, port.Request{Method: "GET", URL: root})
	if err != nil || resp.Status != 500 || resp.Body != nil {
		t.Fatalf("empty queue: %+v %v", resp, err)
	}
}

func TestTransportRecordsAFailure(t *testing.T) {
	t.Parallel()
	transport := &Transport{}
	transport.Enqueue(Reply{Err: domain.Transport(domain.ReasonTimedOut)})
	_, err := transport.RoundTrip(context.Background(), port.Request{
		Method: "PUT",
		URL:    mustURL(t, "https://sync.example/sync/v3/root"),
	})
	if !domaintest.SameError(err, domain.Transport(domain.ReasonTimedOut)) {
		t.Fatal(err)
	}
	if len(transport.Calls()) != 1 {
		t.Fatalf("calls %d", len(transport.Calls()))
	}
}

func TestTransportCallsAreCopies(t *testing.T) {
	t.Parallel()
	transport := &Transport{}
	transport.Enqueue(Reply{Body: []byte("ok"), Header: port.Header{"ETag": {"1"}}})
	body := []byte("abc")
	header := port.Header{"rm-filename": {"roothash"}}
	resp, err := transport.RoundTrip(context.Background(), port.Request{
		Method: "PUT",
		URL:    mustURL(t, "https://sync.example/sync/v3/root"),
		Header: header,
		Body:   body,
	})
	if err != nil {
		t.Fatal(err)
	}
	body[0] = 'Z'
	header["rm-filename"][0] = "mutated"
	resp.Body[0] = 'Q'
	resp.Header["ETag"][0] = "changed"

	calls := transport.Calls()
	if string(calls[0].Body) != "abc" || calls[0].Header["rm-filename"][0] != "roothash" {
		t.Fatalf("record followed the caller: %+v", calls[0])
	}
	calls[0].Body[0] = 'Q'
	if string(transport.Calls()[0].Body) != "abc" {
		t.Fatal("Calls shared the buffer")
	}
	again, err := transport.RoundTrip(context.Background(), port.Request{URL: mustURL(t, "https://sync.example/x")})
	if err != nil || string(again.Body) != "" || again.Status != 500 {
		t.Fatalf("second call %+v %v", again, err)
	}
}

func TestDownloadWritesAndRefuses(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dir := t.TempDir()
	transport := &Transport{}
	transport.Enqueue(
		Reply{Status: 500, Body: []byte("nope")},
		Reply{Body: []byte("hello")},
		Reply{Body: []byte("too-big")},
	)

	var progress [][2]int64
	resp, err := transport.Download(ctx, port.Request{
		URL:  mustURL(t, "https://sync.example/blob"),
		Dest: dir,
		Progress: func(done, total int64) {
			progress = append(progress, [2]int64{done, total})
		},
	})
	if err != nil || resp.Status != 500 || string(resp.Body) != "nope" {
		t.Fatalf("%+v %v", resp, err)
	}
	if len(progress) != 1 || progress[0] != [2]int64{4, 4} {
		t.Fatalf("progress %v", progress)
	}
	if _, err := os.Stat(resp.File); err != nil {
		t.Fatal(err)
	}

	dest := filepath.Join(dir, "named")
	resp, err = transport.Download(ctx, port.Request{
		URL:      mustURL(t, "https://sync.example/blob"),
		Dest:     dest,
		Progress: func(int64, int64) {},
	})
	if err != nil || resp.File != dest {
		t.Fatalf("%+v %v", resp, err)
	}
	got, err := os.ReadFile(dest)
	if err != nil || string(got) != "hello" {
		t.Fatalf("%q %v", got, err)
	}

	before, _ := os.ReadDir(dir)
	_, err = transport.Download(ctx, port.Request{
		URL:   mustURL(t, "https://sync.example/blob"),
		Dest:  dir,
		Limit: 4,
	})
	if !domaintest.SameError(err, domain.OversizedResponse(7, 4)) {
		t.Fatal(err)
	}
	after, _ := os.ReadDir(dir)
	if len(after) != len(before) {
		t.Fatalf("oversized download left a file: %d to %d", len(before), len(after))
	}
}

func TestDownloadMissingParentLeavesNothing(t *testing.T) {
	t.Parallel()
	transport := &Transport{}
	transport.Enqueue(Reply{Body: []byte("x")})
	dest := filepath.Join(t.TempDir(), "missing", "blob")
	_, err := transport.Download(context.Background(), port.Request{
		URL:  mustURL(t, "https://sync.example/blob"),
		Dest: dest,
	})
	if err == nil {
		t.Fatal("wrote into a missing directory")
	}
	if _, statErr := os.Stat(dest); !os.IsNotExist(statErr) {
		t.Fatal(statErr)
	}
}

func TestUploadSendsTheFile(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "doc")
	if err := os.WriteFile(path, []byte("pages"), 0o600); err != nil {
		t.Fatal(err)
	}
	transport := &Transport{}
	transport.Enqueue(Reply{Status: 201})
	var progress [][2]int64
	resp, err := transport.Upload(context.Background(), port.Request{
		Method: "PUT",
		URL:    mustURL(t, "https://sync.example/sync/v3/files/abc"),
		File:   path,
		Progress: func(done, total int64) {
			progress = append(progress, [2]int64{done, total})
		},
	})
	if err != nil || resp.Status != 201 {
		t.Fatalf("%+v %v", resp, err)
	}
	calls := transport.Calls()
	if len(calls) != 1 || string(calls[0].Body) != "pages" {
		t.Fatalf("calls %+v", calls)
	}
	if len(progress) != 1 || progress[0] != [2]int64{5, 5} {
		t.Fatalf("progress %v", progress)
	}
}

func TestRoundTripHonoursACancelledContext(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	transport := &Transport{}
	transport.Enqueue(Reply{Body: []byte("x")})
	_, err := transport.RoundTrip(ctx, port.Request{URL: mustURL(t, "https://sync.example/x")})
	if !errors.Is(err, context.Canceled) || len(transport.Calls()) != 0 {
		t.Fatalf("err %v calls %d", err, len(transport.Calls()))
	}
}

func mustURL(t *testing.T, raw string) url.URL {
	t.Helper()
	parsed, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	return *parsed
}
