package workspacemgr

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/bootstrap"
	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/gitauth"
	"github.com/tysonthomas9/loomcli/internal/gitbranch"
	"github.com/tysonthomas9/loomcli/internal/infra/memstore"
	"github.com/tysonthomas9/loomcli/internal/store"
	"github.com/tysonthomas9/loomcli/internal/webui/service"
	"github.com/tysonthomas9/loomcli/internal/workspaceerrors"
)

type recordingGitCredentialSource struct {
	remotes []string
}

func (s *recordingGitCredentialSource) Resolve(_ context.Context, remoteURL string) (*gitauth.Credential, error) {
	s.remotes = append(s.remotes, remoteURL)
	return nil, nil
}

func TestStoreBackedCreateEmptyWorkspaceCreatesStoreAndLocalState(t *testing.T) {
	loomDir := t.TempDir()
	t.Setenv("LOOM_CONFIG_DIR", loomDir)

	src := initTestGitRepo(t, t.TempDir(), "app")
	st := memstore.New()
	createFn := BuildStoreBackedCreateWorkspace(st)
	wsPath := filepath.Join(loomDir, "workspaces", "my-ws")

	result, err := createFn(context.Background(), service.WorkspaceCreateRequest{
		Name:   "my-ws",
		Type:   "empty",
		Repos:  []string{src},
		Branch: "feature-work",
		Path:   wsPath,
	})
	if err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	if result.WorkspaceID != "MY-WS" {
		t.Fatalf("WorkspaceID = %q, want MY-WS", result.WorkspaceID)
	}
	if result.WorkspacePath != wsPath {
		t.Fatalf("WorkspacePath = %q, want %q", result.WorkspacePath, wsPath)
	}
	if _, err := os.Stat(filepath.Join(wsPath, "app", ".git")); err != nil {
		t.Fatalf("worktree not created: %v", err)
	}

	ws, err := st.Workspaces().Get(context.Background(), "MY-WS")
	if err != nil {
		t.Fatalf("workspace not stored: %v", err)
	}
	if ws.Name != "my-ws" {
		t.Fatalf("workspace name = %q, want my-ws", ws.Name)
	}
	repos, err := st.Repos().List(context.Background(), "MY-WS")
	if err != nil {
		t.Fatalf("list repos: %v", err)
	}
	if len(repos) != 1 || repos[0].Name != "app" {
		t.Fatalf("repos = %#v, want app", repos)
	}
	roles, err := st.Roles().List(context.Background(), "MY-WS")
	if err != nil {
		t.Fatalf("list roles: %v", err)
	}
	if len(roles) != 3 || !hasRole(roles, "plan") || !hasRole(roles, "task") || !hasRole(roles, "lead") {
		t.Fatalf("roles = %#v, want plan, task, and lead", roles)
	}
	roleByName := rolesByName(roles)
	if roleByName["plan"].TaskFilter != "needs_plan" {
		t.Fatalf("plan task filter = %q, want needs_plan", roleByName["plan"].TaskFilter)
	}
	if roleByName["task"].TaskFilter != "has_design" {
		t.Fatalf("task task filter = %q, want has_design", roleByName["task"].TaskFilter)
	}
	if roleByName["lead"].Kind != domain.RoleKindInteractive {
		t.Fatalf("lead kind = %q, want interactive", roleByName["lead"].Kind)
	}

	sc, err := bootstrap.LoadStateCache()
	if err != nil {
		t.Fatalf("load state cache: %v", err)
	}
	if sc.LastWorkspace != "MY-WS" {
		t.Fatalf("LastWorkspace = %q, want MY-WS", sc.LastWorkspace)
	}
	local := sc.Workspaces["MY-WS"]
	if local.Path != wsPath {
		t.Fatalf("local path = %q, want %q", local.Path, wsPath)
	}
	if local.Repos["app"] != filepath.Join(wsPath, "app") {
		t.Fatalf("local repo path = %q", local.Repos["app"])
	}
}

func TestStoreBackedCreateEmptyWorkspaceAllowsExternalEmptyPath(t *testing.T) {
	loomDir := t.TempDir()
	t.Setenv("LOOM_CONFIG_DIR", loomDir)

	externalPath := filepath.Join(t.TempDir(), "picked-workspace")
	if err := os.MkdirAll(externalPath, 0755); err != nil {
		t.Fatalf("mkdir external path: %v", err)
	}

	st := memstore.New()
	createFn := BuildStoreBackedCreateWorkspace(st)

	result, err := createFn(context.Background(), service.WorkspaceCreateRequest{
		Name: "external-ws",
		Type: "empty",
		Path: externalPath,
	})
	if err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	if result.WorkspaceID != "EXTERNAL-WS" || result.WorkspacePath != externalPath {
		t.Fatalf("result = %#v, want EXTERNAL-WS at %s", result, externalPath)
	}

	sc, err := bootstrap.LoadStateCache()
	if err != nil {
		t.Fatalf("load state cache: %v", err)
	}
	if sc.Workspaces["EXTERNAL-WS"].Path != externalPath {
		t.Fatalf("local path = %q, want %q", sc.Workspaces["EXTERNAL-WS"].Path, externalPath)
	}
}

func TestStoreBackedCreateWorkspaceRejectsExternalNonEmptyPath(t *testing.T) {
	loomDir := t.TempDir()
	t.Setenv("LOOM_CONFIG_DIR", loomDir)

	externalPath := filepath.Join(t.TempDir(), "documents")
	if err := os.MkdirAll(externalPath, 0755); err != nil {
		t.Fatalf("mkdir external path: %v", err)
	}
	if err := os.WriteFile(filepath.Join(externalPath, "keep.txt"), []byte("do not remove\n"), 0644); err != nil {
		t.Fatalf("write external file: %v", err)
	}

	st := memstore.New()
	createFn := BuildStoreBackedCreateWorkspace(st)

	_, err := createFn(context.Background(), service.WorkspaceCreateRequest{
		Name: "external-ws",
		Type: "empty",
		Path: externalPath,
	})
	if err == nil {
		t.Fatal("create workspace succeeded, want non-empty path validation error")
	}
	if _, statErr := os.Stat(filepath.Join(externalPath, "keep.txt")); statErr != nil {
		t.Fatalf("non-empty external path was modified, stat err=%v", statErr)
	}
}

