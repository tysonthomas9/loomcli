package serve

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/infra/memstore"
	"github.com/tysonthomas9/loomcli/internal/store"
)

// readerCapableEvents satisfies store.TriggerEventStore AND the optional
// store.IssueJournalReader capability, so a store returning it from
// TriggerEvents() passes the bridge's capability gate (the path a fleet-db
// client takes). memstore deliberately does not implement IssueJournalReader.
type readerCapableEvents struct {
	store.TriggerEventStore
}

func (readerCapableEvents) ListIssueEvents(_ context.Context, _, afterCursor string, _ int) ([]store.JournalEvent, string, bool, error) {
	return nil, afterCursor, false, nil
}

// readerCapableStore wraps a memstore but advertises the issue-journal reader
// capability, so startIssueJournalBridge takes the enabled branch.
type readerCapableStore struct {
	store.Store
}

func (s readerCapableStore) TriggerEvents() store.TriggerEventStore {
	return readerCapableEvents{TriggerEventStore: s.Store.TriggerEvents()}
}

// seedWorkspace creates a workspace so an unscoped bridge sweep has a target.
func seedWorkspace(t *testing.T, s store.Store, key string) {
	t.Helper()
	if _, err := s.Workspaces().Create(context.Background(), store.WorkspaceCreate{Key: key, Name: key}); err != nil {
		t.Fatalf("seed workspace %q: %v", key, err)
	}
}

func TestStartIssueJournalBridge_MemstoreGatedNoLoop(t *testing.T) {
	dir := t.TempDir()
	statePath := filepath.Join(dir, "issue-bridge-cursor.json")
	t.Setenv(envLoomIssueBridgeStatePath, statePath)
	t.Setenv(envLoomIssueBridgeDisabled, "")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// memstore does not implement store.IssueJournalReader, so the bridge must
	// not start: no cursor state file is ever created.
	startIssueJournalBridge(ctx, memstore.New(), nil)

	// Also a nil store is a clean no-op.
	startIssueJournalBridge(ctx, nil, nil)

	if _, err := os.Stat(statePath); !os.IsNotExist(err) {
		t.Fatalf("cursor state file created for memstore-gated serve: stat err = %v", err)
	}
}

func TestStartIssueJournalBridge_DisabledFlagHonored(t *testing.T) {
	dir := t.TempDir()
	statePath := filepath.Join(dir, "issue-bridge-cursor.json")
	t.Setenv(envLoomIssueBridgeStatePath, statePath)
	t.Setenv(envLoomIssueBridgeDisabled, "1")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Even with a reader-capable store the disabled flag wins: no loop, no
	// cursor file.
	startIssueJournalBridge(ctx, readerCapableStore{Store: memstore.New()}, nil)

	if _, err := os.Stat(statePath); !os.IsNotExist(err) {
		t.Fatalf("cursor state file created while bridge disabled: stat err = %v", err)
	}
}

func TestStartIssueJournalBridge_EnabledLoopWritesCursorState(t *testing.T) {
	dir := t.TempDir()
	statePath := filepath.Join(dir, "issue-bridge-cursor.json")
	t.Setenv(envLoomIssueBridgeStatePath, statePath)
	t.Setenv(envLoomIssueBridgeDisabled, "")
	t.Setenv(envLoomIssueBridgeInterval, "1")
	t.Setenv("LOOM_WORKSPACE", "") // unscoped sweep walks every known workspace

	mem := memstore.New()
	seedWorkspace(t, mem, "WS")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// A reader-capable store passes the gate; the first pass fast-forwards the
	// seeded workspace to the (empty) journal tail and persists its cursor, so
	// the state file appears.
	startIssueJournalBridge(ctx, readerCapableStore{Store: mem}, nil)

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(statePath); err == nil {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("cursor state file %q was never written by the enabled bridge loop", statePath)
}

func TestIssueBridgeInterval(t *testing.T) {
	tests := []struct {
		value string
		want  time.Duration
	}{
		{"", 2 * time.Second},
		{"5", 5 * time.Second},
		{"0", 1 * time.Second},
		{"-3", 1 * time.Second},
		{"invalid", 2 * time.Second},
		{"100000", 3600 * time.Second},
	}
	for _, tt := range tests {
		t.Run(tt.value, func(t *testing.T) {
			t.Setenv(envLoomIssueBridgeInterval, tt.value)
			if got := issueBridgeInterval(); got != tt.want {
				t.Fatalf("issueBridgeInterval() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestIssueBridgeDisabled(t *testing.T) {
	for _, value := range []string{"1", "true", "TRUE", "yes", "on"} {
		t.Run("disabled_"+value, func(t *testing.T) {
			t.Setenv(envLoomIssueBridgeDisabled, value)
			if !issueBridgeDisabled() {
				t.Fatalf("issueBridgeDisabled() = false for %q", value)
			}
		})
	}
	for _, value := range []string{"", "0", "false", "off", "no", "unexpected"} {
		t.Run("enabled_"+value, func(t *testing.T) {
			t.Setenv(envLoomIssueBridgeDisabled, value)
			if issueBridgeDisabled() {
				t.Fatalf("issueBridgeDisabled() = true for %q", value)
			}
		})
	}
}

func TestIssueBridgeStatePath(t *testing.T) {
	t.Run("explicit override", func(t *testing.T) {
		t.Setenv(envLoomIssueBridgeStatePath, "/tmp/custom-cursor.json")
		if got := issueBridgeStatePath(); got != "/tmp/custom-cursor.json" {
			t.Fatalf("issueBridgeStatePath() = %q, want explicit override", got)
		}
	})
	t.Run("default under loom dir", func(t *testing.T) {
		t.Setenv(envLoomIssueBridgeStatePath, "")
		t.Setenv("LOOM_CONFIG_DIR", "/var/loom-state")
		want := filepath.Join("/var/loom-state", issueBridgeCursorFileName)
		if got := issueBridgeStatePath(); got != want {
			t.Fatalf("issueBridgeStatePath() = %q, want %q", got, want)
		}
	})
}
