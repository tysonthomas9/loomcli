package serve

import (
	"errors"
	"path/filepath"
	"testing"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/tysonthomas9/loomcli/internal/cli"
	"github.com/tysonthomas9/loomcli/internal/metrics/agentmetrics"
	"github.com/tysonthomas9/loomcli/internal/metrics/spawnmetrics"
	"github.com/tysonthomas9/loomcli/internal/sessions"
)

// serve is started in-process by more than one test, so a second registration
// has to be success rather than a panic.
func TestRegisterAgentMetricsToleratesRepeatRegistration(t *testing.T) {
	dir := t.TempDir()

	if err := registerAgentMetrics(dir); err != nil {
		t.Fatalf("first registration: %v", err)
	}
	if err := registerAgentMetrics(dir); err != nil {
		t.Fatalf("second registration: %v", err)
	}

	// And prove the first one really reached the DEFAULT registry — a
	// registration that silently went nowhere would also return nil twice.
	err := prometheus.Register(agentmetrics.New(dir))
	var already prometheus.AlreadyRegisteredError
	if !errors.As(err, &already) {
		t.Fatalf("default registry does not hold the collector: %v", err)
	}
}

// The daemon writes the snapshot and serve reads it: two processes, one path.
// This asserts the two derivations are byte-identical, which is the whole
// contract between spawnmetrics and agentmetrics.
func TestCollectorPathsMatchTheDaemonWiring(t *testing.T) {
	dir := t.TempDir()
	c := agentmetrics.New(dir)

	if want := spawnmetrics.SnapshotPath(dir); c.SnapshotPath() != want {
		t.Errorf("snapshot path = %q, want %q", c.SnapshotPath(), want)
	}

	store, err := sessions.NewStore(dir)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	if want := filepath.Join(store.Dir(), "index.jsonl"); c.SessionIndexPath() != want {
		t.Errorf("session index path = %q, want %q", c.SessionIndexPath(), want)
	}
}

// The runtime dir the collector is built from at startup is the same one serve
// hands to SessionRuntimeDir, so the collector reads the directory this process
// actually writes sessions into — whatever GetWorkspaceRuntimeDir resolves to
// here (it caches through sync.Once for the process lifetime).
func TestStartupRuntimeDirIsTheOneServeUses(t *testing.T) {
	dir := cli.GetWorkspaceRuntimeDir()
	c := agentmetrics.New(dir)

	if want := spawnmetrics.SnapshotPath(dir); c.SnapshotPath() != want {
		t.Errorf("snapshot path = %q, want %q", c.SnapshotPath(), want)
	}
	if want := filepath.Join(dir, "sessions", "index.jsonl"); c.SessionIndexPath() != want {
		t.Errorf("session index path = %q, want %q", c.SessionIndexPath(), want)
	}
}
