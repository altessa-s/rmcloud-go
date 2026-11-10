// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package httpapi

import (
	"bytes"
	"context"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/altessa-s/rmcloud-go/internal/domain"
	"github.com/altessa-s/rmcloud-go/internal/port"
)

// privateFile is the mode of every file a download writes: the body may
// be a user's document.
const privateFile = 0o600

// downloadConnsPerHost keeps a large download from taking every connection
// the metadata calls need.
const downloadConnsPerHost = 6

// Download writes the body to Dest. The ceiling is Limit, or the declared
// size, or the blob ceiling. A body past that ceiling is removed. Any
// status is returned with the file; this method does not interpret it.
//
// The body has no idle bound: a large file over a slow link may pause for
// longer than a small read ever should. A dead HTTP/2 connection is found
// by its ping instead.
func (t *Transport) Download(ctx context.Context, req port.Request) (port.Response, error) {
	limit := req.Limit
	if limit <= 0 {
		limit = domain.LimitFor(req.Expected)
	}
	if refused := (domain.Ceiling{Limit: limit}).Refusal(0, req.Expected); refused != nil {
		return port.Response{}, refused
	}
	httpReq, err := newRequest(ctx, req)
	if err != nil {
		return port.Response{}, err
	}
	f, final, err := openDest(req.Dest)
	if err != nil {
		return port.Response{}, err
	}
	name := f.Name()
	keep := false
	defer func() {
		_ = f.Close()
		if !keep {
			_ = os.Remove(name)
		}
	}()
	setMemoryBody(httpReq, req.Body)
	resp, snap, method, err := t.do(ctx, t.download, httpReq, req.Body, false)
	if err != nil {
		return port.Response{}, err
	}
	if resp.Body == nil {
		resp.Body = io.NopCloser(bytes.NewReader(nil))
	}
	defer func() { _ = resp.Body.Close() }()
	total := int64(-1)
	if req.Expected > 0 {
		total = req.Expected
	}
	n, err := copyLimited(f, resp.Body, limit, func(written int64) {
		if req.Progress != nil {
			req.Progress(written, total)
		}
	})
	if errors.Is(err, errTooBig) {
		finalURL, finalMethod := finalHop(resp, snap, method)
		t.logger().Debug("oversized response", "bytes", n)
		t.wire.oversized(resp.StatusCode, finalMethod, finalURL, resp.Header, limit)
		return port.Response{}, domain.OversizedResponse(n, limit)
	}
	if err != nil {
		return t.fail(ctx, method, snap, err)
	}
	if err := f.Close(); err != nil {
		return t.fail(ctx, method, snap, err)
	}
	if final != "" {
		if err := os.Rename(name, final); err != nil {
			return t.fail(ctx, method, snap, err)
		}
		name = final
	}
	keep = true
	finalURL, finalMethod := finalHop(resp, snap, method)
	t.wire.received(resp.StatusCode, finalMethod, finalURL, resp.Header, nil, true, n)
	return port.Response{
		Status: resp.StatusCode,
		Header: cloneHeader(resp.Header),
		File:   name,
	}, nil
}

// openDest opens the file a download writes. An empty dest, or a directory,
// is a new temporary file there, and final is empty. Any other dest is a
// file name: the body goes to a temporary file beside it, and final is the
// name it is renamed to once the body is whole. A file already at that name
// is replaced only then, and is left alone when the download fails.
func openDest(dest string) (f *os.File, final string, err error) {
	if dest == "" {
		f, err = createTemp("")
		return f, "", err
	}
	info, err := os.Stat(dest)
	switch {
	case err == nil && info.IsDir():
		f, err = createTemp(dest)
		return f, "", err
	case err != nil && !errors.Is(err, fs.ErrNotExist):
		return nil, "", err
	}
	f, err = createTemp(filepath.Dir(dest))
	if err != nil {
		return nil, "", err
	}
	return f, dest, nil
}

func createTemp(dir string) (*os.File, error) {
	f, err := os.CreateTemp(dir, "rmcloud-*")
	if err != nil {
		return nil, err
	}
	if err := os.Chmod(f.Name(), privateFile); err != nil {
		name := f.Name()
		_ = f.Close()
		_ = os.Remove(name)
		return nil, err
	}
	return f, nil
}
