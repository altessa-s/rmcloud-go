# fs

```go
import "github.com/altessa-s/rmcloud-go/internal/adapter/fs"
```

Package fs is the operating system behind the ports: the filesystem and
the host's version.

The protocol packages declare what they need in package port and require
it as a constructor argument. Only the composition root hands them the values from
here; a test hands them the doubles from package porttest instead.

## API

| Symbol |
|---|
| `func HostVersion() string` |
| `type Files struct{}` |

See `doc.go` and `go doc` for the full contract.
