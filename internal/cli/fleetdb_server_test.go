package cli

import (
	"context"
	"io"
	"log/slog"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/tysonthomas9/fleet-db/pkg/client"
)

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// skipIfNoFleetDB skips the test if fleet-db binary is not in PATH.
func skipIfNoFleetDB(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("fleet-db"); err != nil {
		t.Skip("fleet-db binary not in PATH")
	}
}

// startTestServer creates a FleetDBServer with AutoStart=true and registers cleanup.
func startTestServer(t *testing.T) *FleetDBServer {
	t.Helper()
	skipIfNoFleetDB(t)

	cfg := FleetDBServerConfig{
		AutoStart: true,
		Workspace: "test",
		Actor:     "test-runner",
	}

	server, err := NewFleetDBServer(cfg, testLogger())
	if err != nil {
		t.Fatalf("NewFleetDBServer: %v", err)
	}
	t.Cleanup(func() {
		if err := server.Stop(); err != nil {
			t.Logf("server.Stop: %v", err)
		}
	})
	return server
}

func TestNewFleetDBServer_InvalidConfig(t *testing.T) {
	cfg := FleetDBServerConfig{
		AutoStart: false,
		RedisURL:  "",
	}
	_, err := NewFleetDBServer(cfg, testLogger())
	if err == nil {
		t.Fatal("expected error for invalid config, got nil")
	}
	want := "either RedisURL or AutoStart must be set"
	if got := err.Error(); !strings.Contains(got, want) {
		t.Errorf("error = %q, want substring %q", got, want)
	}
}

func TestNewFleetDBServer_BinaryNotFound(t *testing.T) {
	cfg := FleetDBServerConfig{
		AutoStart:  true,
		FleetDBBin: "nonexistent-binary-xyz-12345",
	}
	_, err := NewFleetDBServer(cfg, testLogger())
	if err == nil {
		t.Fatal("expected error for missing binary, got nil")
	}
	want := "not found"
	if got := err.Error(); !strings.Contains(got, want) {
		t.Errorf("error = %q, want substring %q", got, want)
	}
}

func TestFleetDBServer_StartStop(t *testing.T) {
	server := startTestServer(t)

	// Backend should be non-nil
	backend := server.Backend()
	if backend == nil {
		t.Fatal("Backend() returned nil")
	}

	// BackendName should be "fleetdb"
	if got := backend.BackendName(); got != "fleetdb" {
		t.Errorf("BackendName() = %q, want %q", got, "fleetdb")
	}
}

func TestFleetDBServer_WorkspaceCreation(t *testing.T) {
	server := startTestServer(t)

	// Calling GetIssue on a non-existent ID should get "not found",
	// not "workspace not found", proving the workspace was created.
	ctx := context.Background()
	_, err := server.backend.svc.GetIssue(ctx, "nonexistent-id")
	if err == nil {
		t.Fatal("expected error for non-existent issue, got nil")
	}
	errStr := err.Error()
	if strings.Contains(errStr, "workspace") {
		t.Errorf("error mentions workspace (workspace may not have been created): %v", err)
	}
}

func TestClientIssueToBdIssue(t *testing.T) {
	issue := &client.Issue{
		ID:       "test-123",
		Title:    "Test Issue",
		Status:   client.StatusOpen,
		Priority: 2,
		Type:     client.TypeTask,
		Design:   "some design",
		Assignee: "alice",
		Labels:   []string{"bug", "urgent"},
	}

	bd := clientIssueToBdIssue(issue)

	if bd.ID != "test-123" {
		t.Errorf("ID = %q, want %q", bd.ID, "test-123")
	}
	if bd.Title != "Test Issue" {
		t.Errorf("Title = %q, want %q", bd.Title, "Test Issue")
	}
	if bd.Status != "open" {
		t.Errorf("Status = %q, want %q", bd.Status, "open")
	}
	if bd.Priority != 2 {
		t.Errorf("Priority = %d, want %d", bd.Priority, 2)
	}
	if bd.IssueType != "task" {
		t.Errorf("IssueType = %q, want %q", bd.IssueType, "task")
	}
	if bd.Design != "some design" {
		t.Errorf("Design = %q, want %q", bd.Design, "some design")
	}
	if bd.Assignee != "alice" {
		t.Errorf("Assignee = %q, want %q", bd.Assignee, "alice")
	}
	if len(bd.Labels) != 2 || bd.Labels[0] != "bug" || bd.Labels[1] != "urgent" {
		t.Errorf("Labels = %v, want [bug urgent]", bd.Labels)
	}
}

