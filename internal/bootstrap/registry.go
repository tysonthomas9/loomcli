package bootstrap

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/tysonthomas9/loomcli/internal/lockfile"
	"github.com/tysonthomas9/loomcli/internal/netutil"
)

// EnvFleetDBRegistry overrides the host-wide active-fleet-db registry
// path. Defaults to $HOME/.loom/fleet-db/active.json. Tests set this to
// keep their probes isolated from the host's real registry.
const EnvFleetDBRegistry = "LOOM_FLEET_DB_REGISTRY"

// EnvFleetDBNoDiscovery, when set to "1", disables both reads from and
// writes to the host-wide registry. The per-data-dir reuse path still
// runs. Useful for tests and for users who intentionally want isolated
// fleet-db instances per data dir.
//
// Smart default: when LOOM_CONFIG_DIR is set AND
// LOOM_FLEET_DB_REGISTRY is NOT set, discovery is disabled
// automatically. An operator pointing the data dir at a non-default
// location and not opting into an explicit registry path is almost
// certainly running a parallel stack and would be surprised to silently
// join the host's existing fleet-db. Set
// LOOM_FLEET_DB_NO_DISCOVERY=0 to override and re-enable.
const EnvFleetDBNoDiscovery = "LOOM_FLEET_DB_NO_DISCOVERY"

// activeRegistryFile is the basename written under the registry directory.
const activeRegistryFile = "active.json"

// activeRegistryLockFile guards check-and-register so two concurrently
// starting `loom serve` processes can't both win the race and both
// register themselves.
const activeRegistryLockFile = "active.lock"

// ActiveRegistryEntry describes the currently-active local fleet-db on
// this host. There is at most one entry; the goal of the registry is
// exactly to prevent a second instance.
type ActiveRegistryEntry struct {
	PID       int       `json:"pid"`
	URL       string    `json:"url"`
	DataDir   string    `json:"data_dir,omitempty"`
	StartedAt time.Time `json:"started_at"`
	Schema    int       `json:"schema,omitempty"`
}

// activeRegistrySchema is the on-disk schema version. Bumped if the
// shape of ActiveRegistryEntry changes in a non-backward-compatible way.
const activeRegistrySchema = 1

// activeRegistryLock owns a flock on the registry's sibling lockfile.
type activeRegistryLock struct {
	path string
	file *os.File
}

// Release drops the lock. Safe to call multiple times.
func (l *activeRegistryLock) Release() error {
	if l == nil || l.file == nil {
		return nil
	}
	var err error
	if unlockErr := lockfile.FlockUnlock(l.file); unlockErr != nil {
		err = unlockErr
	}
	if closeErr := l.file.Close(); closeErr != nil && err == nil {
		err = closeErr
	}
	l.file = nil
	if err != nil {
		return fmt.Errorf("%s: %w", l.path, err)
	}
	return nil
}

// RegistryPath resolves the active-fleet-db registry file path.
// Returns "" when both LOOM_FLEET_DB_REGISTRY and $HOME are unavailable —
// callers should treat that as "no registry; behave as today".
func RegistryPath() string {
	if p := os.Getenv(EnvFleetDBRegistry); p != "" {
		return p
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return ""
	}
	return filepath.Join(home, ".loom", "fleet-db", activeRegistryFile)
}

// registryLockPath returns the path to the lockfile sibling to the
// registry. The two live in the same directory so a single MkdirAll
// covers both.
func registryLockPath(registryPath string) string {
	return filepath.Join(filepath.Dir(registryPath), activeRegistryLockFile)
}

// discoveryDisabled reports whether registry reads and writes are
// short-circuited.
//
// Explicit override (LOOM_FLEET_DB_NO_DISCOVERY=1 or =0) always wins.
// In the absence of that, when LOOM_CONFIG_DIR is set (non-empty) but
// LOOM_FLEET_DB_REGISTRY is NOT, discovery defaults to disabled. The
// operator has chosen a non-default data dir without coordinating on
// the registry path, which is the parallel-stack scenario; silently
// joining the host fleet-db there has caused real confusion. Empty
// values are treated as unset to match LoomDir()'s convention.
func discoveryDisabled() bool {
	if v, ok := os.LookupEnv(EnvFleetDBNoDiscovery); ok {
		return v == "1"
	}
	return os.Getenv("LOOM_CONFIG_DIR") != "" && os.Getenv(EnvFleetDBRegistry) == ""
}