func TestAddWorktreesRecoversCorruptBranchRef(t *testing.T) {
	src := initTestGitRepo(t, t.TempDir(), "app")
	baseBranch := strings.TrimSpace(gitOutput(t, src, "branch", "--show-current"))
	runGit(t, src, "checkout", "-b", "local-coder")
	if err := os.WriteFile(filepath.Join(src, "agent.txt"), []byte("agent\n"), 0o644); err != nil {
		t.Fatalf("write agent file: %v", err)
	}
	runGit(t, src, "add", "agent.txt")
	runGit(t, src, "commit", "-m", "agent")
	agentSHA := strings.TrimSpace(gitOutput(t, src, "rev-parse", "HEAD"))
	runGit(t, src, "checkout", baseBranch)
	corruptWorkspaceBranchRef(t, src, "local-coder")

	wsDir := filepath.Join(t.TempDir(), "workspace")
	ctx := service.WithCreateWarnings(context.Background())
	created, repos, err := addWorktrees(ctx, []resolvedRepo{{path: src, name: "app"}}, wsDir, "local-coder")
	if err != nil {
		t.Fatalf("addWorktrees: %v", err)
	}
	if len(created) != 1 || len(repos) != 1 {
		t.Fatalf("created=%d repos=%d, want one each", len(created), len(repos))
	}
	if warnings := service.GetCreateWarnings(ctx); len(warnings) != 0 {
		t.Fatalf("warnings = %v, want none", warnings)
	}
	if got := strings.TrimSpace(gitOutput(t, filepath.Join(wsDir, "app"), "rev-parse", "HEAD")); got != agentSHA {
		t.Fatalf("worktree HEAD = %s, want recovered reflog SHA %s", got, agentSHA)
	}
}

func TestAddWorktreesSkipsUnrecoverableCheckoutWithWarning(t *testing.T) {
	src := initTestGitRepo(t, t.TempDir(), "app")
	wsDir := filepath.Join(t.TempDir(), "workspace")
	blockedPath := filepath.Join(wsDir, "app")
	if err := os.MkdirAll(blockedPath, 0o755); err != nil {
		t.Fatalf("mkdir blocked checkout path: %v", err)
	}
	if err := os.WriteFile(filepath.Join(blockedPath, "not-a-checkout.txt"), []byte("blocked\n"), 0o644); err != nil {
		t.Fatalf("write blocked checkout marker: %v", err)
	}

	ctx := service.WithCreateWarnings(context.Background())
	created, repos, err := addWorktrees(ctx, []resolvedRepo{{path: src, name: "app"}}, wsDir, "local-coder")
	if err != nil {
		t.Fatalf("addWorktrees returned fatal error: %v", err)
	}
	if len(created) != 0 {
		t.Fatalf("created = %v, want no created worktrees", created)
	}
	if len(repos) != 0 {
		t.Fatalf("repos = %#v, want skipped checkout omitted from runnable state", repos)
	}
	warnings := service.GetCreateWarnings(ctx)
	if len(warnings) != 1 || !strings.Contains(warnings[0], "Skipped checkout") {
		t.Fatalf("warnings = %v, want skipped checkout warning", warnings)
	}
}

func TestStoreBackedAddReposAttachesLocalRepoToEmptyWorkspace(t *testing.T) {
	loomDir := t.TempDir()
	t.Setenv("LOOM_CONFIG_DIR", loomDir)

	st := memstore.New()
	createFn := BuildStoreBackedCreateWorkspace(st)
	wsPath := filepath.Join(loomDir, "workspaces", "my-ws")

	if _, err := createFn(context.Background(), service.WorkspaceCreateRequest{
		Name: "my-ws",
		Type: "empty",
		Path: wsPath,
	}); err != nil {
		t.Fatalf("create empty workspace: %v", err)
	}

	src := initTestGitRepo(t, t.TempDir(), "api")
	addFn := BuildStoreBackedAddRepos(st)
	result, err := addFn(context.Background(), service.WorkspaceAddReposRequest{
		WorkspaceID: "MY-WS",
		Repos:       []string{src},
		Branch:      "feature-work",
	})
	if err != nil {
		t.Fatalf("add repo: %v", err)
	}
	if result.WorkspaceID != "MY-WS" || result.WorkspacePath != wsPath {
		t.Fatalf("result = %#v, want MY-WS at %s", result, wsPath)
	}
	if _, err := os.Stat(filepath.Join(wsPath, "api", ".git")); err != nil {
		t.Fatalf("worktree not created: %v", err)
	}

	repos, err := st.Repos().List(context.Background(), "MY-WS")
	if err != nil {
		t.Fatalf("list repos: %v", err)
	}
	if len(repos) != 1 || repos[0].Name != "api" || repos[0].DefaultBranch != "feature-work" {
		t.Fatalf("repos = %#v, want api on feature-work", repos)
	}

	sc, err := bootstrap.LoadStateCache()
	if err != nil {
		t.Fatalf("load state cache: %v", err)
	}
	local := sc.Workspaces["MY-WS"]
	if local.Path != wsPath {
		t.Fatalf("local path = %q, want %q", local.Path, wsPath)
	}
	if local.Repos["api"] != filepath.Join(wsPath, "api") {
		t.Fatalf("local repo path = %q", local.Repos["api"])
	}
}

func TestStoreBackedAddReposAutoDetectsLocalRepoDefaultBranch(t *testing.T) {
	loomDir := t.TempDir()
	t.Setenv("LOOM_CONFIG_DIR", loomDir)

	st := memstore.New()
	createFn := BuildStoreBackedCreateWorkspace(st)
	wsPath := filepath.Join(loomDir, "workspaces", "my-ws")
	if _, err := createFn(context.Background(), service.WorkspaceCreateRequest{
		Name: "my-ws",
		Type: "empty",
		Path: wsPath,
	}); err != nil {
		t.Fatalf("create empty workspace: %v", err)
	}

	src := initTestGitRepo(t, t.TempDir(), "api")
	runGit(t, src, "branch", "-M", "main")
	origin := filepath.Join(t.TempDir(), "origin.git")
	runGit(t, "", "init", "--bare", origin)
	// Match local-mode fixtures whose bare origin HEAD is stale while the
	// attached source checkout and origin/main correctly identify the base.
	runGit(t, origin, "symbolic-ref", "HEAD", "refs/heads/master")
	runGit(t, src, "remote", "add", "origin", origin)
	runGit(t, src, "push", "--set-upstream", "origin", "main")
	runGit(t, src, "checkout", "-b", "feature/current-work")
	addFn := BuildStoreBackedAddRepos(st)
	if _, err := addFn(context.Background(), service.WorkspaceAddReposRequest{
		WorkspaceID: "MY-WS",
		Repos:       []string{src},
	}); err != nil {
		t.Fatalf("add local repo without branch override: %v", err)
	}

	repos, err := st.Repos().List(context.Background(), "MY-WS")
	if err != nil {
		t.Fatalf("list repos: %v", err)
	}
	if len(repos) != 1 || repos[0].DefaultBranch != "main" || repos[0].RemoteURL != origin {
		t.Fatalf("repos = %#v, want detected source default branch main", repos)
	}
	if got := strings.TrimSpace(gitOutput(t, src, "branch", "--show-current")); got != "feature/current-work" {
		t.Fatalf("source checkout branch = %q, want feature/current-work preserved", got)
	}
	checkout := filepath.Join(wsPath, "api")
	if got := strings.TrimSpace(gitOutput(t, checkout, "branch", "--show-current")); got != "my-ws" {
		t.Fatalf("workspace checkout branch = %q, want isolation branch my-ws", got)
	}
}

