package filesystem

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// scopedMockFileOps is a minimal FileMechanics that only resolves the workspace
// root, so these tests exercise the real fileServiceImpl scoped code paths.
type scopedMockFileOps struct {
	wsRoot        string
	repoRoot      string
	agentRoot     string
	agentRoots    map[string]string
	agentRepoErrs map[string]error
	wsData        *WorkspaceTopology
	dataDir       string
	wsErr         error
	gitStatusFunc func(context.Context, string) (GitFileStatusResult, error)
	gitShowFunc   func(context.Context, string, string, string, int64) (*GitFileContentAtRev, error)
	branchFunc    func(context.Context, string) (string, error)
}

func (m scopedMockFileOps) ResolveAgentWorktree(_, name string) (*Worktree, error) {
	if name != "agent-a" || m.agentRoot == "" {
		return nil, errors.New("agent worktree not found")
	}
	return &Worktree{Name: name, Path: m.agentRoot, RepoName: "repo-a"}, nil
}
func (m scopedMockFileOps) ResolveAgentWorktreeForRepo(_, name, repo string) (*Worktree, error) {
	if name != "agent-a" {
		return nil, errors.New("agent worktree not found")
	}
	if m.agentRepoErrs != nil {
		if err := m.agentRepoErrs[repo]; err != nil {
			return nil, err
		}
	}
	if m.agentRoots != nil {
		root := m.agentRoots[repo]
		if root == "" {
			return nil, ErrAgentWorktreeNotFound
		}
		return &Worktree{Name: name, Path: root, RepoName: repo}, nil
	}
	if repo == "repo-a" && m.agentRoot != "" {
		return &Worktree{Name: name, Path: m.agentRoot, RepoName: repo}, nil
	}
	return nil, ErrAgentWorktreeNotFound
}
func (m scopedMockFileOps) ResolveWorkspaceRoot(_ string) (string, error) {
	if m.wsErr != nil {
		return "", m.wsErr
	}
	return m.wsRoot, nil
}
func (m scopedMockFileOps) ResolveWorkspaceData(_ string) (*WorkspaceTopology, error) {
	if m.wsErr != nil {
		return nil, m.wsErr
	}
	if m.wsData != nil {
		return m.wsData, nil
	}
	ws := &WorkspaceTopology{
		ID:   "ws",
		Path: m.wsRoot,
		Repos: []WorkspaceRepo{{
			Name: "repo-a",
			Path: m.repoRoot,
		}},
		Agents: []WorkspaceAgent{{Name: "agent-a"}},
	}
	return ws, nil
}

func (m scopedMockFileOps) GitStatusPorcelain(ctx context.Context, worktreePath string) (GitFileStatusResult, error) {
	if m.gitStatusFunc != nil {
		return m.gitStatusFunc(ctx, worktreePath)
	}
	cmd := exec.Command("git", "-C", worktreePath, "status", "--porcelain") //nolint:norawexec // Test mock intentionally exercises real git status output.
	out, err := cmd.Output()
	if err != nil {
		return GitFileStatusResult{}, err
	}
	return GitFileStatusResult{Entries: parseTestPorcelainStatus(string(out))}, nil
}

func (m scopedMockFileOps) GitShowFileAtRev(ctx context.Context, worktreePath, rev, path string, maxBytes int64) (*GitFileContentAtRev, error) {
	if m.gitShowFunc != nil {
		return m.gitShowFunc(ctx, worktreePath, rev, path, maxBytes)
	}
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
	return &GitFileContentAtRev{Content: out, Size: size, Truncated: truncated}, nil
}

func (m scopedMockFileOps) GitDiffFile(_ context.Context, worktreePath, path, from, to string) (GitBoundedTextResult, error) {
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
	return GitBoundedTextResult{Output: string(out)}, err
}

func (m scopedMockFileOps) GitLogFile(_ context.Context, worktreePath, path string, limit int) (GitBoundedTextResult, error) {
	out, err := exec.Command("git", "-C", worktreePath, "log", "--follow", "-n", strconv.Itoa(limit), "--format=%H%x00%an%x00%at%x00%s%x00", "--", path).CombinedOutput() //nolint:norawexec // Test mock runs fixed git command.
	return GitBoundedTextResult{Output: string(out)}, err
}

func (m scopedMockFileOps) GitBlamePorcelain(_ context.Context, worktreePath, path string) (GitBoundedTextResult, error) {
	out, err := exec.Command("git", "-C", worktreePath, "blame", "--porcelain", "--", path).CombinedOutput() //nolint:norawexec // Test mock runs fixed git command.
	return GitBoundedTextResult{Output: string(out)}, err
}

func (m scopedMockFileOps) ResolveLoomDataDir() (string, error) {
	if m.dataDir != "" {
		return m.dataDir, nil
	}
	return "", errors.New("loom data directory not configured")
}

func (m scopedMockFileOps) GitCurrentBranch(ctx context.Context, worktreePath string) (string, error) {
	if m.branchFunc != nil {
		return m.branchFunc(ctx, worktreePath)
	}
	out, err := exec.Command("git", "-C", worktreePath, "rev-parse", "--abbrev-ref", "HEAD").Output() //nolint:norawexec // Test mock runs fixed git command.
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

func (m scopedMockFileOps) RepairCheckout(_, _, _, _ string, _ bool) (RepairResult, error) {
	return RepairResult{Repaired: false, Method: "none", Message: "not implemented"}, nil
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

func scopedSvc(root string) *fileServiceImpl {
	return newFileService(scopedMockFileOps{wsRoot: root})
}

func writeScopedFile(ctx context.Context, svc *fileServiceImpl, wsID string, scope FileScope, target, repo, path, content string) error {
	_, err := svc.WriteFileConditionalScoped(ctx, wsID, scope, target, repo, path, content, FileWritePreconditions{})
	return err
}

func mustScopedVersion(t *testing.T, svc *fileServiceImpl, ctx context.Context, scope FileScope, target, path string) string {
	t.Helper()
	result, err := svc.StatPathScoped(ctx, "ws", scope, target, "", path)
	if err != nil {
		t.Fatalf("stat %q for version: %v", path, err)
	}
	return result.Version
}

func TestFileServiceImpl_ViewerFiltersSensitiveSurfaces(t *testing.T) {
	root := t.TempDir()
	for path, content := range map[string]string{
		"readme.txt":        "needle public",
		".env":              "needle secret env",
		"config/.env.local": "needle secret local",
		"keys/server.pem":   "needle secret pem",
		".ssh/id_ed25519":   "needle secret ssh",
		"home/.netrc":       "needle secret netrc",
	} {
		mustWrite(t, filepath.Join(root, path), content)
	}
	svc := scopedSvc(root)
	viewer := context.Background()

	tree, err := svc.ListDirectoryScoped(viewer, "ws", ScopeWorkspace, "", "", ".")
	if err != nil {
		t.Fatalf("ListDirectoryScoped: %v", err)
	}
	for _, entry := range tree.Entries {
		if IsSensitiveFilePath(entry.Name) {
			t.Fatalf("tree exposed sensitive entry %q", entry.Name)
		}
	}

	if _, err := svc.ReadFileScoped(viewer, "ws", ScopeWorkspace, "", "", ".env"); err == nil {
		t.Fatal("viewer read of .env succeeded")
	} else if serviceErr, ok := err.(*Failure); !ok || serviceErr.Kind != failureForbidden {
		t.Fatalf("viewer read error = %T %v, want forbidden", err, err)
	}

	index, err := svc.IndexFilesScoped(viewer, "ws", ScopeWorkspace, "", "")
	if err != nil {
		t.Fatalf("IndexFilesScoped: %v", err)
	}
	if len(index.Paths) != 1 || index.Paths[0] != "readme.txt" {
		t.Fatalf("viewer index = %v, want only readme.txt", index.Paths)
	}

	search, err := svc.SearchFilesScoped(viewer, "ws", ScopeWorkspace, "", "", FileSearchRequest{
		Query:   "needle",
		Exclude: &[]string{},
	})
	if err != nil {
		t.Fatalf("SearchFilesScoped: %v", err)
	}
	if len(search.Results) != 1 || search.Results[0].Path != "readme.txt" {
		t.Fatalf("viewer search = %+v, want only readme.txt", search.Results)
	}
}

func TestFileServiceImpl_EditorCanAccessSensitiveFiles(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, ".env.production"), "TOKEN=secret")
	svc := scopedSvc(root)
	editor := context.Background()
	access := fileAccessFromGrant(NewAccessGrantIssuer().ReadWrite(true))

	read, err := svc.readFileScoped(editor, "ws", ScopeWorkspace, "", "", ".env.production", access)
	if err != nil || read.Content != "TOKEN=secret" {
		t.Fatalf("editor read = %+v, err=%v", read, err)
	}
	if _, err := svc.writeFileConditionalScoped(editor, "ws", ScopeWorkspace, "", "", ".env.production", "TOKEN=updated", FileWritePreconditions{}, access); err != nil {
		t.Fatalf("editor write: %v", err)
	}
}

