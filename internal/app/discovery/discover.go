// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package discovery

import (
	"context"
	"encoding/json/v2"
	"net/url"
	"strings"
	"time"

	"github.com/altessa-s/rmcloud-go/internal/domain"
	"github.com/altessa-s/rmcloud-go/internal/port"
)

// discoveryTimeout bounds one discovery call. Discovery is advice; a slow
// answer is no answer.
const discoveryTimeout = 3 * time.Second

// discovered is a parsed discovery document. A nil pointer is a negative
// cache: the host answered, and the answer was not usable.
type discovered struct {
	notifications *url.URL
	webapp        *url.URL
}

func (r *Resolver) noteDrift(webapp *url.URL) {
	if webapp == nil {
		return
	}
	host := webapp.Hostname()
	if strings.EqualFold(host, r.configured.AuthHost.Hostname()) {
		return
	}
	r.logger().Info("discovery publishes a different auth host", "host", host)
}

func chooseNotifications(answer *discovered, ep domain.Endpoints, region string) *url.URL {
	if answer == nil || answer.notifications == nil {
		return nil
	}
	host := answer.notifications.Hostname()
	if strings.EqualFold(host, ep.SyncHost.Hostname()) {
		return answer.notifications
	}
	if region != "" && strings.HasPrefix(strings.ToLower(host), strings.ToLower(region)+".") {
		return answer.notifications
	}
	return nil
}

// discover returns the cached document when it is younger than an hour.
// Otherwise it joins the discovery call in flight, or starts one. A caller
// whose ctx ends first gets no document. Auth drift is logged once per call
// that stores a parsed document.
func (r *Resolver) discover(ctx context.Context, loc url.URL) *discovered {
	if answer, fresh := r.cached(); fresh {
		return answer
	}
	answer, err := r.discovery.Do(ctx, loc.String(), func(work context.Context) (*discovered, error) {
		if answer, fresh := r.cached(); fresh {
			return answer, nil
		}
		// Capture the time before the fetch. A clock that jumps while the
		// call is on the wire must not make this result look fresh.
		at := r.clock.Now()
		answer, store := r.fetch(work, loc)
		if !store {
			return answer, nil
		}
		r.mu.Lock()
		r.attempt = answer
		r.attemptAt = at
		r.haveAttempt = true
		r.mu.Unlock()
		if answer != nil {
			r.noteDrift(answer.webapp)
		}
		return answer, nil
	})
	if err != nil {
		return nil
	}
	return answer
}

// cached is the last discovery attempt while it is younger than an hour.
func (r *Resolver) cached() (*discovered, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.haveAttempt && r.clock.Now().Sub(r.attemptAt) < domain.DiscoveryTTL {
		return r.attempt, true
	}
	return nil, false
}

// fetch reads discovery. store is false when the call did not get an HTTP
// answer, or there is no discovery transport: the cache is left alone and
// the next call tries again. An HTTP status other than 200, and a body that
// is not the document, are stored as a negative cache.
func (r *Resolver) fetch(ctx context.Context, loc url.URL) (answer *discovered, store bool) {
	if r.trip == nil || ctx.Err() != nil {
		return nil, false
	}
	ctx, cancel := context.WithTimeout(ctx, discoveryTimeout)
	defer cancel()
	resp, err := r.trip.RoundTrip(ctx, port.Request{Method: domain.MethodGet, URL: loc})
	if err != nil {
		// Discovery is advice: the configured hosts still work. Say why it
		// was not taken, so a TLS or proxy failure is not invisible.
		r.logger().Debug("discovery did not answer; keeping the configured hosts", "err", err)
		return nil, false
	}
	return interpret(resp.Body, resp.Status)
}

// interpret stores a negative cache for anything but a 200 whose body is
// the document. A body past [domain.DiscoveryLimit] is not parsed.
func interpret(body []byte, status int) (*discovered, bool) {
	if status != domain.StatusOK || len(body) > domain.DiscoveryLimit || len(body) == 0 {
		return nil, true
	}
	return parseDiscovery(body)
}

func parseDiscovery(body []byte) (*discovered, bool) {
	var obj map[string]string
	if err := json.Unmarshal(body, &obj); err != nil || obj == nil {
		return nil, true
	}
	d := &discovered{}
	if u, ok := domain.TrustedHost(obj["notifications"]); ok {
		d.notifications = new(u)
	}
	if u, ok := domain.TrustedHost(obj["webapp"]); ok {
		d.webapp = new(u)
	}
	return d, true
}