func TestDetectRepoDefaultBranchFailsClosedForUnadvertisedNoncanonicalRemote(t *testing.T) {
	src := initTestGitRepo(t, t.TempDir(), "api")
	runGit(t, src, "branch", "-M", "develop")
	origin := filepath.Join(t.TempDir(), "origin.git")
	runGit(t, "", "init", "--bare", origin)
	runGit(t, origin, "symbolic-ref", "HEAD", "refs/heads/missing")
	runGit(t, src, "remote", "add", "origin", origin)
	runGit(t, src, "push", "--set-upstream", "origin", "develop")

	branch, err := detectRepoDefaultBranch(src)
	if err == nil || branch != "" {
		t.Fatalf("detect default branch = %q, err=%v; want fail-closed explicit-override error", branch, err)
	}
	if !strings.Contains(err.Error(), "specify one explicitly") {
		t.Fatalf("error = %q, want explicit default-branch guidance", err)
	}
}

func TestDetectRepoDefaultBranchRejectsTagThatLooksLikeRemoteMain(t *testing.T) {
	src := initTestGitRepo(t, t.TempDir(), "api")
	runGit(t, src, "branch", "-M", "develop")
	origin := filepath.Join(t.TempDir(), "origin.git")
	runGit(t, "", "init", "--bare", origin)
	runGit(t, origin, "symbolic-ref", "HEAD", "refs/heads/missing")
	runGit(t, src, "remote", "add", "origin", origin)
	runGit(t, src, "push", "--set-upstream", "origin", "develop")
	runGit(t, src, "tag", "origin/main")
	runGit(t, src, "symbolic-ref", "refs/remotes/origin/HEAD", "refs/tags/origin/main")

	branch, err := detectRepoDefaultBranch(src)
	if err == nil || branch != "" {
		t.Fatalf("detect default branch = %q, err=%v; want tag-shaped remote ref rejected", branch, err)
	}
	if !strings.Contains(err.Error(), "specify one explicitly") {
		t.Fatalf("error = %q, want explicit default-branch guidance", err)
	}
}

func TestDetectRepoDefaultBranchRejectsSymbolicRemoteMainToTag(t *testing.T) {
	src := initTestGitRepo(t, t.TempDir(), "api")
	runGit(t, src, "branch", "-M", "develop")
	origin := filepath.Join(t.TempDir(), "origin.git")
	runGit(t, "", "init", "--bare", origin)
	runGit(t, origin, "symbolic-ref", "HEAD", "refs/heads/missing")
	runGit(t, src, "remote", "add", "origin", origin)
	runGit(t, src, "push", "--set-upstream", "origin", "develop")
	runGit(t, src, "tag", "evil")
	runGit(t, src, "symbolic-ref", "refs/remotes/origin/main", "refs/tags/evil")

	branch, err := detectRepoDefaultBranch(src)
	if err == nil || branch != "" {
		t.Fatalf("detect default branch = %q, err=%v; want symbolic remote main-to-tag rejected", branch, err)
	}
	if !strings.Contains(err.Error(), "specify one explicitly") {
		t.Fatalf("error = %q, want explicit default-branch guidance", err)
	}
}

func TestDetectRepoDefaultBranchRejectsNoOriginHeadThroughBranchToTag(t *testing.T) {
	src := initTestGitRepo(t, t.TempDir(), "api")
	runGit(t, src, "branch", "-M", "main")
	runGit(t, src, "tag", "evil")
	runGit(t, src, "symbolic-ref", "refs/heads/main", "refs/tags/evil")

	branch, err := detectRepoDefaultBranch(src)
	if err == nil || branch != "" {
		t.Fatalf("detect default branch = %q, err=%v; want no-origin HEAD-to-tag rejected", branch, err)
	}
}

func TestStoreBackedAddReposAttachesCheckedOutExistingDefaultBranch(t *testing.T) {
	loomDir := t.TempDir()
	t.Setenv("LOOM_CONFIG_DIR", loomDir)

	st := memstore.New()
	createFn := BuildStoreBackedCreateWorkspace(st)
	wsPath := filepath.Join(loomDir, "workspaces", "my-ws")
	if _, err := createFn(context.Background(), service.WorkspaceCreateRequest{
		Name: "my-ws",
		Type: "empty",
		Path: wsPath,
	}); err != nil {
		t.Fatalf("create empty workspace: %v", err)
	}

	src := initTestGitRepo(t, t.TempDir(), "api")
	branch := strings.TrimSpace(gitOutput(t, src, "branch", "--show-current"))
	sourceSHA := strings.TrimSpace(gitOutput(t, src, "rev-parse", "HEAD"))
	addFn := BuildStoreBackedAddRepos(st)
	if _, err := addFn(context.Background(), service.WorkspaceAddReposRequest{
		WorkspaceID: "MY-WS",
		Repos:       []string{src},
		Branch:      branch,
	}); err != nil {
		t.Fatalf("add checked-out default branch: %v", err)
	}

	checkout := filepath.Join(wsPath, "api")
	if got := strings.TrimSpace(gitOutput(t, checkout, "rev-parse", "HEAD")); got != sourceSHA {
		t.Fatalf("workspace checkout HEAD = %s, want source branch tip %s", got, sourceSHA)
	}
	cmd := exec.Command("git", "-C", checkout, "symbolic-ref", "-q", "HEAD") //nolint:norawexec // Real Git fixture verifies detached-HEAD semantics.
	if err := cmd.Run(); err == nil {
		t.Fatal("workspace checkout unexpectedly shares the source branch; want detached HEAD")
	}
	if got := strings.TrimSpace(gitOutput(t, src, "branch", "--show-current")); got != branch {
		t.Fatalf("source checkout branch = %q, want unchanged %q", got, branch)
	}

	sc, err := bootstrap.LoadStateCache()
	if err != nil {
		t.Fatalf("load state cache: %v", err)
	}
	if got := sc.Workspaces["MY-WS"].Repos["api"]; got != checkout {
		t.Fatalf("local repo path = %q, want %q", got, checkout)
	}
}