func TestFileServiceImpl_ViewerFiltersGitStatusAndCheckoutCounts(t *testing.T) {
	wsRoot := t.TempDir()
	repoRoot := filepath.Join(wsRoot, "repo-a")
	if err := os.MkdirAll(repoRoot, 0755); err != nil {
		t.Fatal(err)
	}
	initGitRepo(t, repoRoot)
	mustWrite(t, filepath.Join(repoRoot, "public.txt"), "public")
	mustWrite(t, filepath.Join(repoRoot, ".env"), "secret")
	svc := newFileService(scopedMockFileOps{
		wsRoot:   wsRoot,
		repoRoot: repoRoot,
		wsData: &WorkspaceTopology{
			ID:    "ws",
			Path:  wsRoot,
			Repos: []WorkspaceRepo{{Name: "repo-a", Path: repoRoot}},
		},
	})
	viewer := context.Background()

	status, err := svc.GitStatusScoped(viewer, "ws", ScopeRepo, "repo-a", "")
	if err != nil {
		t.Fatalf("GitStatusScoped: %v", err)
	}
	if _, ok := status.Status[".env"]; ok || status.Status["public.txt"] != "??" {
		t.Fatalf("viewer status = %#v", status)
	}

	checkouts, err := svc.ListFileCheckouts(viewer, "ws")
	if err != nil {
		t.Fatalf("ListFileCheckouts: %v", err)
	}
	if len(checkouts.Checkouts) != 1 || checkouts.Checkouts[0].ChangeCount != 1 {
		t.Fatalf("viewer checkouts = %+v, want one public change", checkouts.Checkouts)
	}
}

type scopedCase struct {
	name   string
	scope  FileScope
	target string
	root   string
}

func setupScopedService(t *testing.T) (*fileServiceImpl, []scopedCase) {
	t.Helper()
	wsRoot := t.TempDir()
	repoRoot := filepath.Join(wsRoot, "repo-a")
	agentRoot := filepath.Join(wsRoot, "worktrees", "repo-a", "agent-a")
	for _, dir := range []string{repoRoot, agentRoot} {
		if err := os.MkdirAll(dir, 0755); err != nil {
			t.Fatal(err)
		}
	}
	svc := newFileService(scopedMockFileOps{
		wsRoot:    wsRoot,
		repoRoot:  repoRoot,
		agentRoot: agentRoot,
	})
	return svc, []scopedCase{
		{name: "workspace", scope: ScopeWorkspace, root: wsRoot},
		{name: "repo", scope: ScopeRepo, target: "repo-a", root: repoRoot},
		{name: "agent", scope: ScopeAgent, target: "agent-a", root: agentRoot},
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

func wantKind(t *testing.T, err error, kind failureKind) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected error of kind %q, got nil", kind)
	}
	var svcErr *Failure
	if !errors.As(err, &svcErr) {
		t.Fatalf("error = %T %v, want *Failure", err, err)
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

func resultPaths(results []FileSearchFileResult) []string {
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

	res, err := scopedSvc(dir).ListDirectoryScoped(context.Background(), "ws", ScopeWorkspace, "", "", "")
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

func TestFileServiceImpl_ListDirectoryScoped_HidesCaseVariantGit(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".GIT"), 0755); err != nil {
		t.Fatal(err)
	}
	res, err := scopedSvc(dir).ListDirectoryScoped(context.Background(), "ws", ScopeWorkspace, "", "", "")
	if err != nil {
		t.Fatalf("ListDirectoryScoped: %v", err)
	}
	for _, entry := range res.Entries {
		if strings.EqualFold(entry.Name, ".git") {
			t.Fatalf("case-variant .git listed: %+v", res.Entries)
		}
	}
}

func TestRootedFileStore_RemainsAnchoredWhenScopePathIsReplaced(t *testing.T) {
	parent := t.TempDir()
	rootPath := filepath.Join(parent, "scope")
	if err := os.Mkdir(rootPath, 0755); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, filepath.Join(rootPath, "file.txt"), "original")
	root, err := openScopedRoot(rootPath)
	if err != nil {
		t.Fatalf("openScopedRoot: %v", err)
	}
	defer root.Close()

	movedPath := filepath.Join(parent, "moved-scope")
	if err := os.Rename(rootPath, movedPath); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(rootPath, 0755); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, filepath.Join(rootPath, "file.txt"), "replacement")

	data, _, _, err := root.store.Read("file.txt", maxRequestBody)
	if err != nil {
		t.Fatalf("rooted read after rename: %v", err)
	}
	if string(data) != "original" {
		t.Fatalf("rooted read = %q, want original", data)
	}
}

func TestWithGitCheckoutIdentityUsesHeldRootAfterPathReplacement(t *testing.T) {
	parent := t.TempDir()
	rootPath := filepath.Join(parent, "checkout")
	if err := os.Mkdir(rootPath, 0755); err != nil {
		t.Fatal(err)
	}
	root, err := openScopedRoot(rootPath)
	if err != nil {
		t.Fatalf("openScopedRoot: %v", err)
	}
	defer root.Close()

	movedPath := filepath.Join(parent, "checkout-original")
	if err := os.Rename(rootPath, movedPath); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(rootPath, 0755); err != nil {
		t.Fatal(err)
	}

	ctx, err := withGitCheckoutIdentity(context.Background(), rootPath, root)
	if err != nil {
		t.Fatalf("withGitCheckoutIdentity: %v", err)
	}
	identity, ok := GitWorktreeIdentityFromContext(ctx)
	if !ok {
		t.Fatal("git checkout identity missing from context")
	}
	heldInfo, err := root.store.root.Stat(".")
	if err != nil {
		t.Fatalf("stat held root: %v", err)
	}
	replacementInfo, err := os.Lstat(rootPath)
	if err != nil {
		t.Fatalf("stat replacement root: %v", err)
	}
	if !os.SameFile(identity.Info, heldInfo) {
		t.Fatal("captured identity does not match the descriptor-held root")
	}
	if os.SameFile(identity.Info, replacementInfo) {
		t.Fatal("captured identity followed the replacement checkout path")
	}
}

