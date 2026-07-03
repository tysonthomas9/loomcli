package svcimpl

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/ops"
	"github.com/tysonthomas9/loomcli/internal/webui/service"
)

// scopedMockFileOps is a minimal ops.FileOps that only resolves the workspace
// root, so these tests exercise the real fileServiceImpl scoped code paths.
type scopedMockFileOps struct {
	wsRoot    string
	repoRoot  string
	agentRoot string
	dataDir   string
	wsErr     error
}

func (m scopedMockFileOps) ResolveAgentWorktree(_, name string) (*ops.AgentWorktree, error) {
	if name != "agent-a" || m.agentRoot == "" {
		return nil, errors.New("agent worktree not found")
	}
	return &ops.AgentWorktree{Name: name, Path: m.agentRoot, RepoName: "repo-a"}, nil
}
func (m scopedMockFileOps) ResolveAgentWorktreeOrPrimary(_, _ string) (*ops.AgentWorktree, error) {
	return nil, errors.New("not used")
}
func (m scopedMockFileOps) ResolveWorkspaceRoot(_ string) (string, error) {
	if m.wsErr != nil {
		return "", m.wsErr
	}
	return m.wsRoot, nil
}
func (m scopedMockFileOps) ResolveWorkspaceData(_ string) (*ops.WorkspaceData, error) {
	if m.wsErr != nil {
		return nil, m.wsErr
	}
	ws := &ops.WorkspaceData{
		ID:   "ws",
		Path: m.wsRoot,
		Repos: []ops.WorkspaceRepo{{
			Name: "repo-a",
			Path: m.repoRoot,
		}},
		Agents: []ops.WorkspaceAgentInfo{{Name: "agent-a"}},
	}
	return ws, nil
}

func (m scopedMockFileOps) GitStatusPorcelain(worktreePath string) (map[string]string, error) {
	cmd := exec.Command("git", "-C", worktreePath, "status", "--porcelain") //nolint:norawexec // Test mock intentionally exercises real git status output.
	out, err := cmd.Output()
	if err != nil {
		return nil, err
	}
	return parseTestPorcelainStatus(string(out)), nil
}

func (m scopedMockFileOps) GitShowFileAtRev(worktreePath, rev, path string, maxBytes int64) (*ops.GitFileContentAtRev, error) {
	spec := rev + ":" + path
	sizeOut, err := exec.Command("git", "-C", worktreePath, "cat-file", "-s", spec).Output() //nolint:norawexec // Test mock runs fixed git command.
	if err != nil {
		return nil, err
	}
	size, err := strconv.ParseInt(strings.TrimSpace(string(sizeOut)), 10, 64)
	if err != nil {
		return nil, err
	}
	out, err := exec.Command("git", "-C", worktreePath, "show", spec).Output() //nolint:norawexec // Test mock runs fixed git command.
	if err != nil {
		return nil, err
	}
	truncated := int64(len(out)) > maxBytes
	if truncated {
		out = out[:maxBytes]
	}
	return &ops.GitFileContentAtRev{Content: out, Size: size, Truncated: truncated}, nil
}

func (m scopedMockFileOps) GitDiffFile(worktreePath, path, from, to string) (string, error) {
	if from == "" {
		from = "HEAD"
	}
	args := []string{"-C", worktreePath, "diff"}
	if to != "" {
		args = append(args, from+".."+to)
	} else {
		args = append(args, from)
	}
	args = append(args, "--", path)
	out, err := exec.Command("git", args...).CombinedOutput() //nolint:norawexec // Test mock runs fixed git command.
	return string(out), err
}

func (m scopedMockFileOps) GitLogFile(worktreePath, path string, limit int) (string, error) {
	out, err := exec.Command("git", "-C", worktreePath, "log", "--follow", "-n", strconv.Itoa(limit), "--format=%H%x1f%an%x1f%at%x1f%s%x1e", "--", path).CombinedOutput() //nolint:norawexec // Test mock runs fixed git command.
	return string(out), err
}

func (m scopedMockFileOps) GitBlamePorcelain(worktreePath, path string) (string, error) {
	out, err := exec.Command("git", "-C", worktreePath, "blame", "--porcelain", "--", path).CombinedOutput() //nolint:norawexec // Test mock runs fixed git command.
	return string(out), err
}

func (m scopedMockFileOps) ResolveLoomDataDir() (string, error) {
	if m.dataDir != "" {
		return m.dataDir, nil
	}
	return os.TempDir(), nil
}

func parseTestPorcelainStatus(output string) map[string]string {
	trimmed := strings.Trim(output, "\r\n")
	if trimmed == "" {
		return map[string]string{}
	}
	lines := strings.Split(trimmed, "\n")
	status := make(map[string]string, len(lines))
	for _, line := range lines {
		line = strings.TrimRight(line, "\r")
		if len(line) < 3 {
			continue
		}
		xy := line[:2]
		path := strings.TrimSpace(line[3:])
		if path == "" {
			continue
		}
		if strings.Contains(path, " -> ") {
			parts := strings.Split(path, " -> ")
			path = parts[len(parts)-1]
		}
		status[path] = xy
	}
	return status
}

func scopedSvc(root string) service.FileService {
	return NewFileService(scopedMockFileOps{wsRoot: root})
}

type scopedCase struct {
	name   string
	scope  service.FileScope
	target string
	root   string
}

