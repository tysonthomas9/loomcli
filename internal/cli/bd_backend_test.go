package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
)

// Compile-time interface check.
var _ IssueTracker = (*bdBackend)(nil)

func newTestBackend(runner *MockBDRunner) *bdBackend {
	return newBDBackend(runner, "/test/dir")
}

func TestBdBackend_RunCommand(t *testing.T) {
	t.Run("returns stdout on success", func(t *testing.T) {
		mock := &MockBDRunner{Result: CommandResult{Stdout: "hello\n"}}
		b := newTestBackend(mock)

		out, err := b.RunCommand("/some/dir", "status")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if out != "hello\n" {
			t.Errorf("got %q, want %q", out, "hello\n")
		}
		if len(mock.Calls) != 1 {
			t.Fatalf("expected 1 call, got %d", len(mock.Calls))
		}
		if mock.Calls[0].Dir != "/some/dir" {
			t.Errorf("dir = %q, want /some/dir", mock.Calls[0].Dir)
		}
		if !slicesEqual(mock.Calls[0].Args, []string{"status"}) {
			t.Errorf("args = %v", mock.Calls[0].Args)
		}
	})

	t.Run("propagates error", func(t *testing.T) {
		mock := &MockBDRunner{Result: CommandResult{Err: fmt.Errorf("bd failed")}}
		b := newTestBackend(mock)

		_, err := b.RunCommand("/dir", "bad")
		if err == nil {
			t.Fatal("expected error")
		}
		if err.Error() != "bd failed" {
			t.Errorf("error = %q", err)
		}
	})
}

func TestBdBackend_Ready(t *testing.T) {
	issues := []BdIssue{{ID: "t-1", Title: "Task 1", Status: "open"}}
	data, _ := json.Marshal(issues)

	t.Run("with all opts", func(t *testing.T) {
		mock := &MockBDRunner{Result: CommandResult{Stdout: string(data)}}
		b := newTestBackend(mock)

		got, err := b.Ready(context.Background(), ReadyOpts{Limit: 5, ParentID: "epic-1"})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(got) != 1 || got[0].ID != "t-1" {
			t.Errorf("got %v", got)
		}
		args := mock.Calls[0].Args
		want := []string{"ready", "--json", "--limit", "5", "--parent", "epic-1"}
		if !slicesEqual(args, want) {
			t.Errorf("args = %v, want %v", args, want)
		}
	})

	t.Run("minimal opts", func(t *testing.T) {
		mock := &MockBDRunner{Result: CommandResult{Stdout: "[]"}}
		b := newTestBackend(mock)

		got, err := b.Ready(context.Background(), ReadyOpts{})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(got) != 0 {
			t.Errorf("expected empty, got %v", got)
		}
		args := mock.Calls[0].Args
		if !slicesEqual(args, []string{"ready", "--json"}) {
			t.Errorf("args = %v", args)
		}
	})
}

