# auth

```go
import "github.com/altessa-s/rmcloud-go/internal/app/auth"
```

Package auth buys and keeps the device token and the user token.

Registration, revocation, and the user-token exchange live here. The user
token is not part of the account surface a host is given: the host asks
for a plan, and the token stays inside this process. One exchange is in
flight at a time. A caller that leaves does not cancel it, because the
others are still waiting on that same answer.

A user token is judged by its own exp, which is the server's time. Each
bought token's iat shows how far the local clock stands from the
server's, and every later judgement allows for that skew, so a machine
whose clock is hours off keeps the token it bought instead of buying
another on every request. A skew past five minutes is logged.

## API

| Symbol |
|---|
| `const DefaultExchangeTimeout = 2 * time.Minute` |
| `type Option func(o *options)` |
| `func WithDeviceDesc(v string) Option` |
| `func WithEndpoints(v domain.Endpoints) Option` |
| `func WithExchangeTimeout(v time.Duration) Option` |
| `func WithID(v func() (string, error)) Option` |
| `func WithLogger(v *slog.Logger) Option` |
| `func WithPolicy(v retry.Policy) Option` |
| `func WithUserAgent(ua string) Option` |
| `type Provider struct{ ... }` |
| `func New(secrets port.SecretStore, trip port.RoundTripper, clk clock.Clock, ...) *Provider` |

See `doc.go` and `go doc` for the full contract.
