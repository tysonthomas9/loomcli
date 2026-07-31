package driver

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/bootstrap"
	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/infra/memstore"
	"github.com/tysonthomas9/loomcli/internal/modules/sourcecontrol"
	"github.com/tysonthomas9/loomcli/internal/store"
)

type testTaskMaterializer struct{}

// testTaskSourceControl keeps the shared test helper name used by the lineage
// tests while exposing only the narrowed task/PR Materializer contract.
type testTaskSourceControl = testTaskMaterializer

func (testTaskMaterializer) PrepareTaskCheckout(
	ctx context.Context,
	command sourcecontrol.TaskCheckoutCommand,
) (*sourcecontrol.TaskCheckout, error) {
	cache, err := bootstrap.LoadStateCache()
	if err != nil {
		return nil, err
	}
	local, ok := cache.Workspaces[command.WorkspaceKey]
	if !ok || len(local.Repos) != 1 {
		return nil, sourcecontrol.ErrInvalidMaterialization
	}
	var repoPath string
	for _, path := range local.Repos {
		repoPath = path
	}
	result := &sourcecontrol.TaskCheckout{
		WorkspaceKey: command.WorkspaceKey, TaskRunID: command.TaskRunID,
		RepositoryRef: command.RepositoryRef, CheckoutPath: repoPath,
	}
	if strings.TrimSpace(command.BaseBranch) == "" {
		return result, nil
	}
	sum := sha256.Sum256([]byte(command.TaskRunID))
	result.BaseRef = "refs/loom/task-runs/" + hex.EncodeToString(sum[:8]) + "/base"
	baseOutput, err := testGitOutputForMaterializer(
		ctx,
		repoPath,
		"rev-parse",
		"--verify",
		command.BaseBranch+"^{commit}",
	)
	if err != nil {
		return nil, fmt.Errorf("resolve test base %q: %w", command.BaseBranch, err)
	}
	baseCommit := strings.TrimSpace(baseOutput)
	if _, err := testGitOutputForMaterializer(
		ctx,
		repoPath,
		"update-ref",
		result.BaseRef,
		baseCommit,
	); err != nil {
		return nil, fmt.Errorf("publish test base ref: %w", err)
	}
	result.BaseCommit = baseCommit
	return result, nil
}

func (testTaskMaterializer) PreparePullRequestCheckout(
	context.Context,
	sourcecontrol.PullRequestCheckoutCommand,
) (*sourcecontrol.PullRequestCheckout, error) {
	return nil, sourcecontrol.ErrUnavailable
}

var _ sourcecontrol.Materializer = testTaskMaterializer{}

func testGitOutputForMaterializer(
	ctx context.Context,
	dir string,
	args ...string,
) (string, error) {
	command := exec.CommandContext(ctx, "git", args...) //nolint:norawexec,gosec // test helper.
	command.Dir = dir
	output, err := command.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(string(output)))
	}
	return string(output), nil
}

