package bootstrap

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/lockfile"
)

func TestActiveRegistryRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "active.json")
	entry := ActiveRegistryEntry{
		PID:     os.Getpid(),
		URL:     "http://127.0.0.1:12345",
		DataDir: t.TempDir(),
	}
	if err := WriteActiveRegistry(path, entry); err != nil {
		t.Fatalf("WriteActiveRegistry: %v", err)
	}
	got, err := ReadActiveRegistry(path)
	if err != nil {
		t.Fatalf("ReadActiveRegistry: %v", err)
	}
	if got == nil {
		t.Fatal("ReadActiveRegistry returned nil after write")
	}
	if got.PID != entry.PID || got.URL != entry.URL || got.DataDir != entry.DataDir {
		t.Fatalf("round-trip mismatch: got %+v want %+v", got, entry)
	}
	if got.StartedAt.IsZero() {
		t.Fatal("StartedAt should be auto-populated by Write")
	}
	if got.Schema == 0 {
		t.Fatal("Schema should be auto-populated by Write")
	}
}

func TestReadActiveRegistryMissingFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "does-not-exist.json")
	got, err := ReadActiveRegistry(path)
	if err != nil {
		t.Fatalf("missing file should not error: %v", err)
	}
	if got != nil {
		t.Fatalf("missing file should return nil entry, got %+v", got)
	}
}

func TestRemoveActiveRegistryIfOwnerOnlyRemovesMatchingEntry(t *testing.T) {
	path := filepath.Join(t.TempDir(), "active.json")
	if err := WriteActiveRegistry(path, ActiveRegistryEntry{PID: 111, URL: "http://a"}); err != nil {
		t.Fatalf("write: %v", err)
	}
	// Non-matching pid: no removal.
	RemoveActiveRegistryIfOwner(path, 222, "http://a")
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("non-matching pid removed the entry: %v", err)
	}
	// Non-matching url: no removal.
	RemoveActiveRegistryIfOwner(path, 111, "http://wrong")
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("non-matching url removed the entry: %v", err)
	}
	// Matching: removed.
	RemoveActiveRegistryIfOwner(path, 111, "http://a")
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("matching owner should remove entry, stat err = %v", err)
	}
}

func TestTryReuseActiveRegistryHealthyProbe(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/healthz" {
			http.NotFound(w, r)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	path := filepath.Join(t.TempDir(), "active.json")
	if err := WriteActiveRegistry(path, ActiveRegistryEntry{PID: os.Getpid(), URL: srv.URL}); err != nil {
		t.Fatalf("write: %v", err)
	}

	url, ok, err := tryReuseActiveRegistry(context.Background(), path, nil, time.Second)
	if err != nil {
		t.Fatalf("tryReuseActiveRegistry: %v", err)
	}
	if !ok {
		t.Fatal("expected reusable entry")
	}
	if url != srv.URL {
		t.Fatalf("url = %q, want %q", url, srv.URL)
	}
}

func TestTryReuseActiveRegistryReportsStalePIDAsMissWithoutEviction(t *testing.T) {
	path := filepath.Join(t.TempDir(), "active.json")
	if err := WriteActiveRegistry(path, ActiveRegistryEntry{PID: 999999999, URL: "http://127.0.0.1:1"}); err != nil {
		t.Fatalf("write: %v", err)
	}

	url, ok, err := tryReuseActiveRegistry(context.Background(), path, nil, 200*time.Millisecond)
	if err != nil {
		t.Fatalf("stale-pid path should not error: %v", err)
	}
	if ok {
		t.Fatalf("stale pid should not be reusable; got url %q", url)
	}
	// The lock-free probe must NOT mutate the file; the authoritative eviction
	// happens under the registry lock inside StartEmbedded. See registry.go.
	if _, statErr := os.Stat(path); statErr != nil {
		t.Fatalf("lock-free probe should not evict the stale entry; stat err = %v", statErr)
	}
}

