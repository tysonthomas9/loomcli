package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"
)

// Compile-time interface check.
var _ IssueTracker = (*fleetDBBackend)(nil)

// mockFleetService implements FleetService for testing.
// Thread-safe: GetDependencies is called concurrently by dep hydration.
type mockFleetService struct {
	mu sync.Mutex // protects call-tracking fields written from goroutines

	readyIssues   []BdIssue
	readyErr      error
	listIssues    []BdIssue
	listErr       error
	blockedIssues []BdIssue
	blockedErr    error
	stats         BdStats
	statsErr      error
	issue         *BdIssue
	issueErr      error
	issueText     string
	issueTextErr  error
	deps          []Dependency
	depsErr       error
	claimErr      error
	closeErr      error
	reopenErr     error
	deferErr      error
	assignErr     error
	updateErr     error

	// Call tracking.
	lastReadyLimit   int
	lastReadyParent  string
	lastListStatus   string
	lastListType     string
	lastListAssignee string
	lastListLimit    int
	lastGetIssueID   string
	lastClaimID      string
	lastCloseID      string
	lastCloseReason  string
	lastReopenID     string
	lastDeferID      string
	lastAssignID     string
	lastAssignee     string
	lastUpdateID     string
	lastUpdateFields map[string]*string
	getDepsCount     int // number of GetDependencies calls
}

func (m *mockFleetService) GetReady(_ context.Context, limit int, parentID string) ([]BdIssue, error) {
	m.lastReadyLimit = limit
	m.lastReadyParent = parentID
	if m.readyErr != nil {
		return nil, m.readyErr
	}
	return m.readyIssues, nil
}

func (m *mockFleetService) ListIssues(_ context.Context, status, issueType, assignee string, limit int) ([]BdIssue, error) {
	m.lastListStatus = status
	m.lastListType = issueType
	m.lastListAssignee = assignee
	m.lastListLimit = limit
	if m.listErr != nil {
		return nil, m.listErr
	}
	return m.listIssues, nil
}

func (m *mockFleetService) GetBlocked(_ context.Context) ([]BdIssue, error) {
	if m.blockedErr != nil {
		return nil, m.blockedErr
	}
	return m.blockedIssues, nil
}

func (m *mockFleetService) CountByStatus(_ context.Context) (BdStats, error) {
	if m.statsErr != nil {
		return BdStats{}, m.statsErr
	}
	return m.stats, nil
}

func (m *mockFleetService) GetIssue(_ context.Context, id string) (*BdIssue, error) {
	m.lastGetIssueID = id
	if m.issueErr != nil {
		return nil, m.issueErr
	}
	return m.issue, nil
}

func (m *mockFleetService) GetIssueText(_ context.Context, id string) (string, error) {
	m.lastGetIssueID = id
	if m.issueTextErr != nil {
		return "", m.issueTextErr
	}
	return m.issueText, nil
}

func (m *mockFleetService) GetDependencies(_ context.Context, _ string) ([]Dependency, error) {
	m.mu.Lock()
	m.getDepsCount++
	m.mu.Unlock()
	if m.depsErr != nil {
		return nil, m.depsErr
	}
	return m.deps, nil
}

func (m *mockFleetService) ClaimIssue(_ context.Context, id string) error {
	m.lastClaimID = id
	return m.claimErr
}

func (m *mockFleetService) CloseIssue(_ context.Context, id, reason string) error {
	m.lastCloseID = id
	m.lastCloseReason = reason
	return m.closeErr
}

func (m *mockFleetService) ReopenIssue(_ context.Context, id string) error {
	m.lastReopenID = id
	return m.reopenErr
}

func (m *mockFleetService) DeferIssue(_ context.Context, id string, _ time.Time) error {
	m.lastDeferID = id
	return m.deferErr
}

func (m *mockFleetService) AssignIssue(_ context.Context, id, assignee string) error {
	m.lastAssignID = id
	m.lastAssignee = assignee
	return m.assignErr
}

