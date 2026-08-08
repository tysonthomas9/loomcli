package serve

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/backend"
	"github.com/tysonthomas9/loomcli/internal/cli/serve/runtimecomposition"
	driverexecutor "github.com/tysonthomas9/loomcli/internal/driver"
	trigger "github.com/tysonthomas9/loomcli/internal/infra/automationruntime"
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

type repositoryRequirementTestBackend struct {
	backend.IssueBackend
	result *backend.RepositoryRequirementResult
	err    error
	ids    []string
}

func (b *repositoryRequirementTestBackend) BlockRepositoryRequired(_ context.Context, id string) (*backend.RepositoryRequirementResult, error) {
	b.ids = append(b.ids, id)
	return b.result, b.err
}

func (*repositoryRequirementTestBackend) SetIssueRepository(_ context.Context, id, repo string) (*backend.IssueData, error) {
	return &backend.IssueData{ID: id, SourceRepo: repo}, nil
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
	mem := memstore.New()
	runtimecomposition.StartIssueJournalBridge(
		ctx, mem, nil, nil, nil, &trigger.InternalSource{Store: mem},
		buildServeRuntimeConfig().IssueJournal,
	)

	// Also a nil store is a clean no-op.
	runtimecomposition.StartIssueJournalBridge(
		ctx, nil, nil, nil, nil, nil,
		buildServeRuntimeConfig().IssueJournal,
	)

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
	mem := memstore.New()
	runtimecomposition.StartIssueJournalBridge(
		ctx, readerCapableStore{Store: mem}, nil, nil, nil,
		&trigger.InternalSource{Store: mem},
		buildServeRuntimeConfig().IssueJournal,
	)

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
	runtimecomposition.StartIssueJournalBridge(
		ctx, readerCapableStore{Store: mem}, nil, nil, nil,
		&trigger.InternalSource{Store: mem},
		buildServeRuntimeConfig().IssueJournal,
	)

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

func TestDriverStaleTaskMaxAgeMatchesSweeperDefault(t *testing.T) {
	t.Setenv(envLoomDriverStaleTaskMaxAge, "")
	if got := driverStaleTaskMaxAge(); got != driverexecutor.DefaultStaleTaskRunMaxAge {
		t.Fatalf("driverStaleTaskMaxAge() = %v, want driver default %v", got, driverexecutor.DefaultStaleTaskRunMaxAge)
	}
}

func TestBuildIssueJournalBridgeAlwaysEnablesTaskReviewEvents(t *testing.T) {
	mem := memstore.New()
	source := &trigger.InternalSource{Store: mem}

	t.Setenv(envLoomIssueBridgeDisabled, "")
	t.Setenv(envLoomIssueBridgeStatePath, filepath.Join(t.TempDir(), "cursor.json"))
	bridge := runtimecomposition.BuildIssueJournalBridge(
		readerCapableStore{Store: mem}, nil, nil, nil, source,
		buildServeRuntimeConfig().IssueJournal,
	)
	if bridge == nil || !bridge.EmitTaskReview {
		t.Fatalf("bridge = %+v, want generic task-review lane enabled", bridge)
	}
}

func TestTaskReadyRepositoryRequired(t *testing.T) {
	tests := []struct {
		name       string
		issueType  string
		sourceRepo string
		repoCount  int
		want       bool
	}{
		{name: "single repo fallback", issueType: "task", repoCount: 1, want: false},
		{name: "zero repos", issueType: "task", repoCount: 0, want: true},
		{name: "multiple repos", issueType: "task", repoCount: 2, want: true},
		{name: "explicit repo", issueType: "task", sourceRepo: "acme/app", repoCount: 2, want: false},
		{name: "workspace scoped epic", issueType: "epic", repoCount: 2, want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := runtimecomposition.TaskReadyRepositoryRequired(tt.issueType, tt.sourceRepo, tt.repoCount); got != tt.want {
				t.Fatalf("taskReadyRepositoryRequired(%q, %q, %d) = %v, want %v",
					tt.issueType, tt.sourceRepo, tt.repoCount, got, tt.want)
			}
		})
	}
}

