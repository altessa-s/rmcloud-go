// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package domain

import (
	"fmt"
	"time"
)

// Kind names one case of [Error]. The set is closed: adding a kind is a
// breaking change. The compiler does not check that a switch on Kind is
// exhaustive; the exhaustive linter in this module's configuration does, and
// callers who want the same guarantee should enable it too.
type Kind string

// The kinds of [Error].
const (
	KindNotRegistered        Kind = "notRegistered"
	KindInvalidOneTimeCode   Kind = "invalidOneTimeCode"
	KindUnauthorized         Kind = "unauthorized"
	KindAccountWithoutTablet Kind = "accountWithoutTablet"
	KindNoRootYet            Kind = "noRootYet"
	KindUnsupportedSchema    Kind = "unsupportedSchema"
	KindMalformedIndex       Kind = "malformedIndex"
	KindMalformedResponse    Kind = "malformedResponse"
	KindHashMismatch         Kind = "hashMismatch"
	KindMissingHash          Kind = "missingHash"
	KindRateLimited          Kind = "rateLimited"
	KindNotOnThisPlan        Kind = "notOnThisPlan"
	KindRootConflict         Kind = "rootConflict"
	KindHTTP                 Kind = "http"
	KindWriteRejected        Kind = "writeRejected"
	KindTransport            Kind = "transport"
	KindUnsupportedPlatform  Kind = "unsupportedPlatform"
	KindNothingToAsk         Kind = "nothingToAsk"
	KindOversizedResponse    Kind = "oversizedResponse"
)

// TransportReason is why a network call failed. Retry and "host is not
// there" read it.
type TransportReason string

// The reasons a [TransportError] carries.
const (
	ReasonTimedOut               TransportReason = "timedOut"
	ReasonCannotFindHost         TransportReason = "cannotFindHost"
	ReasonCannotConnectToHost    TransportReason = "cannotConnectToHost"
	ReasonNetworkConnectionLost  TransportReason = "networkConnectionLost"
	ReasonDNSLookupFailed        TransportReason = "dnsLookupFailed"
	ReasonNotConnectedToInternet TransportReason = "notConnectedToInternet"
	// ReasonSecureConnectionFailed: the TLS handshake failed, most often on a
	// certificate the trust roots do not accept. Repeating cannot help, and
	// the host answered, so it is there.
	ReasonSecureConnectionFailed TransportReason = "secureConnectionFailed"
	// ReasonCircuitOpen: the host failed often enough lately that it is not
	// dialed for a while. It says nothing about the host being gone.
	ReasonCircuitOpen TransportReason = "circuitOpen"
	// ReasonBlocked: the dial was refused by policy, such as a production
	// host resolving to a private address. Repeating cannot help.
	ReasonBlocked TransportReason = "blockedByPolicy"
)

// Error is every protocol error. The set of kinds is closed.
//
// A kind with no payload is a sentinel value, matched with errors.Is:
// [ErrNotRegistered], [ErrInvalidOneTimeCode], [ErrUnauthorized],
// [ErrAccountWithoutTablet], [ErrNoRootYet]. Every other kind is its own
// type carrying only its own fields, recovered with errors.As. [KindOf]
// names the case of any error in a chain.
type Error interface {
	error
	Kind() Kind
}

// sentinel is a kind with no payload. Its values are comparable, so
// errors.Is matches them by identity.
type sentinel Kind

func (s sentinel) Kind() Kind { return Kind(s) }

func (s sentinel) Error() string {
	switch Kind(s) {
	case KindNotRegistered:
		return "rmcloud: not registered"
	case KindInvalidOneTimeCode:
		return "rmcloud: invalid one-time code"
	case KindUnauthorized:
		return "rmcloud: unauthorized"
	case KindAccountWithoutTablet:
		return "rmcloud: account without a tablet"
	case KindNoRootYet:
		return "rmcloud: no root yet"
	default:
		return "rmcloud: " + string(s)
	}
}

