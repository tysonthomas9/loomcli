package opencode

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

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
		if out, err := exec.Command("git", args...).CombinedOutput(); err != nil { //nolint:norawexec // a real git worktree is what EnvFile reads
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

// TestOpenWaitsForBridgeTools: a session with bridge settings opens only
// once OpenCode reports the loom server connected for its directory, and
// then only after catalogSettle; one whose server never connects fails
// closed with no session made, so it is never prompted without its tools.
func TestOpenWaitsForBridgeTools(t *testing.T) {
	defer func(w, s time.Duration) { agentWait, catalogSettle = w, s }(agentWait, catalogSettle)
	agentWait, catalogSettle = 300*time.Millisecond, 200*time.Millisecond
	dir := t.TempDir()
	st := newStore()
	c := fakeServer(t, st)
	c.bridgeFile = func(string) (string, error) { return filepath.Join(dir, "settings.json"), nil }
	spec := loomharness.OpenSpec{Key: "lead", Dir: dir, Launch: loomharness.Launch{Env: map[string]string{agentmcp.EnvToken: "tok"}}}
	ctx := context.Background()

	st.mcp = "failed"
	if _, err := c.Open(ctx, spec); !errors.Is(err, loomharness.ErrUnavailable) {
		t.Fatalf("Open with the bridge failed = %v; want harness unavailable", err)
	}
	if len(st.sessions) != 0 {
		t.Fatalf("sessions after a failed bridge = %d; want none", len(st.sessions))
	}

	st.mu.Lock()
	st.mcp = "connected"
	st.mu.Unlock()
	start := time.Now()
	if _, err := c.Open(ctx, spec); err != nil {
		t.Fatal(err)
	}
	if d := time.Since(start); d < catalogSettle {
		t.Fatalf("Open returned %s after connected; want at least catalogSettle %s", d, catalogSettle)
	}

	// A later hand-off finds the server still connected: no settle.
	start = time.Now()
	if err := c.bridge(ctx, dir, spec.Launch.Env); err != nil {
		t.Fatal(err)
	}
	if d := time.Since(start); d >= catalogSettle {
		t.Fatalf("a hand-off on a settled bridge took %s; want no settle", d)
	}

	// The bridge restarts: the next hand-off sees it not yet connected, then
	// connected, and settles again.
	st.mu.Lock()
	st.mcp = "pending"
	st.mu.Unlock()
	go func() {
		time.Sleep(150 * time.Millisecond)
		st.mu.Lock()
		st.mcp = "connected"
		st.mu.Unlock()
	}()
	start = time.Now()
	if err := c.bridge(ctx, dir, spec.Launch.Env); err != nil {
		t.Fatal(err)
	}
	if d := time.Since(start); d < 150*time.Millisecond+catalogSettle {
		t.Fatalf("a hand-off after the bridge reconnected took %s; want the settle after it connected", d)
	}
}