func setupScopedService(t *testing.T) (service.FileService, []scopedCase) {
	t.Helper()
	wsRoot := t.TempDir()
	repoRoot := filepath.Join(wsRoot, "repo-a")
	agentRoot := filepath.Join(wsRoot, "worktrees", "repo-a", "agent-a")
	for _, dir := range []string{repoRoot, agentRoot} {
		if err := os.MkdirAll(dir, 0755); err != nil {
			t.Fatal(err)
		}
	}
	svc := NewFileService(scopedMockFileOps{
		wsRoot:    wsRoot,
		repoRoot:  repoRoot,
		agentRoot: agentRoot,
		dataDir:   t.TempDir(),
	})
	return svc, []scopedCase{
		{name: "workspace", scope: service.ScopeWorkspace, root: wsRoot},
		{name: "repo", scope: service.ScopeRepo, target: "repo-a", root: repoRoot},
		{name: "agent", scope: service.ScopeAgent, target: "agent-a", root: agentRoot},
	}
}

func mustWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
}

func mustGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...) //nolint:norawexec // Test helper uses fixed git commands in temp repos.
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v failed: %v\n%s", args, err, out)
	}
}

func gitOutput(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...) //nolint:norawexec // Test helper uses fixed git commands in temp repos.
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("git %v output failed: %v", args, err)
	}
	return string(out)
}

func initGitRepo(t *testing.T, dir string) {
	t.Helper()
	mustGit(t, dir, "init", "-b", "main")
	mustGit(t, dir, "config", "user.email", "test@example.com")
	mustGit(t, dir, "config", "user.name", "Test User")
}

func commitAll(t *testing.T, dir string) {
	t.Helper()
	mustGit(t, dir, "add", ".")
	mustGit(t, dir, "commit", "-m", "init")
}

func wantKind(t *testing.T, err error, kind service.ErrorKind) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected error of kind %q, got nil", kind)
	}
	var svcErr *service.ServiceError
	if !errors.As(err, &svcErr) {
		t.Fatalf("error = %T %v, want *service.ServiceError", err, err)
	}
	if svcErr.Kind != kind {
		t.Fatalf("error kind = %q, want %q", svcErr.Kind, kind)
	}
}

func withNavigationCaps(t *testing.T, fn func()) {
	t.Helper()
	oldIndexEntries := fileIndexMaxEntries
	oldIndexBudget := fileIndexWalkBudget
	oldSearchFiles := fileSearchMaxFiles
	oldSearchBytes := fileSearchMaxBytes
	oldSearchFileBytes := fileSearchMaxFileBytes
	oldSearchMatches := fileSearchMaxMatches
	oldSearchBudget := fileSearchWalkBudget
	t.Cleanup(func() {
		fileIndexMaxEntries = oldIndexEntries
		fileIndexWalkBudget = oldIndexBudget
		fileSearchMaxFiles = oldSearchFiles
		fileSearchMaxBytes = oldSearchBytes
		fileSearchMaxFileBytes = oldSearchFileBytes
		fileSearchMaxMatches = oldSearchMatches
		fileSearchWalkBudget = oldSearchBudget
	})
	fn()
}

func resultPaths(results []service.FileSearchFileResult) []string {
	paths := make([]string, 0, len(results))
	for _, result := range results {
		paths = append(paths, result.Path)
	}
	sort.Strings(paths)
	return paths
}

func containsPath(paths []string, want string) bool {
	for _, path := range paths {
		if path == want {
			return true
		}
	}
	return false
}

func TestFileServiceImpl_ListDirectoryScoped_HidesGit(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte("package m"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, ".git"), 0755); err != nil {
		t.Fatal(err)
	}

	res, err := scopedSvc(dir).ListDirectoryScoped(context.Background(), "ws", service.ScopeWorkspace, "", "")
	if err != nil {
		t.Fatalf("ListDirectoryScoped: %v", err)
	}
	var sawMain, sawGit bool
	for _, e := range res.Entries {
		sawMain = sawMain || e.Name == "main.go"
		sawGit = sawGit || e.Name == ".git"
	}
	if !sawMain {
		t.Errorf("main.go missing from listing: %+v", res.Entries)
	}
	if sawGit {
		t.Errorf(".git must be hidden from listing: %+v", res.Entries)
	}
}

func TestFileServiceImpl_ReadFileScoped_ReadsContent(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "f.txt"), []byte("body"), 0644); err != nil {
		t.Fatal(err)
	}

	res, err := scopedSvc(dir).ReadFileScoped(context.Background(), "ws", service.ScopeWorkspace, "", "f.txt")
	if err != nil {
		t.Fatalf("ReadFileScoped: %v", err)
	}
	if res.Content != "body" {
		t.Errorf("content = %q, want %q", res.Content, "body")
	}
}

func TestFileServiceImpl_ReadFileScoped_GitExplicitReadAllowed(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".git"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".git", "config"), []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}

	res, err := scopedSvc(dir).ReadFileScoped(context.Background(), "ws", service.ScopeWorkspace, "", ".git/config")
	if err != nil {
		t.Fatalf("ReadFileScoped .git/config: %v", err)
	}
	if res.Content != "x" {
		t.Fatalf("content = %q, want x", res.Content)
	}
}

