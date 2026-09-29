package workspace

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/cli/config"
	"github.com/tysonthomas9/loomcli/internal/loomgit/internal/journal"
)

func gitDeleteTest(t *testing.T, dir string, args ...string) string {
	t.Helper()
	//nolint:norawexec // A real scratch repository is the contract under test.
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v: %s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func deleteFixture(t *testing.T) (config.WorkspaceConfig, string, string) {
	t.Helper()
	configDir := t.TempDir()
	t.Setenv("LOOM_CONFIG_DIR", configDir)
	root := t.TempDir()
	source := filepath.Join(root, "source")
	if err := os.Mkdir(source, 0700); err != nil {
		t.Fatal(err)
	}
	gitDeleteTest(t, source, "init", "-b", "main")
	gitDeleteTest(t, source, "config", "user.name", "Tester")
	gitDeleteTest(t, source, "config", "user.email", "tester@example.test")
	if err := os.WriteFile(filepath.Join(source, "tracked.txt"), []byte("base\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, ".gitignore"), []byte("ignored.bin\n"), 0600); err != nil {
		t.Fatal(err)
	}
	gitDeleteTest(t, source, "add", ".")
	gitDeleteTest(t, source, "commit", "-m", "base")
	wsDir := filepath.Join(root, "workspace")
	if err := os.Mkdir(wsDir, 0700); err != nil {
		t.Fatal(err)
	}
	copy := filepath.Join(wsDir, "repo")
	gitDeleteTest(t, source, "worktree", "add", "-b", "loom/ws/TEST/interactive/lead", copy)
	if err := os.MkdirAll(filepath.Join(configDir, "loomgit"), 0700); err != nil {
		t.Fatal(err)
	}
	st, err := journal.OpenSQLite(filepath.Join(configDir, "loomgit", "store.db"))
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	return config.WorkspaceConfig{ID: "TEST", Path: wsDir, Repos: []config.RepoConfig{{Name: "repo", Path: copy}}}, source, copy
}

func TestDeleteWorkspaceRequiresExactPreviewAndRemovesCapturedWorktree(t *testing.T) {
	ws, source, copy := deleteFixture(t)
	ctx := context.Background()
	if err := os.WriteFile(filepath.Join(copy, "tracked.txt"), []byte("edited\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(copy, "ignored.bin"), []byte("12345"), 0600); err != nil {
		t.Fatal(err)
	}
	preview, err := DryRun(ctx, ws)
	if err != nil {
		t.Fatal(err)
	}
	var ignored bool
	for _, item := range preview.Items {
		if item.Kind == "ignored" && item.Size == 5 {
			ignored = true
		}
	}
	if !ignored {
		t.Fatalf("ignored file missing from preview: %+v", preview.Items)
	}
	deleted := false
	deleteRows := func(context.Context, string) error { deleted = true; return nil }
	var unsaved *ErrUnsavedWork
	if err := DeleteWorkspace(ctx, ws, "", deleteRows); !errors.As(err, &unsaved) {
		t.Fatalf("missing confirmation: %v", err)
	}
	if deleted {
		t.Fatal("rows deleted without confirmation")
	}
	if err := os.WriteFile(filepath.Join(copy, "new.txt"), []byte("new"), 0600); err != nil {
		t.Fatal(err)
	}
	gitDeleteTest(t, copy, "add", "new.txt")
	gitDeleteTest(t, copy, "commit", "-m", "new unpushed commit")
	if err := DeleteWorkspace(ctx, ws, preview.Fingerprint, deleteRows); !errors.As(err, &unsaved) {
		t.Fatalf("stale confirmation: %v", err)
	}
	current, err := DryRun(ctx, ws)
	if err != nil {
		t.Fatal(err)
	}
	if err := DeleteWorkspace(ctx, ws, current.Fingerprint, deleteRows); err != nil {
		t.Fatal(err)
	}
	if !deleted {
		t.Fatal("rows were not deleted")
	}
	if _, err := os.Stat(copy); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("worktree still exists: %v", err)
	}
	if strings.Contains(gitDeleteTest(t, source, "worktree", "list", "--porcelain"), copy) {
		t.Fatal("worktree remains registered")
	}
}

func TestDeleteWorkspaceKeepsIncompleteCaptureAndRowsButRemovesOtherCopies(t *testing.T) {
	ws, source, first := deleteFixture(t)
	second := filepath.Join(ws.Path, "repo2")
	gitDeleteTest(t, source, "worktree", "add", "-b", "loom/ws/TEST/task/second", second)
	ws.Repos = append(ws.Repos, config.RepoConfig{Name: "repo2", Path: second})
	if err := os.WriteFile(filepath.Join(first, "server.pem"), []byte("private"), 0600); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	preview, err := DryRun(ctx, ws)
	if err != nil {
		t.Fatal(err)
	}
	deleted := false
	err = DeleteWorkspace(ctx, ws, preview.Fingerprint, func(context.Context, string) error { deleted = true; return nil })
	if err == nil || !strings.Contains(err.Error(), "incomplete capture") {
		t.Fatalf("incomplete capture err = %v", err)
	}
	if deleted {
		t.Fatal("workspace rows deleted despite incomplete capture")
	}
	if _, err := os.Stat(first); err != nil {
		t.Fatalf("secret worktree removed: %v", err)
	}
	if _, err := os.Stat(second); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("other worktree not removed: %v", err)
	}
}

func TestDeleteWorkspaceListsCloneBranchAndPreservesBundle(t *testing.T) {
	_, source, _ := deleteFixture(t)
	wsDir := t.TempDir()
	clone := filepath.Join(wsDir, "clone")
	gitDeleteTest(t, source, "clone", source, clone)
	gitDeleteTest(t, clone, "config", "user.name", "Tester")
	gitDeleteTest(t, clone, "config", "user.email", "tester@example.test")
	gitDeleteTest(t, clone, "checkout", "-b", "local-only")
	if err := os.WriteFile(filepath.Join(clone, "tracked.txt"), []byte("local\n"), 0600); err != nil {
		t.Fatal(err)
	}
	gitDeleteTest(t, clone, "add", "tracked.txt")
	gitDeleteTest(t, clone, "commit", "-m", "local work")
	ws := config.WorkspaceConfig{ID: "CLONE", Path: wsDir, Repos: []config.RepoConfig{{Name: "clone", Path: clone}}}
	preview, err := DryRun(context.Background(), ws)
	if err != nil {
		t.Fatal(err)
	}
	listed := false
	for _, item := range preview.Items {
		if item.Kind == "clone_branch" && strings.Contains(item.Detail, "local-only") {
			listed = true
		}
	}
	if !listed {
		t.Fatalf("clone branch absent: %+v", preview.Items)
	}
	if err := DeleteWorkspace(context.Background(), ws, preview.Fingerprint, func(context.Context, string) error { return nil }); err != nil {
		t.Fatal(err)
	}
	bundle := filepath.Join(config.GetConfigDir(), "loomgit", "deletion-captures", "CLONE", "clone.bundle")
	if _, err := os.Stat(bundle); err != nil {
		t.Fatalf("clone bundle absent: %v", err)
	}
	if _, err := os.Stat(clone); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("clone still exists: %v", err)
	}
}