// The kinds that carry nothing but themselves.
var (
	// ErrNotRegistered means the store has no device that can buy a user token.
	ErrNotRegistered Error = sentinel(KindNotRegistered)
	// ErrInvalidOneTimeCode means the eight-character code was refused.
	ErrInvalidOneTimeCode Error = sentinel(KindInvalidOneTimeCode)
	// ErrUnauthorized means the token the service saw was refused.
	ErrUnauthorized Error = sentinel(KindUnauthorized)
	// ErrAccountWithoutTablet means a device token is valid and the account has no tablet.
	ErrAccountWithoutTablet Error = sentinel(KindAccountWithoutTablet)
	// ErrNoRootYet means both root paths answered 404.
	ErrNoRootYet Error = sentinel(KindNoRootYet)
)

// UnsupportedSchemaError means the index header is not 3 or 4.
type UnsupportedSchemaError struct{ Schema string }

// MalformedIndexError means one line of an index could not be read.
type MalformedIndexError struct{ Line, Reason string }

// MalformedResponseError means a body was not the text or JSON the call expected.
type MalformedResponseError struct{ What string }

// HashMismatchError means the bytes were neither the file hash nor the node hash.
type HashMismatchError struct{ Expected, Actual string }

// MissingHashError means a call named a blob and gave no usable hash: an
// empty one, or one that is not hex and so cannot be a path segment.
type MissingHashError struct{ Name string }

// RateLimitedError means the service asked the client to wait. RetryAfter
// is the wait it named, when HasRetryAfter is true; zero and negative waits
// are kept as the service sent them.
type RateLimitedError struct {
	RetryAfter    time.Duration
	HasRetryAfter bool
}

// NotOnThisPlanError means the account's plan refused the call. Detail is
// the start of the service's answer.
type NotOnThisPlanError struct{ Detail string }

// RootConflictError means the root generation moved. The caller re-reads.
type RootConflictError struct{ Status int }

// HTTPError is a status this client does not map onto a more specific kind.
type HTTPError struct {
	Status int
	Path   string
}

// WriteRejectedError means a write was refused for a reason other than a
// mapped status.
type WriteRejectedError struct {
	Status int
	Detail string
}

// TransportError is a network failure and why it happened.
type TransportError struct{ Reason TransportReason }

// UnsupportedPlatformError means a port this process cannot provide.
type UnsupportedPlatformError struct{ What string }

// NothingToAskError means a call had an empty list and was not sent.
type NothingToAskError struct{ Call string }

// OversizedResponseError means a body or a declared size passed the
// download ceiling.
type OversizedResponseError struct{ Bytes, Limit int64 }

// Kind implements [Error].
func (*UnsupportedSchemaError) Kind() Kind { return KindUnsupportedSchema }

// Kind implements [Error].
func (*MalformedIndexError) Kind() Kind { return KindMalformedIndex }

// Kind implements [Error].
func (*MalformedResponseError) Kind() Kind { return KindMalformedResponse }

// Kind implements [Error].
func (*HashMismatchError) Kind() Kind { return KindHashMismatch }

// Kind implements [Error].
func (*MissingHashError) Kind() Kind { return KindMissingHash }

// Kind implements [Error].
func (*RateLimitedError) Kind() Kind { return KindRateLimited }

// Kind implements [Error].
func (*NotOnThisPlanError) Kind() Kind { return KindNotOnThisPlan }

// Kind implements [Error].
func (*RootConflictError) Kind() Kind { return KindRootConflict }

// Kind implements [Error].
func (*HTTPError) Kind() Kind { return KindHTTP }

// Kind implements [Error].
func (*WriteRejectedError) Kind() Kind { return KindWriteRejected }

// Kind implements [Error].
func (*TransportError) Kind() Kind { return KindTransport }

// Kind implements [Error].
func (*UnsupportedPlatformError) Kind() Kind { return KindUnsupportedPlatform }

// Kind implements [Error].
func (*NothingToAskError) Kind() Kind { return KindNothingToAsk }

// Kind implements [Error].
func (*OversizedResponseError) Kind() Kind { return KindOversizedResponse }

func (e *UnsupportedSchemaError) Error() string {
	return fmt.Sprintf("rmcloud: unsupported schema %q", e.Schema)
}

