package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// findRepoRoot walks up from the current directory to find the nearest directory
// containing a .git file or directory. Tests run from internal/cli/ which is not
// the repo root.
func findRepoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	for {
		gitPath := filepath.Join(dir, ".git")
		if _, err := os.Lstat(gitPath); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("could not find .git in any parent directory")
		}
		dir = parent
	}
}

// makeTempRepo creates a temporary git repo with one commit and returns its path.
func makeTempRepo(t *testing.T) string {
	t.Helper()
	tmpDir := t.TempDir()
	if _, err := RunGitCommand(tmpDir, "init"); err != nil {
		t.Fatalf("git init failed: %v", err)
	}
	if _, err := RunGitCommand(tmpDir, "config", "user.email", "test@test.com"); err != nil {
		t.Fatalf("git config email failed: %v", err)
	}
	if _, err := RunGitCommand(tmpDir, "config", "user.name", "Test"); err != nil {
		t.Fatalf("git config name failed: %v", err)
	}
	testFile := filepath.Join(tmpDir, "test.txt")
	if err := os.WriteFile(testFile, []byte("hello"), 0644); err != nil {
		t.Fatalf("write test file: %v", err)
	}
	if _, err := RunGitCommand(tmpDir, "add", "."); err != nil {
		t.Fatalf("git add failed: %v", err)
	}
	if _, err := RunGitCommand(tmpDir, "commit", "-m", "init"); err != nil {
		t.Fatalf("git commit failed: %v", err)
	}
	return tmpDir
}

func TestReadBranchFromFS_MatchesGitCLI(t *testing.T) {
	repoRoot := findRepoRoot(t)

	// Get branch from git CLI
	cliResult, err := RunGitCommand(repoRoot, "branch", "--show-current")
	if err != nil {
		t.Fatalf("git branch --show-current failed: %v", err)
	}
	cliBranch := strings.TrimSpace(cliResult)

	// Get branch from filesystem
	fsBranch, err := ReadBranchFromFS(repoRoot)
	if err != nil {
		t.Fatalf("ReadBranchFromFS failed: %v", err)
	}

	if cliBranch != fsBranch {
		t.Errorf("branch mismatch: CLI=%q FS=%q", cliBranch, fsBranch)
	}
}

func TestReadRefSHA_MatchesGitCLI(t *testing.T) {
	repoRoot := findRepoRoot(t)

	// For the current branch, compare ref SHA from filesystem vs git rev-parse
	branch, err := ReadBranchFromFS(repoRoot)
	if err != nil {
		t.Fatalf("ReadBranchFromFS failed: %v", err)
	}
	if branch == "" {
		t.Skip("detached HEAD")
	}

	cliSHA, err := RunGitCommand(repoRoot, "rev-parse", "refs/heads/"+branch)
	if err != nil {
		t.Fatalf("git rev-parse failed: %v", err)
	}

	gitDir, err := resolveGitDir(repoRoot)
	if err != nil {
		t.Fatalf("resolveGitDir failed: %v", err)
	}
	commonDir, err := resolveCommonGitDir(gitDir)
	if err != nil {
		t.Fatalf("resolveCommonGitDir failed: %v", err)
	}

	fsSHA, err := ReadRefSHA(commonDir, "refs/heads/"+branch)
	if err != nil {
		t.Fatalf("ReadRefSHA failed: %v", err)
	}

	if strings.TrimSpace(cliSHA) != fsSHA {
		t.Errorf("SHA mismatch: CLI=%q FS=%q", strings.TrimSpace(cliSHA), fsSHA)
	}
}

func TestResolveGitDir_RegularRepo(t *testing.T) {
	tmpDir := makeTempRepo(t)

	gitDir, err := resolveGitDir(tmpDir)
	if err != nil {
		t.Fatalf("resolveGitDir failed: %v", err)
	}

	// Should end with .git
	if filepath.Base(gitDir) != ".git" {
		t.Errorf("expected gitDir to end with .git, got %q", gitDir)
	}

	fi, err := os.Stat(gitDir)
	if err != nil {
		t.Fatalf("stat gitDir failed: %v", err)
	}
	if !fi.IsDir() {
		t.Errorf("expected gitDir to be a directory for regular repo, got file")
	}
}

