# wiring

```go
import "github.com/altessa-s/rmcloud-go/internal/wiring"
```

Package wiring is the composition root: the one place that knows every
adapter and every use case and connects them. The public package reads
its options, hands them over as a [Config], and keeps only interfaces to
what [Build] returns.

## API

| Symbol |
|---|
| `func DefaultUserAgent() string` |
| `func Files() port.Files` |
| `type Config struct{ ... }` |
| `type Parts struct{ ... }` |
| `func Build(cfg Config) Parts` |

See `doc.go` and `go doc` for the full contract.