func TestStoreBackedAddReposDoesNotPersistSkippedCheckout(t *testing.T) {
	loomDir := t.TempDir()
	t.Setenv("LOOM_CONFIG_DIR", loomDir)

	st := memstore.New()
	createFn := BuildStoreBackedCreateWorkspace(st)
	wsPath := filepath.Join(loomDir, "workspaces", "my-ws")
	if _, err := createFn(context.Background(), service.WorkspaceCreateRequest{
		Name: "my-ws",
		Type: "empty",
		Path: wsPath,
	}); err != nil {
		t.Fatalf("create empty workspace: %v", err)
	}

	src := initTestGitRepo(t, t.TempDir(), "api")
	blockedPath := filepath.Join(wsPath, "api")
	if err := os.MkdirAll(blockedPath, 0o755); err != nil {
		t.Fatalf("mkdir blocked checkout: %v", err)
	}
	if err := os.WriteFile(filepath.Join(blockedPath, "keep.txt"), []byte("keep\n"), 0o644); err != nil {
		t.Fatalf("write blocked checkout marker: %v", err)
	}

	addFn := BuildStoreBackedAddRepos(st)
	if _, err := addFn(context.Background(), service.WorkspaceAddReposRequest{
		WorkspaceID: "MY-WS",
		Repos:       []string{src},
		Branch:      "feature-work",
	}); err == nil {
		t.Fatal("add repo succeeded despite skipped checkout")
	}
	repos, err := st.Repos().List(context.Background(), "MY-WS")
	if err != nil {
		t.Fatalf("list repos: %v", err)
	}
	if len(repos) != 0 {
		t.Fatalf("persisted repos = %#v, want none", repos)
	}
	sc, err := bootstrap.LoadStateCache()
	if err != nil {
		t.Fatalf("load state cache: %v", err)
	}
	if _, ok := sc.Workspaces["MY-WS"].Repos["api"]; ok {
		t.Fatal("state cache persisted the skipped checkout")
	}
	if got, err := os.ReadFile(filepath.Join(blockedPath, "keep.txt")); err != nil || string(got) != "keep\n" {
		t.Fatalf("failed attach modified the pre-existing checkout path: contents=%q err=%v", got, err)
	}
	if out := gitOutput(t, src, "branch", "--list", "feature-work"); strings.TrimSpace(out) != "" {
		t.Fatalf("failed attach left its newly-created branch behind: %q", out)
	}
}

func TestStoreBackedAddReposRollsBackPartialLocalAttach(t *testing.T) {
	loomDir := t.TempDir()
	t.Setenv("LOOM_CONFIG_DIR", loomDir)

	st := memstore.New()
	createFn := BuildStoreBackedCreateWorkspace(st)
	wsPath := filepath.Join(loomDir, "workspaces", "my-ws")
	if _, err := createFn(context.Background(), service.WorkspaceCreateRequest{
		Name: "my-ws",
		Type: "empty",
		Path: wsPath,
	}); err != nil {
		t.Fatalf("create empty workspace: %v", err)
	}

	firstSrc := initTestGitRepo(t, t.TempDir(), "first")
	secondSrc := initTestGitRepo(t, t.TempDir(), "second")
	const branch = "feature-work"
	runGit(t, secondSrc, "branch", branch)
	secondBranchSHA := strings.TrimSpace(gitOutput(t, secondSrc, "rev-parse", branch))

	blockedPath := filepath.Join(wsPath, "second")
	if err := os.MkdirAll(blockedPath, 0o755); err != nil {
		t.Fatalf("mkdir blocked checkout: %v", err)
	}
	if err := os.WriteFile(filepath.Join(blockedPath, "keep.txt"), []byte("keep\n"), 0o644); err != nil {
		t.Fatalf("write blocked checkout marker: %v", err)
	}

	addFn := BuildStoreBackedAddRepos(st)
	if _, err := addFn(context.Background(), service.WorkspaceAddReposRequest{
		WorkspaceID: "MY-WS",
		Repos:       []string{firstSrc, secondSrc},
		Branch:      branch,
	}); err == nil {
		t.Fatal("add repos succeeded despite second checkout being blocked")
	}

	if _, err := os.Stat(filepath.Join(wsPath, "first")); !os.IsNotExist(err) {
		t.Fatalf("first checkout was not rolled back, stat err=%v", err)
	}
	if got, err := os.ReadFile(filepath.Join(blockedPath, "keep.txt")); err != nil || string(got) != "keep\n" {
		t.Fatalf("failed attach modified the blocked second path: contents=%q err=%v", got, err)
	}

	repos, err := st.Repos().List(context.Background(), "MY-WS")
	if err != nil {
		t.Fatalf("list repos: %v", err)
	}
	if len(repos) != 0 {
		t.Fatalf("persisted repos = %#v, want none", repos)
	}
	sc, err := bootstrap.LoadStateCache()
	if err != nil {
		t.Fatalf("load state cache: %v", err)
	}
	if _, ok := sc.Workspaces["MY-WS"].Repos["first"]; ok {
		t.Fatal("state cache persisted the rolled-back first checkout")
	}
	if _, ok := sc.Workspaces["MY-WS"].Repos["second"]; ok {
		t.Fatal("state cache persisted the skipped second checkout")
	}

	if out := strings.TrimSpace(gitOutput(t, firstSrc, "branch", "--list", branch)); out != "" {
		t.Fatalf("rollback left its operation-created branch behind in first repo: %q", out)
	}
	if got := strings.TrimSpace(gitOutput(t, secondSrc, "rev-parse", branch)); got != secondBranchSHA {
		t.Fatalf("pre-existing second repo branch changed or was removed: got %s, want %s", got, secondBranchSHA)
	}
}

func TestStoreBackedAddReposClassifiesLocalRepoNameCollisionAndRollsBack(t *testing.T) {
	loomDir := t.TempDir()
	t.Setenv("LOOM_CONFIG_DIR", loomDir)

	base := memstore.New()
	createFn := BuildStoreBackedCreateWorkspace(base)
	wsPath := filepath.Join(loomDir, "workspaces", "my-ws")
	if _, err := createFn(context.Background(), service.WorkspaceCreateRequest{
		Name: "my-ws",
		Type: "empty",
		Path: wsPath,
	}); err != nil {
		t.Fatalf("create empty workspace: %v", err)
	}

	src := initTestGitRepo(t, t.TempDir(), "shared-repo")
	st := &repoFailStore{Store: base, err: domain.ErrAlreadyExists}
	addFn := BuildStoreBackedAddRepos(st)
	_, err := addFn(context.Background(), service.WorkspaceAddReposRequest{
		WorkspaceID: "MY-WS",
		Repos:       []string{src},
		Branch:      "proof-work",
	})
	var createErr *workspaceerrors.CreateError
	if !errors.As(err, &createErr) || createErr.Code != workspaceerrors.AlreadyExists {
		t.Fatalf("error = %v, want AlreadyExists workspace error", err)
	}
	if !strings.Contains(createErr.Message, "repository names must be unique across workspaces") {
		t.Fatalf("error message = %q, want cross-workspace uniqueness guidance", createErr.Message)
	}

	if _, statErr := os.Stat(filepath.Join(wsPath, "shared-repo")); !os.IsNotExist(statErr) {
		t.Fatalf("failed attach left workspace checkout behind: %v", statErr)
	}
	if out := strings.TrimSpace(gitOutput(t, src, "branch", "--list", "proof-work")); out != "" {
		t.Fatalf("failed attach left operation-created branch behind: %q", out)
	}
	assertWorkspaceHasNoRepo(t, base, "MY-WS", "shared-repo")
}