func TestResolveGitDir_LinkedWorktree(t *testing.T) {
	repoRoot := findRepoRoot(t)

	// Check if the repo root itself is a linked worktree (.git is a file)
	gitPath := filepath.Join(repoRoot, ".git")
	fi, err := os.Lstat(gitPath)
	if err != nil {
		t.Fatalf("lstat .git: %v", err)
	}

	gitDir, err := resolveGitDir(repoRoot)
	if err != nil {
		t.Fatalf("resolveGitDir failed: %v", err)
	}

	if fi.IsDir() {
		t.Skip("repo root is a regular repo, not a linked worktree")
	}

	// For a linked worktree, gitDir should point into the main .git/worktrees/<name>
	if !strings.Contains(gitDir, "worktrees") {
		t.Logf("gitDir=%q (may be valid for non-standard layout)", gitDir)
	}

	// The resolved gitDir should be a real directory
	dfi, err := os.Stat(gitDir)
	if err != nil {
		t.Fatalf("stat resolved gitDir failed: %v", err)
	}
	if !dfi.IsDir() {
		t.Errorf("expected resolved gitDir to be a directory")
	}
}

func TestResolveCommonGitDir(t *testing.T) {
	repoRoot := findRepoRoot(t)

	gitDir, err := resolveGitDir(repoRoot)
	if err != nil {
		t.Fatalf("resolveGitDir failed: %v", err)
	}

	commonDir, err := resolveCommonGitDir(gitDir)
	if err != nil {
		t.Fatalf("resolveCommonGitDir failed: %v", err)
	}

	// The common dir should contain a refs directory
	refsDir := filepath.Join(commonDir, "refs")
	if _, err := os.Stat(refsDir); err != nil {
		t.Errorf("expected refs/ directory in commonDir %q: %v", commonDir, err)
	}
}

func TestReadBranchFromFS_DetachedHead(t *testing.T) {
	tmpDir := makeTempRepo(t)

	// Detach HEAD
	sha, err := RunGitCommand(tmpDir, "rev-parse", "HEAD")
	if err != nil {
		t.Fatalf("git rev-parse HEAD: %v", err)
	}
	if _, err := RunGitCommand(tmpDir, "checkout", "--detach", strings.TrimSpace(sha)); err != nil {
		t.Fatalf("git checkout --detach: %v", err)
	}

	branch, err := ReadBranchFromFS(tmpDir)
	if err != nil {
		t.Fatalf("ReadBranchFromFS failed: %v", err)
	}
	if branch != "" {
		t.Errorf("expected empty branch for detached HEAD, got %q", branch)
	}
}

func TestReadRefSHA_TempRepo(t *testing.T) {
	tmpDir := makeTempRepo(t)

	branch, err := ReadBranchFromFS(tmpDir)
	if err != nil {
		t.Fatalf("ReadBranchFromFS: %v", err)
	}
	if branch == "" {
		t.Skip("detached HEAD in temp repo")
	}

	// Compare filesystem vs CLI
	cliSHA, err := RunGitCommand(tmpDir, "rev-parse", "refs/heads/"+branch)
	if err != nil {
		t.Fatalf("git rev-parse: %v", err)
	}

	gitDir, err := resolveGitDir(tmpDir)
	if err != nil {
		t.Fatalf("resolveGitDir: %v", err)
	}
	commonDir, err := resolveCommonGitDir(gitDir)
	if err != nil {
		t.Fatalf("resolveCommonGitDir: %v", err)
	}

	fsSHA, err := ReadRefSHA(commonDir, "refs/heads/"+branch)
	if err != nil {
		t.Fatalf("ReadRefSHA: %v", err)
	}

	if strings.TrimSpace(cliSHA) != fsSHA {
		t.Errorf("SHA mismatch: CLI=%q FS=%q", strings.TrimSpace(cliSHA), fsSHA)
	}
}

