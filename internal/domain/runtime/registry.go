package runtime

import (
	"fmt"
	"slices"
	"sync"
)

// Registry holds the runtime providers available in one process (the agent
// registers the providers usable on its host). It is safe for concurrent
// use.
type Registry struct {
	mu        sync.RWMutex
	providers map[Type]Provider
}

func NewRegistry() *Registry {
	return &Registry{providers: make(map[Type]Provider)}
}

func (r *Registry) Register(p Provider) error {
	if p == nil {
		return fmt.Errorf("%w: nil provider", ErrInvalidSpec)
	}
	t := p.Type()
	if err := t.Validate(); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, dup := r.providers[t]; dup {
		return fmt.Errorf("%w: provider %q", ErrAlreadyExists, t)
	}
	r.providers[t] = p
	return nil
}

func (r *Registry) Get(t Type) (Provider, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	p, ok := r.providers[t]
	if !ok {
		return nil, fmt.Errorf("%w: provider %q", ErrNotFound, t)
	}
	return p, nil
}

// Types returns the registered runtime types in sorted order.
func (r *Registry) Types() []Type {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]Type, 0, len(r.providers))
	for t := range r.providers {
		out = append(out, t)
	}
	slices.Sort(out)
	return out
}

// Providers returns the registered providers ordered by type.
func (r *Registry) Providers() []Provider {
	r.mu.RLock()
	defer r.mu.RUnlock()
	types := make([]Type, 0, len(r.providers))
	for t := range r.providers {
		types = append(types, t)
	}
	slices.Sort(types)
	out := make([]Provider, 0, len(types))
	for _, t := range types {
		out = append(out, r.providers[t])
	}
	return out
}