func TestStoreBackedAddReposClassifiesCloneRepoNameCollisionAndRollsBack(t *testing.T) {
	loomDir := t.TempDir()
	t.Setenv("LOOM_CONFIG_DIR", loomDir)

	base := memstore.New()
	createFn := BuildStoreBackedCreateWorkspace(base)
	wsPath := filepath.Join(loomDir, "workspaces", "my-ws")
	if _, err := createFn(context.Background(), service.WorkspaceCreateRequest{
		Name: "my-ws",
		Type: "empty",
		Path: wsPath,
	}); err != nil {
		t.Fatalf("create empty workspace: %v", err)
	}

	remote := initTestGitRepo(t, t.TempDir(), "shared-clone")
	st := &repoFailStore{Store: base, err: domain.ErrAlreadyExists}
	addFn := BuildStoreBackedAddRepos(st)
	_, err := addFn(context.Background(), service.WorkspaceAddReposRequest{
		WorkspaceID: "MY-WS",
		CloneURLs:   []string{remote},
	})
	var createErr *workspaceerrors.CreateError
	if !errors.As(err, &createErr) || createErr.Code != workspaceerrors.AlreadyExists {
		t.Fatalf("error = %v, want AlreadyExists workspace error", err)
	}
	if !strings.Contains(createErr.Message, "repository names must be unique across workspaces") {
		t.Fatalf("error message = %q, want cross-workspace uniqueness guidance", createErr.Message)
	}

	if _, statErr := os.Stat(filepath.Join(wsPath, "shared-clone")); !os.IsNotExist(statErr) {
		t.Fatalf("failed clone attach left checkout behind: %v", statErr)
	}
	assertWorkspaceHasNoRepo(t, base, "MY-WS", "shared-clone")
}

func assertWorkspaceHasNoRepo(t *testing.T, st store.Store, workspace, repo string) {
	t.Helper()

	repos, err := st.Repos().List(context.Background(), workspace)
	if err != nil {
		t.Fatalf("list repos: %v", err)
	}
	if len(repos) != 0 {
		t.Fatalf("repos after rejected attach = %#v, want none", repos)
	}

	sc, err := bootstrap.LoadStateCache()
	if err != nil {
		t.Fatalf("load state cache: %v", err)
	}
	if _, ok := sc.Workspaces[workspace].Repos[repo]; ok {
		t.Fatalf("state cache persisted rejected repo %q", repo)
	}
}

func TestStoreBackedAddReposClonesRemoteRepoToEmptyWorkspace(t *testing.T) {
	loomDir := t.TempDir()
	t.Setenv("LOOM_CONFIG_DIR", loomDir)

	st := memstore.New()
	createFn := BuildStoreBackedCreateWorkspace(st)
	wsPath := filepath.Join(loomDir, "workspaces", "my-ws")

	if _, err := createFn(context.Background(), service.WorkspaceCreateRequest{
		Name: "my-ws",
		Type: "empty",
		Path: wsPath,
	}); err != nil {
		t.Fatalf("create empty workspace: %v", err)
	}

	src := initTestGitRepo(t, t.TempDir(), "Hello-World")
	sourceBranch := strings.TrimSpace(gitOutput(t, src, "branch", "--show-current"))
	credentials := &recordingGitCredentialSource{}
	addFn := BuildStoreBackedAddReposWithCredentials(st, credentials)
	result, err := addFn(context.Background(), service.WorkspaceAddReposRequest{
		WorkspaceID: "MY-WS",
		CloneURLs:   []string{src},
	})
	if err != nil {
		t.Fatalf("add clone repo: %v", err)
	}
	if len(credentials.remotes) != 0 {
		t.Fatalf("credential source remotes = %v, want none for anonymous clone success", credentials.remotes)
	}
	if result.WorkspaceID != "MY-WS" || result.WorkspacePath != wsPath {
		t.Fatalf("result = %#v, want MY-WS at %s", result, wsPath)
	}
	if _, err := os.Stat(filepath.Join(wsPath, "hello-world", ".git")); err != nil {
		t.Fatalf("clone checkout not created: %v", err)
	}

	repos, err := st.Repos().List(context.Background(), "MY-WS")
	if err != nil {
		t.Fatalf("list repos: %v", err)
	}
	if len(repos) != 1 || repos[0].Name != "hello-world" || repos[0].RemoteURL != src ||
		repos[0].SourceRepoID != "hello-world" || repos[0].DefaultBranch != sourceBranch {
		t.Fatalf("repos = %#v, want cloned hello-world repo", repos)
	}

	sc, err := bootstrap.LoadStateCache()
	if err != nil {
		t.Fatalf("load state cache: %v", err)
	}
	local := sc.Workspaces["MY-WS"]
	if local.Repos["hello-world"] != filepath.Join(wsPath, "hello-world") {
		t.Fatalf("local repo path = %q", local.Repos["hello-world"])
	}

	// The source is still wired through the store-backed path: once the
	// anonymous clone fails, credential resolution receives the exact remote.
	missingRemote := filepath.Join(t.TempDir(), "missing-private.git")
	if _, err := addFn(context.Background(), service.WorkspaceAddReposRequest{
		WorkspaceID: "MY-WS",
		CloneURLs:   []string{missingRemote},
	}); err == nil {
		t.Fatal("add missing clone repo succeeded")
	}
	if len(credentials.remotes) != 1 || credentials.remotes[0] != missingRemote {
		t.Fatalf("credential source remotes = %v, want fallback [%s]", credentials.remotes, missingRemote)
	}
}

func TestStoreBackedAddReposPersistsEachClonesDetectedDefaultBranch(t *testing.T) {
	loomDir := t.TempDir()
	t.Setenv("LOOM_CONFIG_DIR", loomDir)

	st := memstore.New()
	createFn := BuildStoreBackedCreateWorkspace(st)
	wsPath := filepath.Join(loomDir, "workspaces", "my-ws")
	if _, err := createFn(context.Background(), service.WorkspaceCreateRequest{
		Name: "my-ws",
		Type: "empty",
		Path: wsPath,
	}); err != nil {
		t.Fatalf("create empty workspace: %v", err)
	}

	alpha := initTestGitRepo(t, t.TempDir(), "alpha")
	beta := initTestGitRepo(t, t.TempDir(), "beta")
	runGit(t, alpha, "branch", "-m", "main")
	runGit(t, beta, "branch", "-m", "master")

	addFn := BuildStoreBackedAddRepos(st)
	if _, err := addFn(context.Background(), service.WorkspaceAddReposRequest{
		WorkspaceID: "MY-WS",
		CloneURLs:   []string{alpha, beta},
	}); err != nil {
		t.Fatalf("add mixed-default clones: %v", err)
	}

	repos, err := st.Repos().List(context.Background(), "MY-WS")
	if err != nil {
		t.Fatalf("list repos: %v", err)
	}
	branches := make(map[string]string, len(repos))
	for _, repo := range repos {
		branches[repo.Name] = repo.DefaultBranch
	}
	if branches["alpha"] != "main" || branches["beta"] != "master" {
		t.Fatalf("detected branches = %#v, want alpha=main beta=master", branches)
	}
}

