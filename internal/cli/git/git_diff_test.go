package git

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// setupDiffTestRepo creates a git repo with a main branch and a feature branch
// containing various changes (add, modify, delete, rename, binary).
// Returns the repo dir and the merge-base hash.
func setupDiffTestRepo(t *testing.T) (string, string) {
	t.Helper()
	clearGitEnvVars(t)
	dir := t.TempDir()

	run := func(args ...string) string {
		t.Helper()
		cmd := exec.Command("git", args...) //nolint:norawexec
		cmd.Dir = dir
		cmd.Env = gitSafeEnv(
			"GIT_AUTHOR_NAME=test", "GIT_AUTHOR_EMAIL=test@test.com",
			"GIT_COMMITTER_NAME=test", "GIT_COMMITTER_EMAIL=test@test.com",
		)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v failed: %v\n%s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}

	write := func(name, content string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}

	// Init repo on main
	run("init", "-b", "main")
	run("config", "user.email", "test@test.com")
	run("config", "user.name", "test")

	// Initial commit on main
	write("existing.txt", "line1\nline2\nline3\n")
	write("to-delete.txt", "will be deleted\n")
	write("to-rename.txt", "will be renamed\n")
	run("add", ".")
	run("commit", "-m", "initial commit")

	// Create feature branch
	run("checkout", "-b", "feature")

	// Modify a file
	write("existing.txt", "line1\nline2 modified\nline3\nline4 added\n")

	// Add a new file
	write("new-file.txt", "brand new content\n")

	// Delete a file
	run("rm", "to-delete.txt")

	// Rename a file
	run("mv", "to-rename.txt", "renamed.txt")

	// Add a binary file
	binaryContent := make([]byte, 256)
	for i := range binaryContent {
		binaryContent[i] = byte(i)
	}
	if err := os.WriteFile(filepath.Join(dir, "image.bin"), binaryContent, 0600); err != nil {
		t.Fatal(err)
	}

	run("add", ".")
	run("commit", "-m", "feature changes")

	// Second commit on feature
	write("new-file.txt", "brand new content\nmore content\n")
	run("add", ".")
	run("commit", "-m", "second feature change")

	// Get merge-base
	mergeBase := run("merge-base", "main", "HEAD")

	return dir, mergeBase
}

func TestResolveMergeBase_Success(t *testing.T) {
	dir, expectedBase := setupDiffTestRepo(t)

	got, err := ResolveMergeBase(dir, "main")
	if err != nil {
		t.Fatalf("ResolveMergeBase failed: %v", err)
	}
	if got != expectedBase {
		t.Errorf("merge-base = %q, want %q", got, expectedBase)
	}
}

func TestResolveMergeBase_InvalidRef(t *testing.T) {
	dir, _ := setupDiffTestRepo(t)

	_, err := ResolveMergeBase(dir, "../bad")
	if err == nil {
		t.Error("expected error for invalid ref, got nil")
	}
}