func (m *mockFleetService) UpdateFields(_ context.Context, id string, fields map[string]*string) error {
	m.lastUpdateID = id
	m.lastUpdateFields = fields
	return m.updateErr
}

func newFleetTestLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func newTestFleetDBBackend(mock *mockFleetService) *fleetDBBackend {
	return newFleetDBBackend(mock, newFleetTestLogger())
}

// --- RunCommand dispatch tests ---

func TestFleetDBBackend_RunCommand(t *testing.T) {
	t.Run("ready returns JSON array", func(t *testing.T) {
		mock := &mockFleetService{
			readyIssues: []BdIssue{
				{ID: "t-1", Title: "Task 1", Status: "open", Labels: []string{}, Dependencies: []Dependency{}},
			},
			deps: []Dependency{},
		}
		b := newTestFleetDBBackend(mock)

		out, err := b.RunCommand("/dir", "ready", "--json")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		var issues []BdIssue
		if err := json.Unmarshal([]byte(out), &issues); err != nil {
			t.Fatalf("invalid JSON: %v", err)
		}
		if len(issues) != 1 || issues[0].ID != "t-1" {
			t.Errorf("got %v", issues)
		}
	})

	t.Run("ready with limit and parent", func(t *testing.T) {
		mock := &mockFleetService{readyIssues: []BdIssue{}, deps: []Dependency{}}
		b := newTestFleetDBBackend(mock)

		_, err := b.RunCommand("/dir", "ready", "--json", "--limit", "5", "--parent", "epic-1")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if mock.lastReadyLimit != 5 {
			t.Errorf("limit = %d, want 5", mock.lastReadyLimit)
		}
		if mock.lastReadyParent != "epic-1" {
			t.Errorf("parent = %q, want epic-1", mock.lastReadyParent)
		}
	})

	t.Run("list with all filters", func(t *testing.T) {
		mock := &mockFleetService{listIssues: []BdIssue{
			{ID: "l-1", Status: "open", IssueType: "task", Labels: []string{}, Dependencies: []Dependency{}},
		}}
		b := newTestFleetDBBackend(mock)

		out, err := b.RunCommand("/dir", "list", "--json", "--status=open", "--type=task", "--assignee", "alice", "--limit", "10")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if mock.lastListStatus != "open" {
			t.Errorf("status = %q, want open", mock.lastListStatus)
		}
		if mock.lastListType != "task" {
			t.Errorf("type = %q, want task", mock.lastListType)
		}
		if mock.lastListAssignee != "alice" {
			t.Errorf("assignee = %q, want alice", mock.lastListAssignee)
		}
		if mock.lastListLimit != 10 {
			t.Errorf("limit = %d, want 10", mock.lastListLimit)
		}
		var issues []BdIssue
		if err := json.Unmarshal([]byte(out), &issues); err != nil {
			t.Fatalf("invalid JSON: %v", err)
		}
		if len(issues) != 1 {
			t.Errorf("got %d issues, want 1", len(issues))
		}
	})

	t.Run("blocked returns JSON", func(t *testing.T) {
		mock := &mockFleetService{blockedIssues: []BdIssue{
			{ID: "b-1", Status: "blocked", Labels: []string{}, Dependencies: []Dependency{}},
		}}
		b := newTestFleetDBBackend(mock)

		out, err := b.RunCommand("/dir", "blocked", "--json")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		var issues []BdIssue
		if err := json.Unmarshal([]byte(out), &issues); err != nil {
			t.Fatalf("invalid JSON: %v", err)
		}
		if len(issues) != 1 || issues[0].ID != "b-1" {
			t.Errorf("got %v", issues)
		}
	})

	t.Run("stats returns JSON", func(t *testing.T) {
		mock := &mockFleetService{
			stats: BdStats{},
		}
		mock.stats.Summary.TotalIssues = 10
		mock.stats.Summary.OpenIssues = 5
		mock.stats.Summary.ClosedIssues = 3
		b := newTestFleetDBBackend(mock)

		out, err := b.RunCommand("/dir", "stats", "--json")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		var stats BdStats
		if err := json.Unmarshal([]byte(out), &stats); err != nil {
			t.Fatalf("invalid JSON: %v", err)
		}
		if stats.Summary.TotalIssues != 10 {
			t.Errorf("TotalIssues = %d, want 10", stats.Summary.TotalIssues)
		}
		if stats.Summary.OpenIssues != 5 {
			t.Errorf("OpenIssues = %d, want 5", stats.Summary.OpenIssues)
		}
	})

	t.Run("show with --json returns array", func(t *testing.T) {
		mock := &mockFleetService{
			issue: &BdIssue{ID: "x-1", Title: "Found it", Status: "open", Labels: []string{}, Dependencies: []Dependency{}},
			deps:  []Dependency{{IssueID: "x-1", DependsOnID: "x-0", Type: "blocks"}},
		}
		b := newTestFleetDBBackend(mock)

		out, err := b.RunCommand("/dir", "show", "x-1", "--json")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		var issues []BdIssue
		if err := json.Unmarshal([]byte(out), &issues); err != nil {
			t.Fatalf("invalid JSON: %v", err)
		}
		if len(issues) != 1 || issues[0].ID != "x-1" {
			t.Errorf("got %v", issues)
		}
		if len(issues[0].Dependencies) != 1 {
			t.Errorf("expected 1 dep, got %d", len(issues[0].Dependencies))
		}
	})

	t.Run("show without --json returns text", func(t *testing.T) {
		mock := &mockFleetService{
			issueText: "x-1 · Task title\nStatus: open · Priority: 1 · Type: task\n",
		}
		b := newTestFleetDBBackend(mock)

		out, err := b.RunCommand("/dir", "show", "x-1")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !strings.Contains(out, "x-1") {
			t.Errorf("output missing issue ID: %q", out)
		}
	})

	t.Run("update status to in_progress claims", func(t *testing.T) {
		mock := &mockFleetService{}
		b := newTestFleetDBBackend(mock)

		out, err := b.RunCommand("/dir", "update", "t-1", "--status", "in_progress")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if mock.lastClaimID != "t-1" {
			t.Errorf("claim ID = %q, want t-1", mock.lastClaimID)
		}
		if !strings.Contains(out, "Updated issue") {
			t.Errorf("unexpected output: %q", out)
		}
	})

	t.Run("update status to closed closes", func(t *testing.T) {
		mock := &mockFleetService{}
		b := newTestFleetDBBackend(mock)

		_, err := b.RunCommand("/dir", "update", "t-1", "--status", "closed")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if mock.lastCloseID != "t-1" {
			t.Errorf("close ID = %q, want t-1", mock.lastCloseID)
		}
	})

	t.Run("update status to open reopens", func(t *testing.T) {
		mock := &mockFleetService{}
		b := newTestFleetDBBackend(mock)

		_, err := b.RunCommand("/dir", "update", "t-1", "--status", "open")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if mock.lastReopenID != "t-1" {
			t.Errorf("reopen ID = %q, want t-1", mock.lastReopenID)
		}
	})

	t.Run("update status to deferred defers", func(t *testing.T) {
		mock := &mockFleetService{}
		b := newTestFleetDBBackend(mock)

		_, err := b.RunCommand("/dir", "update", "t-1", "--status", "deferred")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if mock.lastDeferID != "t-1" {
			t.Errorf("defer ID = %q, want t-1", mock.lastDeferID)
		}
	})

	t.Run("update assignee", func(t *testing.T) {
		mock := &mockFleetService{}
		b := newTestFleetDBBackend(mock)

		_, err := b.RunCommand("/dir", "update", "t-1", "--assignee", "bob")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if mock.lastAssignID != "t-1" || mock.lastAssignee != "bob" {
			t.Errorf("assign: id=%q assignee=%q", mock.lastAssignID, mock.lastAssignee)
		}
	})

	t.Run("update design and notes", func(t *testing.T) {
		mock := &mockFleetService{}
		b := newTestFleetDBBackend(mock)

		_, err := b.RunCommand("/dir", "update", "t-1", "--design", "new design", "--notes", "new notes")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if mock.lastUpdateID != "t-1" {
			t.Errorf("update ID = %q, want t-1", mock.lastUpdateID)
		}
		if mock.lastUpdateFields == nil {
			t.Fatal("expected update fields")
		}
		if *mock.lastUpdateFields["design"] != "new design" {
			t.Errorf("design = %q", *mock.lastUpdateFields["design"])
		}
		if *mock.lastUpdateFields["notes"] != "new notes" {
			t.Errorf("notes = %q", *mock.lastUpdateFields["notes"])
		}
	})

	t.Run("close with reason", func(t *testing.T) {
		mock := &mockFleetService{}
		b := newTestFleetDBBackend(mock)

		out, err := b.RunCommand("/dir", "close", "t-1", "--reason", "done")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if mock.lastCloseID != "t-1" || mock.lastCloseReason != "done" {
			t.Errorf("close: id=%q reason=%q", mock.lastCloseID, mock.lastCloseReason)
		}
		if !strings.Contains(out, "Closed issue") {
			t.Errorf("unexpected output: %q", out)
		}
	})

	t.Run("close without reason", func(t *testing.T) {
		mock := &mockFleetService{}
		b := newTestFleetDBBackend(mock)

		_, err := b.RunCommand("/dir", "close", "t-1")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if mock.lastCloseReason != "" {
			t.Errorf("reason = %q, want empty", mock.lastCloseReason)
		}
	})

	t.Run("sync returns synced", func(t *testing.T) {
		b := newTestFleetDBBackend(&mockFleetService{})
		out, err := b.RunCommand("/dir", "sync", "--status")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if out != "synced\n" {
			t.Errorf("got %q, want %q", out, "synced\n")
		}
	})

	t.Run("daemon returns empty", func(t *testing.T) {
		b := newTestFleetDBBackend(&mockFleetService{})
		out, err := b.RunCommand("/dir", "daemon")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if out != "" {
			t.Errorf("got %q, want empty", out)
		}
	})

	t.Run("unknown command returns error", func(t *testing.T) {
		b := newTestFleetDBBackend(&mockFleetService{})
		_, err := b.RunCommand("/dir", "unknown")
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "unknown command: unknown") {
			t.Errorf("error = %q", err)
		}
	})

	t.Run("no args returns error", func(t *testing.T) {
		b := newTestFleetDBBackend(&mockFleetService{})
		_, err := b.RunCommand("/dir")
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "no command specified") {
			t.Errorf("error = %q", err)
		}
	})
}

