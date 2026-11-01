// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package httpapi

import (
	"context"
	"fmt"
	"net/url"
	"syscall"
	"testing"

	"github.com/altessa-s/rmcloud-go/internal/domain"
	"github.com/altessa-s/rmcloud-go/internal/domaintest"
)

func TestClassifyNotHTTP(t *testing.T) {
	nilResp := &url.Error{Op: "Get", URL: "https://h.example/x", Err: errNotHTTP}
	want := domain.MalformedResponse("response is not HTTP")
	for _, err := range []error{nilResp, fmt.Errorf("wrap: %w", nilResp)} {
		if got := classify(context.Background(), err); !domaintest.SameError(got, want) {
			t.Fatalf("%v: got %+v", err, got)
		}
	}
	refused := &url.Error{Op: "Get", URL: "https://h.example/x", Err: syscall.ECONNREFUSED}
	if got := classify(context.Background(), refused); !domaintest.SameError(got, domain.Transport(domain.ReasonCannotConnectToHost)) {
		t.Fatalf("refused: got %+v", got)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if got := classify(ctx, nilResp); got != context.Canceled { //nolint:errorlint // classify returns the context error itself
		t.Fatalf("canceled: got %v", got)
	}
}
