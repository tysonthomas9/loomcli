package agentcapture

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func gitForCapture(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...) //nolint:norawexec // Real temporary repository verifies capture identity.
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v: %s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func TestCaptureWithoutGitconfigUsesLoomIdentity(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "xdg"))
	t.Setenv("GIT_CONFIG_GLOBAL", "")
	if err := os.Unsetenv("GIT_CONFIG_GLOBAL"); err != nil {
		t.Fatal(err)
	}
	repo := t.TempDir()
	gitForCapture(t, repo, "init", "-q")
	if err := os.WriteFile(filepath.Join(repo, "work.txt"), []byte("base"), 0600); err != nil {
		t.Fatal(err)
	}
	gitForCapture(t, repo, "add", "work.txt")
	gitForCapture(t, repo, "-c", "user.name=Base", "-c", "user.email=base@example.test", "commit", "-qm", "base")
	if err := os.WriteFile(filepath.Join(repo, "work.txt"), []byte("edited"), 0600); err != nil {
		t.Fatal(err)
	}
	result, err := Capture(context.Background(), repo, "ws", "attempt", "task", "Task")
	if err != nil {
		t.Fatal(err)
	}
	if !result.Complete || result.SHA == "" {
		t.Fatalf("capture: %+v", result)
	}
	if got := gitForCapture(t, repo, "show", "-s", "--format=%an <%ae>", result.SHA); got != "Loom <loom@localhost>" {
		t.Fatalf("capture identity: %q", got)
	}
}

func TestCaptureWithXDGIdentityAndNoGitconfig(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	xdg := filepath.Join(home, "xdg")
	t.Setenv("XDG_CONFIG_HOME", xdg)
	t.Setenv("GIT_CONFIG_GLOBAL", "")
	if err := os.Unsetenv("GIT_CONFIG_GLOBAL"); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(xdg, "git"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(xdg, "git", "config"), []byte("[user]\nname = XDG User\nemail = xdg@example.test\n"), 0600); err != nil {
		t.Fatal(err)
	}
	repo := t.TempDir()
	gitForCapture(t, repo, "init", "-q")
	gitForCapture(t, repo, "-c", "user.name=Base", "-c", "user.email=base@example.test", "commit", "--allow-empty", "-qm", "base")
	if err := os.WriteFile(filepath.Join(repo, "work.txt"), []byte("edited"), 0600); err != nil {
		t.Fatal(err)
	}
	result, err := Capture(context.Background(), repo, "ws", "attempt", "task", "Task")
	if err != nil {
		t.Fatal(err)
	}
	if got := gitForCapture(t, repo, "show", "-s", "--format=%an <%ae>", result.SHA); got != "XDG User <xdg@example.test>" {
		t.Fatalf("capture identity: %q", got)
	}
}
