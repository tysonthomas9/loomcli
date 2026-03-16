package cli

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/tysonthomas9/fleet-db/pkg/client"
)

// ---------------------------------------------------------------------------
// Test helpers
// ---------------------------------------------------------------------------

func skipIfNoFleetDB(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("fleet-db"); err != nil {
		t.Skip("fleet-db binary not in PATH")
	}
}

func startTestServer(t *testing.T) *FleetDBServer {
	t.Helper()
	skipIfNoFleetDB(t)

	logger := slog.Default()
	srv, err := NewFleetDBServer(FleetDBServerConfig{
		AutoStart: true,
		Workspace: "test",
		Actor:     "test-actor",
	}, logger)
	if err != nil {
		t.Fatalf("NewFleetDBServer: %v", err)
	}
	t.Cleanup(func() {
		if stopErr := srv.Stop(); stopErr != nil {
			t.Errorf("Stop: %v", stopErr)
		}
	})
	return srv
}

// ---------------------------------------------------------------------------
// Server lifecycle tests
// ---------------------------------------------------------------------------

func TestNewFleetDBServer_InvalidConfig(t *testing.T) {
	_, err := NewFleetDBServer(FleetDBServerConfig{
		AutoStart: false,
		RedisURL:  "",
	}, slog.Default())
	if err == nil {
		t.Fatal("expected error for invalid config, got nil")
	}
	if want := "either RedisURL or AutoStart"; !strings.Contains(err.Error(), want) {
		t.Errorf("error = %q, want substring %q", err, want)
	}
}

func TestNewFleetDBServer_BinaryNotFound(t *testing.T) {
	_, err := NewFleetDBServer(FleetDBServerConfig{
		AutoStart:  true,
		FleetDBBin: "nonexistent-binary-xyz",
	}, slog.Default())
	if err == nil {
		t.Fatal("expected error for missing binary, got nil")
	}
	if want := "not found"; !strings.Contains(err.Error(), want) {
		t.Errorf("error = %q, want substring %q", err, want)
	}
}

func TestFleetDBServer_StartStop(t *testing.T) {
	srv := startTestServer(t)

	backend := srv.Backend()
	if backend == nil {
		t.Fatal("Backend() returned nil")
	}

	if got := backend.BackendName(); got != "fleetdb" {
		t.Errorf("BackendName() = %q, want %q", got, "fleetdb")
	}

	// Stop is called by t.Cleanup; verify it does not panic or return an error.
}

// ---------------------------------------------------------------------------
// Type conversion tests
// ---------------------------------------------------------------------------

func TestClientIssueToBdIssue(t *testing.T) {
	src := &client.Issue{
		ID:          "ISSUE-1",
		Title:       "Fix the widget",
		Status:      client.StatusInProgress,
		Priority:    2,
		Type:        client.TypeBug,
		Design:      "some design doc",
		Assignee:    "alice",
		Labels:      []string{"urgent", "backend"},
		Description: "A lengthy description",
		Workspace:   "ws1",
	}

	got := clientIssueToBdIssue(src)

	if got.ID != src.ID {
		t.Errorf("ID = %q, want %q", got.ID, src.ID)
	}
	if got.Title != src.Title {
		t.Errorf("Title = %q, want %q", got.Title, src.Title)
	}
	if got.Status != string(src.Status) {
		t.Errorf("Status = %q, want %q", got.Status, string(src.Status))
	}
	if got.Priority != src.Priority {
		t.Errorf("Priority = %d, want %d", got.Priority, src.Priority)
	}
	if got.IssueType != string(src.Type) {
		t.Errorf("IssueType = %q, want %q", got.IssueType, string(src.Type))
	}
	if got.Design != src.Design {
		t.Errorf("Design = %q, want %q", got.Design, src.Design)
	}
	if got.Assignee != src.Assignee {
		t.Errorf("Assignee = %q, want %q", got.Assignee, src.Assignee)
	}
	if len(got.Labels) != len(src.Labels) {
		t.Fatalf("Labels length = %d, want %d", len(got.Labels), len(src.Labels))
	}
	for i, lbl := range src.Labels {
		if got.Labels[i] != lbl {
			t.Errorf("Labels[%d] = %q, want %q", i, got.Labels[i], lbl)
		}
	}
}

