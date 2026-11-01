# ws

```go
import "github.com/altessa-s/rmcloud-go/internal/adapter/ws"
```

Package ws dials the notification socket with github.com/coder/websocket.

The URL the rest of the client stores is http or https. Dial
rewrites the scheme and leaves the rest of the spelling alone.
A refused upgrade is a protocol error, read from the status.
Redirects are not followed, so the user token stays on the first request.

## API

| Symbol |
|---|
| `type Dialer struct{ ... }` |

See `doc.go` and `go doc` for the full contract.
