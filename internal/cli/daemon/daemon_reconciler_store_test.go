package daemon

import (
	"context"
	"sync"
	"testing"

	cfgpkg "github.com/tysonthomas9/loomcli/internal/cli/config"
	"github.com/tysonthomas9/loomcli/internal/cli/daemon/supervisor"
	"github.com/tysonthomas9/loomcli/internal/events"
	"github.com/tysonthomas9/loomcli/internal/infra/memstore"
	"github.com/tysonthomas9/loomcli/internal/store"
)

// countingStore wraps a store and records how many daemon-profile reads it
// served, so a test can prove reloadAndReconcile read through the injected
// store rather than opening a fleet-db client of its own per tick.
type countingStore struct {
	store.Store
	mu    sync.Mutex
	reads int
}

func (c *countingStore) Daemon() store.DaemonProfileStore {
	c.mu.Lock()
	c.reads++
	c.mu.Unlock()
	return c.Store.Daemon()
}

func (c *countingStore) readCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.reads
}

func TestReloadAndReconcile_ReusesDaemonStoreAcrossTicks(t *testing.T) {
	const wsKey = "WS1"
	ctx := context.Background()

	cfgpkg.InvalidateConfigCache()
	t.Cleanup(cfgpkg.InvalidateConfigCache)
	t.Setenv("LOOM_CONFIG_DIR", t.TempDir())
	t.Setenv("LOOM_WORKSPACE", wsKey)
	// Unreachable: a per-tick bootstrap.OpenStore would fail here, so a green
	// run is itself evidence that no additional store was opened.
	t.Setenv("LOOM_FLEET_DB_URL", "http://127.0.0.1:1")

	base := memstore.New()
	if _, err := base.Workspaces().Create(ctx, store.WorkspaceCreate{
		Key:           wsKey,
		Name:          wsKey,
		DefaultBranch: "main",
	}); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	if _, err := base.Roles().Create(ctx, store.RoleCreate{
		WorkspaceKey: wsKey,
		Name:         "worker",
		Kind:         "worker",
		PromptFile:   "worker.md",
	}); err != nil {
		t.Fatalf("create role: %v", err)
	}

	spy := &countingStore{Store: base}
	projectDir := t.TempDir()

	initial, err := cfgpkg.LoadDaemonConfigWithStore(ctx, spy, projectDir)
	if err != nil {
		t.Fatalf("LoadDaemonConfigWithStore() error = %v", err)
	}
	before := spy.readCount()

	d := &Daemon{
		config:     initial,
		configHash: computeConfigHash(initial),
		projectDir: projectDir,
		store:      spy,
	}
	// A reload failure emits through the supervisor; wire one so a regression
	// reports the count assertion instead of panicking on a nil sup.
	d.sup = &supervisor.Supervisor{
		ConfigSnapshot: d.configSnapshot,
		Shutdown:       make(chan struct{}),
		StoppedAgents:  make(map[string]struct{}),
		EmitEvent:      func(events.Event) {},
	}

	const ticks = 5
	for i := 0; i < ticks; i++ {
		d.reloadAndReconcile()
	}

	// Every tick must have read through the injected store; none may have
	// opened one of its own.
	if got, want := spy.readCount()-before, ticks; got != want {
		t.Errorf("daemon-profile reads through injected store = %d, want %d", got, want)
	}
	if d.configHash != computeConfigHash(initial) {
		t.Errorf("configHash changed across no-op ticks; reload did not read the same store")
	}
}

func TestReloadAndReconcile_NilStoreFallsBackToOpening(t *testing.T) {
	cfgpkg.InvalidateConfigCache()
	t.Cleanup(cfgpkg.InvalidateConfigCache)
	t.Setenv("LOOM_CONFIG_DIR", t.TempDir())
	// No active workspace: the shared path returns built-in defaults without
	// opening anything, which is exactly today's behavior for a nil store.
	t.Setenv("LOOM_WORKSPACE", "")

	initial, err := cfgpkg.LoadDaemonConfig(t.TempDir())
	if err != nil {
		t.Fatalf("LoadDaemonConfig() error = %v", err)
	}
	d := &Daemon{
		config:     initial,
		configHash: computeConfigHash(initial),
		projectDir: t.TempDir(),
	}

	d.reloadAndReconcile() // must not panic on a nil store

	if d.configHash != computeConfigHash(initial) {
		t.Errorf("configHash changed on a nil-store reload")
	}
}
