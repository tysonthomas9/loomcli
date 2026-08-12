package cli

import (
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/tysonthomas9/loomcli/internal/backendnames"
	"github.com/tysonthomas9/loomcli/internal/usage"
)

// Backend is the interface that all AI coding agent backends must implement.
//
// Name returns a unique identifier for the backend (e.g. "claude", "codex").
// InvokeInteractive starts a live, interactive agent session in the terminal.
// InvokeNonInteractive runs a headless agent session that can be canceled via
// the shutdown channel.
type Backend interface {
	Name() string
	InvokeInteractive(workDir, prompt, agentName string) error
	InvokeNonInteractive(workDir, prompt, agentName string, shutdown <-chan struct{}, collector *usage.Collector) error
}

var (
	backends      = make(map[string]Backend)
	activeBackend = backendnames.Codex
	backendFlag   string // set by --backend persistent flag in root.go
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

// ResolveBackendName returns the backend name using the precedence chain:
// --backend flag > LOOM_BACKEND env > default backend. Persistent backend
// settings now live in FleetDB daemon profiles and are applied by the daemon,
// not read from local YAML during CLI startup.
func ResolveBackendName() string {
	// 1. --backend flag (highest priority)
	if backendFlag != "" {
		return backendFlag
	}
	// 2. LOOM_BACKEND environment variable
	if env := os.Getenv("LOOM_BACKEND"); env != "" {
		return env
	}
	return backendnames.Codex
}

// ResolveAndSetBackend resolves the backend name from the precedence chain
// and sets it as the active backend. Returns an error if the resolved name
// is not a registered backend.
func ResolveAndSetBackend() error {
	name := ResolveBackendName()
	return SetBackend(name)
}

// IsRegistered reports whether a backend with the given name exists in the registry.
func IsRegistered(name string) bool {
	backendMu.RLock()
	defer backendMu.RUnlock()
	_, ok := backends[name]
	return ok
}

// GetBackendByName returns the registered backend with the given name.
// Returns nil, false if no backend is registered with that name.
func GetBackendByName(name string) (Backend, bool) {
	backendMu.RLock()
	defer backendMu.RUnlock()
	b, ok := backends[name]
	return b, ok
}

// ValidBackendNames returns a formatted string of valid backend names for help text.
func ValidBackendNames() string {
	return strings.Join(ListBackends(), ", ")
}

// daemonMode records that this process was spawned by the daemon supervisor
// (`--daemon-mode`). It is set once, early, by whichever agent command owns the
// invocation, and read by InvokeAgent to refuse the interactive path.
var daemonMode atomic.Bool

// SetDaemonMode marks this process as daemon-supervised. Call it as soon as
// the flag is known, before any backend invocation.
func SetDaemonMode(on bool) { daemonMode.Store(on) }

// InDaemonMode reports whether this process was spawned by the supervisor.
func InDaemonMode() bool { return daemonMode.Load() }

// ErrInteractiveInDaemonMode is returned when a daemon-supervised process
// tries to start an interactive session.
var ErrInteractiveInDaemonMode = errors.New(
	"interactive invocation refused: this process is daemon-supervised and has no controlling TTY")

// InvokeAgent dispatches an interactive invocation to the active backend.
//
// It refuses outright under `--daemon-mode`. A supervised agent has no
// controlling TTY, so an interactive backend renders its TUI into nothing and
// the process exits 0 having done no work — which the supervisor cannot
// distinguish from a successful run, so completion hooks fire and the pipeline
// advances on a phantom. That failure has been observed twice: once as a custom
// role silently killed by the watchdog at 15 minutes, and once as EVERY daemon
// agent exiting 0 in ~65ms while labels kept being stamped. Both times the
// pipeline looked healthy. Failing loudly here is what makes those bugs visible
// at the first spawn instead of after a day of stamped labels.
func InvokeAgent(workDir, prompt, agentName string) error {
	if InDaemonMode() {
		return fmt.Errorf("%w; use InvokeAgentNonInteractive", ErrInteractiveInDaemonMode)
	}
	backendMu.RLock()
	name := activeBackend
	b, ok := backends[name]
	backendMu.RUnlock()
	if !ok {
		return fmt.Errorf("backend %q not registered", name)
	}
	return b.InvokeInteractive(workDir, prompt, agentName)
}

// InvokeAgentNonInteractive dispatches a non-interactive invocation to the active backend.
func InvokeAgentNonInteractive(workDir, prompt, agentName string, shutdown <-chan struct{}, collector *usage.Collector) error {
	backendMu.RLock()
	name := activeBackend
	b, ok := backends[name]
	backendMu.RUnlock()
	if !ok {
		return fmt.Errorf("backend %q not registered", name)
	}
	return b.InvokeNonInteractive(workDir, prompt, agentName, shutdown, collector)
}

// GetBackendFlag returns the current value of the --backend CLI flag.
func GetBackendFlag() string { return backendFlag }

// TestingBackendFlag returns a pointer to the backendFlag string for test packages.
func TestingBackendFlag() *string { return &backendFlag }

// TestingBackendMu returns the backend registry mutex for test packages.
func TestingBackendMu() *sync.RWMutex { return &backendMu }

// TestingBackends returns the backend registry map for test packages.
func TestingBackends() map[string]Backend { return backends }

// TestingActiveBackend returns a pointer to the activeBackend string for test packages.
func TestingActiveBackend() *string { return &activeBackend }

// TestingResetBackendState saves and restores backend registry state for tests.
// It clears the current registry so the test starts with an empty map, and
// restores the original entries on cleanup.
func TestingResetBackendState(t interface {
	Helper()
	Cleanup(func())
}) {
	t.Helper()
	backendMu.Lock()
	origBackends := make(map[string]Backend, len(backends))
	for k, v := range backends {
		origBackends[k] = v
	}
	origActive := activeBackend
	// Clear the map in-place so tests start with an empty registry.
	for k := range backends {
		delete(backends, k)
	}
	activeBackend = ""
	backendMu.Unlock()
	t.Cleanup(func() {
		backendMu.Lock()
		// Clear any test-added entries.
		for k := range backends {
			delete(backends, k)
		}
		// Restore original entries.
		for k, v := range origBackends {
			backends[k] = v
		}
		activeBackend = origActive
		backendMu.Unlock()
	})
}
