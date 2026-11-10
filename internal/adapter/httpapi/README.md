# httpapi

```go
import "github.com/altessa-s/rmcloud-go/internal/adapter/httpapi"
```

Package httpapi is the net/http adapter behind port.Transport, and the
separate, unauthenticated transport that reads discovery.

Redirects are followed, and the user token stays on the origin the
request started from. The wire log masks credentials. Which host a call
reaches is not decided here; package hosts does that.

No request has a deadline of its own. A request that stopped moving is
given up on: response headers that never come, an in-memory body that
stalls, an HTTP/2 connection that leaves a ping unanswered. Each fails
as a transport error the retry policy repeats, and the caller's context
ending is returned as it is.

## API

| Symbol |
|---|
| `func Pooled(roots *x509.CertPool) *http.Transport` |
| `type Discovery struct{ ... }` |
| `func NewDiscovery(rt http.RoundTripper, opts ...Option) *Discovery` |
| `type Level int` |
| `const LevelOff Level = iota ...` |
| `func ParseLevel(raw string) Level` |
| `type Option func(o *options)` |
| `func WithIdleReadTimeout(v time.Duration) Option` |
| `func WithLevel(v Level) Option` |
| `func WithLogger(v *slog.Logger) Option` |
| `func WithPingAfter(v time.Duration) Option` |
| `func WithPingTimeout(v time.Duration) Option` |
| `func WithResponseHeaderTimeout(v time.Duration) Option` |
| `func WithRootCAs(v *x509.CertPool) Option` |
| `func WithRoundTripper(v http.RoundTripper) Option` |
| `func WithUploadResponseHeaderTimeout(v time.Duration) Option` |
| `func WithWireWriter(v io.Writer) Option` |
| `func WithoutSSRFProtection() Option` |
| `type Transport struct{ ... }` |
| `func New(opts ...Option) *Transport` |

See `doc.go` and `go doc` for the full contract.