// acquireActiveRegistryLock takes a non-blocking exclusive flock on the
// registry's sibling lockfile, creating the directory if needed.
// Returns lockfile.ErrLocked when another process already holds it.
func acquireActiveRegistryLock(registryPath string) (*activeRegistryLock, error) {
	if registryPath == "" {
		return nil, errors.New("registry: empty path")
	}
	dir := filepath.Dir(registryPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, fmt.Errorf("registry: mkdir %s: %w", dir, err)
	}
	lockPath := registryLockPath(registryPath)
	f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0600) //nolint:gosec // user-private registry lock
	if err != nil {
		return nil, fmt.Errorf("registry: open lock %s: %w", lockPath, err)
	}
	if err := lockfile.TryLockExclusive(f); err != nil {
		_ = f.Close()
		return nil, err
	}
	return &activeRegistryLock{path: lockPath, file: f}, nil
}

// ReadActiveRegistry reads the registry file. Returns (nil, nil) when
// the file does not exist. Returns (nil, err) when the file exists but
// is unreadable or malformed JSON — callers should log and treat as
// empty (a future write will overwrite it) rather than aborting startup.
func ReadActiveRegistry(registryPath string) (*ActiveRegistryEntry, error) {
	if registryPath == "" {
		return nil, nil
	}
	data, err := os.ReadFile(registryPath) //nolint:gosec // path is controlled (env or $HOME/.loom)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var entry ActiveRegistryEntry
	if err := json.Unmarshal(data, &entry); err != nil {
		return nil, fmt.Errorf("registry: parse %s: %w", registryPath, err)
	}
	if entry.URL == "" || entry.PID <= 0 {
		return nil, nil
	}
	return &entry, nil
}

// WriteActiveRegistry atomically writes the registry entry. The caller
// is responsible for holding the registry lock.
func WriteActiveRegistry(registryPath string, entry ActiveRegistryEntry) error {
	if registryPath == "" {
		return errors.New("registry: empty path")
	}
	if entry.Schema == 0 {
		entry.Schema = activeRegistrySchema
	}
	if entry.StartedAt.IsZero() {
		entry.StartedAt = time.Now().UTC()
	}
	dir := filepath.Dir(registryPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("registry: mkdir %s: %w", dir, err)
	}
	data, err := json.MarshalIndent(entry, "", "  ")
	if err != nil {
		return fmt.Errorf("registry: marshal: %w", err)
	}
	tmpPath := registryPath + ".tmp"
	if err := os.WriteFile(tmpPath, data, 0600); err != nil { //nolint:gosec // registry is user-private
		return fmt.Errorf("registry: write tmp %s: %w", tmpPath, err)
	}
	if err := os.Rename(tmpPath, registryPath); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("registry: rename %s: %w", registryPath, err)
	}
	_ = os.Chmod(registryPath, 0600)
	return nil
}

// RemoveActiveRegistryIfOwner removes the registry entry only when it
// still matches (pid, url). Used during Stop so we don't trample a
// successor process's registration.
func RemoveActiveRegistryIfOwner(registryPath string, pid int, url string) {
	if registryPath == "" {
		return
	}
	entry, err := ReadActiveRegistry(registryPath)
	if err != nil || entry == nil {
		return
	}
	if entry.PID == pid && entry.URL == url {
		_ = os.Remove(registryPath)
	}
}

