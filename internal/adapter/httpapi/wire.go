// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package httpapi

import (
	"cmp"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"sync"
	"unicode/utf8"
)

// Level is how much of an exchange the wire log prints.
type Level int

const (
	// LevelOff prints nothing.
	LevelOff Level = iota
	// LevelHeaders prints the exchange, the headers, and the size of a body.
	LevelHeaders
	// LevelBodies also prints a body when the content type is text, JSON, or XML.
	LevelBodies
)

// String is the raw level name the announce line prints.
func (l Level) String() string {
	switch l {
	case LevelOff:
		return "off"
	case LevelHeaders:
		return "headers"
	case LevelBodies:
		return "bodies"
	default:
		return "off"
	}
}

// bodyLimit is the most bytes of a readable body the log keeps.
const bodyLimit = 4096

// wire is the log of one transport. Level and writer do not change after New.
type wire struct {
	level     Level
	writer    io.Writer
	announced bool
	mu        sync.Mutex
}

func (w *wire) on() bool {
	return w.level != LevelOff && w.writer != nil
}

// ParseLevel reads a wire level as RMCLOUD_WIRE_LOG spells it. The value is
// not trimmed; anything unknown is off.
func ParseLevel(raw string) Level {
	switch strings.ToLower(raw) {
	case "headers", "1", "true", "yes", "on":
		return LevelHeaders
	case "bodies", "body", "full", "2":
		return LevelBodies
	default:
		return LevelOff
	}
}

// slogDebugWriter sends each visual line to a logger at debug level. A nil
// log function means slog.Default. A body that continues on the next line
// stays one Debug call per line.
type slogDebugWriter struct {
	log func() *slog.Logger
}

func (w slogDebugWriter) Write(p []byte) (int, error) {
	text := strings.TrimRight(string(p), "\r\n")
	if text == "" {
		return len(p), nil
	}
	logger := slog.Default()
	if w.log != nil {
		logger = w.log()
	}
	for line := range strings.SplitSeq(text, "\n") {
		logger.Debug(line)
	}
	return len(p), nil
}