func TestFileServiceImpl_Scoped_UnsupportedScope(t *testing.T) {
	_, err := scopedSvc(t.TempDir()).ListDirectoryScoped(context.Background(), "ws", service.FileScope("bogus"), "", "")
	wantKind(t, err, service.KindValidation)
}

func TestFileServiceImpl_Scoped_WorkspaceRejectsTarget(t *testing.T) {
	_, err := scopedSvc(t.TempDir()).ListDirectoryScoped(context.Background(), "ws", service.ScopeWorkspace, "some-repo", "")
	wantKind(t, err, service.KindValidation)
}

func TestFileServiceImpl_Scoped_WorkspaceNotCheckedOut(t *testing.T) {
	svc := NewFileService(scopedMockFileOps{wsErr: errors.New("workspace not checked out on this machine")})
	_, err := svc.ListDirectoryScoped(context.Background(), "ws", service.ScopeWorkspace, "", "")
	wantKind(t, err, service.KindNotFound)
}

func TestFileServiceImpl_PhaseA_CRUDAndVisibilityAllScopes(t *testing.T) {
	svc, scopes := setupScopedService(t)
	ctx := context.Background()

	for _, sc := range scopes {
		t.Run(sc.name, func(t *testing.T) {
			mustWrite(t, filepath.Join(sc.root, ".env"), "OLD=1")
			mustWrite(t, filepath.Join(sc.root, ".git", "config"), "git config")
			if err := os.MkdirAll(filepath.Join(sc.root, "node_modules", "pkg"), 0755); err != nil {
				t.Fatal(err)
			}

			list, err := svc.ListDirectoryScoped(ctx, "ws", sc.scope, sc.target, "")
			if err != nil {
				t.Fatalf("ListDirectoryScoped: %v", err)
			}
			names := map[string]bool{}
			for _, entry := range list.Entries {
				names[entry.Name] = true
			}
			if names[".git"] {
				t.Fatalf(".git listed in %s scope: %+v", sc.name, list.Entries)
			}
			if !names["node_modules"] {
				t.Fatalf("node_modules missing in %s scope: %+v", sc.name, list.Entries)
			}

			readEnv, err := svc.ReadFileScoped(ctx, "ws", sc.scope, sc.target, ".env")
			if err != nil {
				t.Fatalf("read .env: %v", err)
			}
			if readEnv.Content != "OLD=1" {
				t.Fatalf(".env content = %q", readEnv.Content)
			}
			if err := svc.WriteFileScoped(ctx, "ws", sc.scope, sc.target, ".env", "NEW=1"); err != nil {
				t.Fatalf("write .env: %v", err)
			}
			readEnv, err = svc.ReadFileScoped(ctx, "ws", sc.scope, sc.target, ".env")
			if err != nil {
				t.Fatalf("read written .env: %v", err)
			}
			if readEnv.Content != "NEW=1" {
				t.Fatalf("written .env content = %q", readEnv.Content)
			}

			if err := svc.WriteFileScoped(ctx, "ws", sc.scope, sc.target, ".git/config", "mutated"); err != nil {
				t.Fatalf("write .git/config: %v", err)
			}
			readGit, err := svc.ReadFileScoped(ctx, "ws", sc.scope, sc.target, ".git/config")
			if err != nil {
				t.Fatalf("read .git/config: %v", err)
			}
			if readGit.Content != "mutated" {
				t.Fatalf(".git/config content = %q", readGit.Content)
			}
			if err := svc.MkdirScoped(ctx, "ws", sc.scope, sc.target, ".git/refs/heads"); err != nil {
				t.Fatalf("mkdir .git path: %v", err)
			}
			if err := svc.MovePathScoped(ctx, "ws", sc.scope, sc.target, ".git/config", ".git/config.moved", false); err != nil {
				t.Fatalf("move .git path: %v", err)
			}
			if err := svc.DeletePathScoped(ctx, "ws", sc.scope, sc.target, ".git/config.moved", false); err != nil {
				t.Fatalf("delete .git path: %v", err)
			}

			mustWrite(t, filepath.Join(sc.root, "nonempty", "file.txt"), "x")
			err = svc.DeletePathScoped(ctx, "ws", sc.scope, sc.target, "nonempty", false)
			wantKind(t, err, service.KindConflict)
			if err := svc.DeletePathScoped(ctx, "ws", sc.scope, sc.target, "nonempty", true); err != nil {
				t.Fatalf("recursive delete: %v", err)
			}
			if _, err := os.Stat(filepath.Join(sc.root, "nonempty")); !os.IsNotExist(err) {
				t.Fatalf("nonempty dir still exists after recursive delete: %v", err)
			}

			mustWrite(t, filepath.Join(sc.root, "file-exists"), "x")
			err = svc.MkdirScoped(ctx, "ws", sc.scope, sc.target, "file-exists")
			wantKind(t, err, service.KindConflict)
			if err := svc.MkdirScoped(ctx, "ws", sc.scope, sc.target, "new/dir"); err != nil {
				t.Fatalf("mkdir nested: %v", err)
			}

			mustWrite(t, filepath.Join(sc.root, "move-src.txt"), "src")
			mustWrite(t, filepath.Join(sc.root, "move-dst.txt"), "dst")
			err = svc.MovePathScoped(ctx, "ws", sc.scope, sc.target, "move-src.txt", "move-dst.txt", false)
			wantKind(t, err, service.KindConflict)
			if err := svc.MovePathScoped(ctx, "ws", sc.scope, sc.target, "move-src.txt", "move-dst.txt", true); err != nil {
				t.Fatalf("move overwrite: %v", err)
			}
			got, err := os.ReadFile(filepath.Join(sc.root, "move-dst.txt"))
			if err != nil {
				t.Fatalf("read overwritten dest: %v", err)
			}
			if string(got) != "src" {
				t.Fatalf("move dest content = %q, want src", string(got))
			}

			large := strings.Repeat("A", maxRequestBody+25)
			mustWrite(t, filepath.Join(sc.root, "large.txt"), large)
			readLarge, err := svc.ReadFileScoped(ctx, "ws", sc.scope, sc.target, "large.txt")
			if err != nil {
				t.Fatalf("read large: %v", err)
			}
			if !readLarge.Truncated {
				t.Fatalf("large read truncated = false")
			}
			if len(readLarge.Content) != maxRequestBody {
				t.Fatalf("large content len = %d, want %d", len(readLarge.Content), maxRequestBody)
			}
		})
	}
}

