# httpapi

```go
import "github.com/altessa-s/rmcloud-go/internal/adapter/httpapi"
```

Package httpapi is the net/http adapter behind port.Transport, and the
separate, unauthenticated transport that reads discovery.

Redirects are followed, and the user token stays on the origin the
request started from. The wire log masks credentials. Which host a call
reaches is not decided here; package hosts does that.

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
| `func WithLevel(v Level) Option` |
| `func WithLogger(v *slog.Logger) Option` |
| `func WithRootCAs(v *x509.CertPool) Option` |
| `func WithRoundTripper(v http.RoundTripper) Option` |
| `func WithWireWriter(v io.Writer) Option` |
| `func WithoutSSRFProtection() Option` |
| `type Transport struct{ ... }` |
| `func New(opts ...Option) *Transport` |

See `doc.go` and `go doc` for the full contract.
