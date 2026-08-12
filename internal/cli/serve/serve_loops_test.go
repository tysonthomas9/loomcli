package serve

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	trigger "github.com/tysonthomas9/loomcli/internal/infra/automationruntime"
	"github.com/tysonthomas9/loomcli/internal/infra/memstore"
	"github.com/tysonthomas9/loomcli/internal/modules/workitems"
	"github.com/tysonthomas9/loomcli/internal/store"
)

// readerCapableEvents satisfies store.TriggerEventStore AND the optional
// store.IssueJournalReader capability, so a store returning it from
// TriggerEvents() passes the bridge's capability gate (the path a fleet-db
// client takes). memstore deliberately does not implement IssueJournalReader.
type readerCapableEvents struct {
	store.TriggerEventStore
}

type discardIssueJournalEmitter struct{}

func (discardIssueJournalEmitter) Emit(context.Context, string, trigger.InternalEvent) (*trigger.InternalEmitResult, error) {
	return &trigger.InternalEmitResult{}, nil
}

func issueJournalTaskLaneTestPorts() (
	trigger.TaskReadyIssueLookup,
	trigger.TaskReadySnapshotLister,
	trigger.TaskReadyRepositoryRequiredBlocker,
) {
	return func(context.Context, string, string) (trigger.TaskReadySnapshot, error) {
			return trigger.TaskReadySnapshot{}, nil
		}, func(context.Context, string) ([]trigger.TaskReadySnapshot, error) {
			return nil, nil
		}, func(context.Context, string, string) (trigger.TaskReadyRepositoryRequiredResult, error) {
			return trigger.TaskReadyRepositoryRequiredResult{}, nil
		}
}

type repositoryRequirementTestBackend struct {
	workitems.API
	result *workitems.RepositoryAdmissionResult
	err    error
	ids    []string
}

func (b *repositoryRequirementTestBackend) BlockRepositoryRequired(_ context.Context, command workitems.BlockRepositoryRequiredCommand) (*workitems.RepositoryAdmissionResult, error) {
	b.ids = append(b.ids, command.IssueID)
	return b.result, b.err
}

func (*repositoryRequirementTestBackend) AssignRepository(_ context.Context, command workitems.AssignRepositoryCommand) (*workitems.IssueSummary, error) {
	return &workitems.IssueSummary{ID: command.IssueID, SourceRepo: command.Repository}, nil
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
	if err := startIssueJournalBridge(
		ctx, mem, nil, nil, nil, discardIssueJournalEmitter{},
		buildServeRuntimeConfig().IssueJournal,
	); err != nil {
		t.Fatalf("start memstore-gated bridge: %v", err)
	}

	// Also a nil store is a clean no-op.
	if err := startIssueJournalBridge(
		ctx, nil, nil, nil, nil, nil,
		buildServeRuntimeConfig().IssueJournal,
	); err != nil {
		t.Fatalf("start nil-store bridge: %v", err)
	}

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
	if err := startIssueJournalBridge(
		ctx, readerCapableStore{Store: mem}, nil, nil, nil,
		discardIssueJournalEmitter{},
		buildServeRuntimeConfig().IssueJournal,
	); err != nil {
		t.Fatalf("start disabled bridge: %v", err)
	}

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
	issueLookup, readySnapshots, repositoryAdmission := issueJournalTaskLaneTestPorts()
	if err := startIssueJournalBridge(
		ctx, readerCapableStore{Store: mem}, issueLookup, readySnapshots, repositoryAdmission,
		discardIssueJournalEmitter{},
		buildServeRuntimeConfig().IssueJournal,
	); err != nil {
		t.Fatalf("start enabled bridge: %v", err)
	}

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

func TestDriverStaleTaskMaxAgeDefersToExecutionDefault(t *testing.T) {
	t.Setenv(envLoomDriverStaleTaskMaxAge, "")
	if got := driverStaleTaskMaxAge(); got != 0 {
		t.Fatalf("driverStaleTaskMaxAge() = %v, want zero so Execution applies its default", got)
	}
}

func TestBuildIssueJournalBridgeAlwaysEnablesTaskReviewEvents(t *testing.T) {
	mem := memstore.New()
	source := discardIssueJournalEmitter{}

	t.Setenv(envLoomIssueBridgeDisabled, "")
	t.Setenv(envLoomIssueBridgeStatePath, filepath.Join(t.TempDir(), "cursor.json"))
	if bridge, err := buildIssueJournalBridge(
		readerCapableStore{Store: mem}, nil, nil, nil, source,
		buildServeRuntimeConfig().IssueJournal,
	); err == nil || bridge != nil {
		t.Fatalf("missing task-lane ports returned bridge/error = %+v/%v, want fail-closed composition error", bridge, err)
	}
	issueLookup, readySnapshots, repositoryAdmission := issueJournalTaskLaneTestPorts()
	bridge, err := buildIssueJournalBridge(
		readerCapableStore{Store: mem}, issueLookup, readySnapshots, repositoryAdmission, source,
		buildServeRuntimeConfig().IssueJournal,
	)
	if err != nil || bridge == nil || !bridge.EmitTaskReview {
		t.Fatalf("bridge/error = %+v/%v, want generic task-review lane enabled", bridge, err)
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
			if got := taskReadyRepositoryRequired(tt.issueType, tt.sourceRepo, tt.repoCount); got != tt.want {
				t.Fatalf("taskReadyRepositoryRequired(%q, %q, %d) = %v, want %v",
					tt.issueType, tt.sourceRepo, tt.repoCount, got, tt.want)
			}
		})
	}
}