func TestBdBackend_List(t *testing.T) {
	issues := []BdIssue{{ID: "l-1", Status: "open", IssueType: "task"}}
	data, _ := json.Marshal(issues)

	t.Run("with all opts", func(t *testing.T) {
		mock := &MockBDRunner{Result: CommandResult{Stdout: string(data)}}
		b := newTestBackend(mock)

		got, err := b.List(context.Background(), ListOpts{
			Status:    "open",
			IssueType: "task",
			Assignee:  "alice",
			Limit:     10,
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(got) != 1 || got[0].ID != "l-1" {
			t.Errorf("got %v", got)
		}
		args := mock.Calls[0].Args
		want := []string{"list", "--json", "--status=open", "--type=task", "--assignee", "alice", "--limit", "10"}
		if !slicesEqual(args, want) {
			t.Errorf("args = %v, want %v", args, want)
		}
	})

	t.Run("empty opts", func(t *testing.T) {
		mock := &MockBDRunner{Result: CommandResult{Stdout: "[]"}}
		b := newTestBackend(mock)

		_, err := b.List(context.Background(), ListOpts{})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !slicesEqual(mock.Calls[0].Args, []string{"list", "--json"}) {
			t.Errorf("args = %v", mock.Calls[0].Args)
		}
	})
}

func TestBdBackend_Blocked(t *testing.T) {
	t.Run("returns issues", func(t *testing.T) {
		issues := []BdIssue{{ID: "b-1", Status: "blocked"}}
		data, _ := json.Marshal(issues)
		mock := &MockBDRunner{Result: CommandResult{Stdout: string(data)}}
		b := newTestBackend(mock)

		got, err := b.Blocked(context.Background())
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(got) != 1 || got[0].ID != "b-1" {
			t.Errorf("got %v", got)
		}
		if !slicesEqual(mock.Calls[0].Args, []string{"blocked", "--json"}) {
			t.Errorf("args = %v", mock.Calls[0].Args)
		}
	})

	t.Run("empty result", func(t *testing.T) {
		mock := &MockBDRunner{Result: CommandResult{Stdout: "[]"}}
		b := newTestBackend(mock)

		got, err := b.Blocked(context.Background())
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got == nil {
			t.Error("expected non-nil empty slice")
		}
		if len(got) != 0 {
			t.Errorf("expected empty, got %v", got)
		}
	})
}

func TestBdBackend_Stats(t *testing.T) {
	statsJSON := `{"summary":{"total_issues":10,"open_issues":5,"closed_issues":3,"in_progress_issues":2}}`
	mock := &MockBDRunner{Result: CommandResult{Stdout: statsJSON}}
	b := newTestBackend(mock)

	got, err := b.Stats(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.Summary.TotalIssues != 10 {
		t.Errorf("TotalIssues = %d, want 10", got.Summary.TotalIssues)
	}
	if got.Summary.OpenIssues != 5 {
		t.Errorf("OpenIssues = %d, want 5", got.Summary.OpenIssues)
	}
	if !slicesEqual(mock.Calls[0].Args, []string{"stats", "--json"}) {
		t.Errorf("args = %v", mock.Calls[0].Args)
	}
}

func TestBdBackend_GetIssue(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		issues := []BdIssue{{ID: "x-1", Title: "Found it", Status: "open"}}
		data, _ := json.Marshal(issues)
		mock := &MockBDRunner{Result: CommandResult{Stdout: string(data)}}
		b := newTestBackend(mock)

		got, err := b.GetIssue(context.Background(), "x-1")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got.ID != "x-1" || got.Title != "Found it" {
			t.Errorf("got %+v", got)
		}
		if !slicesEqual(mock.Calls[0].Args, []string{"show", "x-1", "--json"}) {
			t.Errorf("args = %v", mock.Calls[0].Args)
		}
	})

	t.Run("empty array", func(t *testing.T) {
		mock := &MockBDRunner{Result: CommandResult{Stdout: "[]"}}
		b := newTestBackend(mock)

		_, err := b.GetIssue(context.Background(), "missing-1")
		if err == nil {
			t.Fatal("expected error for empty array")
		}
		if got := err.Error(); got != "bd show missing-1: no results" {
			t.Errorf("error = %q", got)
		}
	})

	t.Run("parse error", func(t *testing.T) {
		mock := &MockBDRunner{Result: CommandResult{Stdout: "not json"}}
		b := newTestBackend(mock)

		_, err := b.GetIssue(context.Background(), "bad-1")
		if err == nil {
			t.Fatal("expected error for bad JSON")
		}
	})
}

func TestBdBackend_GetIssueText(t *testing.T) {
	mock := &MockBDRunner{Result: CommandResult{Stdout: "  Issue: x-1\n  Title: Hello\n  "}}
	b := newTestBackend(mock)

	got, err := b.GetIssueText(context.Background(), "x-1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "Issue: x-1\n  Title: Hello" {
		t.Errorf("got %q", got)
	}
	// Should NOT have --json flag
	args := mock.Calls[0].Args
	if !slicesEqual(args, []string{"show", "x-1"}) {
		t.Errorf("args = %v (should not have --json)", args)
	}
}

func TestBdBackend_UpdateStatus(t *testing.T) {
	t.Run("with assignee", func(t *testing.T) {
		mock := &MockBDRunner{Result: CommandResult{}}
		b := newTestBackend(mock)

		err := b.UpdateStatus(context.Background(), "t-1", "in_progress", "alice")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		want := []string{"update", "t-1", "--status", "in_progress", "--assignee", "alice"}
		if !slicesEqual(mock.Calls[0].Args, want) {
			t.Errorf("args = %v, want %v", mock.Calls[0].Args, want)
		}
	})

	t.Run("without assignee", func(t *testing.T) {
		mock := &MockBDRunner{Result: CommandResult{}}
		b := newTestBackend(mock)

		err := b.UpdateStatus(context.Background(), "t-1", "closed", "")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		want := []string{"update", "t-1", "--status", "closed"}
		if !slicesEqual(mock.Calls[0].Args, want) {
			t.Errorf("args = %v, want %v", mock.Calls[0].Args, want)
		}
	})
}

func TestBdBackend_UpdateExternalRef(t *testing.T) {
	mock := &MockBDRunner{Result: CommandResult{}}
	b := newTestBackend(mock)

	err := b.UpdateExternalRef(context.Background(), "t-1", "GH-123")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := []string{"update", "t-1", "--external-ref", "GH-123"}
	if !slicesEqual(mock.Calls[0].Args, want) {
		t.Errorf("args = %v, want %v", mock.Calls[0].Args, want)
	}
}

func TestBdBackend_CloseIssue(t *testing.T) {
	t.Run("with reason", func(t *testing.T) {
		mock := &MockBDRunner{Result: CommandResult{}}
		b := newTestBackend(mock)

		err := b.CloseIssue(context.Background(), "t-1", "done")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		want := []string{"close", "t-1", "--reason", "done"}
		if !slicesEqual(mock.Calls[0].Args, want) {
			t.Errorf("args = %v, want %v", mock.Calls[0].Args, want)
		}
	})

	t.Run("without reason", func(t *testing.T) {
		mock := &MockBDRunner{Result: CommandResult{}}
		b := newTestBackend(mock)

		err := b.CloseIssue(context.Background(), "t-1", "")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		want := []string{"close", "t-1"}
		if !slicesEqual(mock.Calls[0].Args, want) {
			t.Errorf("args = %v, want %v", mock.Calls[0].Args, want)
		}
	})
}

func TestBdBackend_SyncStatus(t *testing.T) {
	mock := &MockBDRunner{Result: CommandResult{Stdout: "  synced: true\n  "}}
	b := newTestBackend(mock)

	got, err := b.SyncStatus(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "synced: true" {
		t.Errorf("got %q, want %q", got, "synced: true")
	}
	if !slicesEqual(mock.Calls[0].Args, []string{"sync", "--status"}) {
		t.Errorf("args = %v", mock.Calls[0].Args)
	}
}

func TestBdBackend_BackendName(t *testing.T) {
	b := newTestBackend(&MockBDRunner{})
	if got := b.BackendName(); got != "beads" {
		t.Errorf("BackendName() = %q, want %q", got, "beads")
	}
}

func TestBdBackend_ErrorWrapping(t *testing.T) {
	ctx := context.Background()

	tests := []struct {
		name string
		fn   func(b *bdBackend) error
		want string
	}{
		{"Ready", func(b *bdBackend) error { _, e := b.Ready(ctx, ReadyOpts{}); return e }, "bd ready: command not found"},
		{"List", func(b *bdBackend) error { _, e := b.List(ctx, ListOpts{}); return e }, "bd list: command not found"},
		{"Blocked", func(b *bdBackend) error { _, e := b.Blocked(ctx); return e }, "bd blocked: command not found"},
		{"Stats", func(b *bdBackend) error { _, e := b.Stats(ctx); return e }, "bd stats: command not found"},
		{"GetIssue", func(b *bdBackend) error { _, e := b.GetIssue(ctx, "x"); return e }, "bd show x: command not found"},
		{"GetIssueText", func(b *bdBackend) error { _, e := b.GetIssueText(ctx, "x"); return e }, "bd show x: command not found"},
		{"UpdateStatus", func(b *bdBackend) error { return b.UpdateStatus(ctx, "x", "open", "") }, "bd update x: command not found"},
		{"UpdateExternalRef", func(b *bdBackend) error { return b.UpdateExternalRef(ctx, "x", "ref") }, "bd update x --external-ref: command not found"},
		{"CloseIssue", func(b *bdBackend) error { return b.CloseIssue(ctx, "x", "") }, "bd close x: command not found"},
		{"SyncStatus", func(b *bdBackend) error { _, e := b.SyncStatus(ctx); return e }, "bd sync: command not found"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mock := &MockBDRunner{Result: CommandResult{Err: fmt.Errorf("command not found")}}
			b := newTestBackend(mock)
			err := tt.fn(b)
			if err == nil {
				t.Fatal("expected error")
			}
			if got := err.Error(); got != tt.want {
				t.Errorf("error = %q, want %q", got, tt.want)
			}
		})
	}
}