func TestDeleteWorkspaceRealRepositoryProbe(t *testing.T) {
	repo := os.Getenv("LOOM_P17_REAL_REPO")
	if repo == "" {
		t.Skip("set LOOM_P17_REAL_REPO for the real-repository probe")
	}
	root, err := os.MkdirTemp("/tmp", "loomgit-p17-real-*")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	configDir := filepath.Join(root, "config")
	if err := os.MkdirAll(filepath.Join(configDir, "loomgit"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("LOOM_CONFIG_DIR", configDir)
	source := filepath.Join(root, "source")
	gitDeleteTest(t, repo, "clone", "--local", repo, source)
	wsDir := filepath.Join(root, "workspace")
	if err := os.Mkdir(wsDir, 0700); err != nil {
		t.Fatal(err)
	}
	copy := filepath.Join(wsDir, "repo")
	gitDeleteTest(t, source, "worktree", "add", "-b", "loom/ws/REALPROBE/interactive/lead", copy)
	f, err := os.OpenFile(filepath.Join(copy, "README.md"), os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString("\nP1.7 deletion probe\n"); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	t.Logf("scratch diff stat: %s", gitDeleteTest(t, copy, "diff", "--stat"))
	ws := config.WorkspaceConfig{ID: "REALPROBE", Path: wsDir, Repos: []config.RepoConfig{{Name: "repo", Path: copy}}}
	preview, err := DryRun(context.Background(), ws)
	if err != nil {
		t.Fatal(err)
	}
	if err := DeleteWorkspace(context.Background(), ws, preview.Fingerprint, func(context.Context, string) error { return nil }); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(copy); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("scratch copy remains: %v", err)
	}
}
