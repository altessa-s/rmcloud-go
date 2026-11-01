// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package neterr

import (
	"errors"
	"net"
	"net/url"
	"os"
	"syscall"
	"testing"
)

// sinkErr keeps the compiler from discarding the benchmarked calls.
var sinkErr error

// BenchmarkClassify maps the failures a sync meets most, each wrapped the way
// net/http returns it.
func BenchmarkClassify(b *testing.B) {
	wrap := func(err error) error { return &url.Error{Op: "Get", URL: "https://h.example/sync/v3/root", Err: err} }
	for _, tc := range []struct {
		name string
		err  error
	}{
		{"refused", wrap(&net.OpError{Op: "dial", Net: "tcp", Err: os.NewSyscallError("connect", syscall.ECONNREFUSED)})},
		{"dns-not-found", wrap(&net.DNSError{Err: "no such host", Name: "h.example", IsNotFound: true})},
		{"deadline", wrap(os.ErrDeadlineExceeded)},
		{"unrecognized", wrap(errors.New("stopped after 10 redirects"))},
	} {
		b.Run(tc.name, func(b *testing.B) {
			ctx := b.Context()
			b.ReportAllocs()
			for b.Loop() {
				sinkErr = Classify(ctx, tc.err)
			}
		})
	}
}
