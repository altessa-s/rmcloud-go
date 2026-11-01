// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package wiring

import (
	"crypto/x509"
	"log/slog"
	"net/http"
	"os"

	"github.com/altessa-s/go-atlas-core/time/clock"

	"github.com/altessa-s/rmcloud-go/internal/adapter/fs"
	"github.com/altessa-s/rmcloud-go/internal/adapter/httpapi"
	"github.com/altessa-s/rmcloud-go/internal/adapter/ws"
	"github.com/altessa-s/rmcloud-go/internal/app/auth"
	"github.com/altessa-s/rmcloud-go/internal/app/discovery"
	"github.com/altessa-s/rmcloud-go/internal/app/notify"
	"github.com/altessa-s/rmcloud-go/internal/app/syncstore"
	"github.com/altessa-s/rmcloud-go/internal/domain"
	"github.com/altessa-s/rmcloud-go/internal/port"
)

// Config is everything [Build] needs, already resolved by the caller: no
// field here has a default of its own except where it says so.
type Config struct {
	Secrets      port.SecretStore
	Endpoints    domain.Endpoints
	Resolver     port.Hosts // nil: discovery for the production pair, else pinned
	UserAgent    string     // empty sends no header
	DeviceDesc   string
	RoundTripper http.RoundTripper // nil: pooled transports with go-tlsutils TLS
	RootCAs      *x509.CertPool
	Logger       *slog.Logger
	OnSignal     func(port.Signal)
}

// Parts are the use cases and the one adapter the facade closes, as the
// ports the facade holds.
type Parts struct {
	Store     port.SyncStore
	Account   port.Account
	Stream    port.NotificationStream
	Transport port.IdleCloser
}

// The concrete parts must keep matching their ports.
var (
	_ port.SyncStore          = (*syncstore.Client)(nil)
	_ port.Account            = (*auth.Provider)(nil)
	_ port.NotificationStream = (*notify.Stream)(nil)
	_ port.IdleCloser         = (*httpapi.Transport)(nil)
	_ port.Hosts              = (*discovery.Resolver)(nil)
)

// Build wires the adapters into the use cases. It does no I/O.
//
// Token calls always go to cfg.Endpoints. With the production pair and no
// resolver, sync and notifications follow discovery and the HTTP clients
// refuse private and loopback dials; any other pair is pinned, and a server
// on the host's own network stays reachable. The notification socket has no
// such guard: its host is one domain.TrustedHost accepted, or the
// configured one.
func Build(cfg Config) Parts {
	discovering := cfg.Endpoints == domain.Production() && cfg.Resolver == nil
	clk := clock.Real()

	httpOpts := []httpapi.Option{
		httpapi.WithRoundTripper(cfg.RoundTripper),
		httpapi.WithRootCAs(cfg.RootCAs),
		httpapi.WithLogger(cfg.Logger),
		// The one switch read from the environment, here rather than in the
		// adapter, so the adapter holds no process-wide configuration.
		httpapi.WithLevel(httpapi.ParseLevel(os.Getenv("RMCLOUD_WIRE_LOG"))),
	}
	if !discovering {
		httpOpts = append(httpOpts, httpapi.WithoutSSRFProtection())
	}
	trip := httpapi.New(httpOpts...)

	account := auth.New(cfg.Secrets, trip, clk,
		auth.WithEndpoints(cfg.Endpoints),
		auth.WithUserAgent(cfg.UserAgent),
		auth.WithLogger(cfg.Logger),
		auth.WithDeviceDesc(cfg.DeviceDesc),
	)

	hosts := cfg.Resolver
	switch {
	case hosts != nil:
	case !discovering:
		hosts = discovery.NewResolver(cfg.Endpoints, clk, discovery.WithPinned(), discovery.WithLogger(cfg.Logger))
	default:
		hosts = discovery.NewResolver(cfg.Endpoints, clk,
			discovery.WithTokens(account),
			discovery.WithDiscovery(httpapi.NewDiscovery(cfg.RoundTripper, httpapi.WithRootCAs(cfg.RootCAs))),
			discovery.WithLogger(cfg.Logger),
		)
	}

	store := syncstore.New(
		account, trip, Files(), clk,
		syncstore.WithHosts(hosts),
		syncstore.WithUserAgent(cfg.UserAgent),
		syncstore.WithLogger(cfg.Logger),
	)
	stream := notify.New(
		hosts, account, ws.Dialer{Transport: socketTransport(cfg.RoundTripper, cfg.RootCAs)}, clk,
		cfg.OnSignal,
		notify.WithUserAgent(cfg.UserAgent),
		notify.WithLogger(cfg.Logger),
	)
	return Parts{Store: store, Account: account, Stream: stream, Transport: trip}
}

// Files is the local filesystem behind the port.
func Files() port.Files { return fs.Files{} }

// DefaultUserAgent is the User-Agent the service expects, for this machine's macOS
// version, or the empty string when that version cannot be read.
func DefaultUserAgent() string {
	if version := fs.HostVersion(); version != "" {
		return domain.UserAgent(version)
	}
	return ""
}

// socketTransport carries the notification upgrade: the host's round
// tripper, or a pooled transport with the clients' TLS.
func socketTransport(rt http.RoundTripper, roots *x509.CertPool) http.RoundTripper {
	if rt != nil {
		return rt
	}
	return httpapi.Pooled(roots)
}