func TestFileServiceImpl_ReadFileScoped_ReadsContent(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "f.txt"), []byte("body"), 0644); err != nil {
		t.Fatal(err)
	}

	res, err := scopedSvc(dir).ReadFileScoped(context.Background(), "ws", ScopeWorkspace, "", "", "f.txt")
	if err != nil {
		t.Fatalf("ReadFileScoped: %v", err)
	}
	if res.Content != "body" {
		t.Errorf("content = %q, want %q", res.Content, "body")
	}
}

func TestFileServiceImpl_ReadFileScoped_GitExplicitReadForbidden(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".git"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".git", "config"), []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}

	for _, path := range []string{".git/config", ".GIT/config", ".GiT/config"} {
		_, err := scopedSvc(dir).ReadFileScoped(context.Background(), "ws", ScopeWorkspace, "", "", path)
		wantKind(t, err, failureForbidden)
	}
}

func TestFileServiceImpl_Scoped_UnsupportedScope(t *testing.T) {
	_, err := scopedSvc(t.TempDir()).ListDirectoryScoped(context.Background(), "ws", FileScope("bogus"), "", "", "")
	wantKind(t, err, failureInvalid)
}

func TestFileServiceImpl_Scoped_WorkspaceRejectsTarget(t *testing.T) {
	_, err := scopedSvc(t.TempDir()).ListDirectoryScoped(context.Background(), "ws", ScopeWorkspace, "some-repo", "", "")
	wantKind(t, err, failureInvalid)
}

func TestFileServiceImpl_Scoped_WorkspaceNotCheckedOut(t *testing.T) {
	svc := newFileService(scopedMockFileOps{wsErr: errors.New("workspace not checked out on this machine")})
	_, err := svc.ListDirectoryScoped(context.Background(), "ws", ScopeWorkspace, "", "", "")
	wantKind(t, err, failureNotFound)
}

func TestFileServiceImpl_PhaseA_CRUDAndVisibilityAllScopes(t *testing.T) {
	svc, scopes := setupScopedService(t)
	ctx := context.Background()
	access := fileAccessFromGrant(NewAccessGrantIssuer().ReadWrite(true))

	for _, sc := range scopes {
		t.Run(sc.name, func(t *testing.T) {
			mustWrite(t, filepath.Join(sc.root, ".env"), "OLD=1")
			mustWrite(t, filepath.Join(sc.root, ".git", "config"), "git config")
			if err := os.MkdirAll(filepath.Join(sc.root, "node_modules", "pkg"), 0755); err != nil {
				t.Fatal(err)
			}

			list, err := svc.ListDirectoryScoped(ctx, "ws", sc.scope, sc.target, "", "")
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

			readEnv, err := svc.readFileScoped(ctx, "ws", sc.scope, sc.target, "", ".env", access)
			if err != nil {
				t.Fatalf("read .env: %v", err)
			}
			if readEnv.Content != "OLD=1" {
				t.Fatalf(".env content = %q", readEnv.Content)
			}
			if _, err := svc.writeFileConditionalScoped(ctx, "ws", sc.scope, sc.target, "", ".env", "NEW=1", FileWritePreconditions{}, access); err != nil {
				t.Fatalf("write .env: %v", err)
			}
			readEnv, err = svc.readFileScoped(ctx, "ws", sc.scope, sc.target, "", ".env", access)
			if err != nil {
				t.Fatalf("read written .env: %v", err)
			}
			if readEnv.Content != "NEW=1" {
				t.Fatalf("written .env content = %q", readEnv.Content)
			}

			wantKind(t, writeScopedFile(ctx, svc, "ws", sc.scope, sc.target, "", ".git/config", "mutated"), failureForbidden)
			_, err = svc.ReadFileScoped(ctx, "ws", sc.scope, sc.target, "", ".GIT/config")
			wantKind(t, err, failureForbidden)
			wantKind(t, svc.MkdirScoped(ctx, "ws", sc.scope, sc.target, "", ".git/refs/heads"), failureForbidden)
			_, err = svc.MovePathVersionedScoped(ctx, "ws", sc.scope, sc.target, "", ".git/config", "config.moved", false, "sha256:test", "")
			wantKind(t, err, failureForbidden)
			envStat, statErr := svc.statPathScoped(ctx, "ws", sc.scope, sc.target, "", ".env", access)
			if statErr != nil {
				t.Fatalf("stat .env: %v", statErr)
			}
			_, err = svc.movePathVersionedScoped(ctx, "ws", sc.scope, sc.target, "", ".env", ".GiT/config", false, envStat.Version, "", access)
			wantKind(t, err, failureForbidden)
			err = svc.DeletePathVersionedScoped(ctx, "ws", sc.scope, sc.target, "", ".git/config", false, "sha256:test")
			wantKind(t, err, failureForbidden)

			mustWrite(t, filepath.Join(sc.root, "nonempty", "file.txt"), "x")
			nonemptyVersion := mustScopedVersion(t, svc, ctx, sc.scope, sc.target, "nonempty")
			err = svc.DeletePathVersionedScoped(ctx, "ws", sc.scope, sc.target, "", "nonempty", false, nonemptyVersion)
			wantKind(t, err, failureConflict)
			if err := svc.DeletePathVersionedScoped(ctx, "ws", sc.scope, sc.target, "", "nonempty", true, nonemptyVersion); err != nil {
				t.Fatalf("recursive delete: %v", err)
			}
			if _, err := os.Stat(filepath.Join(sc.root, "nonempty")); !os.IsNotExist(err) {
				t.Fatalf("nonempty dir still exists after recursive delete: %v", err)
			}

			mustWrite(t, filepath.Join(sc.root, "file-exists"), "x")
			err = svc.MkdirScoped(ctx, "ws", sc.scope, sc.target, "", "file-exists")
			wantKind(t, err, failureConflict)
			if err := svc.MkdirScoped(ctx, "ws", sc.scope, sc.target, "", "new/dir"); err != nil {
				t.Fatalf("mkdir nested: %v", err)
			}

			mustWrite(t, filepath.Join(sc.root, "move-src.txt"), "src")
			mustWrite(t, filepath.Join(sc.root, "move-dst.txt"), "dst")
			sourceVersion := mustScopedVersion(t, svc, ctx, sc.scope, sc.target, "move-src.txt")
			destinationVersion := mustScopedVersion(t, svc, ctx, sc.scope, sc.target, "move-dst.txt")
			_, err = svc.MovePathVersionedScoped(ctx, "ws", sc.scope, sc.target, "", "move-src.txt", "move-dst.txt", false, sourceVersion, "")
			wantKind(t, err, failureConflict)
			if _, err := svc.MovePathVersionedScoped(ctx, "ws", sc.scope, sc.target, "", "move-src.txt", "move-dst.txt", true, sourceVersion, destinationVersion); err != nil {
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
			readLarge, err := svc.ReadFileScoped(ctx, "ws", sc.scope, sc.target, "", "large.txt")
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
			if err := os.MkdirAll(filepath.Join(sc.root, "real-parent"), 0755); err != nil {
				t.Fatal(err)
			}
			mustWrite(t, filepath.Join(sc.root, "real-parent", "nested.txt"), "nested")
			if err := os.Symlink(filepath.Join(sc.root, "real-parent"), filepath.Join(sc.root, "parent-link")); err != nil {
				t.Skip("cannot create directory symlinks on this platform")
			}

			_, err := svc.ReadFileScoped(ctx, "ws", sc.scope, sc.target, "", "../outside.txt")
			wantKind(t, err, failureForbidden)
			err = writeScopedFile(ctx, svc, "ws", sc.scope, sc.target, "", "../outside.txt", "bad")
			wantKind(t, err, failureForbidden)
			err = svc.DeletePathVersionedScoped(ctx, "ws", sc.scope, sc.target, "", "../outside.txt", false, "sha256:test")
			wantKind(t, err, failureForbidden)
			err = svc.MkdirScoped(ctx, "ws", sc.scope, sc.target, "", "../outside-dir")
			wantKind(t, err, failureForbidden)
			_, err = svc.MovePathVersionedScoped(ctx, "ws", sc.scope, sc.target, "", "existing.txt", "../outside-move.txt", false, mustScopedVersion(t, svc, ctx, sc.scope, sc.target, "existing.txt"), "")
			wantKind(t, err, failureForbidden)

			_, err = svc.ReadFileScoped(ctx, "ws", sc.scope, sc.target, "", "link.txt")
			wantKind(t, err, failureForbidden)
			err = writeScopedFile(ctx, svc, "ws", sc.scope, sc.target, "", "link.txt", "bad")
			wantKind(t, err, failureForbidden)
			err = svc.DeletePathVersionedScoped(ctx, "ws", sc.scope, sc.target, "", "link.txt", false, "sha256:test")
			wantKind(t, err, failureForbidden)
			err = svc.MkdirScoped(ctx, "ws", sc.scope, sc.target, "", "link.txt")
			wantKind(t, err, failureForbidden)
			_, err = svc.MovePathVersionedScoped(ctx, "ws", sc.scope, sc.target, "", "link.txt", "moved-link.txt", false, "sha256:test", "")
			wantKind(t, err, failureForbidden)

			_, err = svc.ReadFileScoped(ctx, "ws", sc.scope, sc.target, "", "parent-link/nested.txt")
			wantKind(t, err, failureForbidden)
			err = writeScopedFile(ctx, svc, "ws", sc.scope, sc.target, "", "parent-link/new.txt", "bad")
			wantKind(t, err, failureForbidden)
		})
	}
}

func TestFileServiceImpl_MutationsRejectScopeRootAliases(t *testing.T) {
	dir := t.TempDir()
	svc := scopedSvc(dir)
	ctx := context.Background()
	for _, path := range []string{"", ".", "./", "dir/..", "dir/../."} {
		wantKind(t, svc.DeletePathVersionedScoped(ctx, "ws", ScopeWorkspace, "", "", path, true, "sha256:test"), failureInvalid)
		wantKind(t, svc.MkdirScoped(ctx, "ws", ScopeWorkspace, "", "", path), failureInvalid)
	}
	if _, err := os.Stat(dir); err != nil {
		t.Fatalf("scope root was changed: %v", err)
	}
}

func TestFileServiceImpl_ProtectsGitMetadataUnderAncestorMutation(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "checkout", ".git", "config"), "secret")
	mustWrite(t, filepath.Join(dir, "checkout", "file.txt"), "body")
	svc := scopedSvc(dir)
	ctx := context.Background()

	wantKind(t, svc.DeletePathVersionedScoped(ctx, "ws", ScopeWorkspace, "", "", "checkout", true, "sha256:test"), failureForbidden)
	_, err := svc.MovePathVersionedScoped(ctx, "ws", ScopeWorkspace, "", "", "checkout", "renamed", false, "sha256:test", "")
	wantKind(t, err, failureForbidden)
	if _, err := os.Stat(filepath.Join(dir, "checkout", ".git", "config")); err != nil {
		t.Fatalf("protected metadata changed: %v", err)
	}
}