func TestStoreBackedAddReposRejectsExplicitMissingCloneBranch(t *testing.T) {
	loomDir := t.TempDir()
	t.Setenv("LOOM_CONFIG_DIR", loomDir)

	st := memstore.New()
	createFn := BuildStoreBackedCreateWorkspace(st)
	wsPath := filepath.Join(loomDir, "workspaces", "my-ws")
	if _, err := createFn(context.Background(), service.WorkspaceCreateRequest{
		Name: "my-ws",
		Type: "empty",
		Path: wsPath,
	}); err != nil {
		t.Fatalf("create empty workspace: %v", err)
	}

	src := initTestGitRepo(t, t.TempDir(), "hello-world")
	runGit(t, src, "branch", "-m", "master")
	addFn := BuildStoreBackedAddRepos(st)
	_, err := addFn(context.Background(), service.WorkspaceAddReposRequest{
		WorkspaceID: "MY-WS",
		CloneURLs:   []string{src},
		Branch:      "main",
	})
	if err == nil || !strings.Contains(err.Error(), `default branch "main" does not exist`) {
		t.Fatalf("add clone error = %v, want missing explicit default branch", err)
	}
	if _, statErr := os.Stat(filepath.Join(wsPath, "hello-world")); !os.IsNotExist(statErr) {
		t.Fatalf("failed clone checkout was not rolled back: %v", statErr)
	}
	repos, listErr := st.Repos().List(context.Background(), "MY-WS")
	if listErr != nil || len(repos) != 0 {
		t.Fatalf("repos after rejected clone = %#v, err=%v", repos, listErr)
	}
}

func TestStoreBackedAddReposRejectsEmptyRemoteWithoutCommittedDefaultBranch(t *testing.T) {
	loomDir := t.TempDir()
	t.Setenv("LOOM_CONFIG_DIR", loomDir)

	st := memstore.New()
	createFn := BuildStoreBackedCreateWorkspace(st)
	wsPath := filepath.Join(loomDir, "workspaces", "my-ws")
	if _, err := createFn(context.Background(), service.WorkspaceCreateRequest{
		Name: "my-ws",
		Type: "empty",
		Path: wsPath,
	}); err != nil {
		t.Fatalf("create empty workspace: %v", err)
	}

	emptyRemote := filepath.Join(t.TempDir(), "empty-remote")
	if err := os.MkdirAll(emptyRemote, 0o755); err != nil {
		t.Fatalf("create empty remote: %v", err)
	}
	runGit(t, emptyRemote, "init")

	addFn := BuildStoreBackedAddRepos(st)
	_, err := addFn(context.Background(), service.WorkspaceAddReposRequest{
		WorkspaceID: "MY-WS",
		CloneURLs:   []string{emptyRemote},
	})
	if err == nil || !strings.Contains(err.Error(), "resolvable committed default branch") ||
		!strings.Contains(err.Error(), "specify one explicitly") {
		t.Fatalf("add empty remote error = %v, want committed-branch validation", err)
	}
	if _, statErr := os.Stat(filepath.Join(wsPath, "empty-remote")); !os.IsNotExist(statErr) {
		t.Fatalf("empty remote clone was not rolled back: %v", statErr)
	}
	repos, listErr := st.Repos().List(context.Background(), "MY-WS")
	if listErr != nil || len(repos) != 0 {
		t.Fatalf("repos after rejected empty remote = %#v, err=%v", repos, listErr)
	}
}

func TestStoreBackedCreateEmptyWorkspaceRollsBackOnRepoStoreError(t *testing.T) {
	loomDir := t.TempDir()
	t.Setenv("LOOM_CONFIG_DIR", loomDir)

	src := initTestGitRepo(t, t.TempDir(), "app")
	base := memstore.New()
	st := &repoFailStore{Store: base, err: errors.New("repo create failed")}
	createFn := BuildStoreBackedCreateWorkspace(st)
	wsPath := filepath.Join(loomDir, "workspaces", "my-ws")

	if _, err := createFn(context.Background(), service.WorkspaceCreateRequest{
		Name:   "my-ws",
		Type:   "empty",
		Repos:  []string{src},
		Branch: "feature-work",
		Path:   wsPath,
	}); err == nil {
		t.Fatal("create workspace succeeded, want repo store error")
	}

	if _, err := base.Workspaces().Get(context.Background(), "MY-WS"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("workspace was not rolled back, err=%v", err)
	}
	if _, err := os.Stat(wsPath); !os.IsNotExist(err) {
		t.Fatalf("workspace path still exists after rollback, stat err=%v", err)
	}
	if out := gitOutput(t, src, "branch", "--list", "feature-work"); out != "" {
		t.Fatalf("rollback left branch feature-work behind: %q", out)
	}
	sc, err := bootstrap.LoadStateCache()
	if err != nil {
		t.Fatalf("load state cache: %v", err)
	}
	if sc.LastWorkspace != "" || len(sc.Workspaces) != 0 {
		t.Fatalf("state cache was written on rollback: %#v", sc)
	}
}

func TestStoreBackedCreateEmptyWorkspaceClassifiesCreateRace(t *testing.T) {
	loomDir := t.TempDir()
	t.Setenv("LOOM_CONFIG_DIR", loomDir)

	st := &workspaceCreateRaceStore{Store: memstore.New()}
	createFn := BuildStoreBackedCreateWorkspace(st)

	_, err := createFn(context.Background(), service.WorkspaceCreateRequest{
		Name: "my-ws",
		Type: "empty",
		Path: filepath.Join(loomDir, "workspaces", "my-ws"),
	})
	var createErr *workspaceerrors.CreateError
	if !errors.As(err, &createErr) {
		t.Fatalf("error = %v, want workspace create error", err)
	}
	if createErr.Code != workspaceerrors.AlreadyExists {
		t.Fatalf("error code = %s, want AlreadyExists", createErr.Code)
	}
}

