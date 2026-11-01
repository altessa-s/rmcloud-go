// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package rmcloud

import "github.com/altessa-s/rmcloud-go/internal/domain"

// The public names below are aliases of the protocol's own types, so the
// methods travel with them. `go doc` prints only the alias line for an alias,
// so each comment lists the fields and methods a caller uses.
type (
	// Error is every protocol error. The set of kinds is closed: adding one
	// is a breaking change.
	//
	//	type Error interface {
	//		error
	//		Kind() Kind
	//	}
	//
	// A kind with no payload is a sentinel matched with errors.Is
	// ([ErrNotRegistered], [ErrInvalidOneTimeCode], [ErrUnauthorized],
	// [ErrAccountWithoutTablet], [ErrNoRootYet]). Every other kind is one of
	// the pointer types below, recovered with errors.As. Each error also
	// matches its go-atlas-core canonical error; see the package doc.
	Error = domain.Error

	// UnsupportedSchemaError means an index header is not 3 or 4.
	// Field: Schema string, the header as read.
	UnsupportedSchemaError = domain.UnsupportedSchemaError
	// MalformedIndexError means one line of an index could not be read.
	// Fields: Line string, Reason string.
	MalformedIndexError = domain.MalformedIndexError
	// MalformedResponseError means a body was not the text or JSON the call
	// expected. A body with a duplicate key, invalid UTF-8, or a field name in
	// another case is malformed. Field: What string, the response named.
	MalformedResponseError = domain.MalformedResponseError
	// HashMismatchError means the bytes hashed to neither the file hash nor
	// the node hash asked for. Fields: Expected, Actual string.
	HashMismatchError = domain.HashMismatchError
	// MissingHashError means a call named a blob and gave no usable hash: an
	// empty one, or one that is not hex. Field: Name string.
	MissingHashError = domain.MissingHashError
	// RateLimitedError means the service asked the client to wait.
	// Fields: RetryAfter time.Duration, valid when HasRetryAfter is true.
	RateLimitedError = domain.RateLimitedError
	// NotOnThisPlanError means the account's plan refused the call.
	// Field: Detail string, the start of the service's answer.
	NotOnThisPlanError = domain.NotOnThisPlanError
	// RootConflictError means the root generation moved (a 409 or 412 on
	// [Client.PutRoot]). The caller re-reads and rebuilds. Field: Status int.
	RootConflictError = domain.RootConflictError
	// HTTPError is a status no more specific kind covers.
	// Fields: Status int, Path string.
	HTTPError = domain.HTTPError
	// WriteRejectedError means a write was refused for a reason other than a
	// mapped status. Fields: Status int, Detail string.
	WriteRejectedError = domain.WriteRejectedError
	// TransportError is a network failure. Field: Reason [TransportReason].
	TransportError = domain.TransportError
	// UnsupportedPlatformError means this platform cannot provide something
	// the call needs, such as the notification socket. It is not retried.
	// Field: What string.
	UnsupportedPlatformError = domain.UnsupportedPlatformError
	// NothingToAskError means a call was given an empty list and was not
	// sent. Field: Call string.
	NothingToAskError = domain.NothingToAskError
	// OversizedResponseError means a body or a declared size passed the
	// download ceiling. Fields: Bytes, Limit int64.
	OversizedResponseError = domain.OversizedResponseError

	// Kind names one case of [Error]. It is a string type; the values are
	// the Kind constants.
	Kind = domain.Kind

	// Root is the cloud's root pointer.
	//
	//	type Root struct {
	//		Hash       string // hash of the root index
	//		Generation int64  // generation the pointer was written under
	//	}
	Root = domain.Root

	// Index is a parsed .docSchema file.
	//
	//	type Index struct {
	//		Schema   Schema
	//		Entries  []Entry
	//		Declared *int // the count a v4 file names on its count line; nil for v3
	//	}
	//
	// Methods:
	//   - Entry(ext string) (Entry, bool): the first record whose extension is ext.
	//   - CountMismatch() bool: a v4 count line that disagrees with the records.
	Index = domain.Index

	// Entry is one line of an index: hash:type:documentID:subfiles:size.
	//
	//	type Entry struct {
	//		Hash       string
	//		Type       string // kept as it arrived; see IsDocument
	//		DocumentID string
	//		Subfiles   int
	//		Size       int64
	//	}
	//
	// Methods:
	//   - IsDocument() bool: a root-index record whose name has no extension.
	//     The type field is not consulted.
	//   - IndexName() string: the .docSchema name of the record's document.
	//   - FileName() string: the last component of DocumentID.
	//   - FileBaseName(), FileExtension() string: FileName split at its last dot.
	//   - Line() string: the record as the file spells it.
	Entry = domain.Entry

	// Schema is the digit on an index file's first line: [SchemaV3] or
	// [SchemaV4]. It is not the root path's version.
	Schema = domain.Schema

	// Plan is what the user token says about the account's plan.
	//
	//	type Plan struct {
	//		Level     *string // nil for an account without Connect
	//		SyncScope *string // the first "sync:" scope, if any
	//	}
	//
	// Method: IsConnect() bool reports whether Level is exactly "connect".
	Plan = domain.Plan

	// Endpoints is the auth host and the sync host.
	//
	//	type Endpoints struct {
	//		AuthHost url.URL
	//		SyncHost url.URL
	//	}
	//
	// Methods return the URL of each call on those hosts: NewDeviceToken,
	// NewUserToken, DeleteDeviceToken, Discovery, Root(RootVersion),
	// RootWrite, Blob(hash), CheckFiles, and Notifications.
	Endpoints = domain.Endpoints

	// RootVersion names which root path a read uses: [RootV3] or [RootV4].
	RootVersion = domain.RootVersion

	// SecretName is a key in the caller's [SecretStore]: [SecretDeviceToken],
	// [SecretDeviceID], or [SecretUserToken].
	SecretName = domain.SecretName

	// Secret is one stored credential.
	//
	//	type Secret struct {
	//		Name  SecretName
	//		Value string
	//	}
	Secret = domain.Secret

	// Progress reports bytes transferred against the declared total. A
	// negative total means the length was not declared.
	//
	//	type Progress func(done, total int64)
	Progress = domain.Progress
)

