# porttest

```go
import "github.com/altessa-s/rmcloud-go/internal/porttest"
```

Package porttest holds doubles of the port interfaces, for a test that
must not touch the network, the secret store, or the wall clock.

## API

| Symbol |
|---|
| `var ErrRefused = errors.New("rmcloud: secret deletion refused")` |
| `type Clock struct{ ... }` |
| `func NewClock() *Clock` |
| `func NewClockAt(now time.Time) *Clock` |
| `func Recording(c clock.Clock) *Clock` |
| `type MissingDialer struct{}` |
| `type Reply struct{ ... }` |
| `type Secrets struct{ ... }` |
| `func NewSecrets(initial map[domain.SecretName]string) *Secrets` |
| `type Transport struct{ ... }` |

See `doc.go` and `go doc` for the full contract.