func TestBlockRepositoryRequiredTaskUsesAtomicExtension(t *testing.T) {
	atomic := &repositoryRequirementTestBackend{result: &backend.RepositoryRequirementResult{Changed: true}}
	result, err := runtimecomposition.BlockRepositoryRequiredTask(t.Context(), atomic, "TASK-1")
	if err != nil || !result.Blocked || result.DispatchReady != nil || len(atomic.ids) != 1 || atomic.ids[0] != "TASK-1" {
		t.Fatalf("block result/error/calls = %+v/%v/%v, want changed TASK-1", result, err, atomic.ids)
	}

	atomic.result = &backend.RepositoryRequirementResult{Replayed: true, Blocked: true}
	result, err = runtimecomposition.BlockRepositoryRequiredTask(t.Context(), atomic, "TASK-1")
	if err != nil || result.Blocked || result.DispatchReady != nil {
		t.Fatalf("replayed block result/error = %+v/%v, want suppressed no-op", result, err)
	}

	atomic.result = &backend.RepositoryRequirementResult{
		DispatchReady: true,
		Issue: &backend.IssueData{
			ID: "TASK-1", Status: "open", IssueType: "task", SourceRepo: "fleet-source",
			HasDesign: true, Labels: []string{"phase4"}, UpdatedAt: time.Date(2026, 7, 18, 23, 0, 0, 0, time.UTC),
		},
	}
	result, err = runtimecomposition.BlockRepositoryRequiredTask(t.Context(), atomic, "TASK-1")
	if err != nil || result.Blocked || result.DispatchReady == nil {
		t.Fatalf("dispatch-ready result/error = %+v/%v", result, err)
	}
	if got := result.DispatchReady; got.TaskID != "TASK-1" || got.Status != "open" || got.SourceRepo != "fleet-source" ||
		got.RepositoryRequired || !got.HasDesign || len(got.Labels) != 1 || got.Labels[0] != "phase4" {
		t.Fatalf("canonical dispatch snapshot = %+v", got)
	}

	// An exactly-single-repository workspace satisfies admission without
	// assigning the Issue. DispatchReady still carries a known-empty source repo
	// and must not be reclassified as repository-required by Loom.
	atomic.result = &backend.RepositoryRequirementResult{
		DispatchReady: true,
		Issue:         &backend.IssueData{ID: "TASK-COUNT", Status: "open", IssueType: "task"},
	}
	result, err = runtimecomposition.BlockRepositoryRequiredTask(t.Context(), atomic, "TASK-COUNT")
	if err != nil || result.DispatchReady == nil || result.DispatchReady.SourceRepo != "" || result.DispatchReady.RepositoryRequired {
		t.Fatalf("single-repo dispatch result/error = %+v/%v", result, err)
	}

	atomic.result = &backend.RepositoryRequirementResult{
		DispatchReady: true,
		Issue: &backend.IssueData{
			ID: "TASK-REVIEW", Status: "review", IssueType: "task", SourceRepo: "fleet-source",
		},
	}
	result, err = runtimecomposition.BlockRepositoryRequiredTask(t.Context(), atomic, "TASK-REVIEW")
	if err != nil || result.DispatchReady == nil || result.DispatchReady.Status != "review" ||
		result.DispatchReady.SourceRepo != "fleet-source" {
		t.Fatalf("review dispatch result/error = %+v/%v", result, err)
	}

	atomic.result = &backend.RepositoryRequirementResult{
		Issue: &backend.IssueData{ID: "TASK-1", Status: "in_progress", IssueType: "task", SourceRepo: "fleet-source"},
	}
	result, err = runtimecomposition.BlockRepositoryRequiredTask(t.Context(), atomic, "TASK-1")
	if err != nil || result.Blocked || result.DispatchReady != nil {
		t.Fatalf("stale non-ready result/error = %+v/%v, want suppressed no-op", result, err)
	}

	atomic.err = backend.ErrNotFound("BlockRepositoryRequired", "issue deleted")
	result, err = runtimecomposition.BlockRepositoryRequiredTask(t.Context(), atomic, "TASK-GONE")
	if err != nil || result.Blocked || result.DispatchReady != nil {
		t.Fatalf("deleted block result/error = %+v/%v, want durably stale no-op", result, err)
	}
}

func TestBlockRepositoryRequiredTaskFailsClosedWithoutAtomicExtension(t *testing.T) {
	unsupported := struct{ backend.IssueBackend }{}
	result, err := runtimecomposition.BlockRepositoryRequiredTask(t.Context(), unsupported, "TASK-1")
	if result.Blocked || result.DispatchReady != nil || !backend.IsKind(err, backend.KindNotImplemented) {
		t.Fatalf("unsupported result/error = %+v/%v, want not_implemented", result, err)
	}
}