func TestClientIssuesToBdIssues(t *testing.T) {
	t.Run("empty slice", func(t *testing.T) {
		got := clientIssuesToBdIssues([]*client.Issue{})
		if len(got) != 0 {
			t.Errorf("expected empty slice, got len %d", len(got))
		}
	})

	t.Run("multiple issues", func(t *testing.T) {
		issues := []*client.Issue{
			{ID: "A", Title: "Alpha", Status: client.StatusOpen, Priority: 1, Type: client.TypeTask},
			{ID: "B", Title: "Beta", Status: client.StatusClosed, Priority: 3, Type: client.TypeFeature},
			{ID: "C", Title: "Gamma", Status: client.StatusBlocked, Priority: 5, Type: client.TypeEpic},
		}
		got := clientIssuesToBdIssues(issues)
		if len(got) != len(issues) {
			t.Fatalf("len = %d, want %d", len(got), len(issues))
		}
		for i, issue := range issues {
			if got[i].ID != issue.ID {
				t.Errorf("[%d] ID = %q, want %q", i, got[i].ID, issue.ID)
			}
			if got[i].Title != issue.Title {
				t.Errorf("[%d] Title = %q, want %q", i, got[i].Title, issue.Title)
			}
			if got[i].Status != string(issue.Status) {
				t.Errorf("[%d] Status = %q, want %q", i, got[i].Status, string(issue.Status))
			}
		}
	})
}

func TestClientDepToBdDep(t *testing.T) {
	ts := time.Date(2025, 6, 15, 10, 30, 0, 0, time.UTC)
	src := &client.Dependency{
		IssueID:     "ISSUE-1",
		DependsOnID: "ISSUE-2",
		Type:        client.DepBlocks,
		CreatedAt:   ts,
		CreatedBy:   "bob",
	}

	got := clientDepToBdDep(src)

	if got.IssueID != src.IssueID {
		t.Errorf("IssueID = %q, want %q", got.IssueID, src.IssueID)
	}
	if got.DependsOnID != src.DependsOnID {
		t.Errorf("DependsOnID = %q, want %q", got.DependsOnID, src.DependsOnID)
	}
	if got.Type != string(src.Type) {
		t.Errorf("Type = %q, want %q", got.Type, string(src.Type))
	}
	if got.CreatedBy != src.CreatedBy {
		t.Errorf("CreatedBy = %q, want %q", got.CreatedBy, src.CreatedBy)
	}

	// Verify RFC3339 format
	wantTime := ts.Format(time.RFC3339)
	if got.CreatedAt != wantTime {
		t.Errorf("CreatedAt = %q, want RFC3339 %q", got.CreatedAt, wantTime)
	}
	if _, err := time.Parse(time.RFC3339, got.CreatedAt); err != nil {
		t.Errorf("CreatedAt %q is not valid RFC3339: %v", got.CreatedAt, err)
	}
}

func TestClientDepsToBdDeps(t *testing.T) {
	t.Run("empty slice", func(t *testing.T) {
		got := clientDepsToBdDeps([]*client.Dependency{})
		if len(got) != 0 {
			t.Errorf("expected empty slice, got len %d", len(got))
		}
	})

	t.Run("multiple dependencies", func(t *testing.T) {
		deps := []*client.Dependency{
			{IssueID: "A", DependsOnID: "B", Type: client.DepBlocks, CreatedAt: time.Now(), CreatedBy: "x"},
			{IssueID: "C", DependsOnID: "D", Type: client.DepParentChild, CreatedAt: time.Now(), CreatedBy: "y"},
		}
		got := clientDepsToBdDeps(deps)
		if len(got) != len(deps) {
			t.Fatalf("len = %d, want %d", len(got), len(deps))
		}
		for i, dep := range deps {
			if got[i].IssueID != dep.IssueID {
				t.Errorf("[%d] IssueID = %q, want %q", i, got[i].IssueID, dep.IssueID)
			}
			if got[i].DependsOnID != dep.DependsOnID {
				t.Errorf("[%d] DependsOnID = %q, want %q", i, got[i].DependsOnID, dep.DependsOnID)
			}
			if got[i].Type != string(dep.Type) {
				t.Errorf("[%d] Type = %q, want %q", i, got[i].Type, string(dep.Type))
			}
		}
	})
}

