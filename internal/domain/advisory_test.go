// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package domain

import (
	"strings"
	"testing"
)

func TestIsOrdinary(t *testing.T) {
	if !(RootAdvisory{}).IsOrdinary() {
		t.Fatal("empty is ordinary")
	}
	five := 5
	if (RootAdvisory{SchemaVersion: &five}).IsOrdinary() {
		t.Fatal("schema 5 is not ordinary even when missing-hashes is absent")
	}
	zero := 0
	if (RootAdvisory{MaxNetworkJobs: &zero}).IsOrdinary() {
		t.Fatal("maxNetworkJobs 0 is present")
	}
	yes := true
	if (RootAdvisory{HasMissingHashes: &yes}).IsOrdinary() {
		t.Fatal("hasMissingHashes true is not ordinary")
	}
	no := false
	if !(RootAdvisory{HasMissingHashes: &no, DisableHTTP2: &no}).IsOrdinary() {
		t.Fatal("false flags are ordinary")
	}
	if (RootAdvisory{DisableHTTP2: &yes}).IsOrdinary() {
		t.Fatal("disableHTTP2 true is not ordinary")
	}
	none := "NONE"
	if !(RootAdvisory{DeletedImmutableAction: &none}).IsOrdinary() {
		t.Fatal("NONE is a familiar action")
	}
	del := "delete"
	if (RootAdvisory{DeletedImmutableAction: &del}).IsOrdinary() {
		t.Fatal("delete is not a familiar action")
	}
	one := 1
	if (RootAdvisory{MissingHashes: &one}).IsOrdinary() {
		t.Fatal("a missing-hash count is not ordinary")
	}
}

func TestRemarks(t *testing.T) {
	schema := 4
	action := "rollback"
	jobs := 8
	off := false
	first := RootAdvisory{
		SchemaVersion:          &schema,
		DeletedImmutableAction: &action,
		MaxNetworkJobs:         &jobs,
		DisableHTTP2:           &off,
	}
	lines := first.Remarks(nil)
	joined := strings.Join(lines, "\n")
	for _, want := range []string{
		"schema 4",
		"deletedImmutableAction rollback",
		"maxNetworkJobs 8 (not followed: this client keeps its own ceiling)",
		"disableHTTP2 false (not followed: the system transport negotiates the protocol)",
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("missing %q in %q", want, joined)
		}
	}
	if len(first.Remarks(&first)) != 0 {
		t.Fatal("an equal previous says nothing")
	}
	prevSchema := 3
	prev := RootAdvisory{SchemaVersion: &prevSchema}
	change := first.Remarks(&prev)
	found := false
	for _, line := range change {
		if line == "schema 3 → 4" {
			found = true
		}
	}
	if !found {
		t.Fatalf("change = %#v", change)
	}

	some := RootAdvisory{HasMissingHashes: new(true)}
	if got := some.Remarks(nil); len(got) != 1 || got[0] != "the server reports some hashes it does not hold" {
		t.Fatalf("some = %#v", got)
	}
	cleared := RootAdvisory{}
	had := 2
	before := RootAdvisory{MissingHashes: &had, HasMissingHashes: new(true)}
	got := cleared.Remarks(&before)
	if len(got) != 1 || got[0] != "the server reports no missing hashes" {
		t.Fatalf("cleared = %#v", got)
	}
}
