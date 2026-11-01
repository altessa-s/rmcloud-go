// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package httpapi

import (
	"context"
	"errors"

	"github.com/altessa-s/go-atlas-net/http/client"

	"github.com/altessa-s/rmcloud-go/internal/adapter/neterr"
	"github.com/altessa-s/rmcloud-go/internal/domain"
)

// classify is [neterr.Classify], plus a round tripper that answered nothing
// at all, which only this transport can meet.
func classify(ctx context.Context, err error) error {
	if ctx.Err() == nil {
		// Both come wrapped in errors neterr would read as a lost connection
		// or an unreachable host, which would repeat the call and demote the
		// region host. Neither says the host is gone.
		if client.IsSSRFError(err) != nil {
			return domain.Transport(domain.ReasonBlocked)
		}
		if client.IsCircuitBreakerOpen(err) {
			return domain.Transport(domain.ReasonCircuitOpen)
		}
	}
	mapped := neterr.Classify(ctx, err)
	if _, isProtocol := domain.KindOf(mapped); !isProtocol && errors.Is(mapped, errNotHTTP) {
		return domain.MalformedResponse("response is not HTTP")
	}
	return mapped
}