func TestCountResponseToBdStats(t *testing.T) {
	t.Run("all keys present", func(t *testing.T) {
		resp := &client.CountIssuesResponse{
			Total: 42,
			Groups: map[string]int64{
				"open":        10,
				"closed":      8,
				"in_progress": 5,
				"blocked":     3,
				"deferred":    2,
				"tombstone":   1,
				"pinned":      4,
			},
		}

		got := countResponseToBdStats(resp)

		if got.Summary.TotalIssues != 42 {
			t.Errorf("TotalIssues = %d, want 42", got.Summary.TotalIssues)
		}
		if got.Summary.OpenIssues != 10 {
			t.Errorf("OpenIssues = %d, want 10", got.Summary.OpenIssues)
		}
		if got.Summary.ClosedIssues != 8 {
			t.Errorf("ClosedIssues = %d, want 8", got.Summary.ClosedIssues)
		}
		if got.Summary.InProgressIssues != 5 {
			t.Errorf("InProgressIssues = %d, want 5", got.Summary.InProgressIssues)
		}
		if got.Summary.BlockedIssues != 3 {
			t.Errorf("BlockedIssues = %d, want 3", got.Summary.BlockedIssues)
		}
		if got.Summary.DeferredIssues != 2 {
			t.Errorf("DeferredIssues = %d, want 2", got.Summary.DeferredIssues)
		}
		if got.Summary.TombstoneIssues != 1 {
			t.Errorf("TombstoneIssues = %d, want 1", got.Summary.TombstoneIssues)
		}
		if got.Summary.PinnedIssues != 4 {
			t.Errorf("PinnedIssues = %d, want 4", got.Summary.PinnedIssues)
		}
	})

	t.Run("missing keys default to zero", func(t *testing.T) {
		resp := &client.CountIssuesResponse{
			Total:  3,
			Groups: map[string]int64{"open": 3},
		}

		got := countResponseToBdStats(resp)

		if got.Summary.TotalIssues != 3 {
			t.Errorf("TotalIssues = %d, want 3", got.Summary.TotalIssues)
		}
		if got.Summary.OpenIssues != 3 {
			t.Errorf("OpenIssues = %d, want 3", got.Summary.OpenIssues)
		}
		if got.Summary.ClosedIssues != 0 {
			t.Errorf("ClosedIssues = %d, want 0", got.Summary.ClosedIssues)
		}
	})
}

func TestFormatIssueText(t *testing.T) {
	issue := &client.Issue{
		ID:          "TASK-42",
		Title:       "Implement caching",
		Status:      client.StatusOpen,
		Priority:    1,
		Type:        client.TypeTask,
		Assignee:    "alice",
		Description: "Add Redis caching layer",
	}

	got := formatIssueText(issue)

	want := fmt.Sprintf("# %s: %s\nStatus: %s  Priority: %d  Type: %s\nAssignee: %s\n---\n%s\n",
		issue.ID, issue.Title,
		issue.Status, issue.Priority, issue.Type,
		issue.Assignee,
		issue.Description,
	)

	if got != want {
		t.Errorf("formatIssueText mismatch.\ngot:\n%s\nwant:\n%s", got, want)
	}
}

// ---------------------------------------------------------------------------
// Utility tests
// ---------------------------------------------------------------------------

func TestFindFreePort(t *testing.T) {
	port, err := findFreePort()
	if err != nil {
		t.Fatalf("findFreePort: %v", err)
	}
	if port <= 0 || port >= 65536 {
		t.Fatalf("port %d out of valid range (1-65535)", port)
	}

	ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		t.Fatalf("port %d returned by findFreePort is not available: %v", port, err)
	}
	ln.Close()
}

// ---------------------------------------------------------------------------
// Integration tests (require fleet-db binary)
// ---------------------------------------------------------------------------

func TestFleetDBServer_WorkspaceCreation(t *testing.T) {
	server := startTestServer(t)

	// Verify workspace exists by trying to get a non-existent issue.
	// Should get "not found", not "workspace not found".
	_, err := server.client.GetIssue(context.Background(), server.cfg.Workspace, "nonexistent-id")
	if err == nil {
		t.Fatal("expected error for non-existent issue")
	}
	var ce *client.ClientError
	if !errors.As(err, &ce) {
		t.Fatalf("expected ClientError, got %T: %v", err, err)
	}
	if !ce.IsNotFound() {
		t.Errorf("expected not-found error, got code=%s message=%s", ce.Code, ce.Message)
	}
}

func TestFleetClientAdapter_CloseIssue(t *testing.T) {
	server := startTestServer(t)

	issue, err := server.client.CreateIssue(context.Background(), server.cfg.Workspace, &client.CreateIssueRequest{
		Title:    "Test Close",
		Priority: 2,
		Type:     client.TypeTask,
	})
	if err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}

	adapter := newFleetClientAdapter(server.client, server.cfg.Workspace, slog.Default())
	if err := adapter.CloseIssue(context.Background(), issue.ID, "done"); err != nil {
		t.Fatalf("CloseIssue: %v", err)
	}

	got, err := server.client.GetIssue(context.Background(), server.cfg.Workspace, issue.ID)
	if err != nil {
		t.Fatalf("GetIssue after close: %v", err)
	}
	if got.Status != client.StatusClosed {
		t.Errorf("Status = %s, want closed", got.Status)
	}
}

func TestFleetDBServer_SubprocessCrash(t *testing.T) {
	server := startTestServer(t)

	if err := server.cmd.Process.Kill(); err != nil {
		t.Fatalf("Kill subprocess: %v", err)
	}

	done := make(chan struct{})
	go func() {
		_ = server.Stop()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("Stop() hung after subprocess crash")
	}
}
