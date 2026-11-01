# domaintest

```go
import "github.com/altessa-s/rmcloud-go/internal/domaintest"
```

Package domaintest compares protocol errors in tests.

## API

| Symbol |
|---|
| `func SameError(err, want error) bool` |
| `func SameKind(err error, want domain.Error) bool` |

See `doc.go` and `go doc` for the full contract.
