# neterr

```go
import "github.com/altessa-s/rmcloud-go/internal/adapter/neterr"
```

Package neterr names network failures as protocol errors, so the HTTP
transport and the notification socket report a lost host the same way.

## API

| Symbol |
|---|
| `func Classify(ctx context.Context, err error) error` |

See `doc.go` and `go doc` for the full contract.