func TestClientDepToBdDep(t *testing.T) {
	ts := time.Date(2026, 3, 15, 10, 30, 0, 0, time.UTC)
	dep := &client.Dependency{
		IssueID:     "issue-1",
		DependsOnID: "issue-2",
		Type:        client.DepBlocks,
		CreatedAt:   ts,
		CreatedBy:   "bob",
	}

	bd := clientDepToBdDep(dep)

	if bd.IssueID != "issue-1" {
		t.Errorf("IssueID = %q, want %q", bd.IssueID, "issue-1")
	}
	if bd.DependsOnID != "issue-2" {
		t.Errorf("DependsOnID = %q, want %q", bd.DependsOnID, "issue-2")
	}
	if bd.Type != "blocks" {
		t.Errorf("Type = %q, want %q", bd.Type, "blocks")
	}
	if bd.CreatedBy != "bob" {
		t.Errorf("CreatedBy = %q, want %q", bd.CreatedBy, "bob")
	}

	// Verify RFC3339 formatting
	want := "2026-03-15T10:30:00Z"
	if bd.CreatedAt != want {
		t.Errorf("CreatedAt = %q, want %q", bd.CreatedAt, want)
	}
}

func TestCountResponseToBdStats(t *testing.T) {
	resp := &client.CountIssuesResponse{
		Total: 42,
		Groups: map[string]int64{
			"open":        10,
			"closed":      15,
			"in_progress": 5,
			"blocked":     3,
			"deferred":    4,
			"tombstone":   2,
			"pinned":      3,
		},
	}

	stats := countResponseToBdStats(resp)

	if stats.Summary.TotalIssues != 42 {
		t.Errorf("TotalIssues = %d, want 42", stats.Summary.TotalIssues)
	}
	if stats.Summary.OpenIssues != 10 {
		t.Errorf("OpenIssues = %d, want 10", stats.Summary.OpenIssues)
	}
	if stats.Summary.ClosedIssues != 15 {
		t.Errorf("ClosedIssues = %d, want 15", stats.Summary.ClosedIssues)
	}
	if stats.Summary.InProgressIssues != 5 {
		t.Errorf("InProgressIssues = %d, want 5", stats.Summary.InProgressIssues)
	}
	if stats.Summary.BlockedIssues != 3 {
		t.Errorf("BlockedIssues = %d, want 3", stats.Summary.BlockedIssues)
	}
	if stats.Summary.DeferredIssues != 4 {
		t.Errorf("DeferredIssues = %d, want 4", stats.Summary.DeferredIssues)
	}
	if stats.Summary.TombstoneIssues != 2 {
		t.Errorf("TombstoneIssues = %d, want 2", stats.Summary.TombstoneIssues)
	}
	if stats.Summary.PinnedIssues != 3 {
		t.Errorf("PinnedIssues = %d, want 3", stats.Summary.PinnedIssues)
	}
}

func TestCountResponseToBdStats_MissingKeys(t *testing.T) {
	resp := &client.CountIssuesResponse{
		Total:  5,
		Groups: map[string]int64{"open": 5},
	}

	stats := countResponseToBdStats(resp)

	if stats.Summary.TotalIssues != 5 {
		t.Errorf("TotalIssues = %d, want 5", stats.Summary.TotalIssues)
	}
	// Missing keys should default to 0
	if stats.Summary.ClosedIssues != 0 {
		t.Errorf("ClosedIssues = %d, want 0", stats.Summary.ClosedIssues)
	}
	if stats.Summary.BlockedIssues != 0 {
		t.Errorf("BlockedIssues = %d, want 0", stats.Summary.BlockedIssues)
	}
}