func TestWorktreeChangeDetector_StatusCacheHit(t *testing.T) {
	tmpDir := makeTempRepo(t)
	detector := &worktreeChangeDetector{entries: make(map[string]*worktreeCacheEntry)}

	gitDir, err := resolveGitDir(tmpDir)
	if err != nil {
		t.Fatalf("resolveGitDir failed: %v", err)
	}

	// Initially should be a miss
	hit, _, _, _ := detector.CheckStatus(gitDir)
	if hit {
		t.Error("expected cache miss on first check")
	}

	// Update the cache
	detector.UpdateStatus(gitDir, true, 0, nil)

	// Should be a hit now
	hit, clean, count, changes := detector.CheckStatus(gitDir)
	if !hit {
		t.Error("expected cache hit after update")
	}
	if !clean {
		t.Error("expected clean=true")
	}
	if count != 0 {
		t.Errorf("expected count=0, got %d", count)
	}
	if changes != nil {
		t.Errorf("expected nil changes, got %v", changes)
	}
}

func TestWorktreeChangeDetector_StatusCacheDirty(t *testing.T) {
	tmpDir := makeTempRepo(t)
	detector := &worktreeChangeDetector{entries: make(map[string]*worktreeCacheEntry)}

	gitDir, err := resolveGitDir(tmpDir)
	if err != nil {
		t.Fatalf("resolveGitDir failed: %v", err)
	}

	testChanges := []FileChange{
		{Status: "M", Path: "foo.go"},
		{Status: "A", Path: "bar.go"},
	}
	detector.UpdateStatus(gitDir, false, 2, testChanges)

	hit, clean, count, changes := detector.CheckStatus(gitDir)
	if !hit {
		t.Error("expected cache hit after update")
	}
	if clean {
		t.Error("expected clean=false")
	}
	if count != 2 {
		t.Errorf("expected count=2, got %d", count)
	}
	if len(changes) != 2 {
		t.Errorf("expected 2 changes, got %d", len(changes))
	}
}

func TestWorktreeChangeDetector_StatusCacheInvalidation(t *testing.T) {
	tmpDir := makeTempRepo(t)
	detector := &worktreeChangeDetector{entries: make(map[string]*worktreeCacheEntry)}

	gitDir, err := resolveGitDir(tmpDir)
	if err != nil {
		t.Fatalf("resolveGitDir failed: %v", err)
	}

	// Populate cache
	detector.UpdateStatus(gitDir, true, 0, nil)

	// Verify cache hit
	hit, _, _, _ := detector.CheckStatus(gitDir)
	if !hit {
		t.Fatal("expected cache hit")
	}

	// Modify a tracked file to trigger an index change
	testFile := filepath.Join(tmpDir, "test.txt")
	if err := os.WriteFile(testFile, []byte("modified"), 0644); err != nil {
		t.Fatalf("write test file: %v", err)
	}
	// Stage the change to modify the index mtime
	if _, err := RunGitCommand(tmpDir, "add", "."); err != nil {
		t.Fatalf("git add: %v", err)
	}

	// Now the index mtime has changed, so cache should miss
	hit, _, _, _ = detector.CheckStatus(gitDir)
	if hit {
		t.Error("expected cache miss after index modification")
	}
}

func TestCommitCache_GetSet(t *testing.T) {
	cache := &commitCache{entries: make(map[string][]CommitDetail)}

	// Miss on empty cache
	_, ok := cache.Get("abc123")
	if ok {
		t.Error("expected miss on empty cache")
	}

	// Empty SHA should be a miss
	_, ok = cache.Get("")
	if ok {
		t.Error("expected miss on empty SHA")
	}

	// Set and get
	commits := []CommitDetail{{Hash: "abc", Message: "test"}}
	cache.Set("abc123", commits)

	got, ok := cache.Get("abc123")
	if !ok {
		t.Error("expected cache hit")
	}
	if len(got) != 1 || got[0].Hash != "abc" {
		t.Errorf("unexpected cached commits: %v", got)
	}

	// Set with empty SHA should be a no-op
	cache.Set("", commits)
	_, ok = cache.Get("")
	if ok {
		t.Error("expected miss on empty SHA even after Set")
	}
}
