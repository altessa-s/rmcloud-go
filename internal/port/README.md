# port

```go
import "github.com/altessa-s/rmcloud-go/internal/port"
```

Package port is what the protocol asks of its host.

The interfaces are narrow on purpose. A secret store, an HTTP exchange, a
notification socket, and the local files are separate reasons to change.
Time is clock.Clock from go-atlas-core.
This package holds no implementation and does not import net/http, so a test
can stand in for all of them.

## API

| Symbol |
|---|
| `type Account interface{ ... }` |
| `type Dialer interface{ ... }` |
| `type Downloader interface{ ... }` |
| `type Event struct{ ... }` |
| `type EventKind string` |
| `const EventSyncComplete EventKind = "SyncComplete" ...` |
| `type Files interface{ ... }` |
| `type Frame struct{ ... }` |
| `type Header map[string][]string` |
| `type Hosts interface{ ... }` |
| `type IdleCloser interface{ ... }` |
| `type NotificationHosts interface{ ... }` |
| `type NotificationStream interface{ ... }` |
| `type RefreshableTokens interface{ ... }` |
| `type Request struct{ ... }` |
| `type Response struct{ ... }` |
| `type RoundTripper interface{ ... }` |
| `type SecretStore interface{ ... }` |
| `type Signal struct{ ... }` |
| `type SignalKind int` |
| `const SignalConnected SignalKind = iota ...` |
| `type Socket interface{ ... }` |
| `type SyncHosts interface{ ... }` |
| `type SyncStore interface{ ... }` |
| `type Transport interface{ ... }` |
| `type Uploader interface{ ... }` |
| `type UserTokens interface{ ... }` |

See `doc.go` and `go doc` for the full contract.