func TestFileServiceImpl_RejectsSymlinkAliasToGit(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, ".git", "config"), "secret")
	if err := os.Symlink(filepath.Join(dir, ".git"), filepath.Join(dir, "metadata")); err != nil {
		t.Skip("cannot create symlinks on this platform")
	}
	svc := scopedSvc(dir)
	_, err := svc.ReadFileScoped(context.Background(), "ws", ScopeWorkspace, "", "", "metadata/config")
	wantKind(t, err, failureForbidden)
}

func TestFileServiceImpl_RecursiveDeleteDoesNotFollowDescendantSymlink(t *testing.T) {
	dir := t.TempDir()
	outside := t.TempDir()
	mustWrite(t, filepath.Join(outside, "keep.txt"), "keep")
	if err := os.Mkdir(filepath.Join(dir, "delete-me"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(dir, "delete-me", "outside")); err != nil {
		t.Skip("cannot create symlinks on this platform")
	}
	svc := scopedSvc(dir)
	ctx := context.Background()
	version := mustScopedVersion(t, svc, ctx, ScopeWorkspace, "", "delete-me")
	if err := svc.DeletePathVersionedScoped(ctx, "ws", ScopeWorkspace, "", "", "delete-me", true, version); err != nil {
		t.Fatalf("recursive delete: %v", err)
	}
	if got, err := os.ReadFile(filepath.Join(outside, "keep.txt")); err != nil || string(got) != "keep" {
		t.Fatalf("outside symlink target changed: content=%q err=%v", got, err)
	}
}

func TestFileServiceImpl_AtomicWritePreservesPermissions(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "script.sh")
	mustWrite(t, path, "old")
	if err := os.Chmod(path, 0750); err != nil {
		t.Fatal(err)
	}
	if err := writeScopedFile(context.Background(), scopedSvc(dir), "ws", ScopeWorkspace, "", "", "script.sh", "new"); err != nil {
		t.Fatalf("ordinary save: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0750 {
		t.Fatalf("permissions = %o, want 750", got)
	}
}

func TestFileServiceImpl_PhaseA_InvalidTargetsRejected(t *testing.T) {
	svc, _ := setupScopedService(t)
	ctx := context.Background()

	_, err := svc.ListDirectoryScoped(ctx, "ws", ScopeRepo, "missing-repo", "", "")
	wantKind(t, err, failureNotFound)
	_, err = svc.ListDirectoryScoped(ctx, "ws", ScopeAgent, "missing-agent", "", "")
	wantKind(t, err, failureNotFound)
}

func TestFileServiceImpl_RepoQualifiedAgentScopeResolution(t *testing.T) {
	wsRoot := t.TempDir()
	repoARoot := filepath.Join(wsRoot, "repo-a")
	repoBRoot := filepath.Join(wsRoot, "repo-b")
	agentARoot := filepath.Join(wsRoot, "worktrees", "repo-a", "agent-a")
	agentBRoot := filepath.Join(wsRoot, "worktrees", "repo-b", "agent-a")
	for _, dir := range []string{repoARoot, repoBRoot, agentARoot, agentBRoot} {
		if err := os.MkdirAll(dir, 0755); err != nil {
			t.Fatal(err)
		}
	}
	mustWrite(t, filepath.Join(agentARoot, "legacy.txt"), "legacy")
	mustWrite(t, filepath.Join(agentBRoot, "qualified.txt"), "repo-b")

	svc := newFileService(scopedMockFileOps{
		wsRoot:    wsRoot,
		repoRoot:  repoARoot,
		agentRoot: agentARoot,
		agentRoots: map[string]string{
			"repo-a": agentARoot,
			"repo-b": agentBRoot,
		},
		agentRepoErrs: map[string]error{
			"repo-c":       ErrAgentRepoNotAllowed,
			"repo-missing": ErrAgentWorktreeNotFound,
		},
		wsData: &WorkspaceTopology{
			ID:   "ws",
			Path: wsRoot,
			Repos: []WorkspaceRepo{
				{Name: "repo-a", Path: repoARoot},
				{Name: "repo-b", Path: repoBRoot},
				{Name: "repo-c", Path: filepath.Join(wsRoot, "repo-c")},
				{Name: "repo-missing", Path: filepath.Join(wsRoot, "repo-missing")},
			},
			Agents: []WorkspaceAgent{{Name: "agent-a", Repos: []string{"repo-a", "repo-b", "repo-missing"}}},
		},
	})
	ctx := context.Background()

	legacy, err := svc.ReadFileScoped(ctx, "ws", ScopeAgent, "agent-a", "", "legacy.txt")
	if err != nil {
		t.Fatalf("legacy agent read: %v", err)
	}
	if legacy.Content != "legacy" {
		t.Fatalf("legacy content = %q", legacy.Content)
	}

	qualified, err := svc.ReadFileScoped(ctx, "ws", ScopeAgent, "agent-a", "repo-b", "qualified.txt")
	if err != nil {
		t.Fatalf("repo-qualified agent read: %v", err)
	}
	if qualified.Content != "repo-b" {
		t.Fatalf("qualified content = %q", qualified.Content)
	}

	_, err = svc.ListDirectoryScoped(ctx, "ws", ScopeAgent, "agent-a", "repo-c", "")
	wantKind(t, err, failureInvalid)

	_, err = svc.ListDirectoryScoped(ctx, "ws", ScopeAgent, "agent-a", "repo-missing", "")
	wantKind(t, err, failureNotFound)

	_, err = svc.ListDirectoryScoped(ctx, "ws", ScopeWorkspace, "", "repo-a", "")
	wantKind(t, err, failureInvalid)

	_, err = svc.ListDirectoryScoped(ctx, "ws", ScopeRepo, "repo-a", "repo-b", "")
	wantKind(t, err, failureInvalid)
}

func TestResolveScopedContainingCheckout(t *testing.T) {
	svc, scopes := setupScopedService(t)
	impl := svc
	for _, sc := range scopes {
		initGitRepo(t, sc.root)
		mustWrite(t, filepath.Join(sc.root, "src", "file.txt"), "body")
		commitAll(t, sc.root)
	}

	ctx := context.Background()
	_ = ctx
	for _, sc := range scopes {
		t.Run(sc.name, func(t *testing.T) {
			root, cleanPath, checkout, err := impl.resolveScopedContainingCheckout("ws", sc.scope, sc.target, "", "src/file.txt")
			if err != nil {
				t.Fatalf("resolveScopedContainingCheckout: %v", err)
			}
			defer root.Close()
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

	_, _, _, err := impl.resolveScopedContainingCheckout("ws", ScopeWorkspace, "", "", "../outside.txt")
	wantKind(t, err, failureForbidden)
}

func TestFileServiceImpl_ReadFileAtRevScoped(t *testing.T) {
	svc, scopes := setupScopedService(t)
	repo := scopes[1]
	initGitRepo(t, repo.root)
	mustWrite(t, filepath.Join(repo.root, "file.txt"), "committed\n")
	commitAll(t, repo.root)
	mustWrite(t, filepath.Join(repo.root, "file.txt"), "working\n")

	res, err := svc.ReadFileAtRevScoped(context.Background(), "ws", repo.scope, repo.target, "", "file.txt", "HEAD")
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
	res, err = svc.ReadFileAtRevScoped(context.Background(), "ws", repo.scope, repo.target, "", "large.txt", "HEAD")
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

func TestFileServiceImpl_ReadUntrackedFileAtRevIsNotFound(t *testing.T) {
	root := t.TempDir()
	svc := newFileService(scopedMockFileOps{
		wsRoot: root,
		gitShowFunc: func(context.Context, string, string, string, int64) (*GitFileContentAtRev, error) {
			return nil, fakeInspectionError{kind: "not_found", message: "git cat-file: path exists on disk, but not in HEAD"}
		},
	})

	_, err := svc.ReadFileAtRevScoped(context.Background(), "ws", ScopeWorkspace, "", "", "untracked.txt", "HEAD")
	wantKind(t, err, failureNotFound)
	if strings.Contains(err.Error(), "exists on disk") || strings.Contains(err.Error(), "cat-file") {
		t.Fatalf("git stderr leaked through service error: %v", err)
	}
}

func TestFileServiceImpl_DiffFileScoped(t *testing.T) {
	svc, scopes := setupScopedService(t)
	repo := scopes[1]
	initGitRepo(t, repo.root)
	mustWrite(t, filepath.Join(repo.root, "file.txt"), "one\n")
	commitAll(t, repo.root)
	mustWrite(t, filepath.Join(repo.root, "file.txt"), "two\n")

	res, err := svc.DiffFileScoped(context.Background(), "ws", repo.scope, repo.target, "", "file.txt", "HEAD", "")
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
	res, err = svc.DiffFileScoped(context.Background(), "ws", repo.scope, repo.target, "", "file.txt", first, second)
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

	res, err := svc.BlameFileScoped(context.Background(), "ws", repo.scope, repo.target, "", "file.txt")
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
	res, err = svc.BlameFileScoped(context.Background(), "ws", repo.scope, repo.target, "", "too-large.txt")
	if err != nil {
		t.Fatalf("BlameFileScoped large: %v", err)
	}
	if !res.Skipped || res.Reason != "too_large" {
		t.Fatalf("large blame = %+v, want too_large skip", res)
	}
}

func TestFileServiceImpl_HistoryContainsOnlyCommits(t *testing.T) {
	svc, scopes := setupScopedService(t)
	repo := scopes[1]
	initGitRepo(t, repo.root)
	mustWrite(t, filepath.Join(repo.root, "file.txt"), "committed\n")
	commitAll(t, repo.root)

	if err := writeScopedFile(context.Background(), svc, "ws", repo.scope, repo.target, "", "file.txt", "ordinary save\n"); err != nil {
		t.Fatalf("ordinary save overwrite: %v", err)
	}
	history, err := svc.HistoryFileScoped(context.Background(), "ws", repo.scope, repo.target, "", "file.txt")
	if err != nil {
		t.Fatalf("HistoryFileScoped: %v", err)
	}
	var sawCommit bool
	for _, entry := range history.Entries {
		if entry.Kind == "commit" && entry.Summary == "init" {
			sawCommit = true
		}
		if entry.Kind != "commit" {
			t.Fatalf("non-commit history entry: %+v", entry)
		}
	}
	if !sawCommit {
		t.Fatalf("history entries = %+v, want commit", history.Entries)
	}
}

func TestParseGitLogHistoryUsesFixedNULFieldCount(t *testing.T) {
	summary := "subject with " + string(rune(0x1e)) + " and " + string(rune(0x1f))
	output := "abc\x00Test User\x001700000000\x00" + summary + "\x00"
	entries := parseGitLogHistory(output)
	if len(entries) != 1 || entries[0].Summary != summary || entries[0].SHA != "abc" {
		t.Fatalf("entries = %+v", entries)
	}
}

type fakeInspectionError struct {
	kind    string
	message string
}

func (e fakeInspectionError) Error() string {
	if e.message != "" {
		return e.message
	}
	return "inspection failed"
}
func (e fakeInspectionError) InspectionKind() string { return e.kind }

func TestMapGitInspectionErrorPreservesKinds(t *testing.T) {
	for _, tc := range []struct {
		kind string
		want failureKind
	}{
		{kind: "timeout", want: failureTimeout},
		{kind: "canceled", want: failureTimeout},
		{kind: "validation", want: failureInvalid},
		{kind: "not_found", want: failureNotFound},
		{kind: "failed", want: failureInternal},
	} {
		err := mapGitInspectionError("git operation", fakeInspectionError{kind: tc.kind})
		serviceErr, ok := err.(*Failure)
		if !ok || serviceErr.Kind != tc.want {
			t.Fatalf("kind %q mapped to %T %+v, want %q", tc.kind, err, err, tc.want)
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

		res, err := scopedSvc(dir).IndexFilesScoped(context.Background(), "ws", ScopeWorkspace, "", "")
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

func TestFileServiceImpl_IndexAndSearchExcludeGitMetadataFile(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, ".git"), "gitdir: /outside/admin\nneedle\n")
	mustWrite(t, filepath.Join(dir, "visible.txt"), "needle\n")
	svc := scopedSvc(dir)
	ctx := context.Background()

	index, err := svc.IndexFilesScoped(ctx, "ws", ScopeWorkspace, "", "")
	if err != nil {
		t.Fatalf("IndexFilesScoped: %v", err)
	}
	if containsPath(index.Paths, ".git") {
		t.Fatalf("index exposed .git metadata file: %+v", index.Paths)
	}
	emptyExclude := []string{}
	search, err := svc.SearchFilesScoped(ctx, "ws", ScopeWorkspace, "", "", FileSearchRequest{
		Query:   "needle",
		Exclude: &emptyExclude,
	})
	if err != nil {
		t.Fatalf("SearchFilesScoped: %v", err)
	}
	if got := resultPaths(search.Results); strings.Join(got, ",") != "visible.txt" {
		t.Fatalf("search paths = %+v, want visible.txt", got)
	}
}

func TestFileServiceImpl_SearchFilesScoped_DefaultExcludesAndOverride(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "src", "main.go"), "needle\n")
	mustWrite(t, filepath.Join(dir, "src", "generated.go"), "needle\n")
	mustWrite(t, filepath.Join(dir, ".git", "config"), "needle\n")
	mustWrite(t, filepath.Join(dir, "node_modules", "pkg", "index.js"), "needle\n")

	svc := scopedSvc(dir)
	ctx := context.Background()
	res, err := svc.SearchFilesScoped(ctx, "ws", ScopeWorkspace, "", "", FileSearchRequest{Query: "needle"})
	if err != nil {
		t.Fatalf("SearchFilesScoped default: %v", err)
	}
	if got := resultPaths(res.Results); strings.Join(got, ",") != "src/generated.go,src/main.go" {
		t.Fatalf("default search paths = %+v", got)
	}

	emptyExclude := []string{}
	res, err = svc.SearchFilesScoped(ctx, "ws", ScopeWorkspace, "", "", FileSearchRequest{
		Query:   "needle",
		Exclude: &emptyExclude,
	})
	if err != nil {
		t.Fatalf("SearchFilesScoped override excludes: %v", err)
	}
	got := resultPaths(res.Results)
	if containsPath(got, ".git/config") {
		t.Fatalf("override search exposed .git/config: %+v", got)
	}
	for _, want := range []string{"node_modules/pkg/index.js"} {
		if !containsPath(got, want) {
			t.Fatalf("override search missing %s: %+v", want, got)
		}
	}

	excludeGenerated := []string{"src/generated.go"}
	res, err = svc.SearchFilesScoped(ctx, "ws", ScopeWorkspace, "", "", FileSearchRequest{
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

	res, err := scopedSvc(dir).SearchFilesScoped(context.Background(), "ws", ScopeWorkspace, "", "", FileSearchRequest{Query: "needle"})
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
			res, err := scopedSvc(dir).SearchFilesScoped(context.Background(), "ws", ScopeWorkspace, "", "", FileSearchRequest{Query: "needle"})
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
			res, err := scopedSvc(dir).SearchFilesScoped(context.Background(), "ws", ScopeWorkspace, "", "", FileSearchRequest{Query: "needle"})
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
			res, err := scopedSvc(dir).SearchFilesScoped(context.Background(), "ws", ScopeWorkspace, "", "", FileSearchRequest{Query: "needle"})
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
			index, err := svc.IndexFilesScoped(ctx, "ws", sc.scope, sc.target, "")
			if err != nil {
				t.Fatalf("IndexFilesScoped: %v", err)
			}
			if !containsPath(index.Paths, sc.name+".txt") {
				t.Fatalf("index paths = %+v, missing scoped file", index.Paths)
			}

			search, err := svc.SearchFilesScoped(ctx, "ws", sc.scope, sc.target, "", FileSearchRequest{Query: "target-" + sc.name})
			if err != nil {
				t.Fatalf("SearchFilesScoped: %v", err)
			}
			if got := resultPaths(search.Results); !containsPath(got, sc.name+".txt") {
				t.Fatalf("search paths = %+v, missing scoped file", got)
			}
		})
	}
}

func TestFileServiceImpl_RepoScopeUsesConfiguredRepoPath(t *testing.T) {
	wsRoot := t.TempDir()
	repoRoot := filepath.Join(wsRoot, "services", "api")
	if err := os.MkdirAll(repoRoot, 0755); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, filepath.Join(repoRoot, "api.txt"), "api\n")
	mustWrite(t, filepath.Join(wsRoot, "api", "wrong.txt"), "wrong\n")
	svc := newFileService(scopedMockFileOps{
		wsRoot: wsRoot,
		wsData: &WorkspaceTopology{
			ID:   "ws",
			Path: wsRoot,
			Repos: []WorkspaceRepo{{
				Name: "api",
				Path: "services/api",
			}},
		},
	})

	index, err := svc.IndexFilesScoped(context.Background(), "ws", ScopeRepo, "api", "")
	if err != nil {
		t.Fatalf("IndexFilesScoped repo api: %v", err)
	}
	if !containsPath(index.Paths, "api.txt") || containsPath(index.Paths, "wrong.txt") {
		t.Fatalf("repo index paths = %+v, want configured path only", index.Paths)
	}
}

func TestFileServiceImpl_AgentScopeAllowsConfigBackedWorkspaceWithoutAgentRows(t *testing.T) {
	wsRoot := t.TempDir()
	agentRoot := filepath.Join(wsRoot, "worktrees", "repo-a", "agent-a")
	if err := os.MkdirAll(agentRoot, 0755); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, filepath.Join(agentRoot, "agent.txt"), "agent\n")
	svc := newFileService(scopedMockFileOps{
		wsRoot:    wsRoot,
		agentRoot: agentRoot,
		wsData: &WorkspaceTopology{
			ID:   "ws",
			Path: wsRoot,
			Repos: []WorkspaceRepo{{
				Name: "repo-a",
				Path: filepath.Join(wsRoot, "repo-a"),
			}},
		},
	})

	index, err := svc.IndexFilesScoped(context.Background(), "ws", ScopeAgent, "agent-a", "")
	if err != nil {
		t.Fatalf("IndexFilesScoped agent-a: %v", err)
	}
	if !containsPath(index.Paths, "agent.txt") {
		t.Fatalf("agent index paths = %+v, missing agent.txt", index.Paths)
	}
}

func TestFileServiceImpl_IndexCacheInvalidatedAfterCRUD(t *testing.T) {
	root := t.TempDir()
	svc := scopedSvc(root)
	ctx := context.Background()
	if err := writeScopedFile(ctx, svc, "ws", ScopeWorkspace, "", "", "one.txt", "1"); err != nil {
		t.Fatalf("write one: %v", err)
	}
	index, err := svc.IndexFilesScoped(ctx, "ws", ScopeWorkspace, "", "")
	if err != nil {
		t.Fatalf("index one: %v", err)
	}
	if !containsPath(index.Paths, "one.txt") {
		t.Fatalf("index missing one.txt: %+v", index.Paths)
	}

	if err := writeScopedFile(ctx, svc, "ws", ScopeWorkspace, "", "", "two.txt", "2"); err != nil {
		t.Fatalf("write two: %v", err)
	}
	index, err = svc.IndexFilesScoped(ctx, "ws", ScopeWorkspace, "", "")
	if err != nil {
		t.Fatalf("index after write: %v", err)
	}
	if !containsPath(index.Paths, "two.txt") {
		t.Fatalf("write did not invalidate index cache: %+v", index.Paths)
	}

	if err := svc.MkdirScoped(ctx, "ws", ScopeWorkspace, "", "", "dir"); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	mustWrite(t, filepath.Join(root, "dir", "three.txt"), "3")
	index, err = svc.IndexFilesScoped(ctx, "ws", ScopeWorkspace, "", "")
	if err != nil {
		t.Fatalf("index after mkdir/write: %v", err)
	}
	if !containsPath(index.Paths, "dir/three.txt") {
		t.Fatalf("mkdir did not invalidate index cache: %+v", index.Paths)
	}

	version := mustScopedVersion(t, svc, ctx, ScopeWorkspace, "", "two.txt")
	if _, err := svc.MovePathVersionedScoped(ctx, "ws", ScopeWorkspace, "", "", "two.txt", "moved.txt", false, version, ""); err != nil {
		t.Fatalf("move: %v", err)
	}
	index, err = svc.IndexFilesScoped(ctx, "ws", ScopeWorkspace, "", "")
	if err != nil {
		t.Fatalf("index after move: %v", err)
	}
	if containsPath(index.Paths, "two.txt") || !containsPath(index.Paths, "moved.txt") {
		t.Fatalf("move did not invalidate index cache: %+v", index.Paths)
	}

	version = mustScopedVersion(t, svc, ctx, ScopeWorkspace, "", "one.txt")
	if err := svc.DeletePathVersionedScoped(ctx, "ws", ScopeWorkspace, "", "", "one.txt", false, version); err != nil {
		t.Fatalf("delete: %v", err)
	}
	index, err = svc.IndexFilesScoped(ctx, "ws", ScopeWorkspace, "", "")
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
		if sc.scope == ScopeWorkspace {
			continue
		}
		initGitRepo(t, sc.root)
		mustWrite(t, filepath.Join(sc.root, "tracked.txt"), "one\n")
		commitAll(t, sc.root)
		mustWrite(t, filepath.Join(sc.root, "tracked.txt"), "two\n")

		status, err := svc.GitStatusScoped(ctx, "ws", sc.scope, sc.target, "")
		if err != nil {
			t.Fatalf("%s GitStatusScoped: %v", sc.name, err)
		}
		if got := status.Status["tracked.txt"]; got != " M" {
			t.Fatalf("%s status[tracked.txt] = %q, want %q; full=%#v", sc.name, got, " M", status)
		}
		if _, ok := status.Status[filepath.ToSlash(filepath.Join(filepath.Base(sc.root), "tracked.txt"))]; ok {
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
		case ScopeRepo:
			repoRoot = sc.root
		case ScopeAgent:
			agentRoot = sc.root
		}
	}

	initGitRepo(t, repoRoot)
	mustWrite(t, filepath.Join(repoRoot, "tracked.txt"), "one\n")
	commitAll(t, repoRoot)
	mustWrite(t, filepath.Join(repoRoot, "tracked.txt"), "two\n")

	initGitRepo(t, agentRoot)
	mustWrite(t, filepath.Join(agentRoot, "new.txt"), "new\n")

	status, err := svc.GitStatusScoped(ctx, "ws", ScopeWorkspace, "", "")
	if err != nil {
		t.Fatalf("GitStatusScoped workspace: %v", err)
	}
	if got := status.Status["repo-a/tracked.txt"]; got != " M" {
		t.Fatalf("status[repo-a/tracked.txt] = %q, want %q; full=%#v", got, " M", status)
	}
	if got := status.Status["worktrees/repo-a/agent-a/new.txt"]; got != "??" {
		t.Fatalf("status[worktrees/repo-a/agent-a/new.txt] = %q, want %q; full=%#v", got, "??", status)
	}
	if _, ok := status.Status["tracked.txt"]; ok {
		t.Fatalf("workspace status should prefix checkout paths, got %#v", status)
	}
}

