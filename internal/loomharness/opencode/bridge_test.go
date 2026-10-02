package opencode

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/agentmcp"
	"github.com/tysonthomas9/loomcli/internal/loomharness"
)

// TestBridgeSettingsPrivateAndUnloaded: an agent's bridge settings, with its
// token, are written mode 0600 into its worktree's private git dir, refused
// with no place configured, and removed when its session unloads.
func TestBridgeSettingsPrivateAndUnloaded(t *testing.T) {
	root := t.TempDir()
	repo, dir := filepath.Join(root, "repo"), filepath.Join(root, "agent1")
	for _, args := range [][]string{{"init", "-q", repo}, {"-C", repo, "-c", "user.name=t", "-c", "user.email=t@t",
		"commit", "-q", "--allow-empty", "-m", "init"}, {"-C", repo, "worktree", "add", "-q", "--detach", dir}} {
		if out, err := exec.Command("git", args...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	st := newStore()
	st.sessions["ses_1"] = map[string]any{"id": "ses_1", "location": map[string]string{"directory": dir}}
	c := fakeServer(t, st)
	env := map[string]string{agentmcp.EnvToken: "tok"}
	if err := c.bridgeEnv(dir, env); err == nil {
		t.Fatal("bridgeEnv wrote settings with no settings file configured")
	}
	c.bridgeFile = agentmcp.EnvFile
	if err := c.bridgeEnv(dir, env); err != nil {
		t.Fatal(err)
	}
	file, _ := agentmcp.EnvFile(dir)
	if fi, err := os.Stat(file); err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("settings file = %v, %v; want mode 0600", fi, err)
	}
	if err := c.Session(loomharness.NativeRef{NativeID: "ses_1"}).Unload(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(file); !os.IsNotExist(err) {
		t.Fatalf("settings after Unload: %v; want removed", err)
	}
}