func TestFileServiceImpl_PhaseA_StructuralGuardsAllScopes(t *testing.T) {
	svc, scopes := setupScopedService(t)
	ctx := context.Background()

	for _, sc := range scopes {
		t.Run(sc.name, func(t *testing.T) {
			mustWrite(t, filepath.Join(sc.root, "existing.txt"), "x")
			mustWrite(t, filepath.Join(sc.root, "target.txt"), "target")
			mustWrite(t, filepath.Join(filepath.Dir(sc.root), "outside.txt"), "outside")
			if err := os.Symlink(filepath.Join(sc.root, "existing.txt"), filepath.Join(sc.root, "link.txt")); err != nil {
				t.Skip("cannot create symlinks on this platform")
			}

			_, err := svc.ReadFileScoped(ctx, "ws", sc.scope, sc.target, "../outside.txt")
			wantKind(t, err, service.KindForbidden)
			err = svc.WriteFileScoped(ctx, "ws", sc.scope, sc.target, "../outside.txt", "bad")
			wantKind(t, err, service.KindForbidden)
			err = svc.DeletePathScoped(ctx, "ws", sc.scope, sc.target, "../outside.txt", false)
			wantKind(t, err, service.KindForbidden)
			err = svc.MkdirScoped(ctx, "ws", sc.scope, sc.target, "../outside-dir")
			wantKind(t, err, service.KindForbidden)
			err = svc.MovePathScoped(ctx, "ws", sc.scope, sc.target, "existing.txt", "../outside-move.txt", false)
			wantKind(t, err, service.KindForbidden)

			_, err = svc.ReadFileScoped(ctx, "ws", sc.scope, sc.target, "link.txt")
			wantKind(t, err, service.KindForbidden)
			err = svc.WriteFileScoped(ctx, "ws", sc.scope, sc.target, "link.txt", "bad")
			wantKind(t, err, service.KindForbidden)
			err = svc.DeletePathScoped(ctx, "ws", sc.scope, sc.target, "link.txt", false)
			wantKind(t, err, service.KindForbidden)
			err = svc.MkdirScoped(ctx, "ws", sc.scope, sc.target, "link.txt")
			wantKind(t, err, service.KindForbidden)
			err = svc.MovePathScoped(ctx, "ws", sc.scope, sc.target, "link.txt", "moved-link.txt", false)
			wantKind(t, err, service.KindForbidden)
		})
	}
}

func TestFileServiceImpl_PhaseA_InvalidTargetsRejected(t *testing.T) {
	svc, _ := setupScopedService(t)
	ctx := context.Background()

	_, err := svc.ListDirectoryScoped(ctx, "ws", service.ScopeRepo, "missing-repo", "")
	wantKind(t, err, service.KindNotFound)
	_, err = svc.ListDirectoryScoped(ctx, "ws", service.ScopeAgent, "missing-agent", "")
	wantKind(t, err, service.KindNotFound)
}

func TestResolveScopedContainingCheckout(t *testing.T) {
	svc, scopes := setupScopedService(t)
	impl := svc.(*fileServiceImpl)
	for _, sc := range scopes {
		initGitRepo(t, sc.root)
		mustWrite(t, filepath.Join(sc.root, "src", "file.txt"), "body")
		commitAll(t, sc.root)
	}

	ctx := context.Background()
	_ = ctx
	for _, sc := range scopes {
		t.Run(sc.name, func(t *testing.T) {
			_, cleanPath, checkout, err := impl.resolveScopedContainingCheckout("ws", sc.scope, sc.target, "src/file.txt")
			if err != nil {
				t.Fatalf("resolveScopedContainingCheckout: %v", err)
			}
			if cleanPath != "src/file.txt" {
				t.Fatalf("cleanPath = %q", cleanPath)
			}
			if checkout.root != sc.root {
				t.Fatalf("checkout root = %q, want %q", checkout.root, sc.root)
			}
			if checkout.relPath != "src/file.txt" {
				t.Fatalf("checkout relPath = %q", checkout.relPath)
			}
		})
	}

	_, _, _, err := impl.resolveScopedContainingCheckout("ws", service.ScopeWorkspace, "", "../outside.txt")
	wantKind(t, err, service.KindForbidden)
}