func TestFileServiceImpl_WorkspaceGitFanoutBoundedAndReportsPartial(t *testing.T) {
	wsRoot := t.TempDir()
	repos := make([]WorkspaceRepo, 0, 8)
	for i := 0; i < 8; i++ {
		name := fmt.Sprintf("repo-%d", i)
		path := filepath.Join(wsRoot, name)
		if err := os.MkdirAll(path, 0755); err != nil {
			t.Fatal(err)
		}
		repos = append(repos, WorkspaceRepo{Name: name, Path: path})
	}
	var active, maxActive atomic.Int32
	statusFunc := func(ctx context.Context, path string) (GitFileStatusResult, error) {
		current := active.Add(1)
		defer active.Add(-1)
		for {
			previous := maxActive.Load()
			if current <= previous || maxActive.CompareAndSwap(previous, current) {
				break
			}
		}
		select {
		case <-time.After(25 * time.Millisecond):
		case <-ctx.Done():
			return GitFileStatusResult{}, ctx.Err()
		}
		if filepath.Base(path) == "repo-3" {
			return GitFileStatusResult{}, errors.New("broken checkout")
		}
		return GitFileStatusResult{Entries: map[string]string{"file.txt": " M"}, Partial: filepath.Base(path) == "repo-4", LimitHit: filepath.Base(path) == "repo-4"}, nil
	}
	svc := newFileService(scopedMockFileOps{
		wsRoot:        wsRoot,
		wsData:        &WorkspaceTopology{ID: "ws", Path: wsRoot, Repos: repos},
		gitStatusFunc: statusFunc,
	})
	result, err := svc.GitStatusScoped(context.Background(), "ws", ScopeWorkspace, "", "")
	if err != nil {
		t.Fatalf("GitStatusScoped: %v", err)
	}
	if maxActive.Load() > workspaceGitConcurrency {
		t.Fatalf("max concurrency = %d", maxActive.Load())
	}
	if !result.Partial || !result.LimitHit || len(result.Errors) != 1 || result.Errors[0].Repo != "repo-3" {
		t.Fatalf("result = %+v", result)
	}
}

