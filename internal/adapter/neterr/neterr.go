// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package neterr

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"io"
	"net"
	"os"

	"github.com/altessa-s/go-atlas-core/errors/network"

	"github.com/altessa-s/rmcloud-go/internal/domain"
)

// Classify maps a transport failure onto a protocol error.
//
// A canceled or expired context is returned as that context error. A body
// cut short is a lost connection; a failed TLS handshake is a failed secure
// connection. Anything this function does not recognize is returned
// unchanged, including the ten-redirect stop.
func Classify(ctx context.Context, err error) error {
	if err == nil {
		return nil
	}
	if ctx != nil {
		if cerr := ctx.Err(); cerr != nil {
			return cerr
		}
	}
	if errors.Is(err, context.Canceled) {
		return context.Canceled
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return context.DeadlineExceeded
	}
	if dns, ok := errors.AsType[*net.DNSError](err); ok {
		if dns.IsNotFound {
			return domain.Transport(domain.ReasonCannotFindHost)
		}
		return domain.Transport(domain.ReasonDNSLookupFailed)
	}
	if code, ok := errnoCode(err); ok {
		if mapped := fromErrno(code); mapped != nil {
			return mapped
		}
	}
	if errors.Is(err, io.ErrUnexpectedEOF) {
		return domain.Transport(domain.ReasonNetworkConnectionLost)
	}
	// A remote TLS alert arrives inside a *net.OpError, so this comes before
	// the lost-connection fallback below.
	if isTLSFailure(err) {
		return domain.Transport(domain.ReasonSecureConnectionFailed)
	}
	if errors.Is(err, os.ErrDeadlineExceeded) {
		return domain.Transport(domain.ReasonTimedOut)
	}
	if _, ok := errors.AsType[*net.OpError](err); ok {
		return domain.Transport(domain.ReasonNetworkConnectionLost)
	}
	if network.IsRequestTimeoutError(err) {
		return domain.Transport(domain.ReasonTimedOut)
	}
	return err
}

// The Winsock numbers errnoCode reports. Unix constants differ by OS; these
// are the ones the protocol already uses.
const (
	wsaNetUnreach  = 10051
	wsaConnAborted = 10053
	wsaConnReset   = 10054
	wsaTimedOut    = 10060
	wsaConnRefused = 10061
	wsaHostUnreach = 10065
)

func fromErrno(code int) error {
	switch code {
	case wsaConnRefused:
		return domain.Transport(domain.ReasonCannotConnectToHost)
	case wsaTimedOut:
		return domain.Transport(domain.ReasonTimedOut)
	case wsaHostUnreach, wsaNetUnreach:
		return domain.Transport(domain.ReasonNotConnectedToInternet)
	case wsaConnReset, wsaConnAborted:
		return domain.Transport(domain.ReasonNetworkConnectionLost)
	default:
		return nil
	}
}

// isTLSFailure reports a failed handshake: a certificate that did not
// verify, an alert from the peer, or a peer that does not speak TLS.
func isTLSFailure(err error) bool {
	if _, ok := errors.AsType[*tls.CertificateVerificationError](err); ok {
		return true
	}
	if _, ok := errors.AsType[x509.UnknownAuthorityError](err); ok {
		return true
	}
	if _, ok := errors.AsType[x509.HostnameError](err); ok {
		return true
	}
	if _, ok := errors.AsType[x509.CertificateInvalidError](err); ok {
		return true
	}
	if _, ok := errors.AsType[tls.AlertError](err); ok {
		return true
	}
	_, ok := errors.AsType[tls.RecordHeaderError](err)
	return ok
}
