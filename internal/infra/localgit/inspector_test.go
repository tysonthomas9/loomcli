package localgit

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/modules/sourcecontrol"
)

func TestInspectorMatchesWithoutReturningObservedRemote(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	root := t.TempDir()
	checkout := filepath.Join(root, "checkout")
	inspectorRunGit(t, "", "init", checkout)
	const remote = "https://github.com/acme/repo.git"
	inspectorRunGit(t, checkout, "remote", "add", "origin", remote)
	inspector := Inspector{}
	if match, err := inspector.MatchRemote(t.Context(), checkout, "origin", remote); err != nil || match != sourcecontrol.CheckoutMatched {
		t.Fatalf("matching checkout = %q, %v", match, err)
	}
	if match, err := inspector.MatchRemote(
		t.Context(),
		checkout,
		"origin",
		"https://github.com/acme/other.git",
	); err != nil || match != sourcecontrol.CheckoutConflict {
		t.Fatalf("different checkout = %q, %v", match, err)
	}
	if match, err := inspector.MatchRemote(
		t.Context(),
		filepath.Join(root, "missing"),
		"origin",
		remote,
	); err != nil || match != sourcecontrol.CheckoutMissing {
		t.Fatalf("missing checkout = %q, %v", match, err)
	}
}

func TestInspectorDoesNotReflectLegacyRemoteCredential(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	checkout := filepath.Join(t.TempDir(), "checkout")
	inspectorRunGit(t, "", "init", checkout)
	const observed = "https://user:legacy-remote-secret@github.com/acme/repo.git"
	inspectorRunGit(t, checkout, "remote", "add", "origin", observed)
	match, err := (Inspector{}).MatchRemote(
		t.Context(),
		checkout,
		"origin",
		"https://github.com/acme/repo.git",
	)
	if err != nil || match != sourcecontrol.CheckoutConflict {
		t.Fatalf("legacy remote inspection = %q, %v", match, err)
	}
	if err != nil && strings.Contains(err.Error(), "legacy-remote-secret") {
		t.Fatalf("inspection reflected legacy credential: %v", err)
	}
}

func TestInspectorRejectsSymlinkAndNonRepositoryTargets(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "target")
	if err := os.WriteFile(target, []byte("not a repository"), 0o600); err != nil {
		t.Fatal(err)
	}
	inspector := Inspector{}
	if match, err := inspector.MatchRemote(t.Context(), target, "origin", "/srv/repo.git"); err != nil || match != sourcecontrol.CheckoutConflict {
		t.Fatalf("file target = %q, %v", match, err)
	}
	link := filepath.Join(root, "link")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	if match, err := inspector.MatchRemote(t.Context(), link, "origin", "/srv/repo.git"); err != nil || match != sourcecontrol.CheckoutConflict {
		t.Fatalf("symlink target = %q, %v", match, err)
	}
}

func TestInspectorCanonicalTargetRejectsSymlinkedWorkspaceParentAndTarget(t *testing.T) {
	root := t.TempDir()
	workspace := filepath.Join(root, "workspace")
	if err := os.Mkdir(workspace, 0o700); err != nil {
		t.Fatal(err)
	}
	inspector := Inspector{}
	target := filepath.Join(workspace, "repo")
	canonical, err := inspector.CanonicalTarget(t.Context(), workspace, target)
	if err != nil {
		t.Fatalf("valid canonical target: %v", err)
	}
	resolvedWorkspace, err := filepath.EvalSymlinks(workspace)
	if err != nil {
		t.Fatal(err)
	}
	wantCanonical := filepath.Join(resolvedWorkspace, "repo")
	if canonical != wantCanonical {
		t.Fatalf("canonical target = %q, want %q", canonical, wantCanonical)
	}

	workspaceLink := filepath.Join(root, "workspace-link")
	if err := os.Symlink(workspace, workspaceLink); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	if _, err := inspector.CanonicalTarget(
		t.Context(),
		workspaceLink,
		filepath.Join(workspaceLink, "repo"),
	); err == nil {
		t.Fatal("symlinked workspace passed containment validation")
	}

	outside := filepath.Join(root, "outside")
	if err := os.Mkdir(outside, 0o700); err != nil {
		t.Fatal(err)
	}
	parentLink := filepath.Join(workspace, "parent-link")
	if err := os.Symlink(outside, parentLink); err != nil {
		t.Fatal(err)
	}
	if _, err := inspector.CanonicalTarget(
		t.Context(),
		workspace,
		filepath.Join(parentLink, "repo"),
	); err == nil {
		t.Fatal("symlinked target parent passed containment validation")
	}

	outsideTarget := filepath.Join(outside, "repo")
	if err := os.Mkdir(outsideTarget, 0o700); err != nil {
		t.Fatal(err)
	}
	targetLink := filepath.Join(workspace, "target-link")
	if err := os.Symlink(outsideTarget, targetLink); err != nil {
		t.Fatal(err)
	}
	if _, err := inspector.CanonicalTarget(t.Context(), workspace, targetLink); err == nil {
		t.Fatal("symlinked target passed containment validation")
	}
}

func inspectorRunGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	command := exec.Command("git", args...) //nolint:norawexec,gosec // test helper.
	command.Dir = dir
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v: %s", args, err, output)
	}
}