func (e *MalformedIndexError) Error() string { return "rmcloud: malformed index: " + e.Reason }

func (e *MalformedResponseError) Error() string { return "rmcloud: malformed response: " + e.What }

func (e *HashMismatchError) Error() string {
	return fmt.Sprintf("rmcloud: hash mismatch: expected %s, got %s", e.Expected, e.Actual)
}

func (e *MissingHashError) Error() string { return "rmcloud: missing hash for " + e.Name }

func (e *RateLimitedError) Error() string {
	if !e.HasRetryAfter {
		return "rmcloud: rate limited"
	}
	return "rmcloud: rate limited, retry after " + e.RetryAfter.String()
}

func (e *NotOnThisPlanError) Error() string { return "rmcloud: not on this plan: " + e.Detail }

func (e *RootConflictError) Error() string {
	return fmt.Sprintf("rmcloud: root conflict: %d", e.Status)
}

func (e *HTTPError) Error() string { return fmt.Sprintf("rmcloud: http %d %s", e.Status, e.Path) }

func (e *WriteRejectedError) Error() string {
	return fmt.Sprintf("rmcloud: write rejected: %d %s", e.Status, e.Detail)
}

func (e *TransportError) Error() string { return "rmcloud: transport: " + string(e.Reason) }

func (e *UnsupportedPlatformError) Error() string {
	return "rmcloud: unsupported on this platform: " + e.What
}

func (e *NothingToAskError) Error() string { return "rmcloud: nothing to ask: " + e.Call }

func (e *OversizedResponseError) Error() string {
	return fmt.Sprintf("rmcloud: oversized response: %d bytes over limit %d", e.Bytes, e.Limit)
}

// UnsupportedSchema is an [UnsupportedSchemaError].
func UnsupportedSchema(schema string) *UnsupportedSchemaError {
	return &UnsupportedSchemaError{Schema: schema}
}

// MalformedIndex is a [MalformedIndexError].
func MalformedIndex(line, reason string) *MalformedIndexError {
	return &MalformedIndexError{Line: line, Reason: reason}
}

// MalformedResponse is a [MalformedResponseError].
func MalformedResponse(what string) *MalformedResponseError {
	return &MalformedResponseError{What: what}
}

// HashMismatch is a [HashMismatchError].
func HashMismatch(expected, actual string) *HashMismatchError {
	return &HashMismatchError{Expected: expected, Actual: actual}
}

// MissingHash is a [MissingHashError].
func MissingHash(name string) *MissingHashError { return &MissingHashError{Name: name} }

// RateLimited is a [RateLimitedError]. A nil retryAfter means the service
// named no wait.
func RateLimited(retryAfter *time.Duration) *RateLimitedError {
	if retryAfter == nil {
		return &RateLimitedError{}
	}
	return &RateLimitedError{RetryAfter: *retryAfter, HasRetryAfter: true}
}

// NotOnThisPlan is a [NotOnThisPlanError].
func NotOnThisPlan(detail string) *NotOnThisPlanError { return &NotOnThisPlanError{Detail: detail} }

// RootConflict is a [RootConflictError].
func RootConflict(status int) *RootConflictError { return &RootConflictError{Status: status} }

// HTTP is an [HTTPError].
func HTTP(status int, path string) *HTTPError { return &HTTPError{Status: status, Path: path} }

// WriteRejected is a [WriteRejectedError].
func WriteRejected(status int, detail string) *WriteRejectedError {
	return &WriteRejectedError{Status: status, Detail: detail}
}

// Transport is a [TransportError].
func Transport(reason TransportReason) *TransportError { return &TransportError{Reason: reason} }

// UnsupportedPlatform is an [UnsupportedPlatformError].
func UnsupportedPlatform(what string) *UnsupportedPlatformError {
	return &UnsupportedPlatformError{What: what}
}

// NothingToAsk is a [NothingToAskError].
func NothingToAsk(call string) *NothingToAskError { return &NothingToAskError{Call: call} }

// OversizedResponse is an [OversizedResponseError].
func OversizedResponse(bytes, limit int64) *OversizedResponseError {
	return &OversizedResponseError{Bytes: bytes, Limit: limit}
}
