// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package domain

import (
	"testing"
)

func TestParseRoot(t *testing.T) {
	root, adv, err := ParseRoot([]byte(`{
		"hash": "abc",
		"generation": 7,
		"schemaVersion": 4,
		"unknown": true,
		"missingHashes": [],
		"disableHTTP2": false
	}`))
	if err != nil {
		t.Fatal(err)
	}
	if root.Hash != "abc" || root.Generation != 7 {
		t.Fatalf("%#v", root)
	}
	if adv.SchemaVersion == nil || *adv.SchemaVersion != 4 {
		t.Fatalf("schema %#v", adv.SchemaVersion)
	}
	if adv.MissingHashes == nil || *adv.MissingHashes != 0 {
		t.Fatalf("empty list %#v", adv.MissingHashes)
	}
	if adv.DisableHTTP2 == nil || *adv.DisableHTTP2 {
		t.Fatalf("http2 %#v", adv.DisableHTTP2)
	}

	_, adv, err = ParseRoot([]byte(`{"hash":"a","generation":1,"missingHashes":null}`))
	if err != nil {
		t.Fatal(err)
	}
	if adv.MissingHashes != nil {
		t.Fatal("null missingHashes stays nil")
	}

	for _, body := range []string{
		`{"generation":1}`,
		`{"hash":null,"generation":1}`,
		`{"hash":"a"}`,
		`{"hash":"a","generation":null}`,
		`{"hash":"a","generation":1.0}`,
		`{"hash":1,"generation":1}`,
		`not json`,
		`null`,
		`{"hash":"a","hash":"b","generation":1}`,
		`{"HASH":"a","GENERATION":1}`,
		"{\"hash\":\"a\xff\",\"generation\":1}",
		`{"hash":"\ud800","generation":1}`,
	} {
		_, _, err = ParseRoot([]byte(body))
		if !sameError(err, MalformedResponse("root response")) {
			t.Fatalf("%s: %v", body, err)
		}
	}
}

func FuzzParseRoot(f *testing.F) {
	f.Add([]byte(`{"hash":"abc","generation":7,"schemaVersion":4,"missingHashes":[],"disableHTTP2":false}`))
	f.Add([]byte(`{"hash":"a","generation":1,"missingHashes":null}`))
	f.Add([]byte(`{}`))
	f.Fuzz(func(t *testing.T, data []byte) {
		_, adv, err := ParseRoot(data)
		if err != nil {
			return
		}
		_ = adv.IsOrdinary()
		_ = adv.Remarks(nil)
		_ = adv.Remarks(&adv)
	})
}