func TestReadActiveRegistryReturnsErrorForMalformedJSON(t *testing.T) {
	path := filepath.Join(t.TempDir(), "active.json")
	if err := os.WriteFile(path, []byte("not json {"), 0600); err != nil {
		t.Fatalf("seed corrupt file: %v", err)
	}
	got, err := ReadActiveRegistry(path)
	if err == nil {
		t.Fatal("malformed JSON should return error so caller can log")
	}
	if got != nil {
		t.Fatalf("malformed JSON should return nil entry, got %+v", got)
	}
}

func TestTryReuseActiveRegistryRejectsUnhealthy(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()

	path := filepath.Join(t.TempDir(), "active.json")
	if err := WriteActiveRegistry(path, ActiveRegistryEntry{PID: os.Getpid(), URL: srv.URL}); err != nil {
		t.Fatalf("write: %v", err)
	}

	_, ok, err := tryReuseActiveRegistry(context.Background(), path, nil, 200*time.Millisecond)
	if ok {
		t.Fatal("unhealthy entry should not be reusable")
	}
	if err == nil {
		t.Fatal("unhealthy entry should surface an error so caller doesn't silently spawn a second fleet-db")
	}
	if _, statErr := os.Stat(path); statErr != nil {
		t.Fatalf("unhealthy entry should NOT be evicted (process may be slow-starting); stat err = %v", statErr)
	}
}

// TestTryReuseActiveRegistryEvictsLivePIDWithDeadURL covers the
// recycled-PID case: the entry's PID is live (reused by an unrelated
// process) but nothing is listening on its URL. The read-only probe must
// report a miss WITHOUT error and WITHOUT mutating the file, so the
// caller falls through to StartEmbedded (which evicts under the lock).
func TestTryReuseActiveRegistryEvictsLivePIDWithDeadURL(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	deadURL := srv.URL
	srv.Close() // free the port so it is refused — simulates the recycled PID's dead fleet-db

	path := filepath.Join(t.TempDir(), "active.json")
	if err := WriteActiveRegistry(path, ActiveRegistryEntry{PID: os.Getpid(), URL: deadURL}); err != nil {
		t.Fatalf("write: %v", err)
	}

	url, ok, err := tryReuseActiveRegistry(context.Background(), path, nil, 200*time.Millisecond)
	if ok || url != "" {
		t.Fatalf("live-pid/dead-url entry should be a miss; got url=%q ok=%v", url, ok)
	}
	if err != nil {
		t.Fatalf("recycled-pid entry should NOT error (caller must proceed to spawn); err = %v", err)
	}
	if _, statErr := os.Stat(path); statErr != nil {
		t.Fatalf("read-only probe must not evict; stat err = %v", statErr)
	}
}

