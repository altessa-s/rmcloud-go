// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package port

import (
	"context"
	"net/url"
	"strings"

	"github.com/altessa-s/rmcloud-go/internal/domain"
)

// Header is a request or response header.
//
// Names are kept as the caller wrote them. Canonical form is the HTTP
// adapter's business, and this package does not import net/http.
type Header map[string][]string

// Get returns the first value associated with the given key, matching
// case-insensitively. If there are no values, Get returns "".
func (h Header) Get(name string) string {
	for key, values := range h {
		if strings.EqualFold(key, name) && len(values) > 0 {
			return values[0]
		}
	}
	return ""
}

// Request is one protocol call. The transport does not interpret the status
// it gets back: 412 and 409 become rootConflict in the client, not here.
type Request struct {
	Method string
	URL    url.URL
	Header Header
	// Body is the request body. Upload leaves it empty and sets File instead.
	Body []byte
	// File is the path of a body that must not be held in memory.
	File string
	// Dest is where Download writes. An empty Dest lets the transport pick a
	// temporary file. A directory means a file inside that directory.
	Dest string
	// Limit is the most bytes Download may accept. Zero means the caller did
	// not set one, and the transport uses the declared size or the blob ceiling.
	Limit int64
	// Expected is the length the server declared. Negative means it did not.
	Expected int64
	Progress domain.Progress
}

// Response is what the transport got back.
//
// A nil error does not mean the call succeeded. Status is the HTTP status,
// and the client decides what it means. File is set by Download; the caller
// owns that file.
type Response struct {
	Status int
	Header Header
	Body   []byte
	File   string
}

// RoundTripper exchanges one request for one response in memory.
type RoundTripper interface {
	RoundTrip(ctx context.Context, req Request) (Response, error)
}

// Downloader writes a response body to a file and stops when it passes the ceiling.
type Downloader interface {
	Download(ctx context.Context, req Request) (Response, error)
}

// Uploader sends a file as the body.
type Uploader interface {
	Upload(ctx context.Context, req Request) (Response, error)
}

// Transport is the three HTTP operations. The scripted double and the
// net/http adapter both implement it, and the client accepts the interface.
type Transport interface {
	RoundTripper
	Downloader
	Uploader
}