func TestFileServiceImpl_ReadFileAtRevScoped(t *testing.T) {
	svc, scopes := setupScopedService(t)
	repo := scopes[1]
	initGitRepo(t, repo.root)
	mustWrite(t, filepath.Join(repo.root, "file.txt"), "committed\n")
	commitAll(t, repo.root)
	mustWrite(t, filepath.Join(repo.root, "file.txt"), "working\n")

	res, err := svc.ReadFileAtRevScoped(context.Background(), "ws", repo.scope, repo.target, "file.txt", "HEAD")
	if err != nil {
		t.Fatalf("ReadFileAtRevScoped: %v", err)
	}
	if res.Content != "committed\n" {
		t.Fatalf("content = %q, want committed", res.Content)
	}

	large := strings.Repeat("A", maxRequestBody+25)
	mustWrite(t, filepath.Join(repo.root, "large.txt"), large)
	mustGit(t, repo.root, "add", "large.txt")
	mustGit(t, repo.root, "commit", "-m", "large")
	res, err = svc.ReadFileAtRevScoped(context.Background(), "ws", repo.scope, repo.target, "large.txt", "HEAD")
	if err != nil {
		t.Fatalf("ReadFileAtRevScoped large: %v", err)
	}
	if !res.Truncated {
		t.Fatalf("large truncated = false")
	}
	if len(res.Content) != maxRequestBody {
		t.Fatalf("large content len = %d, want %d", len(res.Content), maxRequestBody)
	}
}

func TestFileServiceImpl_DiffFileScoped(t *testing.T) {
	svc, scopes := setupScopedService(t)
	repo := scopes[1]
	initGitRepo(t, repo.root)
	mustWrite(t, filepath.Join(repo.root, "file.txt"), "one\n")
	commitAll(t, repo.root)
	mustWrite(t, filepath.Join(repo.root, "file.txt"), "two\n")

	res, err := svc.DiffFileScoped(context.Background(), "ws", repo.scope, repo.target, "file.txt", "HEAD", "")
	if err != nil {
		t.Fatalf("DiffFileScoped working: %v", err)
	}
	if !strings.Contains(res.Patch, "-one") || !strings.Contains(res.Patch, "+two") {
		t.Fatalf("working diff missing expected lines:\n%s", res.Patch)
	}

	mustGit(t, repo.root, "add", "file.txt")
	mustGit(t, repo.root, "commit", "-m", "second")
	first := strings.TrimSpace(gitOutput(t, repo.root, "rev-parse", "HEAD^"))
	second := strings.TrimSpace(gitOutput(t, repo.root, "rev-parse", "HEAD"))
	res, err = svc.DiffFileScoped(context.Background(), "ws", repo.scope, repo.target, "file.txt", first, second)
	if err != nil {
		t.Fatalf("DiffFileScoped rev..rev: %v", err)
	}
	if !strings.Contains(res.Patch, "-one") || !strings.Contains(res.Patch, "+two") {
		t.Fatalf("rev diff missing expected lines:\n%s", res.Patch)
	}
}

func TestFileServiceImpl_BlameFileScoped(t *testing.T) {
	svc, scopes := setupScopedService(t)
	repo := scopes[1]
	initGitRepo(t, repo.root)
	mustWrite(t, filepath.Join(repo.root, "file.txt"), "one\ntwo\n")
	commitAll(t, repo.root)

	res, err := svc.BlameFileScoped(context.Background(), "ws", repo.scope, repo.target, "file.txt")
	if err != nil {
		t.Fatalf("BlameFileScoped: %v", err)
	}
	if res.Skipped || len(res.Lines) == 0 {
		t.Fatalf("blame skipped=%v lines=%+v", res.Skipped, res.Lines)
	}
	if res.Lines[0].Author != "Test User" || res.Lines[0].Summary != "init" {
		t.Fatalf("unexpected blame line: %+v", res.Lines[0])
	}

	mustWrite(t, filepath.Join(repo.root, "too-large.txt"), strings.Repeat("A", maxRequestBody+1))
	res, err = svc.BlameFileScoped(context.Background(), "ws", repo.scope, repo.target, "too-large.txt")
	if err != nil {
		t.Fatalf("BlameFileScoped large: %v", err)
	}
	if !res.Skipped || res.Reason != "too_large" {
		t.Fatalf("large blame = %+v, want too_large skip", res)
	}
}

func TestFileServiceImpl_HistoryMergesCommitsAndBrowserSaves(t *testing.T) {
	svc, scopes := setupScopedService(t)
	repo := scopes[1]
	initGitRepo(t, repo.root)
	mustWrite(t, filepath.Join(repo.root, "file.txt"), "committed\n")
	commitAll(t, repo.root)

	if err := svc.WriteFileScoped(context.Background(), "ws", repo.scope, repo.target, "file.txt", "browser save\n"); err != nil {
		t.Fatalf("WriteFileScoped overwrite: %v", err)
	}
	history, err := svc.HistoryFileScoped(context.Background(), "ws", repo.scope, repo.target, "file.txt")
	if err != nil {
		t.Fatalf("HistoryFileScoped: %v", err)
	}
	var sawCommit, sawSave bool
	for _, entry := range history.Entries {
		if entry.Kind == "commit" && entry.Summary == "init" {
			sawCommit = true
		}
		if entry.Kind == "save" && entry.Content == "committed\n" {
			sawSave = true
		}
	}
	if !sawCommit || !sawSave {
		t.Fatalf("history entries = %+v, want commit and browser save", history.Entries)
	}
}

