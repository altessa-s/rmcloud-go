# discovery

```go
import "github.com/altessa-s/rmcloud-go/internal/app/discovery"
```

Package discovery decides which hosts one call reaches.

The auth host is always the configured one. The sync host follows the
region named in the user token. The notification socket follows the
discovery document when that document names a trusted host for the sync
host in use. Discovery is cached for an hour and read through a port, so
this package makes no request of its own.

## API

| Symbol |
|---|
| `type Option func(o *options)` |
| `func WithDiscovery(v port.RoundTripper) Option` |
| `func WithLogger(v *slog.Logger) Option` |
| `func WithPinned() Option` |
| `func WithTokens(v TokenSource) Option` |
| `type Resolver struct{ ... }` |
| `func NewResolver(configured domain.Endpoints, clk clock.Clock, opts ...Option) *Resolver` |
| `type TokenSource = port.UserTokens` |

See `doc.go` and `go doc` for the full contract.
