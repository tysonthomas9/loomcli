package workspacemgr

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/infra/memstore"
	"github.com/tysonthomas9/loomcli/internal/loomgit"
	loomworkspace "github.com/tysonthomas9/loomcli/internal/loomgit/workspace"
	"github.com/tysonthomas9/loomcli/internal/store"
	"github.com/tysonthomas9/loomcli/internal/webui/service"
)

func TestP18WorkspaceCreationRecordsTrunkAndLeadBranch(t *testing.T) {
	root := t.TempDir()
	t.Setenv("LOOM_CONFIG_DIR", filepath.Join(root, "config"))
	sources := make([]string, 0, 2)
	remoteHeads := map[string]string{}
	for _, name := range []string{"api", "web"} {
		src := initTestGitRepo(t, root, name)
		remote := filepath.Join(root, name+".git")
		if err := os.Mkdir(remote, 0o755); err != nil {
			t.Fatal(err)
		}
		runGit(t, remote, "init", "--bare")
		runGit(t, src, "remote", "add", "origin", remote)
		runGit(t, src, "push", "-u", "origin", "main")
		remoteHeads[name] = strings.TrimSpace(gitOutput(t, src, "rev-parse", "origin/main"))
		if err := os.WriteFile(filepath.Join(src, "later.txt"), []byte("local only"), 0o644); err != nil {
			t.Fatal(err)
		}
		runGit(t, src, "add", "later.txt")
		runGit(t, src, "commit", "-m", "local later")
		sources = append(sources, src)
	}
	st := memstore.New()
	wsDir := filepath.Join(root, "ws1")
	_, err := BuildStoreBackedCreateWorkspace(st)(context.Background(), service.WorkspaceCreateRequest{Name: "ws1", Type: "empty", Repos: sources, Branch: "main", Path: wsDir})
	if err != nil {
		t.Fatal(err)
	}
	records, err := loomworkspace.Records(context.Background(), "WS1")
	if err != nil || len(records) != 2 {
		t.Fatalf("records=%v err=%v", records, err)
	}
	for _, record := range records {
		if record.Trunk != "main" || record.WorkspaceBranch != "loom/ws/WS1/interactive/lead" || record.BaseSHA != remoteHeads[record.Repo] {
			t.Fatalf("record=%+v remote tip=%s", record, remoteHeads[record.Repo])
		}
		checkout := filepath.Join(wsDir, record.Repo)
		if got := strings.TrimSpace(gitOutput(t, checkout, "branch", "--show-current")); got != record.WorkspaceBranch {
			t.Fatalf("checkout branch=%q", got)
		}
		if got := strings.TrimSpace(gitOutput(t, checkout, "rev-parse", "HEAD")); got != remoteHeads[record.Repo] {
			t.Fatalf("checkout HEAD=%s remote tip=%s", got, remoteHeads[record.Repo])
		}
		row, err := st.Repos().Get(context.Background(), "WS1", record.Repo)
		if err != nil || row.DefaultBranch != "main" {
			t.Fatalf("FleetDB row=%+v err=%v", row, err)
		}
	}
}

func TestP18WorkspaceCreationPreflightAndRollback(t *testing.T) {
	for _, tc := range []struct {
		name, trunk, blockingBranch string
		wantCode                    loomgit.Code
	}{
		{name: "unresolvable base", trunk: "missing", wantCode: loomgit.BaseRefUnresolvable},
		{name: "namespace loom", trunk: "main", blockingBranch: "loom", wantCode: loomgit.RefNamespaceConflict},
		{name: "namespace loom/ws", trunk: "main", blockingBranch: "loom/ws", wantCode: loomgit.RefNamespaceConflict},
		{name: "existing working branch", trunk: "main", blockingBranch: "loom/ws/WS1/interactive/lead", wantCode: loomgit.RefNamespaceConflict},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			t.Setenv("LOOM_CONFIG_DIR", filepath.Join(root, "config"))
			api := initTestGitRepo(t, root, "api")
			web := initTestGitRepo(t, root, "web")
			if tc.blockingBranch != "" {
				runGit(t, web, "branch", tc.blockingBranch)
			}
			wsDir := filepath.Join(root, "ws1")
			st := memstore.New()
			_, err := BuildStoreBackedCreateWorkspace(st)(context.Background(), service.WorkspaceCreateRequest{Name: "ws1", Type: "empty", Repos: []string{api, web}, Branch: tc.trunk, Path: wsDir})
			if err == nil {
				t.Fatal("workspace creation succeeded")
			}
			if tc.wantCode != "" && !errors.Is(err, &loomgit.Error{Kind: tc.wantCode}) {
				t.Fatalf("error=%v, want %s", err, tc.wantCode)
			}
			if _, err := os.Stat(wsDir); !os.IsNotExist(err) {
				t.Fatalf("workspace directory remains: %v", err)
			}
			if _, err := st.Workspaces().Get(context.Background(), "WS1"); err == nil {
				t.Fatal("workspace row remains")
			}
			if rows, err := st.Repos().List(context.Background(), "WS1"); err != nil || len(rows) != 0 {
				t.Fatalf("repo rows=%v err=%v", rows, err)
			}
			if rows, err := loomworkspace.Records(context.Background(), "WS1"); err != nil || len(rows) != 0 {
				t.Fatalf("local repo records=%v err=%v", rows, err)
			}
			if _, err := os.Stat(filepath.Join(wsDir, "api")); !os.IsNotExist(err) {
				t.Fatalf("api checkout remains: %v", err)
			}
			if out := gitOutput(t, api, "branch", "--list", "loom/ws/WS1/interactive/lead"); strings.TrimSpace(out) != "" {
				t.Fatalf("api branch remains: %s", out)
			}
		})
	}
}