func TestFileServiceImpl_SaveHistorySnapshotRules(t *testing.T) {
	svc, scopes := setupScopedService(t)
	impl := svc.(*fileServiceImpl)
	repo := scopes[1]

	if err := svc.WriteFileScoped(context.Background(), "ws", repo.scope, repo.target, "file.txt", "0"); err != nil {
		t.Fatalf("create: %v", err)
	}
	entries, err := impl.loadSaveHistory("ws", repo.scope, repo.target, "file.txt")
	if err != nil {
		t.Fatalf("loadSaveHistory after create: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("create recorded save history: %+v", entries)
	}

	for i := 1; i <= fileHistorySaveLimit+5; i++ {
		if err := svc.WriteFileScoped(context.Background(), "ws", repo.scope, repo.target, "file.txt", strconv.Itoa(i)); err != nil {
			t.Fatalf("overwrite %d: %v", i, err)
		}
	}
	entries, err = impl.loadSaveHistory("ws", repo.scope, repo.target, "file.txt")
	if err != nil {
		t.Fatalf("loadSaveHistory: %v", err)
	}
	saves := entries
	if len(saves) != fileHistorySaveLimit {
		t.Fatalf("save count = %d, want %d: %+v", len(saves), fileHistorySaveLimit, saves)
	}
	for _, entry := range saves {
		if entry.Content == "0" || entry.Content == "1" || entry.Content == "2" || entry.Content == "3" || entry.Content == "4" {
			t.Fatalf("old snapshot was not evicted: %+v", saves)
		}
	}
}

func TestFileServiceImpl_IndexFilesScoped_DefaultExcludesSymlinksAndTruncates(t *testing.T) {
	withNavigationCaps(t, func() {
		fileIndexMaxEntries = 2
		fileIndexWalkBudget = time.Minute
		dir := t.TempDir()
		mustWrite(t, filepath.Join(dir, "a.txt"), "a")
		mustWrite(t, filepath.Join(dir, "b.txt"), "b")
		mustWrite(t, filepath.Join(dir, "c.txt"), "c")
		mustWrite(t, filepath.Join(dir, ".git", "config"), "git")
		mustWrite(t, filepath.Join(dir, "node_modules", "pkg", "index.js"), "pkg")
		if err := os.Symlink(filepath.Join(dir, "a.txt"), filepath.Join(dir, "link.txt")); err != nil {
			t.Logf("symlink unavailable: %v", err)
		}

		res, err := scopedSvc(dir).IndexFilesScoped(context.Background(), "ws", service.ScopeWorkspace, "")
		if err != nil {
			t.Fatalf("IndexFilesScoped: %v", err)
		}
		if !res.Truncated {
			t.Fatalf("truncated = false, want true")
		}
		for _, forbidden := range []string{".git/config", "node_modules/pkg/index.js", "link.txt"} {
			if containsPath(res.Paths, forbidden) {
				t.Fatalf("index included %s: %+v", forbidden, res.Paths)
			}
		}
		if len(res.Paths) != 2 {
			t.Fatalf("paths len = %d, want cap 2: %+v", len(res.Paths), res.Paths)
		}
	})
}

func TestFileServiceImpl_SearchFilesScoped_DefaultExcludesAndOverride(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "src", "main.go"), "needle\n")
	mustWrite(t, filepath.Join(dir, "src", "generated.go"), "needle\n")
	mustWrite(t, filepath.Join(dir, ".git", "config"), "needle\n")
	mustWrite(t, filepath.Join(dir, "node_modules", "pkg", "index.js"), "needle\n")

	svc := scopedSvc(dir)
	ctx := context.Background()
	res, err := svc.SearchFilesScoped(ctx, "ws", service.ScopeWorkspace, "", service.FileSearchRequest{Query: "needle"})
	if err != nil {
		t.Fatalf("SearchFilesScoped default: %v", err)
	}
	if got := resultPaths(res.Results); strings.Join(got, ",") != "src/generated.go,src/main.go" {
		t.Fatalf("default search paths = %+v", got)
	}

	emptyExclude := []string{}
	res, err = svc.SearchFilesScoped(ctx, "ws", service.ScopeWorkspace, "", service.FileSearchRequest{
		Query:   "needle",
		Exclude: &emptyExclude,
	})
	if err != nil {
		t.Fatalf("SearchFilesScoped override excludes: %v", err)
	}
	got := resultPaths(res.Results)
	for _, want := range []string{".git/config", "node_modules/pkg/index.js"} {
		if !containsPath(got, want) {
			t.Fatalf("override search missing %s: %+v", want, got)
		}
	}

	excludeGenerated := []string{"src/generated.go"}
	res, err = svc.SearchFilesScoped(ctx, "ws", service.ScopeWorkspace, "", service.FileSearchRequest{
		Query:   "needle",
		Include: []string{"src/*.go"},
		Exclude: &excludeGenerated,
	})
	if err != nil {
		t.Fatalf("SearchFilesScoped include/exclude: %v", err)
	}
	if got := resultPaths(res.Results); strings.Join(got, ",") != "src/main.go" {
		t.Fatalf("include/exclude search paths = %+v", got)
	}
}

