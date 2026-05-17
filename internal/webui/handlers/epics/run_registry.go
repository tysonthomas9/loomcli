package epics

import (
	"sync"
)

type runRegistry struct {
	mu     sync.Mutex
	active map[string]struct{}
}

func newRunRegistry() *runRegistry {
	return &runRegistry{active: make(map[string]struct{})}
}

func (r *runRegistry) tryStart(key string) (func(), bool) {
	if r == nil || key == "" {
		return func() {}, true
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.active[key]; exists {
		return func() {}, false
	}
	r.active[key] = struct{}{}
	return func() {
		r.mu.Lock()
		delete(r.active, key)
		r.mu.Unlock()
	}, true
}
