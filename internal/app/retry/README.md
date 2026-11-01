# retry

```go
import "github.com/altessa-s/rmcloud-go/internal/app/retry"
```

Package retry repeats a call that failed for a reason a second try can fix.

The loop and the backoff are go-atlas-core's retry package. This package
holds what the protocol adds: which errors are worth repeating and a
server's Retry-After. The default policy makes three attempts, from
300ms growing threefold, a 20s ceiling, and up to a fifth of the delay
added as spread. A root conflict is not
repeated here. The writer re-reads the root and rebuilds the tree first.

## API

| Symbol |
|---|
| `func Run[T any](ctx context.Context, r *Runner, op func(context.Context) (T, error)) (T, error)` |
| `func OnlyRateLimits(e domain.Error) bool` |
| `func ShouldRetry(e domain.Error) bool` |
| `type Policy struct{ ... }` |
| `func Default() Policy` |
| `func (p Policy) On(clk clock.Clock) *Runner` |
| `type Runner struct{ ... }` |

See `doc.go` and `go doc` for the full contract.
