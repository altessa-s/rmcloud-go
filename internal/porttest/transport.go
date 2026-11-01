// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package porttest

import (
	"bytes"
	"context"
	"os"
	"sync"

	"github.com/altessa-s/rmcloud-go/internal/domain"
	"github.com/altessa-s/rmcloud-go/internal/port"
)

// Reply is one scripted answer.
//
// Status 0 means 200. Err is returned after the call has been recorded, so a
// test can see the request that failed in the transport.
type Reply struct {
	Status int
	Header port.Header
	Body   []byte
	Err    error
}

// emptyQueueStatus is the answer when no reply was scripted: a server error,
// which the client, not the transport, gives a meaning to.
const emptyQueueStatus = 500

// privateFile is the mode of a file a scripted download writes.
const privateFile = 0o600

// Transport answers from a FIFO and records every call.
//
// A path queue is used before the global queue. An empty queue is status 500
// with an empty body, and that is a response: the client, not the transport,
// decides what a status means.
type Transport struct {
	mu     sync.Mutex
	global []Reply
	paths  map[string][]Reply
	calls  []port.Request
}

// Enqueue appends replies to the global queue.
func (t *Transport) Enqueue(replies ...Reply) {
	t.mu.Lock()
	defer t.mu.Unlock()
	for _, reply := range replies {
		t.global = append(t.global, cloneReply(reply))
	}
}

// EnqueuePath appends replies for requests whose URL.Path equals path.
// The comparison is exact, including the leading slash.
func (t *Transport) EnqueuePath(path string, replies ...Reply) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.paths == nil {
		t.paths = map[string][]Reply{}
	}
	for _, reply := range replies {
		t.paths[path] = append(t.paths[path], cloneReply(reply))
	}
}

// Calls returns a copy of the recorded requests. Mutating the copy does not
// change the record, and mutating the request after RoundTrip does not either.
func (t *Transport) Calls() []port.Request {
	t.mu.Lock()
	defer t.mu.Unlock()
	out := make([]port.Request, len(t.calls))
	for i, call := range t.calls {
		out[i] = snapshot(call)
	}
	return out
}

// RoundTrip pops one reply. The status is returned as it is; this method
// does not map it onto a protocol error.
func (t *Transport) RoundTrip(ctx context.Context, req port.Request) (port.Response, error) {
	if err := ctx.Err(); err != nil {
		return port.Response{}, err
	}
	t.mu.Lock()
	t.calls = append(t.calls, snapshot(req))
	reply, ok := t.take(req.URL.Path)
	t.mu.Unlock()
	if !ok {
		return port.Response{Status: emptyQueueStatus}, nil
	}
	if reply.Err != nil {
		return port.Response{}, reply.Err
	}
	status := reply.Status
	if status == 0 {
		status = 200
	}
	return port.Response{
		Status: status,
		Header: cloneHeader(reply.Header),
		Body:   bytes.Clone(reply.Body),
	}, nil
}

// Download runs one RoundTrip, refuses a body past the ceiling before any
// file exists, and writes what remains. Progress is reported once, at the
// end, as (n, n), including when the status is a failure: the exchange itself
// returned.
func (t *Transport) Download(ctx context.Context, req port.Request) (port.Response, error) {
	resp, err := t.RoundTrip(ctx, req)
	if err != nil {
		return port.Response{}, err
	}
	limit := req.Limit
	if limit <= 0 {
		limit = domain.LimitFor(req.Expected)
	}
	n := int64(len(resp.Body))
	if refusal := (domain.Ceiling{Limit: limit}).Refusal(n, req.Expected); refusal != nil {
		return port.Response{}, refusal
	}
	path, err := writeBody(req.Dest, resp.Body)
	if err != nil {
		return port.Response{}, err
	}
	if req.Progress != nil {
		req.Progress(n, n)
	}
	resp.File = path
	return resp, nil
}

// Upload reads File into the body and sends that through RoundTrip, so a
// fixture sees the bytes where it already looks. Progress is (n, n) after
// the exchange returns.
func (t *Transport) Upload(ctx context.Context, req port.Request) (port.Response, error) {
	if err := ctx.Err(); err != nil {
		return port.Response{}, err
	}
	body, err := os.ReadFile(req.File)
	if err != nil {
		return port.Response{}, err
	}
	req.Body = body
	resp, err := t.RoundTrip(ctx, req)
	if err != nil {
		return port.Response{}, err
	}
	if req.Progress != nil {
		n := int64(len(body))
		req.Progress(n, n)
	}
	return resp, nil
}

func (t *Transport) take(path string) (Reply, bool) {
	if queue := t.paths[path]; len(queue) > 0 {
		reply := queue[0]
		t.paths[path] = queue[1:]
		return reply, true
	}
	if len(t.global) == 0 {
		return Reply{}, false
	}
	reply := t.global[0]
	t.global = t.global[1:]
	return reply, true
}

func writeBody(dest string, body []byte) (string, error) {
	if dest == "" {
		return writeTemp("", body)
	}
	info, err := os.Stat(dest)
	if err == nil && info.IsDir() {
		return writeTemp(dest, body)
	}
	if err := os.WriteFile(dest, body, privateFile); err != nil {
		_ = os.Remove(dest)
		return "", err
	}
	return dest, nil
}

func writeTemp(dir string, body []byte) (string, error) {
	file, err := os.CreateTemp(dir, "rmcloud-*")
	if err != nil {
		return "", err
	}
	name := file.Name()
	if _, err := file.Write(body); err != nil {
		_ = file.Close()
		_ = os.Remove(name)
		return "", err
	}
	if err := file.Close(); err != nil {
		_ = os.Remove(name)
		return "", err
	}
	return name, nil
}

func cloneReply(reply Reply) Reply {
	reply.Header = cloneHeader(reply.Header)
	reply.Body = bytes.Clone(reply.Body)
	return reply
}

func snapshot(req port.Request) port.Request {
	req.Header = cloneHeader(req.Header)
	req.Body = bytes.Clone(req.Body)
	return req
}

func cloneHeader(header port.Header) port.Header {
	if header == nil {
		return nil
	}
	out := make(port.Header, len(header))
	for name, values := range header {
		out[name] = append([]string(nil), values...)
	}
	return out
}

var _ port.Transport = (*Transport)(nil)
