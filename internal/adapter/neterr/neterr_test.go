// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package neterr

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"syscall"
	"testing"

	"github.com/altessa-s/rmcloud-go/internal/domain"
	"github.com/altessa-s/rmcloud-go/internal/domaintest"
)

type timeoutNetError struct{}

func (timeoutNetError) Error() string   { return "i/o timeout" }
func (timeoutNetError) Timeout() bool   { return true }
func (timeoutNetError) Temporary() bool { return true }

func TestClassify(t *testing.T) {
	t.Parallel()

	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	expired, cancelExpired := context.WithTimeout(context.Background(), 0)
	defer cancelExpired()
	<-expired.Done()

	refusedURL := &url.Error{Op: "Get", URL: "https://h.example/x", Err: syscall.ECONNREFUSED}
	plainURL := &url.Error{Op: "Get", URL: "https://h.example/x", Err: errors.New("nope")}
	redirectURL := &url.Error{Op: "Get", URL: "https://h10.example/x", Err: errors.New("stopped after 10 redirects")}
	bareEINVAL := error(syscall.EINVAL)

	tests := []struct {
		name string
		ctx  context.Context
		err  error
		want domain.Error
		same error
	}{
		{
			name: "cancelled context wins over refused",
			ctx:  canceled,
			err:  syscall.ECONNREFUSED,
			same: context.Canceled,
		},
		{
			name: "expired context wins over refused",
			ctx:  expired,
			err:  syscall.ECONNREFUSED,
			same: context.DeadlineExceeded,
		},
		{name: "canceled sentinel", err: context.Canceled, same: context.Canceled},
		{name: "deadline sentinel", err: context.DeadlineExceeded, same: context.DeadlineExceeded},
		{
			name: "dns not found",
			err:  &net.DNSError{Err: "no such host", Name: "h.example", IsNotFound: true, IsTimeout: true},
			want: domain.Transport(domain.ReasonCannotFindHost),
		},
		{
			name: "dns not found inside op",
			err:  &net.OpError{Op: "dial", Net: "tcp", Err: &net.DNSError{Err: "no such host", Name: "h.example", IsNotFound: true, IsTimeout: true}},
			want: domain.Transport(domain.ReasonCannotFindHost),
		},
		{
			name: "dns timeout",
			err:  &net.DNSError{Err: "timeout", Name: "h.example", IsTimeout: true},
			want: domain.Transport(domain.ReasonDNSLookupFailed),
		},
		{name: "refused", err: syscall.ECONNREFUSED, want: domain.Transport(domain.ReasonCannotConnectToHost)},
		{name: "timed out errno", err: syscall.ETIMEDOUT, want: domain.Transport(domain.ReasonTimedOut)},
		{name: "host unreachable", err: syscall.EHOSTUNREACH, want: domain.Transport(domain.ReasonNotConnectedToInternet)},
		{name: "net unreachable", err: syscall.ENETUNREACH, want: domain.Transport(domain.ReasonNotConnectedToInternet)},
		{name: "reset", err: syscall.ECONNRESET, want: domain.Transport(domain.ReasonNetworkConnectionLost)},
		{name: "aborted", err: syscall.ECONNABORTED, want: domain.Transport(domain.ReasonNetworkConnectionLost)},
		{name: "pipe", err: syscall.EPIPE, want: domain.Transport(domain.ReasonNetworkConnectionLost)},
		{name: "poll deadline", err: os.ErrDeadlineExceeded, want: domain.Transport(domain.ReasonTimedOut)},
		{
			name: "op timeout that is not an errno",
			err:  &net.OpError{Op: "dial", Net: "tcp", Err: timeoutNetError{}},
			want: domain.Transport(domain.ReasonNetworkConnectionLost),
		},
		{name: "bare timeout", err: timeoutNetError{}, want: domain.Transport(domain.ReasonTimedOut)},
		{name: "url wraps refused", err: refusedURL, want: domain.Transport(domain.ReasonCannotConnectToHost)},
		{name: "url wraps plain", err: plainURL, same: plainURL},
		{name: "bare einval", err: bareEINVAL, same: bareEINVAL},
		{
			name: "einval inside op",
			err:  &net.OpError{Op: "read", Net: "tcp", Err: syscall.EINVAL},
			want: domain.Transport(domain.ReasonNetworkConnectionLost),
		},
		{name: "ten redirects", err: redirectURL, same: redirectURL},
		{name: "body cut short", err: io.ErrUnexpectedEOF, want: domain.Transport(domain.ReasonNetworkConnectionLost)},
		{
			name: "body cut short inside url",
			err:  &url.Error{Op: "Get", URL: "https://h.example/x", Err: fmt.Errorf("read body: %w", io.ErrUnexpectedEOF)},
			want: domain.Transport(domain.ReasonNetworkConnectionLost),
		},
		{name: "plain eof is not a cut", err: io.EOF, same: io.EOF},
		{
			name: "unknown authority",
			err:  &url.Error{Op: "Get", URL: "https://h.example/x", Err: &tls.CertificateVerificationError{Err: x509.UnknownAuthorityError{}}},
			want: domain.Transport(domain.ReasonSecureConnectionFailed),
		},
		{name: "bare unknown authority", err: x509.UnknownAuthorityError{}, want: domain.Transport(domain.ReasonSecureConnectionFailed)},
		{name: "hostname mismatch", err: x509.HostnameError{Host: "h.example"}, want: domain.Transport(domain.ReasonSecureConnectionFailed)},
		{
			name: "expired certificate",
			err:  x509.CertificateInvalidError{Reason: x509.Expired},
			want: domain.Transport(domain.ReasonSecureConnectionFailed),
		},
		{
			name: "remote alert inside op",
			err:  &net.OpError{Op: "remote error", Net: "tcp", Err: tls.AlertError(42)},
			want: domain.Transport(domain.ReasonSecureConnectionFailed),
		},
		{name: "peer is not tls", err: tls.RecordHeaderError{Msg: "first record does not look like a TLS handshake"},
			want: domain.Transport(domain.ReasonSecureConnectionFailed)},
	}

	if errors.Is(os.ErrDeadlineExceeded, context.DeadlineExceeded) {
		t.Fatal("os.ErrDeadlineExceeded must not be context.DeadlineExceeded")
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := tt.ctx
			if ctx == nil {
				ctx = context.Background()
			}
			got := Classify(ctx, tt.err)
			if tt.same != nil {
				if !errors.Is(got, tt.same) {
					t.Fatalf("got %v (%T), want the original %v", got, got, tt.same)
				}
				return
			}
			if !domaintest.SameError(got, tt.want) {
				t.Fatalf("got %+v, want %+v", got, tt.want)
			}
			if tt.name == "poll deadline" && errors.Is(got, context.DeadlineExceeded) {
				t.Fatal("mapped timeout is not the context deadline")
			}
		})
	}
}
