package workspacemgr

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/cli/config"
	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/infra/memstore"
	"github.com/tysonthomas9/loomcli/internal/loomgit"
	"github.com/tysonthomas9/loomcli/internal/loomgit/taskcopy"
	loomworkspace "github.com/tysonthomas9/loomcli/internal/loomgit/workspace"
	"github.com/tysonthomas9/loomcli/internal/store"
	"github.com/tysonthomas9/loomcli/internal/webui/service"
)

func TestReconcileJournalLandingUsesRecordedDependents(t *testing.T) {
	t.Setenv("LOOM_CONFIG_DIR", t.TempDir())
	ctx := context.Background()
	if err := taskcopy.RecordLineageBase(ctx, "W", "B", "repo", taskcopy.LineageBase{
		Change: "X", Revision: 1, SHA: "base-sha", Ref: "refs/loom/source",
	}); err != nil {
		t.Fatal(err)
	}
	options := landingOptions()
	dependents, err := options.Dependents(ctx, "W", "X")
	if err != nil || len(dependents) != 1 || dependents[0].Task != "B" || dependents[0].Repo != "repo" || options.Restack == nil || options.Predecessors == nil {
		t.Fatalf("landing adapters = %+v, %v", dependents, err)
	}
}

func TestP19CrashAfterWorktreeAddIsAdopted(t *testing.T) {
	if os.Getenv("LOOM_P19_CHILD") == "1" {
		src, wsDir := os.Getenv("LOOM_P19_SOURCE"), os.Getenv("LOOM_P19_WORKSPACE")
		_, err := loomworkspace.EnsureRequest(context.Background(), "WS1", "ws1", "request-1", "main", wsDir, []loomworkspace.Source{{Name: "app", Path: src}})
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(wsDir, "app", "unsaved.txt"), []byte("agent work"), 0o644); err != nil {
			t.Fatal(err)
		}
		os.Exit(17) // FleetDB has no row; local journal and worktree survive.
	}
	root := t.TempDir()
	t.Setenv("LOOM_CONFIG_DIR", filepath.Join(root, "config"))
	src := initTestGitRepo(t, root, "app")
	wsDir := filepath.Join(root, "workspace")
	cmd := exec.Command(os.Args[0], "-test.run=^TestP19CrashAfterWorktreeAddIsAdopted$") //nolint:norawexec // Child process exits between Git and FleetDB writes.
	cmd.Env = append(os.Environ(), "LOOM_P19_CHILD=1", "LOOM_P19_SOURCE="+src, "LOOM_P19_WORKSPACE="+wsDir)
	if err := cmd.Run(); err == nil || !strings.Contains(err.Error(), "exit status 17") {
		t.Fatalf("child crash = %v", err)
	}
	st := memstore.New()
	if err := Reconcile(context.Background(), st); err != nil {
		t.Fatal(err)
	}
	ws, err := st.Workspaces().Get(context.Background(), "WS1")
	if err != nil || ws.State != domain.WorkspaceStateReady {
		t.Fatalf("adopted workspace = %+v, %v", ws, err)
	}
	if got := strings.TrimSpace(gitOutput(t, filepath.Join(wsDir, "app"), "branch", "--show-current")); got != "loom/ws/WS1/interactive/lead" {
		t.Fatalf("adopted branch = %q", got)
	}
	if data, err := os.ReadFile(filepath.Join(wsDir, "app", "unsaved.txt")); err != nil || string(data) != "agent work" {
		t.Fatalf("unsaved work after adoption = %q, %v", data, err)
	}
	if repos, err := loomworkspace.Records(context.Background(), "WS1"); err != nil || len(repos) != 1 {
		t.Fatalf("local records = %+v, %v", repos, err)
	}
	result, err := BuildStoreBackedCreateWorkspace(st)(context.Background(), service.WorkspaceCreateRequest{Name: "ws1", Type: "empty", RequestID: "request-1"})
	if err != nil || result.WorkspaceID != "WS1" || result.WorkspacePath != wsDir {
		t.Fatalf("replay = %+v, %v", result, err)
	}
	if repos, err := st.Repos().List(context.Background(), "WS1"); err != nil || len(repos) != 1 {
		t.Fatalf("replayed repo rows = %+v, %v", repos, err)
	}
}

