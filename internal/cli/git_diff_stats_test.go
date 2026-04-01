package cli

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestParseDiffNumstat_WithChanges(t *testing.T) {
	output := "10\t3\tfile1.go\n5\t0\tfile2.go\n"
	stats := parseDiffNumstat(output)

	if stats.FilesChanged != 2 {
		t.Errorf("FilesChanged = %d, want 2", stats.FilesChanged)
	}
	if stats.LinesAdded != 15 {
		t.Errorf("LinesAdded = %d, want 15", stats.LinesAdded)
	}
	if stats.LinesRemoved != 3 {
		t.Errorf("LinesRemoved = %d, want 3", stats.LinesRemoved)
	}
	if len(stats.FilesTouched) != 2 || stats.FilesTouched[0] != "file1.go" || stats.FilesTouched[1] != "file2.go" {
		t.Errorf("FilesTouched = %v, want [file1.go file2.go]", stats.FilesTouched)
	}
}

func TestParseDiffNumstat_BinaryFiles(t *testing.T) {
	output := "10\t3\tfile1.go\n-\t-\timage.png\n"
	stats := parseDiffNumstat(output)

	if stats.FilesChanged != 2 {
		t.Errorf("FilesChanged = %d, want 2 (includes binary)", stats.FilesChanged)
	}
	if stats.LinesAdded != 10 {
		t.Errorf("LinesAdded = %d, want 10 (binary excluded)", stats.LinesAdded)
	}
	if stats.LinesRemoved != 3 {
		t.Errorf("LinesRemoved = %d, want 3 (binary excluded)", stats.LinesRemoved)
	}
	if len(stats.FilesTouched) != 2 || stats.FilesTouched[0] != "file1.go" || stats.FilesTouched[1] != "image.png" {
		t.Errorf("FilesTouched = %v, want [file1.go image.png]", stats.FilesTouched)
	}
}

func TestParseDiffNumstat_EmptyOutput(t *testing.T) {
	stats := parseDiffNumstat("")
	if stats.FilesChanged != 0 || stats.LinesAdded != 0 || stats.LinesRemoved != 0 {
		t.Errorf("expected zero stats for empty output, got %+v", stats)
	}
	if len(stats.FilesTouched) != 0 {
		t.Errorf("FilesTouched = %v, want empty", stats.FilesTouched)
	}
}

func TestParseDiffNumstat_MalformedLine(t *testing.T) {
	output := "not-a-number\tfile.go\n"
	stats := parseDiffNumstat(output)
	if stats.FilesChanged != 0 {
		t.Errorf("expected 0 files for malformed input, got %d", stats.FilesChanged)
	}
}

func TestParseDiffNumstat_Renames(t *testing.T) {
	output := "10\t5\told.go => new.go\n3\t1\t{src/old => src/new}/main.go\n"
	stats := parseDiffNumstat(output)
	if len(stats.FilesTouched) != 2 {
		t.Fatalf("FilesTouched len = %d, want 2", len(stats.FilesTouched))
	}
	if stats.FilesTouched[0] != "new.go" {
		t.Errorf("FilesTouched[0] = %q, want %q", stats.FilesTouched[0], "new.go")
	}
	if stats.FilesTouched[1] != "src/new/main.go" {
		t.Errorf("FilesTouched[1] = %q, want %q", stats.FilesTouched[1], "src/new/main.go")
	}
}

func TestComputeDiffStats_EmptyRef(t *testing.T) {
	stats := ComputeDiffStats("/tmp", "")
	if stats.FilesChanged != 0 || stats.LinesAdded != 0 || stats.LinesRemoved != 0 {
		t.Errorf("expected zero stats for empty ref, got %+v", stats)
	}
	if len(stats.FilesTouched) != 0 {
		t.Errorf("FilesTouched = %v, want empty", stats.FilesTouched)
	}
}

