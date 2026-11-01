# syncstore

```go
import "github.com/altessa-s/rmcloud-go/internal/app/syncstore"
```

Package syncstore reads and writes the reMarkable sync store.

The package is a client, not a host. A root read tries v4 and falls back
to v3 only on 404. A write always uses v3. A 412 or 409 is the caller's
conflict: this package does not merge documents.

## API

| Symbol |
|---|
| `type Client struct{ ... }` |
| `func New(tokens Tokens, transport port.Transport, files port.Files, clk clock.Clock, ...) *Client` |
| `type Hosts = port.SyncHosts` |
| `type Option func(o *options)` |
| `func WithHosts(v Hosts) Option` |
| `func WithLogger(v *slog.Logger) Option` |
| `func WithPolicy(v retry.Policy) Option` |
| `func WithUserAgent(ua string) Option` |
| `type Tokens = port.RefreshableTokens` |

See `doc.go` and `go doc` for the full contract.