func TestLocalTaskWorktreeResolverCreatesIsolatedTaskRunWorktree(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}

	ctx := context.Background()
	loomDir := t.TempDir()
	t.Setenv("LOOM_CONFIG_DIR", loomDir)

	workspacePath := filepath.Join(t.TempDir(), "workspace")
	repoPath := filepath.Join(workspacePath, "app")
	if err := os.MkdirAll(repoPath, 0o755); err != nil {
		t.Fatalf("mkdir repo: %v", err)
	}
	gitCmd(t, repoPath, "init")
	gitCmd(t, repoPath, "checkout", "-b", "main")
	gitCmd(t, repoPath, "config", "user.name", "Test User")
	gitCmd(t, repoPath, "config", "user.email", "test@example.test")
	writeTestFile(t, filepath.Join(repoPath, "src", "app.js"), "console.log('ok');\n")
	gitCmd(t, repoPath, "add", "src/app.js")
	gitCmd(t, repoPath, "commit", "-m", "base")
	head := strings.TrimSpace(testGitOutput(t, repoPath, "rev-parse", "HEAD"))

	if err := bootstrap.MutateWorkspaceLocalState("TEST", func(local *bootstrap.WorkspaceLocalState) error {
		local.Path = workspacePath
		local.Repos = map[string]string{"app": repoPath}
		return nil
	}); err != nil {
		t.Fatalf("write local state: %v", err)
	}

	st := memstore.New()
	if _, err := st.Workspaces().Create(ctx, store.WorkspaceCreate{Key: "TEST", Name: "test"}); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	if _, err := st.Repos().Create(ctx, store.RepoCreate{
		WorkspaceKey:  "TEST",
		Name:          "app",
		DefaultBranch: "main",
		SourceRepoID:  "frontend",
	}); err != nil {
		t.Fatalf("create repo: %v", err)
	}

	resolved, err := (LocalTaskWorktreeResolver{
		Store: st, SourceControl: testTaskMaterializer{},
	}).ResolveTaskWorktree(ctx, TaskExecRequest{
		WorkspaceKey:     "TEST",
		TaskRunID:        "task/run:1",
		TaskID:           "TEST-1",
		SandboxPlacement: domain.TaskRunPlacement{RepoRef: "frontend"},
	}, t.TempDir())
	if err != nil {
		t.Fatalf("ResolveTaskWorktree: %v", err)
	}
	if resolved.Path == "" || resolved.Path == repoPath {
		t.Fatalf("resolved path = %q, want isolated task worktree distinct from repo %q", resolved.Path, repoPath)
	}
	if resolved.RepoName != "app" || resolved.SourceRepoID != "frontend" {
		t.Fatalf("resolved repo metadata = %+v, want app/frontend", resolved)
	}
	if _, err := os.Stat(filepath.Join(resolved.Path, ".git")); err != nil {
		t.Fatalf("resolved worktree .git missing: %v", err)
	}
	if got := strings.TrimSpace(testGitOutput(t, resolved.Path, "rev-parse", "HEAD")); got != head {
		t.Fatalf("resolved HEAD = %s, want %s", got, head)
	}
	if _, err := os.Stat(filepath.Join(resolved.Path, "src", "app.js")); err != nil {
		t.Fatalf("resolved worktree missing source file: %v", err)
	}
}

func TestLocalTaskWorktreeResolverRequiresSelectorForMultipleRepos(t *testing.T) {
	repos := []*domain.Repo{
		{Name: "alpha", SourceRepoID: "source-alpha"},
		{Name: "beta", SourceRepoID: "source-beta"},
	}
	resolver := LocalTaskWorktreeResolver{}

	if _, err := resolver.selectRepo(context.Background(), "TEST", repos, TaskExecRequest{}); err == nil ||
		!strings.Contains(err.Error(), "task repo selector required") {
		t.Fatalf("selectRepo() error = %v, want explicit-selector requirement", err)
	}

	selected, err := resolver.selectRepo(context.Background(), "TEST", repos, TaskExecRequest{
		RunnerPlacement: domain.TaskRunPlacement{RepoRef: "source-beta"},
	})
	if err != nil {
		t.Fatalf("selectRepo() with selector: %v", err)
	}
	if selected.Name != "beta" {
		t.Fatalf("selectRepo() = %q, want beta", selected.Name)
	}
}

func TestLocalTaskWorktreeResolverKeepsSingleRepoFallback(t *testing.T) {
	repo := &domain.Repo{Name: "only", SourceRepoID: "source-only"}
	selected, err := (LocalTaskWorktreeResolver{}).selectRepo(
		context.Background(), "TEST", []*domain.Repo{repo}, TaskExecRequest{},
	)
	if err != nil {
		t.Fatalf("selectRepo() single repo: %v", err)
	}
	if selected != repo {
		t.Fatalf("selectRepo() = %#v, want only repo", selected)
	}
}