func TestFileServiceImpl_SearchFilesScoped_SkipsSymlinksAndBinaries(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "plain.txt"), "needle\n")
	mustWrite(t, filepath.Join(dir, "binary.dat"), string([]byte{'n', 'e', 0, 'e'}))
	if err := os.Symlink(filepath.Join(dir, "plain.txt"), filepath.Join(dir, "link.txt")); err != nil {
		t.Logf("symlink unavailable: %v", err)
	}

	res, err := scopedSvc(dir).SearchFilesScoped(context.Background(), "ws", service.ScopeWorkspace, "", service.FileSearchRequest{Query: "needle"})
	if err != nil {
		t.Fatalf("SearchFilesScoped: %v", err)
	}
	got := resultPaths(res.Results)
	if strings.Join(got, ",") != "plain.txt" {
		t.Fatalf("search paths = %+v, want plain.txt only", got)
	}
}

func TestFileServiceImpl_SearchFilesScoped_LimitHitCaps(t *testing.T) {
	t.Run("files", func(t *testing.T) {
		withNavigationCaps(t, func() {
			fileSearchMaxFiles = 1
			fileSearchMaxBytes = 1 << 20
			fileSearchMaxMatches = 10
			dir := t.TempDir()
			mustWrite(t, filepath.Join(dir, "a.txt"), "needle\n")
			mustWrite(t, filepath.Join(dir, "b.txt"), "needle\n")
			res, err := scopedSvc(dir).SearchFilesScoped(context.Background(), "ws", service.ScopeWorkspace, "", service.FileSearchRequest{Query: "needle"})
			if err != nil {
				t.Fatalf("SearchFilesScoped: %v", err)
			}
			if !res.LimitHit {
				t.Fatalf("LimitHit = false, want true")
			}
		})
	})
	t.Run("bytes", func(t *testing.T) {
		withNavigationCaps(t, func() {
			fileSearchMaxFiles = 10
			fileSearchMaxBytes = 3
			fileSearchMaxMatches = 10
			dir := t.TempDir()
			mustWrite(t, filepath.Join(dir, "a.txt"), "needle\n")
			res, err := scopedSvc(dir).SearchFilesScoped(context.Background(), "ws", service.ScopeWorkspace, "", service.FileSearchRequest{Query: "needle"})
			if err != nil {
				t.Fatalf("SearchFilesScoped: %v", err)
			}
			if !res.LimitHit {
				t.Fatalf("LimitHit = false, want true")
			}
		})
	})
	t.Run("matches", func(t *testing.T) {
		withNavigationCaps(t, func() {
			fileSearchMaxFiles = 10
			fileSearchMaxBytes = 1 << 20
			fileSearchMaxMatches = 1
			dir := t.TempDir()
			mustWrite(t, filepath.Join(dir, "a.txt"), "needle needle\n")
			res, err := scopedSvc(dir).SearchFilesScoped(context.Background(), "ws", service.ScopeWorkspace, "", service.FileSearchRequest{Query: "needle"})
			if err != nil {
				t.Fatalf("SearchFilesScoped: %v", err)
			}
			if !res.LimitHit {
				t.Fatalf("LimitHit = false, want true")
			}
			if len(res.Results) != 1 || len(res.Results[0].Matches) != 1 {
				t.Fatalf("matches = %+v, want one capped match", res.Results)
			}
		})
	})
}

func TestFileServiceImpl_IndexAndSearch_ScopeTargeting(t *testing.T) {
	svc, scopes := setupScopedService(t)
	for _, sc := range scopes {
		mustWrite(t, filepath.Join(sc.root, sc.name+".txt"), "target-"+sc.name+"\n")
	}
	ctx := context.Background()
	for _, sc := range scopes {
		t.Run(sc.name, func(t *testing.T) {
			index, err := svc.IndexFilesScoped(ctx, "ws", sc.scope, sc.target)
			if err != nil {
				t.Fatalf("IndexFilesScoped: %v", err)
			}
			if !containsPath(index.Paths, sc.name+".txt") {
				t.Fatalf("index paths = %+v, missing scoped file", index.Paths)
			}

			search, err := svc.SearchFilesScoped(ctx, "ws", sc.scope, sc.target, service.FileSearchRequest{Query: "target-" + sc.name})
			if err != nil {
				t.Fatalf("SearchFilesScoped: %v", err)
			}
			if got := resultPaths(search.Results); !containsPath(got, sc.name+".txt") {
				t.Fatalf("search paths = %+v, missing scoped file", got)
			}
		})
	}
}