func TestBlockRepositoryRequiredTaskUsesAtomicExtension(t *testing.T) {
	atomic := &repositoryRequirementTestBackend{result: &workitems.RepositoryAdmissionResult{Changed: true}}
	result, err := blockRepositoryRequiredTask(t.Context(), atomic, "TASK-1")
	if err != nil || !result.Blocked || result.DispatchReady != nil || len(atomic.ids) != 1 || atomic.ids[0] != "TASK-1" {
		t.Fatalf("block result/error/calls = %+v/%v/%v, want changed TASK-1", result, err, atomic.ids)
	}

	atomic.result = &workitems.RepositoryAdmissionResult{Replayed: true, Blocked: true}
	result, err = blockRepositoryRequiredTask(t.Context(), atomic, "TASK-1")
	if err != nil || result.Blocked || result.DispatchReady != nil {
		t.Fatalf("replayed block result/error = %+v/%v, want suppressed no-op", result, err)
	}

	atomic.result = &workitems.RepositoryAdmissionResult{
		DispatchReady: true,
		Issue: &workitems.IssueSummary{
			ID: "TASK-1", Status: "open", IssueType: "task", SourceRepo: "fleet-source",
			HasDesign: true, Labels: []string{"phase4"}, UpdatedAt: time.Date(2026, 7, 18, 23, 0, 0, 0, time.UTC),
		},
	}
	result, err = blockRepositoryRequiredTask(t.Context(), atomic, "TASK-1")
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
	atomic.result = &workitems.RepositoryAdmissionResult{
		DispatchReady: true,
		Issue:         &workitems.IssueSummary{ID: "TASK-COUNT", Status: "open", IssueType: "task"},
	}
	result, err = blockRepositoryRequiredTask(t.Context(), atomic, "TASK-COUNT")
	if err != nil || result.DispatchReady == nil || result.DispatchReady.SourceRepo != "" || result.DispatchReady.RepositoryRequired {
		t.Fatalf("single-repo dispatch result/error = %+v/%v", result, err)
	}

	atomic.result = &workitems.RepositoryAdmissionResult{
		DispatchReady: true,
		Issue: &workitems.IssueSummary{
			ID: "TASK-REVIEW", Status: "review", IssueType: "task", SourceRepo: "fleet-source",
		},
	}
	result, err = blockRepositoryRequiredTask(t.Context(), atomic, "TASK-REVIEW")
	if err != nil || result.DispatchReady == nil || result.DispatchReady.Status != "review" ||
		result.DispatchReady.SourceRepo != "fleet-source" {
		t.Fatalf("review dispatch result/error = %+v/%v", result, err)
	}

	atomic.result = &workitems.RepositoryAdmissionResult{
		Issue: &workitems.IssueSummary{ID: "TASK-1", Status: "in_progress", IssueType: "task", SourceRepo: "fleet-source"},
	}
	result, err = blockRepositoryRequiredTask(t.Context(), atomic, "TASK-1")
	if err != nil || result.Blocked || result.DispatchReady != nil {
		t.Fatalf("stale non-ready result/error = %+v/%v, want suppressed no-op", result, err)
	}

	atomic.err = workitems.AdapterNotFound("BlockRepositoryRequired", "issue deleted")
	result, err = blockRepositoryRequiredTask(t.Context(), atomic, "TASK-GONE")
	if err != nil || result.Blocked || result.DispatchReady != nil {
		t.Fatalf("deleted block result/error = %+v/%v, want durably stale no-op", result, err)
	}
}