func TestP19MissingCheckoutAfterRowsWrittenNeedsAttention(t *testing.T) {
	root := t.TempDir()
	t.Setenv("LOOM_CONFIG_DIR", filepath.Join(root, "config"))
	src := initTestGitRepo(t, root, "app")
	wsDir := filepath.Join(root, "workspace")
	session, err := loomworkspace.EnsureRequest(context.Background(), "WS1", "ws1", "request-2", "main", wsDir, []loomworkspace.Source{{Name: "app", Path: src}})
	if err != nil {
		t.Fatal(err)
	}
	st := memstore.New()
	if _, err := st.Workspaces().Create(context.Background(), store.WorkspaceCreate{Key: "WS1", Name: "ws1", DefaultBranch: "main"}); err != nil {
		t.Fatal(err)
	}
	if err := session.RowsWritten(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := session.Close(); err != nil {
		t.Fatal(err)
	}
	runGit(t, src, "worktree", "remove", filepath.Join(wsDir, "app"))
	if err := Reconcile(context.Background(), st); err != nil {
		t.Fatal(err)
	}
	ws, err := st.Workspaces().Get(context.Background(), "WS1")
	if err != nil || ws.State != domain.WorkspaceStateAttentionRequired {
		t.Fatalf("workspace after lost checkout = %+v, %v", ws, err)
	}
	if !strings.Contains(ws.ErrorMessage, "checkout missing") {
		t.Fatalf("attention message = %q", ws.ErrorMessage)
	}
}

func TestP19InterruptedRepoAttachmentIsAdopted(t *testing.T) {
	root := t.TempDir()
	t.Setenv("LOOM_CONFIG_DIR", filepath.Join(root, "config"))
	src := initTestGitRepo(t, root, "app")
	wsDir := filepath.Join(root, "workspace")
	if err := os.MkdirAll(wsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	branch := "loom/ws/WS1/interactive/lead"
	checkout := filepath.Join(wsDir, "app")
	runGit(t, src, "worktree", "add", checkout, "-b", branch, "HEAD")
	base := strings.TrimSpace(gitOutput(t, checkout, "rev-parse", "HEAD"))
	session, err := loomworkspace.BeginAttach(context.Background(), "WS1", wsDir, []loomgit.WorkspaceRepo{{Workspace: "WS1", Repo: "app", Trunk: "main", WorkspaceBranch: branch, BaseSHA: base}})
	if err != nil {
		t.Fatal(err)
	}
	if err := session.Close(); err != nil {
		t.Fatal(err)
	}
	st := memstore.New()
	if _, err := st.Workspaces().Create(context.Background(), store.WorkspaceCreate{Key: "WS1", Name: "ws1", DefaultBranch: "main"}); err != nil {
		t.Fatal(err)
	}
	if err := Reconcile(context.Background(), st); err != nil {
		t.Fatal(err)
	}
	if repos, err := st.Repos().List(context.Background(), "WS1"); err != nil || len(repos) != 1 {
		t.Fatalf("attached repos = %+v, %v", repos, err)
	}
	if repos, err := loomworkspace.Records(context.Background(), "WS1"); err != nil || len(repos) != 1 {
		t.Fatalf("local attached records = %+v, %v", repos, err)
	}
}

func TestP19DeletedWorkspaceDiscardsInterruptedAttachment(t *testing.T) {
	root := t.TempDir()
	t.Setenv("LOOM_CONFIG_DIR", filepath.Join(root, "config"))
	src := initTestGitRepo(t, root, "app")
	wsDir := filepath.Join(root, "workspace")
	if err := os.MkdirAll(wsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	branch := "loom/ws/WS1/interactive/lead"
	checkout := filepath.Join(wsDir, "app")
	runGit(t, src, "worktree", "add", checkout, "-b", branch, "HEAD")
	base := strings.TrimSpace(gitOutput(t, checkout, "rev-parse", "HEAD"))
	session, err := loomworkspace.BeginAttach(context.Background(), "WS1", wsDir, []loomgit.WorkspaceRepo{{Workspace: "WS1", Repo: "app", Trunk: "main", WorkspaceBranch: branch, BaseSHA: base}})
	if err != nil {
		t.Fatal(err)
	}
	if err := session.Close(); err != nil {
		t.Fatal(err)
	}
	runGit(t, src, "worktree", "remove", checkout)
	st := memstore.New()
	if err := Reconcile(context.Background(), st); err != nil {
		t.Fatalf("deleted workspace should not block serve: %v", err)
	}
	open, err := loomworkspace.OpenCreations(context.Background())
	if err != nil || len(open) != 0 {
		t.Fatalf("orphan attachment journals = %d, err = %v", len(open), err)
	}
}

func TestP19ReconcileContinuesAfterMultipleFailedAttachments(t *testing.T) {
	root := t.TempDir()
	t.Setenv("LOOM_CONFIG_DIR", filepath.Join(root, "config"))
	src := initTestGitRepo(t, root, "app")
	for _, key := range []string{"BAD1", "BAD2"} {
		wsDir := filepath.Join(root, key)
		if err := os.MkdirAll(wsDir, 0o755); err != nil {
			t.Fatal(err)
		}
		branch := "loom/ws/" + key + "/interactive/lead"
		checkout := filepath.Join(wsDir, "app")
		runGit(t, src, "worktree", "add", checkout, "-b", branch, "HEAD")
		base := strings.TrimSpace(gitOutput(t, checkout, "rev-parse", "HEAD"))
		session, err := loomworkspace.BeginAttach(context.Background(), key, wsDir, []loomgit.WorkspaceRepo{{Workspace: key, Repo: "app", Trunk: "main", WorkspaceBranch: branch, BaseSHA: base}})
		if err != nil {
			t.Fatal(err)
		}
		if err := session.Close(); err != nil {
			t.Fatal(err)
		}
	}
	good, err := loomworkspace.EnsureRequest(context.Background(), "GOOD", "good", "good-request", "main", filepath.Join(root, "GOOD"), []loomworkspace.Source{{Name: "app", Path: src}})
	if err != nil {
		t.Fatal(err)
	}
	if err := good.Close(); err != nil {
		t.Fatal(err)
	}
	st := memstore.New()
	err = Reconcile(context.Background(), st)
	if err == nil || !strings.Contains(err.Error(), "BAD1") || !strings.Contains(err.Error(), "BAD2") {
		t.Fatalf("aggregate failures = %v", err)
	}
	ws, err := st.Workspaces().Get(context.Background(), "GOOD")
	if err != nil || ws.State != domain.WorkspaceStateReady {
		t.Fatalf("later workspace was not recovered: %+v, %v", ws, err)
	}
}

func TestP19InterruptedCloneKeepsJournalAndRequestsAttention(t *testing.T) {
	root := t.TempDir()
	t.Setenv("LOOM_CONFIG_DIR", filepath.Join(root, "config"))
	session, err := loomworkspace.BeginCloneRequest(context.Background(), "WS1", "ws1", "request-clone", "main", filepath.Join(root, "workspace"))
	if err != nil {
		t.Fatal(err)
	}
	if err := session.Close(); err != nil {
		t.Fatal(err)
	}
	st := memstore.New()
	if _, err := st.Workspaces().Create(context.Background(), store.WorkspaceCreate{Key: "WS1", Name: "ws1", DefaultBranch: "main"}); err != nil {
		t.Fatal(err)
	}
	if err := Reconcile(context.Background(), st); err != nil {
		t.Fatal(err)
	}
	ws, err := st.Workspaces().Get(context.Background(), "WS1")
	if err != nil || ws.State != domain.WorkspaceStateAttentionRequired {
		t.Fatalf("interrupted clone = %+v, %v", ws, err)
	}
	if _, replay, err := loomworkspace.ReplayResult(context.Background(), "WS1", "request-clone"); err != nil || replay {
		t.Fatalf("incomplete clone replay=%v err=%v", replay, err)
	}
}

func TestP19ProductionDefaultJournalLocation(t *testing.T) {
	root, err := os.MkdirTemp("/tmp", "p19-default-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	t.Setenv("HOME", root)
	t.Setenv("LOOM_CONFIG_DIR", "")
	src := os.Getenv("LOOM_P19_REAL_REPO")
	branch := "v5"
	if src == "" {
		src = initTestGitRepo(t, root, "app")
		branch = "main"
	}
	wsDir := filepath.Join(root, "workspace")
	st := memstore.New()
	result, err := BuildStoreBackedCreateWorkspace(st)(context.Background(), service.WorkspaceCreateRequest{Name: "p19-default-probe", Type: "empty", Repos: []string{src}, Branch: branch, Path: wsDir, RequestID: "default-path"})
	if err != nil {
		t.Fatal(err)
	}
	if result.WorkspaceID != "P19-DEFAULT-PROBE" {
		t.Fatalf("workspace ID = %q", result.WorkspaceID)
	}
	if _, err := os.Stat(filepath.Join(config.GetConfigDir(), "loomgit", "store.db")); err != nil {
		t.Fatalf("default journal location: %v", err)
	}
}