func TestLocalTaskWorktreeResolverTreatsWorkerProfileReposAsScope(t *testing.T) {
	ctx := context.Background()
	repos := []*domain.Repo{
		{Name: "alpha", SourceRepoID: "source-alpha"},
		{Name: "beta", SourceRepoID: "source-beta"},
	}
	st := memstore.New()
	for _, profile := range []store.WorkerProfileCreate{
		{WorkspaceKey: "TEST", ProfileID: "multi", Role: "task", Repos: []string{"alpha", "beta"}},
		{WorkspaceKey: "TEST", ProfileID: "beta-only", Role: "task", Repos: []string{"source-beta"}},
		{WorkspaceKey: "TEST", ProfileID: "alpha-only", Role: "task", Repos: []string{"alpha"}},
	} {
		if _, err := st.WorkerProfiles().Create(ctx, profile); err != nil {
			t.Fatalf("create worker profile %q: %v", profile.ProfileID, err)
		}
	}
	resolver := LocalTaskWorktreeResolver{Store: st}

	if _, err := resolver.selectRepo(ctx, "TEST", repos, TaskExecRequest{WorkerProfileID: "multi"}); err == nil ||
		!strings.Contains(err.Error(), "scope matches 2") {
		t.Fatalf("multi-repo profile without task selector error = %v, want ambiguity", err)
	}
	selected, err := resolver.selectRepo(ctx, "TEST", repos, TaskExecRequest{WorkerProfileID: "beta-only"})
	if err != nil || selected.Name != "beta" {
		t.Fatalf("single-repo profile fallback = %+v, %v; want beta", selected, err)
	}
	selected, err = resolver.selectRepo(ctx, "TEST", repos, TaskExecRequest{
		WorkerProfileID: "multi",
		RunnerPlacement: domain.TaskRunPlacement{RepoRef: "source-beta"},
	})
	if err != nil || selected.Name != "beta" {
		t.Fatalf("explicit selector within profile scope = %+v, %v; want beta", selected, err)
	}
	if _, err := resolver.selectRepo(ctx, "TEST", repos, TaskExecRequest{
		WorkerProfileID: "alpha-only",
		RunnerPlacement: domain.TaskRunPlacement{RepoRef: "source-beta"},
	}); err == nil || !strings.Contains(err.Error(), "outside worker profile") {
		t.Fatalf("explicit selector outside profile scope error = %v, want fail-closed scope error", err)
	}
}

func TestLocalTaskWorktreeResolverDoesNotIgnoreInvalidExplicitSelectorInSingleRepoWorkspace(t *testing.T) {
	repo := &domain.Repo{Name: "only", SourceRepoID: "source-only"}
	if _, err := (LocalTaskWorktreeResolver{}).selectRepo(
		context.Background(), "TEST", []*domain.Repo{repo}, TaskExecRequest{
			RunnerPlacement: domain.TaskRunPlacement{RepoRef: "missing"},
		},
	); err == nil || !strings.Contains(err.Error(), "no workspace repo matches") {
		t.Fatalf("invalid explicit selector error = %v, want no silent single-repo fallback", err)
	}
}

func TestLocalTaskWorktreeResolverProfileScopeDoesNotAliasRemoteBasenames(t *testing.T) {
	ctx := context.Background()
	repos := []*domain.Repo{
		{Name: "alpha-app", SourceRepoID: "source-alpha-app", RemoteURL: "https://github.com/org-a/app.git"},
		{Name: "beta-app", SourceRepoID: "source-beta-app", RemoteURL: "git@github.com:org-b/app.git"},
	}
	st := memstore.New()
	if _, err := st.WorkerProfiles().Create(ctx, store.WorkerProfileCreate{
		WorkspaceKey: "TEST", ProfileID: "org-a-only", Role: "task", Repos: []string{"org-a/app"},
	}); err != nil {
		t.Fatalf("create worker profile: %v", err)
	}
	resolver := LocalTaskWorktreeResolver{Store: st}

	selected, err := resolver.selectRepo(ctx, "TEST", repos, TaskExecRequest{WorkerProfileID: "org-a-only"})
	if err != nil || selected.Name != "alpha-app" {
		t.Fatalf("qualified profile scope fallback = %+v, %v; want alpha-app", selected, err)
	}
	if _, err := resolver.selectRepo(ctx, "TEST", repos, TaskExecRequest{
		WorkerProfileID: "org-a-only",
		RunnerPlacement: domain.TaskRunPlacement{RepoRef: "org-b/app"},
	}); err == nil || !strings.Contains(err.Error(), "outside worker profile") {
		t.Fatalf("same-basename cross-org selector error = %v, want exact scope denial", err)
	}
	if selected := findRepoBySelector(repos, "app"); selected != nil {
		t.Fatalf("ambiguous basename selected %+v, want fail closed", selected)
	}
}

func gitCmd(t *testing.T, dir string, args ...string) {
	t.Helper()
	_ = testGitOutput(t, dir, args...)
}

func testGitOutput(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...) //nolint:gosec // fixed test command. //nolint:norawexec
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s failed: %v\n%s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return string(out)
}

func writeTestFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}
