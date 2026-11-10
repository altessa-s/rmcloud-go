# domain

```go
import "github.com/altessa-s/rmcloud-go/internal/domain"
```

Package domain holds the sync protocol's pure types.

Nothing in this package opens a socket. Parsing, hashing, and the closed
set of protocol errors live here so a test can hold them without a server.
A new [Error] kind is a breaking change for every caller that switches on
[Kind].

## API

| Symbol |
|---|
| `const DeviceDescMac ...` |
| `const FileType = "0" ...` |
| `const IndexNameSuffix = ".docSchema" ...` |
| `const ContentTypeIndex = "text/plain; charset=UTF-8" ...` |
| `const HeaderFilename = "rm-filename" ...` |
| `const StatusOK = 200 ...` |
| `const MethodGet = "GET" ...` |
| `const CheckFilesCall = "check-files"` |
| `func BlobHash(data []byte) string` |
| `func CRC32C(data []byte) uint32` |
| `func CheckFilesBody(hashes []string) ([]byte, error)` |
| `func Claims(token string) map[string]any` |
| `func ContentType(name string) string` |
| `func DecodeIndex(data []byte, expected string) (Index, Addressing, error)` |
| `func DetailOf(body []byte) string` |
| `func DeviceID(token string) *string` |
| `func DigestReader(ctx context.Context, r io.Reader) (Digest, error)` |
| `func Expiry(token string) *time.Time` |
| `func FromEnvironment(env map[string]string) (Endpoints, error)` |
| `func GoogHash(data []byte) string` |
| `func GoogHashSum(sum uint32) string` |
| `func IndexName(documentID string) string` |
| `func IsDocumentUnavailable(err error) bool` |
| `func IsFresh(token string, moment time.Time) bool` |
| `func IsHash(s string) bool` |
| `func IsHostUnreachable(err error) bool` |
| `func IsKind(err error, kind Kind) bool` |
| `func IsServerError(status int) bool` |
| `func IsSuccess(status int) bool` |
| `func IsUsable(expiry, heldSince *time.Time, moment time.Time) bool` |
| `func IssuedAt(token string) *time.Time` |
| `func LimitFor(declared int64) int64` |
| `func NodeHash(entries []Entry) (string, error)` |
| `func NotificationsOn(host url.URL) url.URL` |
| `func Parse(text string) (Index, error)` |
| `func ParseEntry(line string) (Entry, error)` |
| `func ParseMissingFiles(data []byte, asked []string) ([]string, error)` |
| `func ParseRetryAfter(header string) *time.Duration` |
| `func ParseRoot(data []byte) (Root, RootAdvisory, error)` |
| `func PathOf(rawURL string) string` |
| `func RegistrationPage() url.URL` |
| `func SerializeDocument(entries []Entry, id string) string` |
| `func SerializeRoot(entries []Entry) string` |
| `func SystemVersion(raw string) string` |
| `func Tectonic(region string) (url.URL, bool)` |
| `func TrustedHost(value string) (url.URL, bool)` |
| `func UserAgent(macosVersion string) string` |
| `type Addressing int` |
| `const ByFileHash Addressing = iota ...` |
| `type Ceiling struct{ ... }` |
| `type Digest struct{ ... }` |
| `type Endpoints struct{ ... }` |
| `func Production() Endpoints` |
| `type Entry struct{ ... }` |
| `type Error interface{ ... }` |
| `var ErrNotRegistered Error = sentinel(KindNotRegistered) ...` |
| `func FromHTTP(status int, rawURL, retryAfter string, body []byte, unauthorized Error) Error` |
| `type HTTPError struct{ ... }` |
| `func HTTP(status int, path string) *HTTPError` |
| `type HashMismatchError struct{ ... }` |
| `func HashMismatch(expected, actual string) *HashMismatchError` |
| `type Index struct{ ... }` |
| `type Kind string` |
| `const KindNotRegistered Kind = "notRegistered" ...` |
| `func KindOf(err error) (kind Kind, ok bool)` |
| `type MalformedIndexError struct{ ... }` |
| `func MalformedIndex(line, reason string) *MalformedIndexError` |
| `type MalformedResponseError struct{ ... }` |
| `func MalformedResponse(what string) *MalformedResponseError` |
| `type MissingHashError struct{ ... }` |
| `func MissingHash(name string) *MissingHashError` |
| `type NotOnThisPlanError struct{ ... }` |
| `func NotOnThisPlan(detail string) *NotOnThisPlanError` |
| `type NothingToAskError struct{ ... }` |
| `func NothingToAsk(call string) *NothingToAskError` |
| `type OversizedResponseError struct{ ... }` |
| `func OversizedResponse(bytes, limit int64) *OversizedResponseError` |
| `type Plan struct{ ... }` |
| `func PlanFromToken(token string) *Plan` |
| `type Progress func(done, total int64)` |
| `type RateLimitedError struct{ ... }` |
| `func RateLimited(retryAfter *time.Duration) *RateLimitedError` |
| `type Root struct{ ... }` |
| `type RootAdvisory struct{ ... }` |
| `type RootConflictError struct{ ... }` |
| `func RootConflict(status int) *RootConflictError` |
| `type RootVersion string` |
| `const RootV3 RootVersion = "v3" ...` |
| `type Schema string` |
| `const SchemaV3 Schema = "3" ...` |
| `type Secret struct{ ... }` |
| `type SecretName string` |
| `const SecretDeviceToken SecretName = "deviceToken" ...` |
| `type TransportError struct{ ... }` |
| `func Transport(reason TransportReason) *TransportError` |
| `type TransportReason string` |
| `const ReasonTimedOut TransportReason = "timedOut" ...` |
| `type UnsupportedPlatformError struct{ ... }` |
| `func UnsupportedPlatform(what string) *UnsupportedPlatformError` |
| `type UnsupportedSchemaError struct{ ... }` |
| `func UnsupportedSchema(schema string) *UnsupportedSchemaError` |
| `type WriteRejectedError struct{ ... }` |
| `func WriteRejected(status int, detail string) *WriteRejectedError` |

See `doc.go` and `go doc` for the full contract.