func TestComputeDiffStats_WithRealRepo(t *testing.T) {
	clearGitEnvVars(t)
	// Create a temp git repo
	dir := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...) //nolint:norawexec
		cmd.Dir = dir
		cmd.Env = gitSafeEnv(
			"GIT_AUTHOR_NAME=test", "GIT_AUTHOR_EMAIL=test@test.com",
			"GIT_COMMITTER_NAME=test", "GIT_COMMITTER_EMAIL=test@test.com",
		)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v failed: %v\n%s", args, err, out)
		}
	}

	run("init")
	run("config", "user.email", "test@test.com")
	run("config", "user.name", "test")

	// Initial commit
	if err := os.WriteFile(filepath.Join(dir, "file.txt"), []byte("hello\n"), 0600); err != nil {
		t.Fatal(err)
	}
	run("add", ".")
	run("commit", "-m", "initial")

	// Get initial ref
	cmd := exec.Command("git", "rev-parse", "HEAD") //nolint:norawexec
	cmd.Dir = dir
	cmd.Env = gitSafeEnv()
	out, err := cmd.Output()
	if err != nil {
		t.Fatal(err)
	}
	beforeRef := string(out[:len(out)-1])

	// Make changes
	if err := os.WriteFile(filepath.Join(dir, "file.txt"), []byte("hello\nworld\nextra\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "new.txt"), []byte("new file\n"), 0600); err != nil {
		t.Fatal(err)
	}
	run("add", ".")
	run("commit", "-m", "changes")

	stats := ComputeDiffStats(dir, beforeRef)
	if stats.FilesChanged != 2 {
		t.Errorf("FilesChanged = %d, want 2", stats.FilesChanged)
	}
	if stats.LinesAdded < 3 {
		t.Errorf("LinesAdded = %d, want >= 3", stats.LinesAdded)
	}
	if len(stats.FilesTouched) != 2 {
		t.Errorf("FilesTouched len = %d, want 2", len(stats.FilesTouched))
	}
}

func TestComputeDiffStats_NoChanges(t *testing.T) {
	dir := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...) //nolint:norawexec
		cmd.Dir = dir
		cmd.Env = gitSafeEnv(
			"GIT_AUTHOR_NAME=test", "GIT_AUTHOR_EMAIL=test@test.com",
			"GIT_COMMITTER_NAME=test", "GIT_COMMITTER_EMAIL=test@test.com",
		)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v failed: %v\n%s", args, err, out)
		}
	}

	run("init")
	run("config", "user.email", "test@test.com")
	run("config", "user.name", "test")
	if err := os.WriteFile(filepath.Join(dir, "file.txt"), []byte("hello\n"), 0600); err != nil {
		t.Fatal(err)
	}
	run("add", ".")
	run("commit", "-m", "initial")

	cmd := exec.Command("git", "rev-parse", "HEAD") //nolint:norawexec
	cmd.Dir = dir
	cmd.Env = gitSafeEnv()
	out, err := cmd.Output()
	if err != nil {
		t.Fatal(err)
	}
	ref := string(out[:len(out)-1])

	stats := ComputeDiffStats(dir, ref)
	if stats.FilesChanged != 0 || stats.LinesAdded != 0 || stats.LinesRemoved != 0 {
		t.Errorf("expected zero stats for same ref, got %+v", stats)
	}
	if len(stats.FilesTouched) != 0 {
		t.Errorf("FilesTouched = %v, want empty", stats.FilesTouched)
	}
}

func TestComputeDiffStats_InvalidRef(t *testing.T) {
	dir := t.TempDir()
	cmd := exec.Command("git", "init") //nolint:norawexec
	cmd.Dir = dir
	cmd.Env = gitSafeEnv()
	if err := cmd.Run(); err != nil {
		t.Fatal(err)
	}

	stats := ComputeDiffStats(dir, "nonexistent-ref")
	if stats.FilesChanged != 0 || stats.LinesAdded != 0 || stats.LinesRemoved != 0 {
		t.Errorf("expected zero stats for invalid ref, got %+v", stats)
	}
	if len(stats.FilesTouched) != 0 {
		t.Errorf("FilesTouched = %v, want empty", stats.FilesTouched)
	}
}