func TestFileServiceImpl_IndexCacheInvalidatedAfterCRUD(t *testing.T) {
	root := t.TempDir()
	svc := scopedSvc(root)
	ctx := context.Background()
	if err := svc.WriteFileScoped(ctx, "ws", service.ScopeWorkspace, "", "one.txt", "1"); err != nil {
		t.Fatalf("write one: %v", err)
	}
	index, err := svc.IndexFilesScoped(ctx, "ws", service.ScopeWorkspace, "")
	if err != nil {
		t.Fatalf("index one: %v", err)
	}
	if !containsPath(index.Paths, "one.txt") {
		t.Fatalf("index missing one.txt: %+v", index.Paths)
	}

	if err := svc.WriteFileScoped(ctx, "ws", service.ScopeWorkspace, "", "two.txt", "2"); err != nil {
		t.Fatalf("write two: %v", err)
	}
	index, err = svc.IndexFilesScoped(ctx, "ws", service.ScopeWorkspace, "")
	if err != nil {
		t.Fatalf("index after write: %v", err)
	}
	if !containsPath(index.Paths, "two.txt") {
		t.Fatalf("write did not invalidate index cache: %+v", index.Paths)
	}

	if err := svc.MkdirScoped(ctx, "ws", service.ScopeWorkspace, "", "dir"); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	mustWrite(t, filepath.Join(root, "dir", "three.txt"), "3")
	index, err = svc.IndexFilesScoped(ctx, "ws", service.ScopeWorkspace, "")
	if err != nil {
		t.Fatalf("index after mkdir/write: %v", err)
	}
	if !containsPath(index.Paths, "dir/three.txt") {
		t.Fatalf("mkdir did not invalidate index cache: %+v", index.Paths)
	}

	if err := svc.MovePathScoped(ctx, "ws", service.ScopeWorkspace, "", "two.txt", "moved.txt", false); err != nil {
		t.Fatalf("move: %v", err)
	}
	index, err = svc.IndexFilesScoped(ctx, "ws", service.ScopeWorkspace, "")
	if err != nil {
		t.Fatalf("index after move: %v", err)
	}
	if containsPath(index.Paths, "two.txt") || !containsPath(index.Paths, "moved.txt") {
		t.Fatalf("move did not invalidate index cache: %+v", index.Paths)
	}

	if err := svc.DeletePathScoped(ctx, "ws", service.ScopeWorkspace, "", "one.txt", false); err != nil {
		t.Fatalf("delete: %v", err)
	}
	index, err = svc.IndexFilesScoped(ctx, "ws", service.ScopeWorkspace, "")
	if err != nil {
		t.Fatalf("index after delete: %v", err)
	}
	if containsPath(index.Paths, "one.txt") {
		t.Fatalf("delete did not invalidate index cache: %+v", index.Paths)
	}
}

func TestFileServiceImpl_GitStatusScoped_TargetScopes(t *testing.T) {
	ctx := context.Background()
	svc, scopes := setupScopedService(t)
	for _, sc := range scopes {
		if sc.scope == service.ScopeWorkspace {
			continue
		}
		initGitRepo(t, sc.root)
		mustWrite(t, filepath.Join(sc.root, "tracked.txt"), "one\n")
		commitAll(t, sc.root)
		mustWrite(t, filepath.Join(sc.root, "tracked.txt"), "two\n")

		status, err := svc.GitStatusScoped(ctx, "ws", sc.scope, sc.target)
		if err != nil {
			t.Fatalf("%s GitStatusScoped: %v", sc.name, err)
		}
		if got := status["tracked.txt"]; got != " M" {
			t.Fatalf("%s status[tracked.txt] = %q, want %q; full=%#v", sc.name, got, " M", status)
		}
		if _, ok := status[filepath.ToSlash(filepath.Join(filepath.Base(sc.root), "tracked.txt"))]; ok {
			t.Fatalf("%s status should be scope-relative, got %#v", sc.name, status)
		}
	}
}

func TestFileServiceImpl_GitStatusScoped_WorkspaceFanoutPrefixes(t *testing.T) {
	ctx := context.Background()
	svc, scopes := setupScopedService(t)
	var repoRoot, agentRoot string
	for _, sc := range scopes {
		switch sc.scope {
		case service.ScopeRepo:
			repoRoot = sc.root
		case service.ScopeAgent:
			agentRoot = sc.root
		}
	}

	initGitRepo(t, repoRoot)
	mustWrite(t, filepath.Join(repoRoot, "tracked.txt"), "one\n")
	commitAll(t, repoRoot)
	mustWrite(t, filepath.Join(repoRoot, "tracked.txt"), "two\n")

	initGitRepo(t, agentRoot)
	mustWrite(t, filepath.Join(agentRoot, "new.txt"), "new\n")

	status, err := svc.GitStatusScoped(ctx, "ws", service.ScopeWorkspace, "")
	if err != nil {
		t.Fatalf("GitStatusScoped workspace: %v", err)
	}
	if got := status["repo-a/tracked.txt"]; got != " M" {
		t.Fatalf("status[repo-a/tracked.txt] = %q, want %q; full=%#v", got, " M", status)
	}
	if got := status["worktrees/repo-a/agent-a/new.txt"]; got != "??" {
		t.Fatalf("status[worktrees/repo-a/agent-a/new.txt] = %q, want %q; full=%#v", got, "??", status)
	}
	if _, ok := status["tracked.txt"]; ok {
		t.Fatalf("workspace status should prefix checkout paths, got %#v", status)
	}
}

func TestFileServiceImpl_GitStatusScoped_InvalidTargets(t *testing.T) {
	ctx := context.Background()
	svc, _ := setupScopedService(t)

	_, err := svc.GitStatusScoped(ctx, "ws", service.ScopeRepo, "missing")
	wantKind(t, err, service.KindNotFound)

	_, err = svc.GitStatusScoped(ctx, "ws", service.ScopeAgent, "missing")
	wantKind(t, err, service.KindNotFound)

	_, err = svc.GitStatusScoped(ctx, "ws", service.ScopeWorkspace, "repo-a")
	wantKind(t, err, service.KindValidation)
}