// TestEvaluateLockedRegistryEvictsLivePIDWithDeadURL is the under-lock
// counterpart: same recycled-PID state, but here the entry IS evicted and
// the caller is told to spawn (empty joinURL, nil error).
func TestEvaluateLockedRegistryEvictsLivePIDWithDeadURL(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	deadURL := srv.URL
	srv.Close()

	path := filepath.Join(t.TempDir(), "active.json")
	if err := WriteActiveRegistry(path, ActiveRegistryEntry{PID: os.Getpid(), URL: deadURL}); err != nil {
		t.Fatalf("write: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	joinURL, err := evaluateLockedRegistry(ctx, path, slog.Default())
	if err != nil {
		t.Fatalf("recycled-pid entry should be evicted, not errored; err = %v", err)
	}
	if joinURL != "" {
		t.Fatalf("joinURL = %q, want empty (recycled-pid entry should be evicted, not joined)", joinURL)
	}
	if _, statErr := os.Stat(path); !os.IsNotExist(statErr) {
		t.Fatalf("recycled-pid entry should have been evicted under the lock; stat err = %v", statErr)
	}
}

func TestActiveRegistryLockIsExclusive(t *testing.T) {
	path := filepath.Join(t.TempDir(), "active.json")
	first, err := acquireActiveRegistryLock(path)
	if err != nil {
		t.Fatalf("first lock: %v", err)
	}

	second, err := acquireActiveRegistryLock(path)
	if err == nil {
		_ = second.Release()
		_ = first.Release()
		t.Fatal("second lock acquired while first lock was held")
	}
	if !errors.Is(err, lockfile.ErrLocked) {
		_ = first.Release()
		t.Fatalf("second lock err = %v, want ErrLocked", err)
	}
	if err := first.Release(); err != nil {
		t.Fatalf("release first lock: %v", err)
	}

	// After release, a fresh acquire must succeed — proves Release was
	// genuine, not just a state mutation.
	third, err := acquireActiveRegistryLock(path)
	if err != nil {
		t.Fatalf("acquire after release: %v", err)
	}
	if err := third.Release(); err != nil {
		t.Fatalf("release third lock: %v", err)
	}
}

func TestAcquireRegistryLockOrWaitForPeerJoinsHealthyHolder(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/healthz" {
			http.NotFound(w, r)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	path := filepath.Join(t.TempDir(), "active.json")
	t.Setenv(EnvFleetDBNoDiscovery, "")

	// Hold the lock from a peer goroutine while writing a healthy entry.
	holder, err := acquireActiveRegistryLock(path)
	if err != nil {
		t.Fatalf("holder lock: %v", err)
	}
	if err := WriteActiveRegistry(path, ActiveRegistryEntry{PID: os.Getpid(), URL: srv.URL}); err != nil {
		_ = holder.Release()
		t.Fatalf("seed registry: %v", err)
	}
	// Release the holder asynchronously to simulate the peer finishing
	// startup mid-poll.
	releaseCh := make(chan struct{})
	go func() {
		time.Sleep(150 * time.Millisecond)
		_ = holder.Release()
		close(releaseCh)
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	lock, joinURL, err := acquireRegistryLockOrWaitForPeer(ctx, path, slog.Default())
	if err != nil {
		t.Fatalf("acquireRegistryLockOrWaitForPeer: %v", err)
	}
	if lock != nil {
		_ = lock.Release()
		t.Fatalf("did not expect to take the lock — should have read the peer's entry instead")
	}
	if joinURL != srv.URL {
		t.Fatalf("joinURL = %q, want %q", joinURL, srv.URL)
	}
	<-releaseCh
}

func TestAcquireRegistryLockOrWaitForPeerReturnsLockWhenNoHolder(t *testing.T) {
	path := filepath.Join(t.TempDir(), "active.json")
	t.Setenv(EnvFleetDBNoDiscovery, "")

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	lock, joinURL, err := acquireRegistryLockOrWaitForPeer(ctx, path, slog.Default())
	if err != nil {
		t.Fatalf("acquireRegistryLockOrWaitForPeer: %v", err)
	}
	if joinURL != "" {
		t.Fatalf("joinURL = %q, want empty", joinURL)
	}
	if lock == nil {
		t.Fatal("expected lock returned to caller")
	}
	if err := lock.Release(); err != nil {
		t.Fatalf("release: %v", err)
	}
}

func TestAcquireRegistryLockOrWaitForPeerEvictsStaleUnderLock(t *testing.T) {
	path := filepath.Join(t.TempDir(), "active.json")
	t.Setenv(EnvFleetDBNoDiscovery, "")
	if err := WriteActiveRegistry(path, ActiveRegistryEntry{PID: 999999999, URL: "http://127.0.0.1:1"}); err != nil {
		t.Fatalf("seed stale: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	lock, joinURL, err := acquireRegistryLockOrWaitForPeer(ctx, path, slog.Default())
	if err != nil {
		t.Fatalf("acquireRegistryLockOrWaitForPeer: %v", err)
	}
	if joinURL != "" {
		t.Fatalf("joinURL = %q, want empty (stale entry should be evicted, not joined)", joinURL)
	}
	if lock == nil {
		t.Fatal("expected lock returned to caller")
	}
	_ = lock.Release()
	if _, statErr := os.Stat(path); !os.IsNotExist(statErr) {
		t.Fatalf("stale entry should have been evicted under the lock; stat err = %v", statErr)
	}
}

func TestTryReuseActiveRegistryRespectsNoDiscoveryEnv(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/healthz" {
			http.NotFound(w, r)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	path := filepath.Join(t.TempDir(), "active.json")
	if err := WriteActiveRegistry(path, ActiveRegistryEntry{PID: os.Getpid(), URL: srv.URL}); err != nil {
		t.Fatalf("write: %v", err)
	}
	t.Setenv(EnvFleetDBNoDiscovery, "1")

	url, ok, err := tryReuseActiveRegistry(context.Background(), path, nil, time.Second)
	if err != nil {
		t.Fatalf("no-discovery should not error: %v", err)
	}
	if ok {
		t.Fatalf("no-discovery should ignore registry; got url %q", url)
	}
}

// TestDiscoveryDisabledSmartDefault locks the parallel-stack default:
// when an operator sets LOOM_CONFIG_DIR without coordinating on
// LOOM_FLEET_DB_REGISTRY, discovery is implicitly disabled so the
// sandbox does not silently join the host's fleet-db.
func TestDiscoveryDisabledSmartDefault(t *testing.T) {
	cases := []struct {
		name        string
		noDiscovery *string // nil = unset
		configDir   *string
		registry    *string
		want        bool
	}{
		{name: "all unset → enabled", want: false},
		{
			name:      "config-dir alone → smart-default disabled",
			configDir: ptr("/tmp/sandbox"),
			want:      true,
		},
		{
			name:      "config-dir + explicit registry → enabled",
			configDir: ptr("/tmp/sandbox"),
			registry:  ptr("/tmp/sandbox/registry.json"),
			want:      false,
		},
		{
			name:      "config-dir empty string → enabled (treated as unset)",
			configDir: ptr(""),
			want:      false,
		},
		{
			name:        "explicit NO_DISCOVERY=1 wins over smart default",
			noDiscovery: ptr("1"),
			configDir:   ptr("/tmp/sandbox"),
			registry:    ptr("/tmp/sandbox/registry.json"),
			want:        true,
		},
		{
			name:        "explicit NO_DISCOVERY=0 wins over smart default",
			noDiscovery: ptr("0"),
			configDir:   ptr("/tmp/sandbox"),
			want:        false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// t.Setenv only takes a value; use os.Unsetenv + Cleanup
			// to express "truly unset" semantics that LookupEnv reports
			// as ok=false.
			resetEnv(t, EnvFleetDBNoDiscovery, tc.noDiscovery)
			resetEnv(t, "LOOM_CONFIG_DIR", tc.configDir)
			resetEnv(t, EnvFleetDBRegistry, tc.registry)
			if got := discoveryDisabled(); got != tc.want {
				t.Errorf("discoveryDisabled() = %v, want %v", got, tc.want)
			}
		})
	}
}

func ptr(s string) *string { return &s }

func resetEnv(t *testing.T, key string, val *string) {
	t.Helper()
	prev, hadPrev := os.LookupEnv(key)
	t.Cleanup(func() {
		if hadPrev {
			_ = os.Setenv(key, prev)
		} else {
			_ = os.Unsetenv(key)
		}
	})
	if val == nil {
		_ = os.Unsetenv(key)
		return
	}
	_ = os.Setenv(key, *val)
}

// TestOpenStoreLocalJoinsActiveRegistryFromOtherDataDir is the LOOM-7
// repro: a foreign fleet-db (registered under a different data dir) is
// running; OpenStore for *this* data dir must join it instead of trying
// to spawn its own.
//
// Pre-fix: tryReuseLocalStore finds no per-data-dir runtime.json, falls
// through to StartEmbedded which fails because the binary is missing.
// Post-fix: the registry probe between tryReuseLocalStore and
// StartEmbedded joins the foreign URL.
func TestOpenStoreLocalJoinsActiveRegistryFromOtherDataDir(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/healthz" {
			http.NotFound(w, r)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	registryPath := filepath.Join(t.TempDir(), "active.json")
	t.Setenv(EnvFleetDBRegistry, registryPath)
	t.Setenv(EnvFleetDBURL, "")
	t.Setenv(EnvFleetDBNoDiscovery, "")
	// Make StartEmbedded fail loudly if it runs — proves the join took
	// over before spawn was attempted.
	t.Setenv(EnvFleetDBBin, filepath.Join(t.TempDir(), "no-such-fleet-db"))

	if err := WriteActiveRegistry(registryPath, ActiveRegistryEntry{
		PID:     os.Getpid(),
		URL:     srv.URL,
		DataDir: filepath.Join(t.TempDir(), "owner-data-dir"),
	}); err != nil {
		t.Fatalf("seed registry: %v", err)
	}

	otherDataDir := t.TempDir()
	h, err := OpenStore(context.Background(), otherDataDir, nil)
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}
	defer h.Close()
	if h.URL() != srv.URL {
		t.Fatalf("h.URL() = %q, want %q (didn't join the registered fleet-db)", h.URL(), srv.URL)
	}
	if h.embedded != nil {
		t.Fatal("OpenStore spawned its own embedded fleet-db instead of joining the registered one")
	}
	if h.Mode() != ModeLocal {
		t.Fatalf("Mode() = %s, want local", h.Mode())
	}
}

func TestOpenStoreLocalRespectsNoDiscoveryEnv(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/healthz" {
			http.NotFound(w, r)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	registryPath := filepath.Join(t.TempDir(), "active.json")
	t.Setenv(EnvFleetDBRegistry, registryPath)
	t.Setenv(EnvFleetDBURL, "")
	t.Setenv(EnvFleetDBNoDiscovery, "1")
	t.Setenv(EnvFleetDBBin, filepath.Join(t.TempDir(), "no-such-fleet-db"))

	if err := WriteActiveRegistry(registryPath, ActiveRegistryEntry{
		PID: os.Getpid(),
		URL: srv.URL,
	}); err != nil {
		t.Fatalf("seed registry: %v", err)
	}

	otherDataDir := t.TempDir()
	_, err := OpenStore(context.Background(), otherDataDir, nil)
	if err == nil {
		t.Fatal("expected StartEmbedded failure when discovery is disabled; registry should be ignored")
	}
	if !strings.Contains(err.Error(), "fleet-db") {
		t.Fatalf("unexpected error shape: %v", err)
	}
}

// TestOpenStoreLocalPrefersPerDataDirReuseOverRegistry guards the
// ordering of the resolution steps: when a healthy per-data-dir runtime
// exists, OpenStore must reuse it instead of the registry entry. This
// prevents future regressions where the order is silently swapped.
func TestOpenStoreLocalPrefersPerDataDirReuseOverRegistry(t *testing.T) {
	perDataDirSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/healthz" {
			http.NotFound(w, r)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer perDataDirSrv.Close()
	registrySrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/healthz" {
			http.NotFound(w, r)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer registrySrv.Close()

	registryPath := filepath.Join(t.TempDir(), "active.json")
	t.Setenv(EnvFleetDBRegistry, registryPath)
	t.Setenv(EnvFleetDBURL, "")
	t.Setenv(EnvFleetDBNoDiscovery, "")
	t.Setenv(EnvFleetDBBin, filepath.Join(t.TempDir(), "no-such-fleet-db"))

	if err := WriteActiveRegistry(registryPath, ActiveRegistryEntry{PID: os.Getpid(), URL: registrySrv.URL}); err != nil {
		t.Fatalf("seed registry: %v", err)
	}

	dataDir := t.TempDir()
	fleetDir := filepath.Join(dataDir, "fleet-db")
	if err := writeEmbeddedRuntime(fleetDir, embeddedRuntimeInfo{
		PID: os.Getpid(),
		URL: perDataDirSrv.URL,
	}); err != nil {
		t.Fatalf("seed runtime: %v", err)
	}

	h, err := OpenStore(context.Background(), dataDir, nil)
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}
	defer h.Close()
	if h.URL() != perDataDirSrv.URL {
		t.Fatalf("URL = %q, want %q (per-data-dir reuse must win over registry)", h.URL(), perDataDirSrv.URL)
	}
}