func TestFleetClientAdapter_GetReady(t *testing.T) {
	server := startTestServer(t)
	ctx := context.Background()

	// Create an issue via the fleet-db client directly
	_, err := server.client.CreateIssue(ctx, "test", &client.CreateIssueRequest{
		Title:    "Test Ready Issue",
		Priority: 1,
		Type:     client.TypeTask,
	})
	if err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}

	// Call GetReady via the adapter
	issues, err := server.backend.svc.GetReady(ctx, 10, "")
	if err != nil {
		t.Fatalf("GetReady: %v", err)
	}
	if len(issues) == 0 {
		t.Fatal("GetReady returned no issues, expected at least 1")
	}

	found := false
	for _, issue := range issues {
		if issue.Title == "Test Ready Issue" {
			found = true
			break
		}
	}
	if !found {
		t.Error("created issue not found in GetReady results")
	}
}

func TestFleetClientAdapter_CloseIssue(t *testing.T) {
	server := startTestServer(t)
	ctx := context.Background()

	// Create an issue
	created, err := server.client.CreateIssue(ctx, "test", &client.CreateIssueRequest{
		Title:    "Test Close Issue",
		Priority: 2,
		Type:     client.TypeTask,
	})
	if err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}

	// Close via adapter
	if err := server.backend.svc.CloseIssue(ctx, created.ID, "done"); err != nil {
		t.Fatalf("CloseIssue: %v", err)
	}

	// Verify status is closed
	issue, err := server.backend.svc.GetIssue(ctx, created.ID)
	if err != nil {
		t.Fatalf("GetIssue: %v", err)
	}
	if issue.Status != "closed" {
		t.Errorf("status = %q, want %q", issue.Status, "closed")
	}
}

func TestFleetDBServer_SubprocessCrash(t *testing.T) {
	server := startTestServer(t)

	// Kill the subprocess externally
	if err := server.cmd.Process.Kill(); err != nil {
		t.Fatalf("Kill: %v", err)
	}

	// Stop should not hang or panic
	done := make(chan struct{})
	go func() {
		_ = server.Stop()
		close(done)
	}()

	select {
	case <-done:
		// Success - Stop returned
	case <-time.After(10 * time.Second):
		t.Fatal("Stop() timed out after subprocess crash")
	}
}

func TestStripRedisScheme(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"redis://localhost:6379", "localhost:6379"},
		{"redis://host:1234", "host:1234"},
		{"rediss://secure-host:6380", "secure-host:6380"},
		{"localhost:6379", "localhost:6379"},
		{"", ""},
	}
	for _, tt := range tests {
		if got := stripRedisScheme(tt.input); got != tt.want {
			t.Errorf("stripRedisScheme(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestFormatIssueText(t *testing.T) {
	issue := &client.Issue{
		ID:          "abc-123",
		Title:       "My Issue",
		Status:      client.StatusInProgress,
		Priority:    1,
		Type:        client.TypeFeature,
		Assignee:    "carol",
		Description: "Some description text",
	}

	text := formatIssueText(issue)

	if !strings.Contains(text, "abc-123") {
		t.Error("missing issue ID in formatted text")
	}
	if !strings.Contains(text, "My Issue") {
		t.Error("missing title in formatted text")
	}
	if !strings.Contains(text, "in_progress") {
		t.Error("missing status in formatted text")
	}
	if !strings.Contains(text, "carol") {
		t.Error("missing assignee in formatted text")
	}
	if !strings.Contains(text, "Some description text") {
		t.Error("missing description in formatted text")
	}
}