func TestResolveMergeBase_FallsBackToRemoteDefaultWhenConfiguredBranchMissing(t *testing.T) {
	clearGitEnvVars(t)
	root := t.TempDir()
	origin := filepath.Join(root, "origin.git")
	clone := filepath.Join(root, "clone")

	runIn := func(dir string, args ...string) string {
		t.Helper()
		cmd := exec.Command("git", args...) //nolint:norawexec
		cmd.Dir = dir
		cmd.Env = gitSafeEnv(
			"GIT_AUTHOR_NAME=test", "GIT_AUTHOR_EMAIL=test@test.com",
			"GIT_COMMITTER_NAME=test", "GIT_COMMITTER_EMAIL=test@test.com",
		)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v failed: %v\n%s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	write := func(dir, name, content string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}

	runIn(root, "init", "--bare", "--initial-branch=master", origin)
	runIn(root, "clone", origin, clone)
	runIn(clone, "config", "user.email", "test@test.com")
	runIn(clone, "config", "user.name", "test")
	write(clone, "README.md", "base\n")
	runIn(clone, "add", ".")
	runIn(clone, "commit", "-m", "base")
	runIn(clone, "push", "-u", "origin", "master")
	expectedBase := runIn(clone, "rev-parse", "HEAD")

	runIn(clone, "checkout", "-b", "feature")
	write(clone, "feature.txt", "feature\n")
	runIn(clone, "add", ".")
	runIn(clone, "commit", "-m", "feature")

	got, err := ResolveMergeBase(clone, "main")
	if err != nil {
		t.Fatalf("ResolveMergeBase failed: %v", err)
	}
	if got != expectedBase {
		t.Fatalf("merge-base = %q, want %q", got, expectedBase)
	}
}

func TestGoGitDiffSupportsLinkedWorktree(t *testing.T) {
	clearGitEnvVars(t)
	root := t.TempDir()
	repo := filepath.Join(root, "repo")
	worktree := filepath.Join(root, "worktree-feature")

	runIn := func(dir string, args ...string) string {
		t.Helper()
		cmd := exec.Command("git", args...) //nolint:norawexec
		cmd.Dir = dir
		cmd.Env = gitSafeEnv(
			"GIT_AUTHOR_NAME=test", "GIT_AUTHOR_EMAIL=test@test.com",
			"GIT_COMMITTER_NAME=test", "GIT_COMMITTER_EMAIL=test@test.com",
		)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v failed: %v\n%s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	write := func(dir, name, content string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}

	if err := os.MkdirAll(repo, 0700); err != nil {
		t.Fatal(err)
	}
	runIn(repo, "init", "-b", "main")
	runIn(repo, "config", "user.email", "test@test.com")
	runIn(repo, "config", "user.name", "test")
	write(repo, "base.txt", "base\n")
	runIn(repo, "add", ".")
	runIn(repo, "commit", "-m", "base")
	expectedBase := runIn(repo, "rev-parse", "HEAD")

	runIn(repo, "worktree", "add", "-b", "feature", worktree, "main")
	write(worktree, "feature.txt", "feature\n")
	runIn(worktree, "add", ".")
	runIn(worktree, "commit", "-m", "feature")

	got, err := ResolveMergeBase(worktree, "main")
	if err != nil {
		t.Fatalf("ResolveMergeBase on linked worktree failed: %v", err)
	}
	if got != expectedBase {
		t.Fatalf("merge-base = %q, want %q", got, expectedBase)
	}

	files, err := DiffFiles(worktree, got, "HEAD")
	if err != nil {
		t.Fatalf("DiffFiles on linked worktree failed: %v", err)
	}
	if len(files) != 1 || files[0].Path != "feature.txt" || files[0].Status != "A" {
		t.Fatalf("files = %+v, want added feature.txt", files)
	}
}

func TestDiffCommits_Success(t *testing.T) {
	dir, mergeBase := setupDiffTestRepo(t)

	commits, err := DiffCommits(dir, mergeBase, 0)
	if err != nil {
		t.Fatalf("DiffCommits failed: %v", err)
	}
	if len(commits) != 2 {
		t.Fatalf("got %d commits, want 2", len(commits))
	}

	// First commit (newest first)
	if commits[0].Subject != "second feature change" {
		t.Errorf("first commit subject = %q, want %q", commits[0].Subject, "second feature change")
	}
	if commits[0].Author != "test" {
		t.Errorf("author = %q, want %q", commits[0].Author, "test")
	}
	if commits[0].Email != "test@test.com" {
		t.Errorf("email = %q, want %q", commits[0].Email, "test@test.com")
	}
	if commits[0].Hash == "" || commits[0].ShortHash == "" || commits[0].Date == "" {
		t.Error("hash, short_hash, or date is empty")
	}

	// Second commit (oldest)
	if commits[1].Subject != "feature changes" {
		t.Errorf("second commit subject = %q, want %q", commits[1].Subject, "feature changes")
	}
}

func TestDiffCommits_WithLimit(t *testing.T) {
	dir, mergeBase := setupDiffTestRepo(t)

	commits, err := DiffCommits(dir, mergeBase, 1)
	if err != nil {
		t.Fatalf("DiffCommits failed: %v", err)
	}
	if len(commits) != 1 {
		t.Fatalf("got %d commits, want 1", len(commits))
	}
}

func TestDiffCommits_NoCommits(t *testing.T) {
	clearGitEnvVars(t)
	dir := t.TempDir()
	run := func(args ...string) string {
		t.Helper()
		cmd := exec.Command("git", args...) //nolint:norawexec
		cmd.Dir = dir
		cmd.Env = gitSafeEnv(
			"GIT_AUTHOR_NAME=test", "GIT_AUTHOR_EMAIL=test@test.com",
			"GIT_COMMITTER_NAME=test", "GIT_COMMITTER_EMAIL=test@test.com",
		)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v failed: %v\n%s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}

	run("init", "-b", "main")
	run("config", "user.email", "test@test.com")
	run("config", "user.name", "test")
	if err := os.WriteFile(filepath.Join(dir, "f.txt"), []byte("hi\n"), 0600); err != nil {
		t.Fatal(err)
	}
	run("add", ".")
	run("commit", "-m", "init")
	head := run("rev-parse", "HEAD")

	commits, err := DiffCommits(dir, head, 0)
	if err != nil {
		t.Fatalf("DiffCommits failed: %v", err)
	}
	if len(commits) != 0 {
		t.Errorf("got %d commits, want 0", len(commits))
	}
}

func TestDiffFiles_Success(t *testing.T) {
	dir, mergeBase := setupDiffTestRepo(t)

	files, err := DiffFiles(dir, mergeBase, "HEAD")
	if err != nil {
		t.Fatalf("DiffFiles failed: %v", err)
	}

	fileMap := make(map[string]struct {
		status               string
		additions, deletions int
	})
	for _, f := range files {
		fileMap[f.Path] = struct {
			status               string
			additions, deletions int
		}{f.Status, f.Additions, f.Deletions}
	}

	// Check modified file
	if f, ok := fileMap["existing.txt"]; !ok {
		t.Error("missing existing.txt in diff")
	} else if f.status != "M" {
		t.Errorf("existing.txt status = %q, want M", f.status)
	} else if f.additions == 0 {
		t.Error("existing.txt should have additions")
	}

	// Check added file
	if f, ok := fileMap["new-file.txt"]; !ok {
		t.Error("missing new-file.txt in diff")
	} else if f.status != "A" {
		t.Errorf("new-file.txt status = %q, want A", f.status)
	}

	// Check deleted file
	if f, ok := fileMap["to-delete.txt"]; !ok {
		t.Error("missing to-delete.txt in diff")
	} else if f.status != "D" {
		t.Errorf("to-delete.txt status = %q, want D", f.status)
	}

	// Check binary file
	if f, ok := fileMap["image.bin"]; !ok {
		t.Error("missing image.bin in diff")
	} else if f.status != "A" {
		t.Errorf("image.bin status = %q, want A", f.status)
	} else if f.additions != 0 || f.deletions != 0 {
		t.Errorf("image.bin should have 0 additions/deletions for binary, got %d/%d", f.additions, f.deletions)
	}
}

func TestDiffFiles_WithRename(t *testing.T) {
	dir, mergeBase := setupDiffTestRepo(t)

	files, err := DiffFiles(dir, mergeBase, "HEAD")
	if err != nil {
		t.Fatalf("DiffFiles failed: %v", err)
	}

	var found bool
	for _, f := range files {
		if f.Path == "renamed.txt" {
			found = true
			if f.Status != "R" {
				t.Errorf("renamed.txt status = %q, want R", f.Status)
			}
			if f.OldPath != "to-rename.txt" {
				t.Errorf("renamed.txt old_path = %q, want %q", f.OldPath, "to-rename.txt")
			}
			break
		}
	}
	if !found {
		t.Error("renamed.txt not found in diff files")
	}
}

func TestDiffFiles_WithSpecialCharacterFilenames(t *testing.T) {
	clearGitEnvVars(t)
	dir := t.TempDir()
	run := func(args ...string) string {
		t.Helper()
		cmd := exec.Command("git", args...) //nolint:norawexec
		cmd.Dir = dir
		cmd.Env = gitSafeEnv(
			"GIT_AUTHOR_NAME=test", "GIT_AUTHOR_EMAIL=test@test.com",
			"GIT_COMMITTER_NAME=test", "GIT_COMMITTER_EMAIL=test@test.com",
		)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v failed: %v\n%s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	write := func(name, content string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}

	run("init", "-b", "main")
	run("config", "user.email", "test@test.com")
	run("config", "user.name", "test")
	write("base.txt", "base\n")
	run("add", ".")
	run("commit", "-m", "base")
	base := run("rev-parse", "HEAD")

	run("checkout", "-b", "feature")
	tabPath := "tab\tfile.txt"
	newlinePath := "line\nfile.txt"
	write(tabPath, "tab\n")
	write(newlinePath, "newline\n")
	run("add", ".")
	run("commit", "-m", "special filenames")

	files, err := DiffFiles(dir, base, "HEAD")
	if err != nil {
		t.Fatalf("DiffFiles failed: %v", err)
	}
	seen := map[string]string{}
	for _, file := range files {
		seen[file.Path] = file.Status
	}
	if seen[tabPath] != "A" {
		t.Fatalf("tab filename status = %q, want A; files=%+v", seen[tabPath], files)
	}
	if seen[newlinePath] != "A" {
		t.Fatalf("newline filename status = %q, want A; files=%+v", seen[newlinePath], files)
	}
}

func TestDiffFiles_NoChanges(t *testing.T) {
	clearGitEnvVars(t)
	dir := t.TempDir()
	run := func(args ...string) string {
		t.Helper()
		cmd := exec.Command("git", args...) //nolint:norawexec
		cmd.Dir = dir
		cmd.Env = gitSafeEnv(
			"GIT_AUTHOR_NAME=test", "GIT_AUTHOR_EMAIL=test@test.com",
			"GIT_COMMITTER_NAME=test", "GIT_COMMITTER_EMAIL=test@test.com",
		)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v failed: %v\n%s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}

	run("init", "-b", "main")
	run("config", "user.email", "test@test.com")
	run("config", "user.name", "test")
	if err := os.WriteFile(filepath.Join(dir, "f.txt"), []byte("hi\n"), 0600); err != nil {
		t.Fatal(err)
	}
	run("add", ".")
	run("commit", "-m", "init")
	head := run("rev-parse", "HEAD")

	files, err := DiffFiles(dir, head, "HEAD")
	if err != nil {
		t.Fatalf("DiffFiles failed: %v", err)
	}
	if len(files) != 0 {
		t.Errorf("got %d files, want 0", len(files))
	}
}

func TestDiffFilePatch_Success(t *testing.T) {
	dir, mergeBase := setupDiffTestRepo(t)

	result, err := DiffFilePatch(dir, mergeBase, "HEAD", "existing.txt")
	if err != nil {
		t.Fatalf("DiffFilePatch failed: %v", err)
	}
	if result.IsBinary {
		t.Error("existing.txt should not be binary")
	}
	if result.IsTooLarge {
		t.Error("existing.txt should not be too large")
	}
	if result.Patch == "" {
		t.Error("patch should not be empty")
	}
	if !strings.Contains(result.Patch, "existing.txt") {
		t.Error("patch should reference existing.txt")
	}
	if result.Additions == 0 {
		t.Error("should have additions")
	}
}

func TestDiffFilePatch_BinaryFile(t *testing.T) {
	dir, mergeBase := setupDiffTestRepo(t)

	result, err := DiffFilePatch(dir, mergeBase, "HEAD", "image.bin")
	if err != nil {
		t.Fatalf("DiffFilePatch failed: %v", err)
	}
	if !result.IsBinary {
		t.Error("image.bin should be binary")
	}
	if result.Patch != "" {
		t.Error("binary file patch should be empty")
	}
}

func TestDiffFilePatch_TooLarge(t *testing.T) {
	clearGitEnvVars(t)
	dir := t.TempDir()
	run := func(args ...string) string {
		t.Helper()
		cmd := exec.Command("git", args...) //nolint:norawexec
		cmd.Dir = dir
		cmd.Env = gitSafeEnv(
			"GIT_AUTHOR_NAME=test", "GIT_AUTHOR_EMAIL=test@test.com",
			"GIT_COMMITTER_NAME=test", "GIT_COMMITTER_EMAIL=test@test.com",
		)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v failed: %v\n%s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}

	run("init", "-b", "main")
	run("config", "user.email", "test@test.com")
	run("config", "user.name", "test")
	if err := os.WriteFile(filepath.Join(dir, "f.txt"), []byte("init\n"), 0600); err != nil {
		t.Fatal(err)
	}
	run("add", ".")
	run("commit", "-m", "init")
	base := run("rev-parse", "HEAD")

	// Create a file larger than 500KB
	bigContent := strings.Repeat("this is a line of text that will be repeated many times\n", 10000)
	if err := os.WriteFile(filepath.Join(dir, "f.txt"), []byte(bigContent), 0600); err != nil {
		t.Fatal(err)
	}
	run("add", ".")
	run("commit", "-m", "big change")

	result, err := DiffFilePatch(dir, base, "HEAD", "f.txt")
	if err != nil {
		t.Fatalf("DiffFilePatch failed: %v", err)
	}
	if !result.IsTooLarge {
		t.Error("should be flagged as too large")
	}
	if result.Patch != "" {
		t.Error("too-large patch should be empty")
	}
}

func TestDiffFilePatch_NoChange(t *testing.T) {
	clearGitEnvVars(t)
	dir := t.TempDir()
	run := func(args ...string) string {
		t.Helper()
		cmd := exec.Command("git", args...) //nolint:norawexec
		cmd.Dir = dir
		cmd.Env = gitSafeEnv(
			"GIT_AUTHOR_NAME=test", "GIT_AUTHOR_EMAIL=test@test.com",
			"GIT_COMMITTER_NAME=test", "GIT_COMMITTER_EMAIL=test@test.com",
		)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v failed: %v\n%s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}

	run("init", "-b", "main")
	run("config", "user.email", "test@test.com")
	run("config", "user.name", "test")
	if err := os.WriteFile(filepath.Join(dir, "f.txt"), []byte("unchanged\n"), 0600); err != nil {
		t.Fatal(err)
	}
	run("add", ".")
	run("commit", "-m", "init")
	head := run("rev-parse", "HEAD")

	result, err := DiffFilePatch(dir, head, "HEAD", "f.txt")
	if err != nil {
		t.Fatalf("DiffFilePatch failed: %v", err)
	}
	if result.Patch != "" {
		t.Errorf("expected empty patch for unchanged file, got %d bytes", len(result.Patch))
	}
	if result.IsBinary || result.IsTooLarge {
		t.Error("unchanged file should not be binary or too large")
	}
}

func TestParseNumstatRenamePath(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"old.txt => new.txt", "new.txt"},
		{"{old => new}/file.txt", "new/file.txt"},
		{"prefix/{old => new}/suffix.txt", "prefix/new/suffix.txt"},
	}
	for _, tt := range tests {
		got := parseNumstatRenamePath(tt.input)
		if got != tt.want {
			t.Errorf("parseNumstatRenamePath(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}
