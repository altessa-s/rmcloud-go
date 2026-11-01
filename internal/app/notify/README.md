# notify

```go
import "github.com/altessa-s/rmcloud-go/internal/app/notify"
```

Package notify reads the account's push channel.

The channel is receive-only. A frame means the account changed; the
caller re-reads the root. Only SyncComplete says the documents changed.
The socket is kept open with a ping, and a dropped socket is dialed
again after a pause. A platform without a socket is reported once.

## API

| Symbol |
|---|
| `const KindSyncComplete = port.EventSyncComplete ...` |
| `const DefaultPingInterval = 30 * time.Second ...` |
| `const SignalConnected = port.SignalConnected ...` |
| `type Event = port.Event` |
| `type Hosts = port.NotificationHosts` |
| `type Kind = port.EventKind` |
| `type Option func(o *options)` |
| `func WithLogger(v *slog.Logger) Option` |
| `func WithPingInterval(v time.Duration) Option` |
| `func WithPingTimeout(v time.Duration) Option` |
| `func WithUserAgent(ua string) Option` |
| `type Signal = port.Signal` |
| `type SignalKind = port.SignalKind` |
| `type Stream struct{ ... }` |
| `func New(hosts Hosts, tokens Tokens, dialer port.Dialer, clk clock.Clock, ...) *Stream` |
| `type Tokens = port.RefreshableTokens` |

See `doc.go` and `go doc` for the full contract.