func (w *wire) sent(method string, u *url.URL, header http.Header, body []byte, skipBody bool) {
	if !w.on() {
		return
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	var b strings.Builder
	if !w.announced {
		w.announced = true
		fmt.Fprintf(&b, "wire log on (%s): credentials are masked, query strings dropped\n", w.level)
		if w.level == LevelBodies {
			b.WriteString("wire log: bodies include document names and the shape of the library\n")
		}
	}
	method = methodOrGET(method)
	fmt.Fprintf(&b, "→ %s %s\n", method, safeURL(u))
	writeHeaders(&b, "→", header)
	writeBody(&b, "→", u, header, body, false, 0, skipBody, w.level)
	_, _ = w.writer.Write([]byte(b.String()))
}

func (w *wire) received(status int, method string, u *url.URL, header http.Header, body []byte, intoFile bool, n int64) {
	w.response(status, method, u, header, func(b *strings.Builder) {
		writeBody(b, "←", u, header, body, intoFile, n, false, w.level)
	})
}

// oversized is a response whose body passed limit and was dropped unread
// past it.
func (w *wire) oversized(status int, method string, u *url.URL, header http.Header, limit int64) {
	w.response(status, method, u, header, func(b *strings.Builder) {
		fmt.Fprintf(b, "  ← body: more than %d bytes — dropped\n", limit)
	})
}

func (w *wire) response(status int, method string, u *url.URL, header http.Header, body func(*strings.Builder)) {
	if !w.on() {
		return
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	var b strings.Builder
	fmt.Fprintf(&b, "← %d %s %s\n", status, methodOrGET(method), safeURL(u))
	writeHeaders(&b, "←", header)
	body(&b)
	_, _ = w.writer.Write([]byte(b.String()))
}

func (w *wire) failed(method string, u *url.URL, errText string) {
	if !w.on() {
		return
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	var b strings.Builder
	fmt.Fprintf(&b, "←! %s %s — %s\n", methodOrGET(method), safeURL(u), errText)
	_, _ = w.writer.Write([]byte(b.String()))
}

func (w *wire) redirected(status int, u *url.URL) {
	if !w.on() {
		return
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	var b strings.Builder
	fmt.Fprintf(&b, "→→ %d redirect to %s\n", status, safeURL(u))
	_, _ = w.writer.Write([]byte(b.String()))
}

func methodOrGET(method string) string {
	if method == "" {
		return http.MethodGet
	}
	return method
}

func writeHeaders(b *strings.Builder, arrow string, h http.Header) {
	if len(h) == 0 {
		return
	}
	type line struct {
		name  string
		value string
	}
	lines := make([]line, 0, len(h))
	for name, values := range h {
		for _, value := range values {
			shown := value
			if secretHeader(name) {
				shown = maskSecret(value)
			}
			lines = append(lines, line{name, shown})
		}
	}
	slices.SortStableFunc(lines, func(a, b line) int {
		if c := cmp.Compare(strings.ToLower(a.name), strings.ToLower(b.name)); c != 0 {
			return c
		}
		return cmp.Compare(a.name, b.name)
	})
	for _, ln := range lines {
		fmt.Fprintf(b, "  %s %s: %s\n", arrow, ln.name, ln.value)
	}
}

func writeBody(b *strings.Builder, arrow string, u *url.URL, header http.Header, body []byte, intoFile bool, n int64, skipBody bool, level Level) {
	if intoFile {
		fmt.Fprintf(b, "  ← body: %d bytes into a file\n", n)
		return
	}
	if skipBody || len(body) == 0 {
		return
	}
	if u != nil && strings.Contains(u.Path, "/token/json/") {
		fmt.Fprintf(b, "  %s body: %d bytes (credential exchange — never printed)\n", arrow, len(body))
		return
	}
	prefix := body
	if len(prefix) > bodyLimit {
		prefix = prefix[:bodyLimit]
	}
	if level != LevelBodies || !readableType(contentTypeOf(header)) || !utf8.Valid(prefix) {
		fmt.Fprintf(b, "  %s body: %d bytes\n", arrow, len(body))
		return
	}
	text := strings.ReplaceAll(string(prefix), "\n", "\n     ")
	if len(body) > bodyLimit {
		text += fmt.Sprintf("… (%d bytes in all)", len(body))
	}
	fmt.Fprintf(b, "  %s body: %s\n", arrow, text)
}

func readableType(contentType string) bool {
	ct := strings.ToLower(contentType)
	return strings.HasPrefix(ct, "text/") || strings.Contains(ct, "json") || strings.Contains(ct, "xml")
}

func contentTypeOf(h http.Header) string {
	for name, values := range h {
		if strings.EqualFold(name, "Content-Type") && len(values) > 0 {
			return values[0]
		}
	}
	return ""
}

func secretHeader(name string) bool {
	lower := strings.ToLower(name)
	if lower == "rm-token-ttl-hint" {
		return false
	}
	return strings.Contains(lower, "authorization") ||
		strings.Contains(lower, "token") ||
		strings.Contains(lower, "cookie") ||
		strings.Contains(lower, "signature") ||
		strings.Contains(lower, "secret")
}

// maskKey is drawn once per process, so the same secret masks the same way
// across every transport in one log. crypto/rand.Read does not fail: since
// Go 1.24 it crashes the program rather than return a short read.
//
//nolint:gochecknoglobals // one key per process keeps masks comparable
var maskKey = sync.OnceValue(func() []byte {
	key := make([]byte, 32)
	_, _ = rand.Read(key)
	return key
})

func maskSecret(value string) string {
	if value == "" {
		return "<empty>"
	}
	scheme, rest, ok := strings.Cut(value, " ")
	secret := value
	prefix := ""
	if ok {
		prefix = scheme + " "
		secret = rest
	}
	if secret == "" {
		return prefix + "<empty>"
	}
	mac := hmac.New(sha256.New, maskKey())
	_, _ = mac.Write([]byte(secret))
	sum := mac.Sum(nil)
	return fmt.Sprintf("%s<len:%d:%x>", prefix, len(secret), sum[:4])
}

func safeURL(u *url.URL) string {
	if u == nil {
		return "<no url>"
	}
	clone := *u
	n := queryItems(clone.RawQuery)
	clone.RawQuery = ""
	clone.ForceQuery = false
	clone.Opaque = ""
	text := clone.String()
	if n > 0 {
		text += fmt.Sprintf(" (+%d query items)", n)
	}
	return text
}

func queryItems(raw string) int {
	if raw == "" {
		return 0
	}
	n := 0
	for seg := range strings.SplitSeq(raw, "&") {
		if seg != "" {
			n++
		}
	}
	return n
}