func TestStoreBackedCreateEmptyWorkspaceRollsBackLocalStateOnReadyUpdateError(t *testing.T) {
	loomDir := t.TempDir()
	t.Setenv("LOOM_CONFIG_DIR", loomDir)

	st := &workspaceReadyUpdateFailStore{Store: memstore.New()}
	createFn := BuildStoreBackedCreateWorkspace(st)

	_, err := createFn(context.Background(), service.WorkspaceCreateRequest{
		Name: "rollback-ws",
		Type: "empty",
		Path: filepath.Join(loomDir, "workspaces", "rollback-ws"),
	})
	if err == nil {
		t.Fatal("create workspace succeeded, want ready update error")
	}
	if _, getErr := st.Store.Workspaces().Get(context.Background(), "ROLLBACK-WS"); !errors.Is(getErr, domain.ErrNotFound) {
		t.Fatalf("store workspace get err = %v, want ErrNotFound", getErr)
	}
	sc, loadErr := bootstrap.LoadStateCache()
	if loadErr != nil {
		t.Fatalf("load state cache: %v", loadErr)
	}
	if _, ok := sc.Workspaces["ROLLBACK-WS"]; ok {
		t.Fatalf("local state still contains ROLLBACK-WS: %#v", sc.Workspaces["ROLLBACK-WS"])
	}
	if sc.LastWorkspace == "ROLLBACK-WS" {
		t.Fatalf("LastWorkspace = %q, want rollback to clear active workspace", sc.LastWorkspace)
	}
}

func TestStoreBackedCreateCloneWorkspacePersistsLifecycleAndRepos(t *testing.T) {
	loomDir := t.TempDir()
	t.Setenv("LOOM_CONFIG_DIR", loomDir)

	src := initTestGitRepo(t, t.TempDir(), "app")
	sourceBranch := strings.TrimSpace(gitOutput(t, src, "branch", "--show-current"))
	st := memstore.New()
	credentials := &recordingGitCredentialSource{}
	createFn := BuildStoreBackedCreateWorkspaceWithCredentials(st, credentials)
	wsPath := filepath.Join(loomDir, "workspaces", "clone-ws")

	result, err := createFn(context.Background(), service.WorkspaceCreateRequest{
		Name:      "clone-ws",
		Type:      "clone",
		CloneURLs: []string{src},
		Path:      wsPath,
	})
	if err != nil {
		t.Fatalf("clone workspace: %v", err)
	}
	if len(credentials.remotes) != 0 {
		t.Fatalf("credential source remotes = %v, want none for anonymous clone success", credentials.remotes)
	}
	if result.WorkspaceID != "CLONE-WS" {
		t.Fatalf("WorkspaceID = %q, want CLONE-WS", result.WorkspaceID)
	}
	ws, err := st.Workspaces().Get(context.Background(), "CLONE-WS")
	if err != nil {
		t.Fatalf("workspace not stored: %v", err)
	}
	if ws.State != domain.WorkspaceStateReady {
		t.Fatalf("workspace state = %q, want ready", ws.State)
	}
	repos, err := st.Repos().List(context.Background(), "CLONE-WS")
	if err != nil {
		t.Fatalf("list repos: %v", err)
	}
	if len(repos) != 1 || repos[0].Name != "app" || repos[0].RemoteURL != src ||
		repos[0].SourceRepoID != "app" || repos[0].DefaultBranch != sourceBranch {
		t.Fatalf("repos = %#v, want cloned app repo with remote URL", repos)
	}
	if ws.DefaultBranch != sourceBranch {
		t.Fatalf("workspace default branch = %q, want detected %q", ws.DefaultBranch, sourceBranch)
	}
	roles, err := st.Roles().List(context.Background(), "CLONE-WS")
	if err != nil {
		t.Fatalf("list roles: %v", err)
	}
	if len(roles) != 3 || !hasRole(roles, "plan") || !hasRole(roles, "task") || !hasRole(roles, "lead") {
		t.Fatalf("roles = %#v, want plan, task, and lead", roles)
	}
	if _, err := os.Stat(filepath.Join(wsPath, "app", ".git")); err != nil {
		t.Fatalf("clone checkout not created: %v", err)
	}
	sc, err := bootstrap.LoadStateCache()
	if err != nil {
		t.Fatalf("load state cache: %v", err)
	}
	if sc.LastWorkspace != "CLONE-WS" {
		t.Fatalf("LastWorkspace = %q, want CLONE-WS", sc.LastWorkspace)
	}
	if sc.Workspaces["CLONE-WS"].Repos["app"] != filepath.Join(wsPath, "app") {
		t.Fatalf("state repo path = %q", sc.Workspaces["CLONE-WS"].Repos["app"])
	}

	// A failed anonymous clone still proves the credential source is wired
	// through the create path and receives the exact remote for fallback.
	missingRemote := filepath.Join(t.TempDir(), "missing-private.git")
	if _, err := createFn(context.Background(), service.WorkspaceCreateRequest{
		Name:      "clone-ws-auth-fallback",
		Type:      "clone",
		CloneURLs: []string{missingRemote},
		Path:      filepath.Join(loomDir, "workspaces", "clone-ws-auth-fallback"),
	}); err == nil {
		t.Fatal("create workspace from missing clone repo succeeded")
	}
	if len(credentials.remotes) != 1 || credentials.remotes[0] != missingRemote {
		t.Fatalf("credential source remotes = %v, want fallback [%s]", credentials.remotes, missingRemote)
	}
}

func TestStoreBackedCreateCloneWorkspaceNormalizesRepoNameForFleetStore(t *testing.T) {
	loomDir := t.TempDir()
	t.Setenv("LOOM_CONFIG_DIR", loomDir)

	src := initTestGitRepo(t, t.TempDir(), "Hello-World")
	sourceBranch := strings.TrimSpace(gitOutput(t, src, "branch", "--show-current"))
	st := memstore.New()
	createFn := BuildStoreBackedCreateWorkspace(st)
	wsPath := filepath.Join(loomDir, "workspaces", "clone-ws")

	_, err := createFn(context.Background(), service.WorkspaceCreateRequest{
		Name:      "clone-ws",
		Type:      "clone",
		CloneURLs: []string{src},
		Path:      wsPath,
	})
	if err != nil {
		t.Fatalf("clone workspace: %v", err)
	}

	repos, err := st.Repos().List(context.Background(), "CLONE-WS")
	if err != nil {
		t.Fatalf("list repos: %v", err)
	}
	if len(repos) != 1 || repos[0].Name != "hello-world" || repos[0].SourceRepoID != "hello-world" ||
		repos[0].DefaultBranch != sourceBranch {
		t.Fatalf("repos = %#v, want normalized hello-world repo", repos)
	}
	if _, err := os.Stat(filepath.Join(wsPath, "hello-world", ".git")); err != nil {
		t.Fatalf("clone checkout not created at normalized path: %v", err)
	}
}

func TestStoreBackedCreateCloneWorkspaceClassifiesCreateRace(t *testing.T) {
	loomDir := t.TempDir()
	t.Setenv("LOOM_CONFIG_DIR", loomDir)

	st := &workspaceCreateRaceStore{Store: memstore.New()}
	createFn := BuildStoreBackedCreateWorkspace(st)
	src := initTestGitRepo(t, t.TempDir(), "app")

	_, err := createFn(context.Background(), service.WorkspaceCreateRequest{
		Name:      "clone-ws",
		Type:      "clone",
		CloneURLs: []string{src},
		Path:      filepath.Join(loomDir, "workspaces", "clone-ws"),
	})
	var createErr *workspaceerrors.CreateError
	if !errors.As(err, &createErr) {
		t.Fatalf("error = %v, want workspace create error", err)
	}
	if createErr.Code != workspaceerrors.AlreadyExists {
		t.Fatalf("error code = %s, want AlreadyExists", createErr.Code)
	}
}

