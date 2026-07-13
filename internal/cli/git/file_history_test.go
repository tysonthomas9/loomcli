package git

import (
	"strings"
	"testing"
)

func TestDiffFileRootCommitUsesEmptyTree(t *testing.T) {
	repo := newGitDiffTestRepo(t, "main")
	repo.write("root.txt", "root line\n")
	root := repo.commitAll("root")

	patch, err := DiffFile(repo.dir, "root.txt", root+"^", root)
	if err != nil {
		t.Fatalf("DiffFile root commit failed: %v", err)
	}
	if patch == "" {
		t.Fatal("expected non-empty root commit patch")
	}
	for _, want := range []string{
		"diff --git a/root.txt b/root.txt",
		"new file mode",
		"--- /dev/null",
		"+++ b/root.txt",
		"+root line",
	} {
		if !strings.Contains(patch, want) {
			t.Fatalf("root commit patch missing %q:\n%s", want, patch)
		}
	}
}

func TestDiffFileNonRootCommitParentSyntaxStillWorks(t *testing.T) {
	repo := newGitDiffTestRepo(t, "main")
	repo.write("file.txt", "base\n")
	repo.commitAll("root")
	repo.write("file.txt", "base\nnext\n")
	head := repo.commitAll("second")

	patch, err := DiffFile(repo.dir, "file.txt", head+"^", head)
	if err != nil {
		t.Fatalf("DiffFile non-root commit failed: %v", err)
	}
	if !strings.Contains(patch, "+next") {
		t.Fatalf("non-root patch missing expected addition:\n%s", patch)
	}
	if strings.Contains(patch, "--- /dev/null") {
		t.Fatalf("non-root patch unexpectedly used empty tree:\n%s", patch)
	}
}
