// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package domain

import "testing"

func TestUserAgentAndVersion(t *testing.T) {
	if got := UserAgent("14.5"); got != "desktop/3.28.0 (macos 14.5)" {
		t.Fatal(got)
	}
	if SystemVersion("14.5.0") != "14.5" {
		t.Fatal(SystemVersion("14.5.0"))
	}
	if SystemVersion("14.5.1") != "14.5.1" {
		t.Fatal(SystemVersion("14.5.1"))
	}
	if SystemVersion("14.5") != "14.5" {
		t.Fatal(SystemVersion("14.5"))
	}
}

func TestContentType(t *testing.T) {
	if ContentType("root.docSchema") != ContentTypeIndex {
		t.Fatal(ContentType("root.docSchema"))
	}
	if ContentType("doc.pdf") != ContentTypeBlob {
		t.Fatal(ContentType("doc.pdf"))
	}
	if ContentTypeJSON != "application/json" {
		t.Fatal(ContentTypeJSON)
	}
}
