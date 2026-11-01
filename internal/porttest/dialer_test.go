// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package porttest

import (
	"context"
	"net/url"
	"testing"

	"github.com/altessa-s/rmcloud-go/internal/app/retry"
	"github.com/altessa-s/rmcloud-go/internal/domain"
	"github.com/altessa-s/rmcloud-go/internal/domaintest"
)

func TestMissingSocketIsNotRetried(t *testing.T) {
	t.Parallel()
	sleeper := NewClock()
	_, err := retry.Run(context.Background(), retry.Default().On(sleeper), func(ctx context.Context) (struct{}, error) {
		_, dialErr := MissingDialer{}.Dial(ctx, url.URL{}, nil)
		return struct{}{}, dialErr
	})
	if !domaintest.SameError(err, domain.UnsupportedPlatform("notification socket")) {
		t.Fatal(err)
	}
	if len(sleeper.Delays()) != 0 {
		t.Fatalf("delays %v", sleeper.Delays())
	}
}