func TestFileServiceImpl_WorkspaceGitFanoutHonorsCallerDeadline(t *testing.T) {
	wsRoot := t.TempDir()
	path := filepath.Join(wsRoot, "repo")
	if err := os.MkdirAll(path, 0755); err != nil {
		t.Fatal(err)
	}
	svc := newFileService(scopedMockFileOps{
		wsRoot: wsRoot,
		wsData: &WorkspaceTopology{ID: "ws", Path: wsRoot, Repos: []WorkspaceRepo{{Name: "repo", Path: path}}},
		gitStatusFunc: func(ctx context.Context, _ string) (GitFileStatusResult, error) {
			<-ctx.Done()
			return GitFileStatusResult{}, ctx.Err()
		},
	})
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	result, err := svc.GitStatusScoped(ctx, "ws", ScopeWorkspace, "", "")
	if err != nil {
		t.Fatalf("GitStatusScoped: %v", err)
	}
	if !result.Partial || len(result.Errors) != 1 {
		t.Fatalf("result = %+v", result)
	}
}

func TestFileServiceImpl_CheckoutBranchFailurePreservesStatusAndReportsPartial(t *testing.T) {
	wsRoot := t.TempDir()
	path := filepath.Join(wsRoot, "repo")
	if err := os.MkdirAll(path, 0755); err != nil {
		t.Fatal(err)
	}
	svc := newFileService(scopedMockFileOps{
		wsRoot: wsRoot,
		wsData: &WorkspaceTopology{ID: "ws", Path: wsRoot, Repos: []WorkspaceRepo{{Name: "repo", Path: path}}},
		gitStatusFunc: func(context.Context, string) (GitFileStatusResult, error) {
			return GitFileStatusResult{Entries: map[string]string{"changed.txt": " M"}}, nil
		},
		branchFunc: func(context.Context, string) (string, error) { return "", errors.New("branch unavailable") },
	})
	result, err := svc.ListFileCheckouts(context.Background(), "ws")
	if err != nil {
		t.Fatalf("ListFileCheckouts: %v", err)
	}
	if len(result.Checkouts) != 1 || result.Checkouts[0].ChangeCount != 1 || result.Checkouts[0].StatusError {
		t.Fatalf("status was not preserved: %+v", result.Checkouts)
	}
	if !result.Partial || !result.Checkouts[0].Partial || len(result.Errors) != 1 || !strings.Contains(result.Errors[0].Error, "branch unavailable") {
		t.Fatalf("branch failure not surfaced: %+v", result)
	}
}

