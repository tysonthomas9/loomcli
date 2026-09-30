package driver

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/bootstrap"
	"github.com/tysonthomas9/loomcli/internal/domain"
	loomworkspace "github.com/tysonthomas9/loomcli/internal/loomgit/workspace"
	"github.com/tysonthomas9/loomcli/internal/stackstore"
	"github.com/tysonthomas9/loomcli/internal/store"
)

func TestDelegatedTaskBasesOnLeadTipWithUnresolvedLineage(t *testing.T) {
	f := setupLineageFixture(t)
	f.resolver.Lineage = unresolvedLineageLookup(t)
	ctx := context.Background()
	local, err := bootstrap.LoadStateCache()
	if err != nil {
		t.Fatal(err)
	}
	source := local.Workspaces["TEST"].Repos["app"]
	wsDir := filepath.Join(t.TempDir(), "lead-workspace")
	session, err := loomworkspace.Ensure(ctx, "TEST", "main", wsDir, []loomworkspace.Source{{Name: "app", Path: source}})
	if err != nil {
		t.Fatal(err)
	}
	if err := session.RowsWritten(ctx); err != nil {
		t.Fatal(err)
	}
	if err := session.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if err := session.Close(); err != nil {
		t.Fatal(err)
	}
	areas, err := loomworkspace.EnsureWorkingArea(ctx, "TEST", "L1", wsDir, []loomworkspace.WorkingAreaSource{{Name: "app", Path: source}})
	if err != nil {
		t.Fatal(err)
	}
	leadPath := areas[0].Path
	writeTestFile(t, filepath.Join(leadPath, "lead.txt"), "lead\n")
	gitCmd(t, leadPath, "add", "lead.txt")
	gitCmd(t, leadPath, "commit", "-m", "lead")
	leadTip := strings.TrimSpace(testGitOutput(t, leadPath, "rev-parse", "HEAD"))
	if _, err := f.resolver.Store.AgentSessions().Create(ctx, store.AgentSessionCreate{WorkspaceKey: "TEST", SessionID: "lead-session", AgentID: "L1", Kind: domain.AgentSessionKindOrchestration}); err != nil {
		t.Fatal(err)
	}
	request := TaskExecRequest{WorkspaceKey: "TEST", TaskRunID: "task/own", TaskID: "independent", ParentSessionID: "lead-session", SandboxPlacement: domain.TaskRunPlacement{RepoRef: "frontend"}}
	request.Input, err = WithLineage(request.Input, TaskLineage{StackID: "unreadable", BaseRef: "missing"})
	if err != nil {
		t.Fatal(err)
	}
	copy, err := f.resolver.ResolveTaskWorktree(ctx, request, t.TempDir())
	if err != nil || copy.BaseSHA != leadTip {
		t.Fatalf("independent base = %s, want %s: %v", copy.BaseSHA, leadTip, err)
	}
}

func unresolvedLineageLookup(t *testing.T) TaskLineageLookup {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "stacks.json"), []byte("invalid json"), 0o600); err != nil {
		t.Fatal(err)
	}
	return StackLineageLookup{Store: stackstore.New(dir)}
}
