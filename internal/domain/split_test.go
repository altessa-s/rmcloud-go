// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package domain

import (
	"slices"
	"strings"
	"testing"
)

// splitNewlinesRunes is the rune-by-rune definition splitNewlines must keep
// matching: LF, CR, VT, FF, NEL, LS, and PS, CR LF as one break, invalid bytes
// read as U+FFFD.
func splitNewlinesRunes(text string) []string {
	if text == "" {
		return nil
	}
	var lines []string
	var b strings.Builder
	ended := false
	runes := []rune(text)
	for i := 0; i < len(runes); i++ { //nolint:intrange // i also skips the LF of a CR LF pair
		r := runes[i]
		if r == '\r' && i+1 < len(runes) && runes[i+1] == '\n' {
			lines = append(lines, b.String())
			b.Reset()
			ended = true
			i++
			continue
		}
		if isNewline(r) {
			lines = append(lines, b.String())
			b.Reset()
			ended = true
			continue
		}
		b.WriteRune(r)
		ended = false
	}
	if ended {
		return append(lines, "")
	}
	return append(lines, b.String())
}

var splitSeeds = []string{
	"",
	"a",
	"a\n",
	"\n",
	"a\r\nb",
	"a\rb\n\nc",
	"\r\n\r\n",
	"a\r",
	"x y z\u0085w\vv\fu",
	"bad\xffbyte\n\xc3",
	"\xe2\x80",
	"3\n0:.:1:5\nh:80000000:id:0:5\n",
}

func TestSplitNewlinesMatchesRunes(t *testing.T) {
	t.Parallel()
	for _, text := range splitSeeds {
		if got, want := splitNewlines(text), splitNewlinesRunes(text); !slices.Equal(got, want) {
			t.Errorf("splitNewlines(%q) = %q, want %q", text, got, want)
		}
	}
}

func FuzzSplitNewlines(f *testing.F) {
	for _, text := range splitSeeds {
		f.Add(text)
	}
	f.Fuzz(func(t *testing.T, text string) {
		if got, want := splitNewlines(text), splitNewlinesRunes(text); !slices.Equal(got, want) {
			t.Fatalf("splitNewlines(%q) = %q, want %q", text, got, want)
		}
	})
}