func TestFileServiceImpl_ListFileCheckouts_IncludesMissingAndChangeCounts(t *testing.T) {
	ctx := context.Background()
	wsRoot := t.TempDir()
	repoARoot := filepath.Join(wsRoot, "repo-a")
	repoBRoot := filepath.Join(wsRoot, "repo-b")
	agentARoot := filepath.Join(wsRoot, "worktrees", "repo-a", "agent-a")
	for _, dir := range []string{repoARoot, agentARoot} {
		if err := os.MkdirAll(dir, 0755); err != nil {
			t.Fatal(err)
		}
	}

	initGitRepo(t, repoARoot)
	mustWrite(t, filepath.Join(repoARoot, "tracked.txt"), "one\n")
	commitAll(t, repoARoot)
	mustWrite(t, filepath.Join(repoARoot, "tracked.txt"), "two\n")

	initGitRepo(t, agentARoot)
	mustWrite(t, filepath.Join(agentARoot, "tracked.txt"), "one\n")
	commitAll(t, agentARoot)
	mustWrite(t, filepath.Join(agentARoot, "new.txt"), "new\n")

	svc := newFileService(scopedMockFileOps{
		wsRoot:    wsRoot,
		repoRoot:  repoARoot,
		agentRoot: agentARoot,
		wsData: &WorkspaceTopology{
			ID:   "ws",
			Path: wsRoot,
			Repos: []WorkspaceRepo{
				{Name: "repo-a", Path: repoARoot},
				{Name: "repo-b", Path: repoBRoot},
			},
			Agents: []WorkspaceAgent{{Name: "agent-a", Repos: []string{"repo-a", "repo-b"}}},
		},
	})

	result, err := svc.ListFileCheckouts(ctx, "ws")
	if err != nil {
		t.Fatalf("ListFileCheckouts: %v", err)
	}
	checkouts := map[string]FileCheckout{}
	for _, checkout := range result.Checkouts {
		key := checkout.Kind + ":" + checkout.Repo
		if checkout.Agent != "" {
			key = checkout.Kind + ":" + checkout.Agent + ":" + checkout.Repo
		}
		checkouts[key] = checkout
	}

	repoA := checkouts["repo:repo-a"]
	if !repoA.Exists || repoA.ChangeCount != 1 || repoA.Branch == "" {
		t.Fatalf("repo-a checkout = %+v", repoA)
	}
	repoB := checkouts["repo:repo-b"]
	if repoB.Exists || repoB.ChangeCount != 0 || repoB.Branch != "" {
		t.Fatalf("repo-b checkout = %+v", repoB)
	}
	agentA := checkouts["agent:agent-a:repo-a"]
	if !agentA.Exists || agentA.ChangeCount != 1 || agentA.Branch == "" {
		t.Fatalf("agent repo-a checkout = %+v", agentA)
	}
	agentB := checkouts["agent:agent-a:repo-b"]
	if agentB.Exists || agentB.ChangeCount != 0 || agentB.Branch != "" {
		t.Fatalf("agent repo-b checkout = %+v", agentB)
	}
	if len(checkouts) != 4 {
		t.Fatalf("checkouts = %+v, want 4 entries", result.Checkouts)
	}
}