// --- JSON parity tests ---

func TestFleetDBBackend_JSONParity(t *testing.T) {
	t.Run("issue_type field populated", func(t *testing.T) {
		mock := &mockFleetService{
			listIssues: []BdIssue{
				{ID: "p-1", Status: "open", IssueType: "task", Labels: []string{}, Dependencies: []Dependency{}},
			},
		}
		b := newTestFleetDBBackend(mock)

		out, err := b.RunCommand("/dir", "list", "--json")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !strings.Contains(out, `"issue_type":"task"`) {
			t.Errorf("JSON missing issue_type field: %s", out)
		}
	})

	t.Run("labels is empty array not null", func(t *testing.T) {
		mock := &mockFleetService{
			listIssues: []BdIssue{
				{ID: "p-2", Status: "open", Labels: []string{}, Dependencies: []Dependency{}},
			},
		}
		b := newTestFleetDBBackend(mock)

		out, err := b.RunCommand("/dir", "list", "--json")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !strings.Contains(out, `"labels":[]`) {
			t.Errorf("JSON labels should be [] not null: %s", out)
		}
	})

	t.Run("dependencies is empty array not null", func(t *testing.T) {
		mock := &mockFleetService{
			listIssues: []BdIssue{
				{ID: "p-3", Status: "open", Labels: []string{}, Dependencies: []Dependency{}},
			},
		}
		b := newTestFleetDBBackend(mock)

		out, err := b.RunCommand("/dir", "list", "--json")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !strings.Contains(out, `"dependencies":[]`) {
			t.Errorf("JSON dependencies should be [] not null: %s", out)
		}
	})

	t.Run("empty results return empty array", func(t *testing.T) {
		mock := &mockFleetService{readyIssues: []BdIssue{}, deps: []Dependency{}}
		b := newTestFleetDBBackend(mock)

		out, err := b.RunCommand("/dir", "ready", "--json")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		out = strings.TrimSpace(out)
		if out != "[]" {
			t.Errorf("empty result should be []: %q", out)
		}
	})
}

