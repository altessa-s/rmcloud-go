// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package httpapi

import (
	"context"
	"io"
	"os"
	"sync"

	"github.com/altessa-s/rmcloud-go/internal/domain"
	"github.com/altessa-s/rmcloud-go/internal/port"
)

// Upload sends File, or Body when File is empty, and reads the response
// the same way RoundTrip does. The request body is not printed. Progress,
// when set, sees the bytes of File as they are handed to the connection,
// against the file's size; a body that is sent again after a redirect
// counts from zero again.
func (t *Transport) Upload(ctx context.Context, req port.Request) (port.Response, error) {
	httpReq, err := newRequest(ctx, req)
	if err != nil {
		return port.Response{}, err
	}
	if req.File != "" {
		opened, openErr := os.Open(req.File) // #nosec G304 -- the caller names the file
		if openErr != nil {
			return port.Response{}, openErr
		}
		f := &uploadFile{File: opened}
		var extras []*uploadFile
		// net/http closes the body it sent; these close the rest. A file
		// is closed once, by whichever comes first.
		defer func() {
			_ = f.Close()
			for _, extra := range extras {
				_ = extra.Close()
			}
		}()
		info, statErr := f.Stat()
		if statErr != nil {
			return port.Response{}, statErr
		}
		size := info.Size()
		path, progress := req.File, req.Progress
		httpReq.Body = withProgress(f, size, progress)
		httpReq.ContentLength = size
		httpReq.GetBody = func() (io.ReadCloser, error) {
			reopened, reopenErr := os.Open(path) // #nosec G304 -- the caller names the file
			if reopenErr != nil {
				return nil, reopenErr
			}
			again := &uploadFile{File: reopened}
			extras = append(extras, again)
			return withProgress(again, size, progress), nil
		}
	} else {
		setMemoryBody(httpReq, req.Body)
	}
	resp, snap, method, err := t.do(ctx, t.memory, httpReq, nil, true) //nolint:bodyclose // readMemory closes it
	if err != nil {
		return port.Response{}, err
	}
	return t.readMemory(ctx, resp, snap, method)
}

// uploadFile is a file an upload body reads. Close closes it once; a later
// call returns what the first one did.
type uploadFile struct {
	*os.File
	once sync.Once
	err  error
}

func (f *uploadFile) Close() error {
	f.once.Do(func() { f.err = f.File.Close() })
	return f.err
}

// progressBody reports each read of an upload body.
type progressBody struct {
	io.ReadCloser
	total    int64
	done     int64
	progress domain.Progress
}

func withProgress(body io.ReadCloser, total int64, progress domain.Progress) io.ReadCloser {
	if progress == nil {
		return body
	}
	return &progressBody{ReadCloser: body, total: total, progress: progress}
}

func (p *progressBody) Read(b []byte) (int, error) {
	n, err := p.ReadCloser.Read(b)
	if n > 0 {
		p.done += int64(n)
		p.progress(p.done, p.total)
	}
	return n, err
}
