// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package discovery

import (
	"fmt"
	"os"
	"testing"

	"github.com/altessa-s/go-atlas-core/runtime/leakcheck"
)

// TestMain fails a passing run that leaves a goroutine blocked forever.
func TestMain(m *testing.M) {
	code := m.Run()
	if code == 0 {
		if err := leakcheck.Check(); err != nil {
			fmt.Fprintln(os.Stderr, err)
			code = 1
		}
	}
	os.Exit(code)
}