// --- Typed method tests ---

func TestFleetDBBackend_TypedMethods(t *testing.T) {
	ctx := context.Background()

	t.Run("Ready", func(t *testing.T) {
		mock := &mockFleetService{
			readyIssues: []BdIssue{{ID: "r-1", Labels: []string{}, Dependencies: []Dependency{}}},
			deps:        []Dependency{},
		}
		b := newTestFleetDBBackend(mock)

		got, err := b.Ready(ctx, ReadyOpts{Limit: 5, ParentID: "e-1"})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(got) != 1 || got[0].ID != "r-1" {
			t.Errorf("got %v", got)
		}
		if mock.lastReadyLimit != 5 {
			t.Errorf("limit = %d, want 5", mock.lastReadyLimit)
		}
		if mock.lastReadyParent != "e-1" {
			t.Errorf("parent = %q, want e-1", mock.lastReadyParent)
		}
	})

	t.Run("List", func(t *testing.T) {
		mock := &mockFleetService{
			listIssues: []BdIssue{{ID: "l-1", Labels: []string{}, Dependencies: []Dependency{}}},
		}
		b := newTestFleetDBBackend(mock)

		got, err := b.List(ctx, ListOpts{Status: "open", IssueType: "task", Assignee: "alice", Limit: 10})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(got) != 1 {
			t.Errorf("got %d issues, want 1", len(got))
		}
		if mock.lastListStatus != "open" || mock.lastListType != "task" || mock.lastListAssignee != "alice" || mock.lastListLimit != 10 {
			t.Errorf("list opts not passed correctly")
		}
	})

	t.Run("Blocked", func(t *testing.T) {
		mock := &mockFleetService{
			blockedIssues: []BdIssue{{ID: "b-1", Labels: []string{}, Dependencies: []Dependency{}}},
		}
		b := newTestFleetDBBackend(mock)

		got, err := b.Blocked(ctx)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(got) != 1 || got[0].ID != "b-1" {
			t.Errorf("got %v", got)
		}
	})

	t.Run("Stats", func(t *testing.T) {
		mock := &mockFleetService{}
		mock.stats.Summary.TotalIssues = 10
		mock.stats.Summary.OpenIssues = 5
		b := newTestFleetDBBackend(mock)

		got, err := b.Stats(ctx)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got.Summary.TotalIssues != 10 || got.Summary.OpenIssues != 5 {
			t.Errorf("stats = %+v", got)
		}
	})

	t.Run("GetIssue with deps", func(t *testing.T) {
		mock := &mockFleetService{
			issue: &BdIssue{ID: "x-1", Title: "Test", Labels: []string{}, Dependencies: []Dependency{}},
			deps:  []Dependency{{IssueID: "x-1", DependsOnID: "x-0", Type: "blocks"}},
		}
		b := newTestFleetDBBackend(mock)

		got, err := b.GetIssue(ctx, "x-1")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got.ID != "x-1" {
			t.Errorf("ID = %q", got.ID)
		}
		if len(got.Dependencies) != 1 {
			t.Errorf("expected 1 dep, got %d", len(got.Dependencies))
		}
	})

	t.Run("GetIssue not found", func(t *testing.T) {
		mock := &mockFleetService{issue: nil}
		b := newTestFleetDBBackend(mock)

		_, err := b.GetIssue(ctx, "missing")
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "not found") {
			t.Errorf("error = %q", err)
		}
	})

	t.Run("GetIssueText", func(t *testing.T) {
		mock := &mockFleetService{issueText: "x-1 · Title\nStatus: open"}
		b := newTestFleetDBBackend(mock)

		got, err := b.GetIssueText(ctx, "x-1")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !strings.Contains(got, "x-1") {
			t.Errorf("got %q", got)
		}
	})

	t.Run("UpdateStatus in_progress", func(t *testing.T) {
		mock := &mockFleetService{}
		b := newTestFleetDBBackend(mock)

		err := b.UpdateStatus(ctx, "t-1", "in_progress", "alice")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if mock.lastClaimID != "t-1" {
			t.Errorf("claim ID = %q", mock.lastClaimID)
		}
		if mock.lastAssignID != "t-1" || mock.lastAssignee != "alice" {
			t.Errorf("assign: id=%q assignee=%q", mock.lastAssignID, mock.lastAssignee)
		}
	})

	t.Run("UpdateStatus without assignee", func(t *testing.T) {
		mock := &mockFleetService{}
		b := newTestFleetDBBackend(mock)

		err := b.UpdateStatus(ctx, "t-1", "closed", "")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if mock.lastCloseID != "t-1" {
			t.Errorf("close ID = %q", mock.lastCloseID)
		}
		if mock.lastAssignID != "" {
			t.Errorf("should not have assigned: %q", mock.lastAssignID)
		}
	})

	t.Run("UpdateExternalRef is noop", func(t *testing.T) {
		mock := &mockFleetService{}
		b := newTestFleetDBBackend(mock)

		err := b.UpdateExternalRef(ctx, "t-1", "GH-123")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("CloseIssue", func(t *testing.T) {
		mock := &mockFleetService{}
		b := newTestFleetDBBackend(mock)

		err := b.CloseIssue(ctx, "t-1", "done")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if mock.lastCloseID != "t-1" || mock.lastCloseReason != "done" {
			t.Errorf("close: id=%q reason=%q", mock.lastCloseID, mock.lastCloseReason)
		}
	})

	t.Run("SyncStatus", func(t *testing.T) {
		b := newTestFleetDBBackend(&mockFleetService{})
		got, err := b.SyncStatus(ctx)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != "synced" {
			t.Errorf("got %q", got)
		}
	})

	t.Run("BackendName", func(t *testing.T) {
		b := newTestFleetDBBackend(&mockFleetService{})
		if got := b.BackendName(); got != "fleetdb" {
			t.Errorf("BackendName() = %q, want fleetdb", got)
		}
	})
}

// --- Error wrapping tests ---

func TestFleetDBBackend_ErrorWrapping(t *testing.T) {
	cmdErr := fmt.Errorf("service error")
	ctx := context.Background()

	tests := []struct {
		name    string
		setup   func() *mockFleetService
		fn      func(*fleetDBBackend) error
		wantErr string
	}{
		{
			"Ready", func() *mockFleetService { return &mockFleetService{readyErr: cmdErr} },
			func(b *fleetDBBackend) error { _, e := b.Ready(ctx, ReadyOpts{}); return e },
			"fleetdb ready: service error",
		},
		{
			"List", func() *mockFleetService { return &mockFleetService{listErr: cmdErr} },
			func(b *fleetDBBackend) error { _, e := b.List(ctx, ListOpts{}); return e },
			"fleetdb list: service error",
		},
		{
			"Blocked", func() *mockFleetService { return &mockFleetService{blockedErr: cmdErr} },
			func(b *fleetDBBackend) error { _, e := b.Blocked(ctx); return e },
			"fleetdb blocked: service error",
		},
		{
			"Stats", func() *mockFleetService { return &mockFleetService{statsErr: cmdErr} },
			func(b *fleetDBBackend) error { _, e := b.Stats(ctx); return e },
			"fleetdb stats: service error",
		},
		{
			"GetIssue", func() *mockFleetService { return &mockFleetService{issueErr: cmdErr} },
			func(b *fleetDBBackend) error { _, e := b.GetIssue(ctx, "x"); return e },
			"fleetdb show x: service error",
		},
		{
			"GetIssueText", func() *mockFleetService { return &mockFleetService{issueTextErr: cmdErr} },
			func(b *fleetDBBackend) error { _, e := b.GetIssueText(ctx, "x"); return e },
			"fleetdb show x: service error",
		},
		{
			"UpdateStatus", func() *mockFleetService { return &mockFleetService{claimErr: cmdErr} },
			func(b *fleetDBBackend) error { return b.UpdateStatus(ctx, "x", "in_progress", "") },
			"fleetdb update x: service error",
		},
		{
			"CloseIssue", func() *mockFleetService { return &mockFleetService{closeErr: cmdErr} },
			func(b *fleetDBBackend) error { return b.CloseIssue(ctx, "x", "") },
			"fleetdb close x: service error",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			b := newTestFleetDBBackend(tt.setup())
			err := tt.fn(b)
			if err == nil {
				t.Fatal("expected error")
			}
			if got := err.Error(); got != tt.wantErr {
				t.Errorf("error = %q, want %q", got, tt.wantErr)
			}
		})
	}
}

