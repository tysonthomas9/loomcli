package opencode

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/agentmcp"
	"github.com/tysonthomas9/loomcli/internal/loomharness"
)

// TestBridgeRegisteredNotWritten: an agent's bridge, with its token, is
// registered as the "loom" MCP server of its location through OpenCode's
// runtime API, nothing is written to disk, an agent with settings is refused
// with no bridge command configured, and the registration is removed when
// its session unloads.
func TestBridgeRegisteredNotWritten(t *testing.T) {
	dir := t.TempDir()
	st := newStore()
	st.sessions["ses_1"] = map[string]any{"id": "ses_1", "location": map[string]string{"directory": dir}}
	c := fakeServer(t, st)
	ctx := context.Background()
	env := map[string]string{agentmcp.EnvToken: "tok"}
	if err := c.bridge(ctx, dir, env); !errors.Is(err, loomharness.ErrUnavailable) || st.puts != 0 {
		t.Fatalf("bridge with no command = %v, %d PUTs; want unavailable, none", err, st.puts)
	}
	c.bridgeCmd = []string{"/loom", "agent", "mcp-bridge"}
	if err := c.bridge(ctx, dir, env); err != nil {
		t.Fatal(err)
	}
	st.mu.Lock()
	got, _ := json.Marshal(st.bridges[dir])
	st.mu.Unlock()
	if want := `{"command":["/loom","agent","mcp-bridge"],"environment":{"LOOM_AGENT_TOKEN":"tok"},"type":"local"}`; string(got) != want {
		t.Fatalf("registered config = %s; want %s", got, want)
	}
	if entries, err := os.ReadDir(dir); err != nil || len(entries) != 0 {
		t.Fatalf("worktree holds %v, %v; want nothing written", entries, err)
	}
	s := c.Session(loomharness.NativeRef{NativeID: "ses_1"})
	if err := s.Unload(ctx); err != nil {
		t.Fatal(err)
	}
	if _, ok := st.bridges[dir]; ok {
		t.Fatal("bridge still registered after Unload")
	}
	if err := s.Unload(ctx); err != nil {
		t.Fatalf("Unload with the bridge already gone = %v; want nil", err)
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
	c.bridgeCmd = []string{"/loom", "agent", "mcp-bridge"}
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

	// OpenCode restarted and lost the registration: registered again, and
	// settled again.
	st.mu.Lock()
	delete(st.bridges, dir)
	puts := st.puts
	st.mu.Unlock()
	start = time.Now()
	if err := c.bridge(ctx, dir, spec.Launch.Env); err != nil {
		t.Fatal(err)
	}
	if d := time.Since(start); d < catalogSettle || st.puts != puts+1 {
		t.Fatalf("a hand-off after a lost registration took %s with %d PUTs; want one PUT and the settle", d, st.puts-puts)
	}
}

// TestUnbridgeGoneLocation: removing the bridge of a location whose
// directory is gone, which OpenCode has not loaded, succeeds: OpenCode
// answers 500 there, for its MCP list too, and holds no bridge for it. A
// Delete removes the working copy first, so for an idle agent every Retire
// hit that 500 and the agent stayed stopping. A 500 from a loaded location,
// whose directory is gone or not, is still reported: it may hold the bridge.
func TestUnbridgeGoneLocation(t *testing.T) {
	st := newStore()
	c := fakeServer(t, st)
	ctx := context.Background()
	if err := c.unbridge(ctx, filepath.Join(t.TempDir(), "removed")); err != nil {
		t.Fatalf("unbridge of a gone, unloaded location = %v; want nil", err)
	}
	loaded := filepath.Join(t.TempDir(), "loaded")
	st.mu.Lock()
	st.mcpDown, st.bridges = true, map[string]map[string]any{loaded: {"type": "local"}}
	st.mu.Unlock()
	if err := c.unbridge(ctx, t.TempDir()); err == nil {
		t.Fatal("unbridge with OpenCode failing = nil; want the error")
	}
	if err := c.unbridge(ctx, loaded); err == nil {
		t.Fatal("unbridge of a loaded location whose directory is gone, with OpenCode failing = nil; want the error")
	}
}
