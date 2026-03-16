package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
)

func TestBdBackend_Ready(t *testing.T) {
	issues := []BdIssue{{ID: "T-1", Title: "Task 1", Status: "open", Priority: 1}}
	data, _ := json.Marshal(issues)

	bd := &MockBDRunner{RunFunc: func(dir string, args ...string) CommandResult {
		assertArgs(t, args, "ready", "--json", "--limit", "10", "--parent", "epic-1")
		return CommandResult{Stdout: string(data)}
	}}
	b := newBdBackend(bd, "/work")

	got, err := b.Ready(context.Background(), ReadyOpts{Limit: 10, ParentID: "epic-1"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 1 || got[0].ID != "T-1" {
		t.Errorf("got %v, want [{ID:T-1}]", got)
	}
}

func TestBdBackend_Ready_NoOpts(t *testing.T) {
	bd := &MockBDRunner{RunFunc: func(dir string, args ...string) CommandResult {
		assertArgs(t, args, "ready", "--json")
		return CommandResult{Stdout: "[]"}
	}}
	b := newBdBackend(bd, "/work")

	got, err := b.Ready(context.Background(), ReadyOpts{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("expected empty slice, got %v", got)
	}
}

func TestBdBackend_List(t *testing.T) {
	issues := []BdIssue{{ID: "T-2", Status: "in_progress"}}
	data, _ := json.Marshal(issues)

	bd := &MockBDRunner{RunFunc: func(dir string, args ...string) CommandResult {
		assertArgs(t, args, "list", "--json", "--status", "in_progress", "--assignee", "bot", "--type", "task", "--limit", "5")
		return CommandResult{Stdout: string(data)}
	}}
	b := newBdBackend(bd, "/work")

	got, err := b.List(context.Background(), ListOpts{
		Status: "in_progress", Assignee: "bot", Type: "task", Limit: 5,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 1 || got[0].ID != "T-2" {
		t.Errorf("got %v", got)
	}
}

func TestBdBackend_Blocked(t *testing.T) {
	issues := []BdIssue{{ID: "T-3", Status: "blocked"}}
	data, _ := json.Marshal(issues)

	bd := &MockBDRunner{RunFunc: func(dir string, args ...string) CommandResult {
		assertArgs(t, args, "blocked", "--json")
		return CommandResult{Stdout: string(data)}
	}}
	b := newBdBackend(bd, "/work")

	got, err := b.Blocked(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 1 || got[0].ID != "T-3" {
		t.Errorf("got %v", got)
	}
}

func TestBdBackend_Stats(t *testing.T) {
	stats := BdStats{}
	stats.Summary.TotalIssues = 10
	stats.Summary.OpenIssues = 7
	stats.Summary.ClosedIssues = 3
	data, _ := json.Marshal(stats)

	bd := &MockBDRunner{RunFunc: func(dir string, args ...string) CommandResult {
		assertArgs(t, args, "stats", "--json")
		return CommandResult{Stdout: string(data)}
	}}
	b := newBdBackend(bd, "/work")

	got, err := b.Stats(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.Summary.TotalIssues != 10 {
		t.Errorf("TotalIssues = %d, want 10", got.Summary.TotalIssues)
	}
	if got.Summary.OpenIssues != 7 {
		t.Errorf("OpenIssues = %d, want 7", got.Summary.OpenIssues)
	}
}

func TestBdBackend_GetIssue(t *testing.T) {
	issues := []BdIssue{{ID: "T-4", Title: "Detail task"}}
	data, _ := json.Marshal(issues)

	bd := &MockBDRunner{RunFunc: func(dir string, args ...string) CommandResult {
		assertArgs(t, args, "show", "T-4", "--json")
		return CommandResult{Stdout: string(data)}
	}}
	b := newBdBackend(bd, "/work")

	got, err := b.GetIssue(context.Background(), "T-4")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.ID != "T-4" || got.Title != "Detail task" {
		t.Errorf("got %+v", got)
	}
}

func TestBdBackend_GetIssue_NotFound(t *testing.T) {
	bd := &MockBDRunner{Result: CommandResult{Stdout: "[]"}}
	b := newBdBackend(bd, "/work")

	_, err := b.GetIssue(context.Background(), "MISSING")
	if err == nil {
		t.Fatal("expected error for empty result")
	}
	if got := err.Error(); got != "bdBackend.GetIssue(MISSING): not found" {
		t.Errorf("error = %q", got)
	}
}

func TestBdBackend_GetIssueText(t *testing.T) {
	bd := &MockBDRunner{RunFunc: func(dir string, args ...string) CommandResult {
		assertArgs(t, args, "show", "T-5")
		return CommandResult{Stdout: "Human-readable output"}
	}}
	b := newBdBackend(bd, "/work")

	got, err := b.GetIssueText(context.Background(), "T-5")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "Human-readable output" {
		t.Errorf("got %q", got)
	}
}

func TestBdBackend_UpdateIssue(t *testing.T) {
	bd := &MockBDRunner{RunFunc: func(dir string, args ...string) CommandResult {
		assertArgs(t, args, "update", "T-6", "--status", "in_progress")
		return CommandResult{}
	}}
	b := newBdBackend(bd, "/work")

	err := b.UpdateIssue(context.Background(), "T-6", UpdateOpts{Status: "in_progress"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestBdBackend_UpdateIssue_AllFields(t *testing.T) {
	assignee := "drift"
	bd := &MockBDRunner{RunFunc: func(dir string, args ...string) CommandResult {
		assertArgs(t, args, "update", "T-7", "--status", "review", "--assignee", "drift", "--design", "plan text", "--claim")
		return CommandResult{}
	}}
	b := newBdBackend(bd, "/work")

	err := b.UpdateIssue(context.Background(), "T-7", UpdateOpts{
		Status: "review", Assignee: &assignee, Design: "plan text", Claim: true,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestBdBackend_UpdateIssue_ClearAssignee(t *testing.T) {
	empty := ""
	bd := &MockBDRunner{RunFunc: func(dir string, args ...string) CommandResult {
		assertArgs(t, args, "update", "T-8", "--assignee", "")
		return CommandResult{}
	}}
	b := newBdBackend(bd, "/work")

	err := b.UpdateIssue(context.Background(), "T-8", UpdateOpts{Assignee: &empty})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestBdBackend_UpdateExternalRef(t *testing.T) {
	bd := &MockBDRunner{RunFunc: func(dir string, args ...string) CommandResult {
		assertArgs(t, args, "update", "T-9", "--external-ref", "GH-123")
		return CommandResult{}
	}}
	b := newBdBackend(bd, "/work")

	err := b.UpdateExternalRef(context.Background(), "T-9", "GH-123")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestBdBackend_CloseIssue(t *testing.T) {
	bd := &MockBDRunner{RunFunc: func(dir string, args ...string) CommandResult {
		assertArgs(t, args, "close", "T-10", "--reason", "completed")
		return CommandResult{}
	}}
	b := newBdBackend(bd, "/work")

	err := b.CloseIssue(context.Background(), "T-10", "completed")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestBdBackend_RunCommand(t *testing.T) {
	bd := &MockBDRunner{RunFunc: func(dir string, args ...string) CommandResult {
		if dir != "/custom" {
			t.Errorf("dir = %q, want /custom", dir)
		}
		assertArgs(t, args, "ready", "--json")
		return CommandResult{Stdout: "raw output"}
	}}
	b := newBdBackend(bd, "/work")

	got, err := b.RunCommand("/custom", "ready", "--json")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "raw output" {
		t.Errorf("got %q", got)
	}
}

func TestBdBackend_BackendName(t *testing.T) {
	b := newBdBackend(&MockBDRunner{}, "/work")
	if name := b.BackendName(); name != "beads" {
		t.Errorf("got %q, want beads", name)
	}
}

func TestBdBackend_ErrorPropagation(t *testing.T) {
	bd := &MockBDRunner{Result: CommandResult{Err: fmt.Errorf("connection refused")}}
	b := newBdBackend(bd, "/work")
	ctx := context.Background()

	tests := []struct {
		name string
		fn   func() error
	}{
		{"Ready", func() error { _, e := b.Ready(ctx, ReadyOpts{}); return e }},
		{"List", func() error { _, e := b.List(ctx, ListOpts{}); return e }},
		{"Blocked", func() error { _, e := b.Blocked(ctx); return e }},
		{"Stats", func() error { _, e := b.Stats(ctx); return e }},
		{"GetIssue", func() error { _, e := b.GetIssue(ctx, "X"); return e }},
		{"GetIssueText", func() error { _, e := b.GetIssueText(ctx, "X"); return e }},
		{"UpdateIssue", func() error { return b.UpdateIssue(ctx, "X", UpdateOpts{Status: "open"}) }},
		{"UpdateExternalRef", func() error { return b.UpdateExternalRef(ctx, "X", "ref") }},
		{"CloseIssue", func() error { return b.CloseIssue(ctx, "X", "reason") }},
		{"RunCommand", func() error { _, e := b.RunCommand("/d", "test"); return e }},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.fn()
			if err == nil {
				t.Fatal("expected error")
			}
		})
	}
}

func TestBdBackend_ParseError(t *testing.T) {
	bd := &MockBDRunner{Result: CommandResult{Stdout: "not json"}}
	b := newBdBackend(bd, "/work")
	ctx := context.Background()

	_, err := b.Ready(ctx, ReadyOpts{})
	if err == nil {
		t.Fatal("expected parse error")
	}

	_, err = b.Stats(ctx)
	if err == nil {
		t.Fatal("expected parse error")
	}

	_, err = b.GetIssue(ctx, "X")
	if err == nil {
		t.Fatal("expected parse error")
	}
}

func TestBdBackend_UsesStructDir(t *testing.T) {
	var capturedDir string
	bd := &MockBDRunner{RunFunc: func(dir string, args ...string) CommandResult {
		capturedDir = dir
		return CommandResult{Stdout: "[]"}
	}}
	b := newBdBackend(bd, "/my/project")

	_, _ = b.Ready(context.Background(), ReadyOpts{})
	if capturedDir != "/my/project" {
		t.Errorf("dir = %q, want /my/project", capturedDir)
	}
}

// assertArgs is a test helper that checks args match expected values.
func assertArgs(t *testing.T, got []string, want ...string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("args = %v, want %v", got, want)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Fatalf("args[%d] = %q, want %q (full: %v)", i, got[i], want[i], got)
		}
	}
}
