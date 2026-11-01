// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

// Package rmcloud speaks the reMarkable sync protocol.
//
// The package is a client library. It does not choose a filesystem layout,
// a credential store, or a user interface. Secrets come in through a
// [SecretStore] the caller supplies, and every answer is one of this
// package's own types.
//
// [New] is the client. It does not dial until a call asks it to, and
// [Client.Close] releases it. [WithLogger] and [WithRoundTripper] route its
// records and its traffic through the caller's own.
//
// # Calls
//
//   - Device: [Client.RegisterDevice], [Client.IsRegistered],
//     [Client.RevokeDevice], [Client.ForgetDevice], [Client.Plan].
//   - Root: [Client.Root], [Client.RootIndex], [Client.PutRoot].
//   - Blobs: [Client.Blob], [Client.BlobFile], [Client.Index],
//     [Client.Upload], [Client.UploadFile], [Client.MissingFiles].
//   - Notifications: [Client.StartNotifications], [Client.StopNotifications].
//   - Index format: [Parse], [DecodeIndex], [SerializeRoot],
//     [SerializeDocument], [NodeHash], [BlobHash], [FileHash], [GoogHash].
//
// Reads check the bytes against the hash they were asked for. Responses are
// decoded strictly: a duplicate key, invalid UTF-8, or a field name in
// another case is a [MalformedResponseError].
//
// # Errors
//
// Every call that reaches the service fails with an [Error]. Match a
// sentinel with errors.Is, recover an error type with errors.As, or name the
// case with [KindOf] and [IsKind]. Each error also matches its go-atlas-core
// canonical error (github.com/altessa-s/go-atlas-core/errors): transport
// failures, rate limits and 5xx are ErrUnavailable, a root conflict is
// ErrConflict, a 404 or a missing root is ErrNotFound, a refused code is
// ErrInvalidArgument, and a missing registration, plan or platform facility
// is ErrFailedPrecondition.
//
// The public types are aliases of the protocol's internal types; their
// comments list the fields and methods.
//
// Adding a kind to [Error] is a breaking change. Go does not check that a
// switch on [Kind] is exhaustive; callers who want a new kind to break
// their build rather than fall through should enable the exhaustive linter.
package rmcloud
