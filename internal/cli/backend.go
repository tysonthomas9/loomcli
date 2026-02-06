package cli

import (
	"fmt"
	"sort"
	"sync"
)

// Backend is the interface that all AI coding agent backends must implement.
type Backend interface {
	Name() string
	InvokeInteractive(workDir, prompt, agentName string) error
	InvokeNonInteractive(workDir, prompt, agentName string, shutdown <-chan struct{}) error
}

var (
	backends      = make(map[string]Backend)
	activeBackend = "claude"
	backendMu     sync.RWMutex
)

// RegisterBackend adds a backend to the registry by its Name().
// Panics if b is nil (programming error).
func RegisterBackend(b Backend) {
	if b == nil {
		panic("cli: RegisterBackend called with nil backend")
	}
	backendMu.Lock()
	defer backendMu.Unlock()
	backends[b.Name()] = b
}

// SetBackend validates and switches the active backend.
func SetBackend(name string) error {
	backendMu.Lock()
	defer backendMu.Unlock()
	if _, ok := backends[name]; !ok {
		return fmt.Errorf("unknown backend %q; available: %v", name, listBackendsLocked())
	}
	activeBackend = name
	return nil
}

// GetBackendName returns the name of the currently active backend.
func GetBackendName() string {
	backendMu.RLock()
	defer backendMu.RUnlock()
	return activeBackend
}

// ListBackends returns a sorted list of registered backend names.
func ListBackends() []string {
	backendMu.RLock()
	defer backendMu.RUnlock()
	return listBackendsLocked()
}

func listBackendsLocked() []string {
	names := make([]string, 0, len(backends))
	for name := range backends {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// InvokeBackend dispatches an interactive invocation to the active backend.
func InvokeBackend(workDir, prompt, agentName string) error {
	backendMu.RLock()
	name := activeBackend
	b, ok := backends[name]
	backendMu.RUnlock()
	if !ok {
		return fmt.Errorf("backend %q not registered", name)
	}
	return b.InvokeInteractive(workDir, prompt, agentName)
}

// InvokeBackendNonInteractive dispatches a non-interactive invocation to the active backend.
func InvokeBackendNonInteractive(workDir, prompt, agentName string, shutdown <-chan struct{}) error {
	backendMu.RLock()
	name := activeBackend
	b, ok := backends[name]
	backendMu.RUnlock()
	if !ok {
		return fmt.Errorf("backend %q not registered", name)
	}
	return b.InvokeNonInteractive(workDir, prompt, agentName, shutdown)
}