// tryReuseActiveRegistry checks the host-wide registry for an active
// fleet-db this process can join. Returns (url, true, nil) on success.
// On miss returns ("", false, nil); on probe error returns ("", false, err)
// so callers can surface the conflict rather than silently spawning a
// second instance.
//
// This probe runs WITHOUT holding the registry lock. It is therefore
// read-only — it does not evict stale entries (the authoritative
// eviction happens under the lock inside StartEmbedded). Stale entries
// reported by this probe simply produce a (false, nil) miss because
// the live-PID check fails, and the caller proceeds to StartEmbedded
// which will evict.
func tryReuseActiveRegistry(ctx context.Context, registryPath string, logger *slog.Logger, timeout time.Duration) (string, bool, error) {
	if registryPath == "" || discoveryDisabled() {
		return "", false, nil
	}
	entry, err := ReadActiveRegistry(registryPath)
	if err != nil {
		if logger != nil {
			logger.Warn("registry read failed; ignoring", "path", registryPath, "err", err)
		}
		return "", false, nil
	}
	if entry == nil {
		return "", false, nil
	}
	if !lockfile.IsProcessRunning(entry.PID) {
		// Stale: report miss without mutating; StartEmbedded evicts under the lock.
		if logger != nil {
			logger.Debug("registry entry has stale pid; deferring eviction to StartEmbedded", "pid", entry.PID, "url", entry.URL)
		}
		return "", false, nil
	}
	healthCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	if err := netutil.WaitForHealthz(healthCtx, entry.URL, healthCheckTimeout); err != nil {
		// Live PID but unhealthy. Distinguish a recycled PID (the dead
		// fleet-db's PID was reused by an unrelated process, so nothing is
		// listening on entry.URL) from a fleet-db that is genuinely present
		// but sick. When the URL is unreachable the entry is effectively
		// stale: report a miss without mutating so the caller proceeds to
		// StartEmbedded, which evicts under the lock and spawns. When
		// something IS listening, surface the conflict instead of starting
		// a second one and orphaning a live peer.
		if !netutil.DialReachable(entry.URL, healthCheckTimeout) {
			if logger != nil {
				logger.Warn("registry entry pid live but url unreachable; treating as stale (deferring eviction to StartEmbedded)", "pid", entry.PID, "url", entry.URL)
			}
			return "", false, nil
		}
		return "", false, fmt.Errorf("registry: fleet-db at %s (pid %d) is registered but not healthy: %w", entry.URL, entry.PID, err)
	}
	if logger != nil {
		logger.Debug("reusing fleet-db from active registry", "url", entry.URL, "pid", entry.PID, "owner_data_dir", entry.DataDir)
	}
	return entry.URL, true, nil
}

// acquireRegistryLockOrWaitForPeer is the entry point used by
// StartEmbedded to take the host-wide registry lock. It encapsulates
// three intertwined behaviors so the caller stays linear:
//
//  1. When discovery is disabled or no registry path is resolvable,
//     it returns (nil, "", nil) — caller proceeds with no registry
//     interaction at all.
//  2. When a healthy entry is already registered, it returns
//     (nil, joinURL, nil) — caller turns this into an
//     embeddedReuseError so openLocalStore can join the foreign URL.
//  3. When the lock is currently held by another starter, it polls
//     the registry for that starter's eventual entry. If one appears
//     and is healthy within startupTimeout, returns the join URL.
//     If the holder crashes or the registry stays empty, returns
//     (nil, "", err) so the caller knows to fall through to spawn
//     once it has the lock — at which point the caller re-acquires.
//
// On success acquiring the lock with an empty/stale registry, it
// evicts any stale entry under the lock and returns the live lock for
// the caller to hold across subprocess startup.
func acquireRegistryLockOrWaitForPeer(ctx context.Context, registryPath string, logger *slog.Logger) (*activeRegistryLock, string, error) {
	if registryPath == "" || discoveryDisabled() {
		return nil, "", nil
	}
	if logger == nil {
		logger = slog.Default()
	}
	deadline := time.Now().Add(startupTimeout)
	for {
		select {
		case <-ctx.Done():
			return nil, "", ctx.Err()
		default:
		}
		lock, lockErr := acquireActiveRegistryLock(registryPath)
		if lockErr == nil {
			joinURL, evictErr := evaluateLockedRegistry(ctx, registryPath, logger)
			if evictErr != nil {
				_ = lock.Release()
				return nil, "", evictErr
			}
			if joinURL != "" {
				_ = lock.Release()
				return nil, joinURL, nil
			}
			return lock, "", nil
		}
		if !errors.Is(lockErr, lockfile.ErrLocked) {
			logger.Warn("registry lock unavailable; skipping host-wide discovery", "err", lockErr)
			return nil, "", nil
		}
		// Lock is held. Poll the registry for the holder's entry.
		entry, _ := ReadActiveRegistry(registryPath)
		if entry != nil && lockfile.IsProcessRunning(entry.PID) {
			healthCtx, cancel := context.WithTimeout(ctx, healthCheckTimeout)
			hzErr := netutil.WaitForHealthz(healthCtx, entry.URL, healthCheckTimeout)
			cancel()
			if hzErr == nil {
				return nil, entry.URL, nil
			}
		}
		if time.Now().After(deadline) {
			return nil, "", fmt.Errorf("embedded: registry lock %s held for >%s; another loom serve appears stuck during startup", registryLockPath(registryPath), startupTimeout)
		}
		select {
		case <-ctx.Done():
			return nil, "", ctx.Err()
		case <-time.After(200 * time.Millisecond):
		}
	}
}

