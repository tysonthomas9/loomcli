//nolint:revive // Tests use the established driver package name to exercise unexported helpers.
package driver

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/domain"
)

func TestApplyPatchBackAppliesPatchWhenBaseMatches(t *testing.T) {
	ctx := context.Background()
	repo := newPatchBackRepo(t)
	base := repo.commitFile("file.txt", "old\n", "initial")
	patch := []byte("diff --git a/file.txt b/file.txt\n--- a/file.txt\n+++ b/file.txt\n@@ -1 +1 @@\n-old\n+new\n")

	result, err := ApplyPatchBack(ctx, PatchBackOptions{WorktreePath: repo.dir, BaseRef: base, Patch: patch})
	if err != nil {
		t.Fatalf("ApplyPatchBack: %v", err)
	}
	if !result.Applied || result.Status != PatchBackApplied || result.PreservePatch {
		t.Fatalf("result = %+v, want applied without patch preservation", result)
	}
	if got := repo.read("file.txt"); got != "new\n" {
		t.Fatalf("file content = %q, want patched content", got)
	}
}

func TestApplyPatchBackPreservesPatchWhenBaseMismatches(t *testing.T) {
	ctx := context.Background()
	repo := newPatchBackRepo(t)
	base := repo.commitFile("file.txt", "old\n", "initial")
	repo.commitFile("other.txt", "user change\n", "advance head")
	patch := []byte("diff --git a/file.txt b/file.txt\n--- a/file.txt\n+++ b/file.txt\n@@ -1 +1 @@\n-old\n+new\n")

	result, err := ApplyPatchBack(ctx, PatchBackOptions{WorktreePath: repo.dir, BaseRef: base, Patch: patch})
	if err != nil {
		t.Fatalf("ApplyPatchBack: %v", err)
	}
	if result.Applied || !result.PreservePatch || result.Status != PatchBackBaseMismatch || result.ErrorClass != PatchBackBaseMismatch {
		t.Fatalf("result = %+v, want base mismatch with preserved patch", result)
	}
	if string(result.PreservedPatch) != string(patch) {
		t.Fatalf("preserved patch = %q, want original patch", string(result.PreservedPatch))
	}
	if got := repo.read("file.txt"); got != "old\n" {
		t.Fatalf("file content = %q, want unchanged", got)
	}
}

func TestApplyPatchBackPreservesPatchWhenApplyCheckConflicts(t *testing.T) {
	ctx := context.Background()
	repo := newPatchBackRepo(t)
	base := repo.commitFile("file.txt", "old\n", "initial")
	repo.write("file.txt", "local edit\n")
	patch := []byte("diff --git a/file.txt b/file.txt\n--- a/file.txt\n+++ b/file.txt\n@@ -1 +1 @@\n-old\n+new\n")

	result, err := ApplyPatchBack(ctx, PatchBackOptions{WorktreePath: repo.dir, BaseRef: base, Patch: patch})
	if err != nil {
		t.Fatalf("ApplyPatchBack: %v", err)
	}
	if result.Applied || !result.PreservePatch || result.Status != PatchBackConflict || result.ErrorClass != PatchBackConflict {
		t.Fatalf("result = %+v, want conflict with preserved patch", result)
	}
	if string(result.PreservedPatch) != string(patch) {
		t.Fatalf("preserved patch = %q, want original patch", string(result.PreservedPatch))
	}
	if got := repo.read("file.txt"); got != "local edit\n" {
		t.Fatalf("file content = %q, want local edit preserved", got)
	}
}

func TestApplyPatchBackPreservesPatchWhenBaseRefIsUnreachable(t *testing.T) {
	ctx := context.Background()
	repo := newPatchBackRepo(t)
	repo.commitFile("file.txt", "old\n", "initial")
	patch := []byte("diff --git a/file.txt b/file.txt\n--- a/file.txt\n+++ b/file.txt\n@@ -1 +1 @@\n-old\n+new\n")

	result, err := ApplyPatchBack(ctx, PatchBackOptions{WorktreePath: repo.dir, BaseRef: "missing-base", Patch: patch})
	if err != nil {
		t.Fatalf("ApplyPatchBack: %v", err)
	}
	if result.Applied || !result.PreservePatch || result.Status != PatchBackBaseUnreachable || result.ErrorClass != PatchBackBaseUnreachable {
		t.Fatalf("result = %+v, want unreachable base with preserved patch", result)
	}
	if string(result.PreservedPatch) != string(patch) {
		t.Fatalf("preserved patch = %q, want original patch", string(result.PreservedPatch))
	}
}

func TestApplyPatchBackRejectsMissingInputs(t *testing.T) {
	ctx := context.Background()
	if _, err := ApplyPatchBack(ctx, PatchBackOptions{}); !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("ApplyPatchBack empty err = %v, want ErrInvalid", err)
	}
}

type patchBackRepo struct {
	t   *testing.T
	dir string
}

func newPatchBackRepo(t *testing.T) patchBackRepo {
	t.Helper()
	dir := t.TempDir()
	repo := patchBackRepo{t: t, dir: dir}
	repo.git("init", "--initial-branch=main")
	repo.git("config", "user.email", "test@example.com")
	repo.git("config", "user.name", "Test User")
	return repo
}

func (r patchBackRepo) commitFile(path, content, message string) string {
	r.t.Helper()
	r.write(path, content)
	r.git("add", path)
	r.git("commit", "-m", message)
	return strings.TrimSpace(r.git("rev-parse", "HEAD"))
}

func (r patchBackRepo) write(path, content string) {
	r.t.Helper()
	fullPath := filepath.Join(r.dir, path)
	if err := os.MkdirAll(filepath.Dir(fullPath), 0o755); err != nil {
		r.t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(fullPath, []byte(content), 0o644); err != nil {
		r.t.Fatalf("write %s: %v", path, err)
	}
}

func (r patchBackRepo) read(path string) string {
	r.t.Helper()
	body, err := os.ReadFile(filepath.Join(r.dir, path))
	if err != nil {
		r.t.Fatalf("read %s: %v", path, err)
	}
	return string(body)
}

func (r patchBackRepo) git(args ...string) string {
	r.t.Helper()
	cmd := exec.Command("git", args...) //nolint:gosec //nolint:norawexec // test helper uses fixed git executable with test-controlled args.
	cmd.Dir = r.dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		r.t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return string(out)
}