func TestP18WorkspaceCreationUsesRemoteOnlyRelease(t *testing.T) {
	root := t.TempDir()
	t.Setenv("LOOM_CONFIG_DIR", filepath.Join(root, "config"))
	src := initTestGitRepo(t, root, "api")
	remote := filepath.Join(root, "remote.git")
	if err := os.Mkdir(remote, 0o755); err != nil {
		t.Fatal(err)
	}
	runGit(t, remote, "init", "--bare")
	runGit(t, src, "remote", "add", "origin", remote)
	runGit(t, src, "branch", "release")
	runGit(t, src, "push", "origin", "release")
	runGit(t, src, "branch", "-D", "release")
	want := strings.TrimSpace(gitOutput(t, src, "rev-parse", "origin/release"))
	wsDir := filepath.Join(root, "ws1")
	_, err := BuildStoreBackedCreateWorkspace(memstore.New())(context.Background(), service.WorkspaceCreateRequest{Name: "ws1", Type: "empty", Repos: []string{src}, Branch: "release", Path: wsDir})
	if err != nil {
		t.Fatal(err)
	}
	checkout := filepath.Join(wsDir, "api")
	if got := strings.TrimSpace(gitOutput(t, checkout, "rev-parse", "HEAD")); got != want {
		t.Fatalf("checkout HEAD=%s, remote release=%s", got, want)
	}
	if got := strings.TrimSpace(gitOutput(t, checkout, "branch", "--show-current")); got != "loom/ws/WS1/interactive/lead" {
		t.Fatalf("checkout branch=%q", got)
	}
}

func TestP18PreV2WorkspaceIsRejectedBeforeAddRepos(t *testing.T) {
	root := t.TempDir()
	t.Setenv("LOOM_CONFIG_DIR", filepath.Join(root, "config"))
	src := initTestGitRepo(t, root, "api")
	st := memstore.New()
	if _, err := st.Workspaces().Create(context.Background(), store.WorkspaceCreate{Key: "OLD", Name: "old", DefaultBranch: "main"}); err != nil {
		t.Fatal(err)
	}
	_, err := BuildStoreBackedAddRepos(st)(context.Background(), service.WorkspaceAddReposRequest{WorkspaceID: "OLD", Repos: []string{src}})
	if !errors.Is(err, &loomgit.Error{Kind: loomgit.WorkspaceUnsupported}) || !strings.Contains(err.Error(), "created before v2, recreate it") {
		t.Fatalf("error=%v", err)
	}
	if rows, err := st.Repos().List(context.Background(), "OLD"); err != nil || len(rows) != 0 {
		t.Fatalf("repo rows=%v err=%v", rows, err)
	}
	if _, err := os.Stat(filepath.Join(root, "config", "loomgit")); !os.IsNotExist(err) {
		t.Fatalf("local store created during rejection: %v", err)
	}
}

func TestP18SecondWorktreeAddFailureRollsBackFirst(t *testing.T) {
	root := t.TempDir()
	t.Setenv("LOOM_CONFIG_DIR", filepath.Join(root, "config"))
	api := initTestGitRepo(t, root, "api")
	web := initTestGitRepo(t, root, "web")
	wsDir := filepath.Join(root, "ws1")
	blocked := filepath.Join(wsDir, "web")
	if err := os.MkdirAll(blocked, 0o755); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(blocked, "keep.txt")
	if err := os.WriteFile(marker, []byte("user file"), 0o644); err != nil {
		t.Fatal(err)
	}
	sources := []loomworkspace.Source{{Name: "api", Path: api}, {Name: "web", Path: web}}
	if _, err := loomworkspace.Ensure(context.Background(), "WS1", "main", wsDir, sources); err == nil {
		t.Fatal("second worktree add should fail")
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("blocked user path changed: %v", err)
	}
	if _, err := os.Stat(filepath.Join(wsDir, "api")); !os.IsNotExist(err) {
		t.Fatalf("api checkout remains: %v", err)
	}
	for _, src := range []string{api, web} {
		if out := strings.TrimSpace(gitOutput(t, src, "branch", "--list", "loom/ws/WS1/interactive/lead")); out != "" {
			t.Fatalf("branch remains in %s: %s", src, out)
		}
	}
	if records, err := loomworkspace.Records(context.Background(), "WS1"); err != nil || len(records) != 0 {
		t.Fatalf("records=%v err=%v", records, err)
	}
	if err := os.Remove(marker); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(blocked); err != nil {
		t.Fatal(err)
	}
	session, err := loomworkspace.Ensure(context.Background(), "WS1", "main", wsDir, sources)
	if err != nil {
		t.Fatalf("retry after rollback: %v", err)
	}
	defer func() { _ = session.Close() }()
	if err := session.Commit(context.Background()); err != nil {
		t.Fatal(err)
	}
}