// --- parseArgs tests ---

func TestParseArgs(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want map[string]string
	}{
		{
			"flag=value",
			[]string{"--status=open"},
			map[string]string{"status": "open"},
		},
		{
			"flag value",
			[]string{"--limit", "5"},
			map[string]string{"limit": "5"},
		},
		{
			"boolean flag",
			[]string{"--json"},
			map[string]string{"json": "true"},
		},
		{
			"positional args",
			[]string{"issue-1", "issue-2"},
			map[string]string{"_0": "issue-1", "_1": "issue-2"},
		},
		{
			"mixed",
			[]string{"issue-1", "--status=open", "--json", "--limit", "10"},
			map[string]string{"_0": "issue-1", "status": "open", "json": "true", "limit": "10"},
		},
		{
			"empty value after equals",
			[]string{"--flag="},
			map[string]string{"flag": ""},
		},
		{
			"empty args",
			[]string{},
			map[string]string{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseArgs(tt.args)
			if len(got) != len(tt.want) {
				t.Errorf("got %v, want %v", got, tt.want)
				return
			}
			for k, v := range tt.want {
				if got[k] != v {
					t.Errorf("key %q: got %q, want %q", k, got[k], v)
				}
			}
		})
	}
}

// --- Unsupported status test ---

func TestFleetDBBackend_UnsupportedStatus(t *testing.T) {
	mock := &mockFleetService{}
	b := newTestFleetDBBackend(mock)

	err := b.UpdateStatus(context.Background(), "t-1", "nonsense", "")
	if err == nil {
		t.Fatal("expected error for unsupported status")
	}
	if !strings.Contains(err.Error(), "unsupported status") {
		t.Errorf("error = %q", err)
	}
}

// --- Dependency hydration failure isolation test ---

func TestFleetDBBackend_DepHydrationFailure(t *testing.T) {
	mock := &mockFleetService{
		readyIssues: []BdIssue{
			{ID: "r-1", Labels: []string{}, Dependencies: []Dependency{}},
			{ID: "r-2", Labels: []string{}, Dependencies: []Dependency{}},
		},
		depsErr: fmt.Errorf("dep fetch failed"),
	}
	b := newTestFleetDBBackend(mock)

	// Should still succeed even though dep fetch failed.
	got, err := b.Ready(context.Background(), ReadyOpts{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 issues, got %d", len(got))
	}
	// Dependencies should be empty arrays (not nil).
	for _, issue := range got {
		if issue.Dependencies == nil {
			t.Errorf("issue %s: dependencies should not be nil", issue.ID)
		}
	}
}