// evaluateLockedRegistry runs the under-lock checks: evict stale entry,
// or return joinURL if the entry is healthy.
func evaluateLockedRegistry(ctx context.Context, registryPath string, logger *slog.Logger) (string, error) {
	if logger == nil {
		logger = slog.Default()
	}
	entry, readErr := ReadActiveRegistry(registryPath)
	if readErr != nil {
		logger.Warn("registry read failed under lock; overwriting on next write", "path", registryPath, "err", readErr)
		_ = os.Remove(registryPath)
		return "", nil
	}
	if entry == nil {
		return "", nil
	}
	if !lockfile.IsProcessRunning(entry.PID) {
		logger.Debug("evicting stale registry entry", "pid", entry.PID, "url", entry.URL)
		_ = os.Remove(registryPath)
		return "", nil
	}
	healthCtx, cancel := context.WithTimeout(ctx, healthCheckTimeout)
	hzErr := netutil.WaitForHealthz(healthCtx, entry.URL, healthCheckTimeout)
	cancel()
	if hzErr == nil {
		return entry.URL, nil
	}
	// Live PID but unhealthy. If nothing is listening on entry.URL the PID
	// was recycled (or the fleet-db died without cleanup) — evict and let
	// the caller spawn a fresh one. This is safe from split-brain because
	// there is no live server to conflict with. Otherwise a real server is
	// present but sick: surface the error rather than orphan it.
	if !netutil.DialReachable(entry.URL, healthCheckTimeout) {
		logger.Warn("evicting registry entry: pid live but fleet-db url unreachable (recycled pid / dead server)", "pid", entry.PID, "url", entry.URL)
		_ = os.Remove(registryPath)
		return "", nil
	}
	return "", fmt.Errorf("embedded: registry entry %s (pid %d) is registered but unhealthy: %w", entry.URL, entry.PID, hzErr)
}

// embeddedReuseError wraps ErrEmbeddedAlreadyRunning with the URL of the
// fleet-db this process should join instead. openLocalStore unwraps it
// to build a joined StoreHandle.
type embeddedReuseError struct {
	URL string
	err error
}

func (e *embeddedReuseError) Error() string { return e.err.Error() }
func (e *embeddedReuseError) Unwrap() error { return e.err }

// ReuseURLFromError extracts the URL embedded in an ErrEmbeddedAlreadyRunning
// returned by StartEmbedded when a foreign fleet-db is already registered.
// Returns "" if the error does not carry a join URL.
func ReuseURLFromError(err error) string {
	var reuse *embeddedReuseError
	if errors.As(err, &reuse) {
		return reuse.URL
	}
	return ""
}