// The kinds of [Error], one per case. See the sentinel or the error type of
// the same name for what each one means.
const (
	KindNotRegistered        = domain.KindNotRegistered
	KindInvalidOneTimeCode   = domain.KindInvalidOneTimeCode
	KindUnauthorized         = domain.KindUnauthorized
	KindAccountWithoutTablet = domain.KindAccountWithoutTablet
	KindNoRootYet            = domain.KindNoRootYet
	KindUnsupportedSchema    = domain.KindUnsupportedSchema
	KindMalformedIndex       = domain.KindMalformedIndex
	KindMalformedResponse    = domain.KindMalformedResponse
	KindHashMismatch         = domain.KindHashMismatch
	KindMissingHash          = domain.KindMissingHash
	KindRateLimited          = domain.KindRateLimited
	KindNotOnThisPlan        = domain.KindNotOnThisPlan
	KindRootConflict         = domain.KindRootConflict
	KindHTTP                 = domain.KindHTTP
	KindWriteRejected        = domain.KindWriteRejected
	KindTransport            = domain.KindTransport
	KindUnsupportedPlatform  = domain.KindUnsupportedPlatform
	KindNothingToAsk         = domain.KindNothingToAsk
	KindOversizedResponse    = domain.KindOversizedResponse
)

// TransportReason is why a [KindTransport] error happened. It is a string
// type; the values are the Reason constants.
type TransportReason = domain.TransportReason

// The reasons a [KindTransport] error carries.
const (
	ReasonTimedOut               = domain.ReasonTimedOut
	ReasonCannotFindHost         = domain.ReasonCannotFindHost
	ReasonCannotConnectToHost    = domain.ReasonCannotConnectToHost
	ReasonNetworkConnectionLost  = domain.ReasonNetworkConnectionLost
	ReasonDNSLookupFailed        = domain.ReasonDNSLookupFailed
	ReasonNotConnectedToInternet = domain.ReasonNotConnectedToInternet
	ReasonSecureConnectionFailed = domain.ReasonSecureConnectionFailed
	ReasonCircuitOpen            = domain.ReasonCircuitOpen
	ReasonBlocked                = domain.ReasonBlocked
)

// The device descriptions the service accepts at registration. See
// [WithDeviceDesc].
const (
	// DeviceDescMac is the description sent by default.
	DeviceDescMac = domain.DeviceDescMac

	// DeviceDescWindows is the other accepted description.
	DeviceDescWindows = domain.DeviceDescWindows
)

// The two index schemas, the digit on an index file's first line.
const (
	SchemaV3 = domain.SchemaV3
	SchemaV4 = domain.SchemaV4
)

// The two root paths. A read tries v4, then v3; a write is always v3.
const (
	RootV3 = domain.RootV3
	RootV4 = domain.RootV4
)

// The type field of an index record. Either value can mark a document;
// see [Entry.IsDocument].
const (
	FileType     = domain.FileType
	DocumentType = domain.DocumentType
)

// Names the service gives to indexes and to the root pointer.
const (
	IndexNameSuffix = domain.IndexNameSuffix
	RootIndexName   = domain.RootIndexName
	RootHashName    = domain.RootHashName
)

// Content types for a blob upload. See [ContentType].
const (
	ContentTypeIndex = domain.ContentTypeIndex
	ContentTypeBlob  = domain.ContentTypeBlob
	ContentTypeJSON  = domain.ContentTypeJSON
)

// The three keys in the caller's [SecretStore]. The raw strings are the
// contract with that store.
const (
	SecretDeviceToken = domain.SecretDeviceToken
	SecretDeviceID    = domain.SecretDeviceID
	SecretUserToken   = domain.SecretUserToken
)
