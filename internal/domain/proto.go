// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package domain

import (
	"strings"
	"time"
)

const (
	// userAgentVersion is the version in the User-Agent the service expects.
	userAgentVersion = "3.28.0"

	// DeviceDescMac is the device description sent at registration by default.
	DeviceDescMac = "desktop-macos"
	// DeviceDescWindows is the other device description the service accepts.
	DeviceDescWindows = "desktop-windows"

	// BlobSizeLimit is the ceiling for a download that did not declare a size.
	BlobSizeLimit int64 = 1024 * 1024 * 1024

	// DetailLimit is how much of an error body is kept.
	DetailLimit = 500

	// UserTokenLifetime is how long a user token without an exp is kept.
	UserTokenLifetime = 20 * time.Hour
	// ExpiryMargin is how much life a token must have left to be handed out.
	ExpiryMargin = 5 * time.Minute
	// DiscoveryTTL is how long a discovery answer is believed.
	DiscoveryTTL = time.Hour

	// DiscoveryLimit is the most bytes of a discovery body that are parsed.
	// A longer body is not the document.
	DiscoveryLimit = 1 << 20

	// HashChunk is how much of a blob one read hashes.
	HashChunk = 4 * 1024 * 1024
)

// The type field of an index record. Either value can mark a document.
const (
	FileType     = "0"
	DocumentType = "80000000"
)

// Names the service gives to indexes and to the root pointer.
const (
	IndexNameSuffix = ".docSchema"
	RootIndexName   = "root.docSchema"
	RootHashName    = "roothash"
)

// Content types for a blob upload.
const (
	ContentTypeIndex = "text/plain; charset=UTF-8"
	ContentTypeBlob  = "application/octet-stream"
	ContentTypeJSON  = "application/json"
)

// Header names the protocol uses.
const (
	HeaderFilename      = "rm-filename"
	HeaderGoogHash      = "x-goog-hash"
	HeaderUserAgent     = "User-Agent"
	HeaderAuthorization = "Authorization"
)

// The HTTP statuses this protocol gives a meaning to, spelled here so the
// use-case packages need not import net/http.
const (
	StatusOK                 = 200
	StatusBadRequest         = 400
	StatusUnauthorized       = 401
	StatusPaymentRequired    = 402
	StatusForbidden          = 403
	StatusNotFound           = 404
	StatusConflict           = 409
	StatusPreconditionFailed = 412
	StatusTooManyRequests    = 429
)

// The HTTP methods this protocol uses.
const (
	MethodGet  = "GET"
	MethodPost = "POST"
	MethodPut  = "PUT"
)

// IsSuccess reports a 2xx status.
func IsSuccess(status int) bool { return status >= StatusOK && status < StatusOK+statusClass }

// IsServerError reports a 5xx status.
func IsServerError(status int) bool {
	return status >= serverErrors && status < serverErrors+statusClass
}

const (
	statusClass  = 100
	serverErrors = 500
)

// Progress reports bytes written against the total the caller was told.
// A negative total means the length was not declared.
type Progress func(done, total int64)

// UserAgent is the User-Agent the service expects, for a macOS version.
func UserAgent(macosVersion string) string {
	return "desktop/" + userAgentVersion + " (macos " + macosVersion + ")"
}

// SystemVersion is a macOS product version as the User-Agent spells it.
// A trailing patch of zero is dropped: "14.5.0" becomes "14.5".
func SystemVersion(raw string) string {
	const withPatch = 3
	parts := strings.Split(raw, ".")
	if len(parts) >= withPatch && parts[2] == "0" {
		return parts[0] + "." + parts[1]
	}
	if len(parts) >= withPatch {
		return parts[0] + "." + parts[1] + "." + parts[2]
	}
	return raw
}

// ContentType is the Content-Type for a blob name. An index (the .docSchema
// suffix) is UTF-8 text. Everything else is opaque bytes.
func ContentType(name string) string {
	if strings.HasSuffix(name, IndexNameSuffix) {
		return ContentTypeIndex
	}
	return ContentTypeBlob
}
