// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package porttest

import (
	"context"
	"errors"
	"maps"
	"sync"

	"github.com/altessa-s/rmcloud-go/internal/domain"
	"github.com/altessa-s/rmcloud-go/internal/port"
)

// ErrRefused is Forget's answer when the double has been told to keep the
// value. It is not a [domain.Error]: a store that will not delete is a host
// failure, and the retry policy must not treat it as a protocol failure.
var ErrRefused = errors.New("rmcloud: secret deletion refused")

// Secrets is the three protocol secrets, in memory.
//
// RefuseForget acts out the one store failure worth rehearsing. Forgetting a
// device drops its token first and the rest of the registration only if that
// delete landed, so a credential cannot outlive every name we have for it.
type Secrets struct {
	mu     sync.Mutex
	values map[domain.SecretName]string
	refuse bool
}

// NewSecrets returns a store holding initial. The map is copied.
func NewSecrets(initial map[domain.SecretName]string) *Secrets {
	values := maps.Clone(initial)
	if values == nil {
		values = make(map[domain.SecretName]string)
	}
	return &Secrets{values: values}
}

// RefuseForget makes the next Forget calls fail closed and keep the value.
func (s *Secrets) RefuseForget(refuse bool) {
	s.mu.Lock()
	s.refuse = refuse
	s.mu.Unlock()
}

// Secret returns the stored value. An absent name is ok false and a nil error.
func (s *Secrets) Secret(ctx context.Context, which domain.SecretName) (string, bool, error) {
	if err := ctx.Err(); err != nil {
		return "", false, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	value, ok := s.values[which]
	return value, ok, nil
}

// Save writes value under which, including the empty string, which is present.
func (s *Secrets) Save(ctx context.Context, which domain.SecretName, value string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.values[which] = value
	return nil
}

// Forget removes which. Removing a name that is not there is success.
// A refused delete leaves the value where it was.
func (s *Secrets) Forget(ctx context.Context, which domain.SecretName) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.refuse {
		return ErrRefused
	}
	delete(s.values, which)
	return nil
}

var _ port.SecretStore = (*Secrets)(nil)
