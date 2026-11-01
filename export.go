// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package rmcloud

import (
	"context"
	"net/url"

	"github.com/altessa-s/rmcloud-go/internal/domain"
	"github.com/altessa-s/rmcloud-go/internal/wiring"
)

// The kinds of [Error] that carry no payload. Match them with errors.Is.
var (
	ErrNotRegistered        = domain.ErrNotRegistered
	ErrInvalidOneTimeCode   = domain.ErrInvalidOneTimeCode
	ErrUnauthorized         = domain.ErrUnauthorized
	ErrAccountWithoutTablet = domain.ErrAccountWithoutTablet
	ErrNoRootYet            = domain.ErrNoRootYet
)

// KindOf names the protocol error in err's chain. ok is false when there
// is none.
func KindOf(err error) (Kind, bool) { return domain.KindOf(err) }

// IsKind reports whether err's chain holds a protocol error of kind.
func IsKind(err error, kind Kind) bool { return domain.IsKind(err, kind) }

// IsDocumentUnavailable reports an error about the object asked for rather
// than the request: a 404, or bytes that are not a readable index or do not
// match their hash. A transport failure, a rate limit, or a 5xx is not.
func IsDocumentUnavailable(err error) bool { return domain.IsDocumentUnavailable(err) }

// IsHostUnreachable reports a transport failure whose reason says the host
// could not be found or reached. Being offline does not count.
func IsHostUnreachable(err error) bool { return domain.IsHostUnreachable(err) }

// Parse reads an index. Schema 3 and 4 are accepted.
func Parse(text string) (Index, error) { return domain.Parse(text) }

// SerializeRoot writes a root index. The result is always schema 4.
func SerializeRoot(entries []Entry) string { return domain.SerializeRoot(entries) }

// SerializeDocument writes one document's index. The result is always schema 4.
func SerializeDocument(entries []Entry, id string) string {
	return domain.SerializeDocument(entries, id)
}

// DecodeIndex parses an index and holds it to the address the caller was
// given: the hash of its bytes, or the hash of its children's hashes.
func DecodeIndex(data []byte, expected string) (Index, error) {
	index, _, err := domain.DecodeIndex(data, expected)
	return index, err
}

// IndexName is the rm-filename of a document index.
func IndexName(documentID string) string { return domain.IndexName(documentID) }

// BlobHash is the SHA-256 of a blob's bytes, lowercase hex.
func BlobHash(data []byte) string { return domain.BlobHash(data) }

// FileHash is [BlobHash] over the file at path, read in pieces. A
// canceled ctx stops the read between pieces.
func FileHash(ctx context.Context, path string) (string, error) {
	digest, err := wiring.Files().Digest(ctx, path)
	if err != nil {
		return "", err
	}
	return digest.SHA256, nil
}

// NodeHash is the SHA-256 of the children's hashes, in documentID order.
func NodeHash(entries []Entry) (string, error) { return domain.NodeHash(entries) }

// GoogHash is the x-goog-hash value an upload of data carries: its CRC-32C
// checksum as "crc32c=" and standard base64.
func GoogHash(data []byte) string { return domain.GoogHash(data) }

// ContentType is the Content-Type for a blob name.
func ContentType(name string) string { return domain.ContentType(name) }

// Production is the service's production auth and sync hosts, the default
// for [WithEndpoints]. With it, sync and notifications follow discovery.
func Production() Endpoints { return domain.Production() }

// FromEnvironment applies RMCLOUD_HOST, RMCLOUD_AUTH_HOST, and
// RMCLOUD_SYNC_HOST from env, which is not the process environment. HTTPS
// is accepted anywhere and HTTP only on the loopback. A refused value
// leaves its host alone and is named in the error; the endpoints returned
// are usable either way, and the error never quotes the value itself.
func FromEnvironment(env map[string]string) (Endpoints, error) {
	return domain.FromEnvironment(env)
}

// RegistrationPage is the web page where the account holder gets the
// one-time code that [Client.RegisterDevice] takes.
func RegistrationPage() url.URL { return domain.RegistrationPage() }
