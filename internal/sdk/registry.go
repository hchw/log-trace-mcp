package sdk

import (
	"errors"
	"fmt"
	"sort"
	"sync"
)

// ErrTenantNotFound indicates an unknown or disabled tenant.
var ErrTenantNotFound = errors.New("tenant not found")

// AdapterInfo pairs an adapter with its engine type.
type AdapterInfo struct {
	Adapter Adapter
	Engine  EngineType
}

type slot struct {
	adapter Adapter
	engine  EngineType
	refs    int
	retired bool
}

// Registry holds the live adapter set and supports atomic hot reloads.
type Registry struct {
	mu    sync.Mutex
	slots map[string]*slot
}

// NewRegistry returns an empty registry.
func NewRegistry() *Registry {
	return &Registry{slots: map[string]*slot{}}
}

// Replace atomically swaps in a new adapter set. Adapters from the previous set
// are retired and closed once no in-flight caller holds them.
func (r *Registry) Replace(items map[string]AdapterInfo) {
	newSlots := make(map[string]*slot, len(items))
	for id, info := range items {
		newSlots[id] = &slot{adapter: info.Adapter, engine: info.Engine}
	}

	r.mu.Lock()
	old := r.slots
	r.slots = newSlots
	var toClose []Adapter
	for _, s := range old {
		s.retired = true
		if s.refs == 0 {
			toClose = append(toClose, s.adapter)
		}
	}
	r.mu.Unlock()

	for _, a := range toClose {
		_ = a.Close()
	}
}

// Acquire returns the adapter for id plus a release function that must be
// called when the caller is done.
func (r *Registry) Acquire(id string) (Adapter, EngineType, func(), error) {
	r.mu.Lock()
	s, ok := r.slots[id]
	if !ok {
		ids := r.idsLocked()
		r.mu.Unlock()
		if len(ids) == 0 {
			return nil, "", nil, fmt.Errorf("%w: %q (no tenants configured)", ErrTenantNotFound, id)
		}
		return nil, "", nil, fmt.Errorf("%w: %q (available: %v)", ErrTenantNotFound, id, ids)
	}
	s.refs++
	r.mu.Unlock()

	release := func() {
		r.mu.Lock()
		s.refs--
		closeNow := s.retired && s.refs == 0
		r.mu.Unlock()
		if closeNow {
			_ = s.adapter.Close()
		}
	}
	return s.adapter, s.engine, release, nil
}

// TypeOf returns the engine type for an tenant.
func (r *Registry) TypeOf(id string) (EngineType, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	s, ok := r.slots[id]
	if !ok {
		return "", false
	}
	return s.engine, true
}

// IDs returns the sorted set of known tenant ids.
func (r *Registry) IDs() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.idsLocked()
}

func (r *Registry) idsLocked() []string {
	ids := make([]string, 0, len(r.slots))
	for id := range r.slots {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

// Close closes all adapters.
func (r *Registry) Close() error {
	r.mu.Lock()
	slots := r.slots
	r.slots = map[string]*slot{}
	r.mu.Unlock()
	var firstErr error
	for _, s := range slots {
		if err := s.adapter.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}