func TestStoreBackedCreateCloneWorkspaceRollsBackStoreOnCloneFailure(t *testing.T) {
	loomDir := t.TempDir()
	t.Setenv("LOOM_CONFIG_DIR", loomDir)

	st := memstore.New()
	createFn := BuildStoreBackedCreateWorkspace(st)
	wsPath := filepath.Join(loomDir, "workspaces", "clone-ws")

	_, err := createFn(context.Background(), service.WorkspaceCreateRequest{
		Name:      "clone-ws",
		Type:      "clone",
		CloneURLs: []string{filepath.Join(t.TempDir(), "missing")},
		Path:      wsPath,
	})
	if err == nil {
		t.Fatal("clone workspace succeeded, want git clone error")
	}

	if _, getErr := st.Workspaces().Get(context.Background(), "CLONE-WS"); !errors.Is(getErr, domain.ErrNotFound) {
		t.Fatalf("workspace was not rolled back, err=%v", getErr)
	}
	if _, statErr := os.Stat(wsPath); !os.IsNotExist(statErr) {
		t.Fatalf("workspace path still exists after clone failure, stat err=%v", statErr)
	}
	sc, err := bootstrap.LoadStateCache()
	if err != nil {
		t.Fatalf("load state cache: %v", err)
	}
	if sc.LastWorkspace != "" || len(sc.Workspaces) != 0 {
		t.Fatalf("state cache was written on clone rollback: %#v", sc)
	}
}

func TestStoreBackedCreateCloneWorkspaceKeepsPreexistingExternalRootOnFailure(t *testing.T) {
	loomDir := t.TempDir()
	t.Setenv("LOOM_CONFIG_DIR", loomDir)

	externalPath := filepath.Join(t.TempDir(), "picked-workspace")
	if err := os.MkdirAll(externalPath, 0755); err != nil {
		t.Fatalf("mkdir external path: %v", err)
	}

	st := memstore.New()
	createFn := BuildStoreBackedCreateWorkspace(st)

	_, err := createFn(context.Background(), service.WorkspaceCreateRequest{
		Name:      "clone-ws",
		Type:      "clone",
		CloneURLs: []string{filepath.Join(t.TempDir(), "missing")},
		Path:      externalPath,
	})
	if err == nil {
		t.Fatal("clone workspace succeeded, want git clone error")
	}
	if info, statErr := os.Stat(externalPath); statErr != nil || !info.IsDir() {
		t.Fatalf("pre-existing external workspace root was removed, info=%v err=%v", info, statErr)
	}
}

type repoFailStore struct {
	*memstore.Store
	err error
}

func (s *repoFailStore) Repos() store.RepoStore {
	return repoFailer{err: s.err}
}

type workspaceCreateRaceStore struct {
	*memstore.Store
}

func (s *workspaceCreateRaceStore) Workspaces() store.WorkspaceStore {
	return workspaceCreateRaceWorkspaceStore{WorkspaceStore: s.Store.Workspaces()}
}

type workspaceCreateRaceWorkspaceStore struct {
	store.WorkspaceStore
}

func (s workspaceCreateRaceWorkspaceStore) Create(context.Context, store.WorkspaceCreate) (*domain.Workspace, error) {
	return nil, domain.ErrAlreadyExists
}

type workspaceReadyUpdateFailStore struct {
	*memstore.Store
}

func (s *workspaceReadyUpdateFailStore) Workspaces() store.WorkspaceStore {
	return workspaceReadyUpdateFailWorkspaceStore{WorkspaceStore: s.Store.Workspaces()}
}

type workspaceReadyUpdateFailWorkspaceStore struct {
	store.WorkspaceStore
}

func (s workspaceReadyUpdateFailWorkspaceStore) Update(ctx context.Context, key string, patch store.WorkspaceUpdate) (*domain.Workspace, error) {
	if patch.State != nil && *patch.State == domain.WorkspaceStateReady {
		return nil, errors.New("ready update failed")
	}
	return s.WorkspaceStore.Update(ctx, key, patch)
}

type repoFailer struct {
	err error
}

func hasRole(roles []*domain.Role, name string) bool {
	for _, role := range roles {
		if role.Name == name {
			return true
		}
	}
	return false
}

func rolesByName(roles []*domain.Role) map[string]*domain.Role {
	out := make(map[string]*domain.Role, len(roles))
	for _, role := range roles {
		out[role.Name] = role
	}
	return out
}

func (r repoFailer) Create(context.Context, store.RepoCreate) (*domain.Repo, error) {
	return nil, r.err
}

func (r repoFailer) Get(context.Context, string, string) (*domain.Repo, error) {
	return nil, domain.ErrNotFound
}

func (r repoFailer) List(context.Context, string) ([]*domain.Repo, error) {
	return nil, nil
}

func (r repoFailer) Update(context.Context, string, string, store.RepoUpdate) (*domain.Repo, error) {
	return nil, r.err
}

func (r repoFailer) Delete(context.Context, string, string) error {
	return nil
}

func initTestGitRepo(t *testing.T, parent, name string) string {
	t.Helper()
	path := filepath.Join(parent, name)
	if err := os.MkdirAll(path, 0755); err != nil {
		t.Fatalf("mkdir repo: %v", err)
	}
	runGit(t, path, "init")
	runGit(t, path, "config", "user.email", "test@example.com")
	runGit(t, path, "config", "user.name", "Test User")
	if err := os.WriteFile(filepath.Join(path, "README.md"), []byte("test\n"), 0644); err != nil {
		t.Fatalf("write readme: %v", err)
	}
	runGit(t, path, "add", "README.md")
	runGit(t, path, "commit", "-m", "init")
	return path
}

func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...) //nolint:norawexec // Test helper creates real git repos for workspace lifecycle coverage.
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v failed: %v\n%s", args, err, out)
	}
}

func gitOutput(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...) //nolint:norawexec // Test helper creates real git repos for workspace lifecycle coverage.
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v failed: %v\n%s", args, err, out)
	}
	return string(out)
}

func corruptWorkspaceBranchRef(t *testing.T, repoPath, branch string) {
	t.Helper()
	common, err := gitbranch.CommonDir(repoPath)
	if err != nil {
		t.Fatalf("git common dir: %v", err)
	}
	refPath := filepath.Join(common, "refs", "heads", filepath.FromSlash(branch))
	if err := os.MkdirAll(filepath.Dir(refPath), 0o755); err != nil {
		t.Fatalf("mkdir branch ref parent: %v", err)
	}
	if err := os.WriteFile(refPath, nil, 0o644); err != nil {
		t.Fatalf("corrupt branch ref: %v", err)
	}
}