func TestFileServiceImpl_CheckoutsAndWorkspaceGitStatusSkipBrokenCheckout(t *testing.T) {
	ctx := context.Background()
	wsRoot := t.TempDir()
	repoRoot := filepath.Join(wsRoot, "repo-a")
	agentRoot := filepath.Join(wsRoot, "worktrees", "repo-a", "agent-a")
	for _, dir := range []string{repoRoot, agentRoot} {
		if err := os.MkdirAll(dir, 0755); err != nil {
			t.Fatal(err)
		}
	}

	initGitRepo(t, repoRoot)
	mustWrite(t, filepath.Join(repoRoot, "tracked.txt"), "one\n")
	commitAll(t, repoRoot)
	mustWrite(t, filepath.Join(repoRoot, "tracked.txt"), "two\n")

	if err := os.WriteFile(filepath.Join(agentRoot, ".git"), []byte("gitdir: /missing/git/admin/dir\n"), 0644); err != nil {
		t.Fatal(err)
	}

	svc := newFileService(scopedMockFileOps{
		wsRoot:    wsRoot,
		repoRoot:  repoRoot,
		agentRoot: agentRoot,
		wsData: &WorkspaceTopology{
			ID:   "ws",
			Path: wsRoot,
			Repos: []WorkspaceRepo{{
				Name: "repo-a",
				Path: repoRoot,
			}},
			Agents: []WorkspaceAgent{{Name: "agent-a", Repos: []string{"repo-a"}}},
		},
	})

	result, err := svc.ListFileCheckouts(ctx, "ws")
	if err != nil {
		t.Fatalf("ListFileCheckouts: %v", err)
	}
	checkouts := map[string]FileCheckout{}
	for _, checkout := range result.Checkouts {
		key := checkout.Kind + ":" + checkout.Repo
		if checkout.Agent != "" {
			key = checkout.Kind + ":" + checkout.Agent + ":" + checkout.Repo
		}
		checkouts[key] = checkout
	}

	repoCheckout := checkouts["repo:repo-a"]
	if !repoCheckout.Exists || repoCheckout.ChangeCount != 1 || repoCheckout.StatusError {
		t.Fatalf("healthy repo checkout = %+v", repoCheckout)
	}
	agentCheckout := checkouts["agent:agent-a:repo-a"]
	if !agentCheckout.Exists || agentCheckout.ChangeCount != 0 || !agentCheckout.StatusError {
		t.Fatalf("broken agent checkout = %+v", agentCheckout)
	}

	status, err := svc.GitStatusScoped(ctx, "ws", ScopeWorkspace, "", "")
	if err != nil {
		t.Fatalf("GitStatusScoped workspace: %v", err)
	}
	if got := status.Status["repo-a/tracked.txt"]; got != " M" {
		t.Fatalf("status[repo-a/tracked.txt] = %q, want %q; full=%#v", got, " M", status)
	}
	for path := range status.Status {
		if strings.HasPrefix(path, "worktrees/repo-a/agent-a/") {
			t.Fatalf("workspace status included broken checkout path %q; full=%#v", path, status)
		}
	}
}

func TestFileServiceImpl_GitStatusScoped_InvalidTargets(t *testing.T) {
	ctx := context.Background()
	svc, _ := setupScopedService(t)

	_, err := svc.GitStatusScoped(ctx, "ws", ScopeRepo, "missing", "")
	wantKind(t, err, failureNotFound)

	_, err = svc.GitStatusScoped(ctx, "ws", ScopeAgent, "missing", "")
	wantKind(t, err, failureNotFound)

	_, err = svc.GitStatusScoped(ctx, "ws", ScopeWorkspace, "repo-a", "")
	wantKind(t, err, failureInvalid)
}
