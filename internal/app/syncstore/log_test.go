// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package syncstore

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"

	"github.com/altessa-s/rmcloud-go/internal/domain"
	"github.com/altessa-s/rmcloud-go/internal/porttest"
)

// captureLog installs a debug handler for the rest of the test.
// These tests do not call t.Parallel: the default logger is process-wide.
func captureLog(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(prev) })
	return &buf
}

func TestRootPointerLogsSchema(t *testing.T) {
	buf := captureLog(t)
	r := newRig(t, nil)
	r.trip.Enqueue(porttest.Reply{Body: rootBody("aa", 1, `"schemaVersion":3`)})
	_, err := r.client.Root(t.Context())
	must(t, err)
	text := buf.String()
	if !strings.Contains(text, "root pointer: schema 3") {
		t.Fatal(text)
	}
	if strings.Contains(text, "level=INFO") {
		t.Fatal(text)
	}
}

func TestHashAndGenerationOnlyIsSilentUntilTheNextSchema(t *testing.T) {
	buf := captureLog(t)
	r := newRig(t, nil)
	r.trip.Enqueue(
		porttest.Reply{Body: rootBody("aa", 1, "")},
		porttest.Reply{Body: rootBody("aa", 1, `"schemaVersion":3`)},
	)
	_, err := r.client.Root(t.Context())
	must(t, err)
	if strings.Contains(buf.String(), "root pointer") {
		t.Fatal(buf.String())
	}
	_, err = r.client.Root(t.Context())
	must(t, err)
	if !strings.Contains(buf.String(), "schema nothing → 3") {
		t.Fatal(buf.String())
	}
}

func TestIdenticalAdvisoryIsLoggedOnce(t *testing.T) {
	buf := captureLog(t)
	r := newRig(t, nil)
	body := rootBody("aa", 1, `"schemaVersion":3`)
	r.trip.Enqueue(porttest.Reply{Body: body}, porttest.Reply{Body: body})
	_, err := r.client.Root(t.Context())
	must(t, err)
	_, err = r.client.Root(t.Context())
	must(t, err)
	if strings.Count(buf.String(), "root pointer: schema 3") != 1 {
		t.Fatal(buf.String())
	}
}

func TestMaxNetworkJobsIsNotOrdinary(t *testing.T) {
	buf := captureLog(t)
	r := newRig(t, nil)
	r.trip.Enqueue(porttest.Reply{Body: rootBody("aa", 1, `"maxNetworkJobs":2`)})
	_, err := r.client.Root(t.Context())
	must(t, err)
	text := buf.String()
	if !strings.Contains(text, "maxNetworkJobs 2 (not followed: this client keeps its own ceiling)") {
		t.Fatal(text)
	}
	if !strings.Contains(text, "level=INFO") {
		t.Fatal(text)
	}
	if strings.Contains(text, "→") {
		t.Fatal(text)
	}
}

func TestMissingRootIsNotLoggedAsAnError(t *testing.T) {
	buf := captureLog(t)
	r := newRig(t, nil)
	r.trip.Enqueue(
		porttest.Reply{Status: 404},
		porttest.Reply{Body: rootBody("bb", 2, "")},
	)
	_, err := r.client.Root(t.Context())
	must(t, err)
	text := buf.String()
	want := `level=DEBUG msg="request failed" path=/sync/v4/root err="rmcloud: http 404 /sync/v4/root"`
	if !strings.Contains(text, want) {
		t.Fatal(text)
	}
	if strings.Contains(text, "level=ERROR") {
		t.Fatal(text)
	}
	if strings.Contains(text, "root pointer") {
		t.Fatal(text)
	}
}

func TestMalformedRootIsNotARequestFailure(t *testing.T) {
	buf := captureLog(t)
	r := newRig(t, nil)
	r.trip.Enqueue(porttest.Reply{Body: []byte("{}")})
	_, err := r.client.Root(t.Context())
	wantEqual(t, err, domain.MalformedResponse("root response"))
	text := buf.String()
	if strings.Contains(text, "request failed") || strings.Contains(text, "root pointer") {
		t.Fatal(text)
	}
}

func TestNodeHashIndexIsLogged(t *testing.T) {
	buf := captureLog(t)
	text := "3\naaaa:80000000:doc-1:1:210\n"
	parsed, err := domain.Parse(text)
	must(t, err)
	node, err := domain.NodeHash(parsed.Entries)
	must(t, err)
	r := newRig(t, nil)
	r.trip.Enqueue(porttest.Reply{Body: []byte(text)})
	_, err = r.client.Index(t.Context(), node, "doc.docSchema")
	must(t, err)
	if !strings.Contains(buf.String(), "index is addressed by its node hash") {
		t.Fatal(buf.String())
	}
}

func TestCountMismatchIsLogged(t *testing.T) {
	buf := captureLog(t)
	text := "4\n0:.:99:160\naaaa:80000000:doc-1:2:120\n"
	r := newRig(t, nil)
	r.trip.Enqueue(porttest.Reply{Body: []byte(text)})
	_, err := r.client.Index(t.Context(), domain.BlobHash([]byte(text)), "root.docSchema")
	must(t, err)
	if !strings.Contains(buf.String(), "index count line disagrees with its records") ||
		!strings.Contains(buf.String(), "declared=99 records=1") {
		t.Fatal(buf.String())
	}
}

func TestInjectedLoggerReceivesTheRecords(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	r := newRig(t, nil, WithLogger(logger))
	r.trip.Enqueue(porttest.Reply{Body: rootBody("aa", 1, `"schemaVersion":3`)})
	_, err := r.client.Root(t.Context())
	must(t, err)
	if !strings.Contains(buf.String(), "root pointer: schema 3") {
		t.Fatal(buf.String())
	}
}
