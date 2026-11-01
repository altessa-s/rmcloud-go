// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package domain

import (
	"slices"
	"strings"
	"testing"
)

func TestParseMissingFiles(t *testing.T) {
	asked := []string{"aa", "BB", "cc"}
	got, err := ParseMissingFiles([]byte(`{"missingFiles":["cc","bb"],"extra":1}`), asked)
	if err != nil || !slices.Equal(got, []string{"cc", "BB"}) {
		t.Fatalf("%q %v", got, err)
	}
	for _, body := range []string{
		`{}`,
		`{"missingFiles":null}`,
		`{"missingFiles":"aa"}`,
		`{"missingFiles":["dd"]}`,
		`{"missingFiles":["aa"],"missingFiles":[]}`,
		`not json`,
		`null`,
	} {
		if _, err := ParseMissingFiles([]byte(body), asked); !IsKind(err, KindMalformedResponse) {
			t.Fatalf("%s: %v", body, err)
		}
	}
}

func TestCheckFilesBody(t *testing.T) {
	body, err := CheckFilesBody([]string{"aa", "BB"})
	if err != nil || string(body) != `{"files":["aa","BB"]}` {
		t.Fatalf("%s %v", body, err)
	}
	if _, err := CheckFilesBody(nil); !sameError(err, NothingToAsk(CheckFilesCall)) {
		t.Fatal(err)
	}
	if _, err := CheckFilesBody([]string{"aa", ""}); !sameError(err, MissingHash(CheckFilesCall)) {
		t.Fatal(err)
	}
}

func FuzzParseMissingFiles(f *testing.F) {
	f.Add(`{"missingFiles":["aa"]}`, "aa,bb")
	f.Add(`{"missingFiles":[]}`, "aa")
	f.Add(`{"missingFiles":["AA","aa"]}`, "aa")
	f.Fuzz(func(t *testing.T, body, askedList string) {
		asked := strings.Split(askedList, ",")
		missing, err := ParseMissingFiles([]byte(body), asked)
		if err != nil {
			return
		}
		for _, hash := range missing {
			if !slices.Contains(asked, hash) {
				t.Fatalf("%q was not asked about: %q", hash, asked)
			}
		}
	})
}
