package agentwire

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/loomstore"
)

// TestWorktree: an agent's own worktree is found only in its workspace, only
// once checked out, and never after it is deleted (3.2t).
func TestWorktree(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	st, err := loomstore.Open(ctx, filepath.Join(dir, "agents.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()
	wt, base, branch := filepath.Join(dir, "worktrees", "app", "a1"), "main", "loom/agent/a1"
	add := func(id string, path *string) {
		t.Helper()
		if err := st.InsertAgent(ctx, loomstore.Agent{AgentID: id, WorkspaceID: "ws", Name: id, ProfileKey: id,
			Preset: "task", PresetVersion: "1", Mode: "persistent", InteractionMode: "interactive", RoleKind: "task",
			SpecJSON: "{}", SpecVersion: 1, OwnerKind: "user", OwnerID: "u", CreatedByKind: "user", CreatedByID: "u",
			CreateRequestID: id, Repo: "/src/app", BaseRef: &base, WorktreePath: path, Branch: &branch,
			Harness: "opencode", State: "idle"}); err != nil {
			t.Fatal(err)
		}
	}
	add("a1", &wt)
	add("a2", nil)
	a := &API{store: st}

	if _, ok := a.Worktree(ctx, "ws", "a1"); ok {
		t.Fatal("found a worktree that is not checked out")
	}
	if err := os.MkdirAll(filepath.Join(wt, ".git"), 0o750); err != nil {
		t.Fatal(err)
	}
	got, ok := a.Worktree(ctx, "ws", "a1")
	if !ok || got.Path != wt || got.Branch != branch || got.DefaultBranch != base || got.RepoName != "app" || !got.AgentAPI {
		t.Fatalf("Worktree(a1) = %+v, %v", got, ok)
	}
	for _, c := range []struct{ ws, id string }{{"other", "a1"}, {"ws", "a2"}, {"ws", "missing"}} {
		if _, ok := a.Worktree(ctx, c.ws, c.id); ok {
			t.Fatalf("Worktree(%s, %s) found one", c.ws, c.id)
		}
	}
	if err := st.Tombstone(ctx, "a1", time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, ok := a.Worktree(ctx, "ws", "a1"); ok {
		t.Fatal("found a deleted agent's worktree")
	}
}
