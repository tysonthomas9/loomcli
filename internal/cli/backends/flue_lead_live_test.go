package backends

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/flue"
)

func requireFlueE2E(t *testing.T) {
	t.Helper()
	if os.Getenv("LOOM_FLUE_E2E") == "" {
		t.Skip("set LOOM_FLUE_E2E=1 to run (needs node + built flue project + provider auth)")
	}
}

// TestFlueInvokeLeadREPL exercises the real interactive stdin loop in
// defaultFlueInvokeLead: it swaps os.Stdin for a pipe, feeds one user message
// after the seeded turn, and verifies the agent acted on it.
func TestFlueInvokeLeadREPL(t *testing.T) {
	requireFlueE2E(t)
	workDir := t.TempDir()

	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	oldStdin := os.Stdin
	os.Stdin = r
	t.Cleanup(func() { os.Stdin = oldStdin })

	go func() {
		_, _ = io.WriteString(w, "Create repl-ok.txt containing exactly: repl ok. Then stop.\n")
		_ = w.Close() // EOF ends the REPL after this turn
	}()

	// Light seed prompt so the first turn is quick (not the full lead prompt).
	if err := defaultFlueInvokeLead(workDir, "You are a test assistant. Reply 'ready' and wait for instructions."); err != nil {
		t.Fatalf("defaultFlueInvokeLead: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(workDir, "repl-ok.txt"))
	if err != nil {
		t.Fatalf("REPL turn did not create the file: %v", err)
	}
	if !strings.Contains(string(data), "repl ok") {
		t.Fatalf("file content = %q, want 'repl ok'", string(data))
	}
}

// TestFlueTwoServersDistinctPorts confirms two concurrently-started servers get
// distinct ports, both pass /healthz, and both stop cleanly (no leaked node).
func TestFlueTwoServersDistinctPorts(t *testing.T) {
	requireFlueE2E(t)
	ctx := context.Background()
	mgr := flue.DefaultManager()

	s1, err := mgr.StartServer(ctx, slog.Default(), t.TempDir(), resolveFlueModel())
	if err != nil {
		t.Fatalf("server 1: %v", err)
	}
	defer s1.Stop()
	s2, err := mgr.StartServer(ctx, slog.Default(), t.TempDir(), resolveFlueModel())
	if err != nil {
		t.Fatalf("server 2: %v", err)
	}
	defer s2.Stop()

	if s1.URL() == s2.URL() {
		t.Fatalf("both servers got the same URL %s (port collision)", s1.URL())
	}
	for _, u := range []string{s1.URL(), s2.URL()} {
		resp, err := http.Get(u + "/healthz") //nolint:noctx // test
		if err != nil {
			t.Fatalf("healthz %s: %v", u, err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("healthz %s = %d", u, resp.StatusCode)
		}
	}

	// Stop s1 and confirm it stops serving while s2 keeps serving.
	s1.Stop()
	time.Sleep(200 * time.Millisecond)
	if resp, err := http.Get(s1.URL() + "/healthz"); err == nil { //nolint:noctx // test
		_ = resp.Body.Close()
		t.Errorf("server 1 still serving after Stop")
	}
	resp, err := http.Get(s2.URL() + "/healthz") //nolint:noctx // test
	if err != nil {
		t.Fatalf("server 2 should still be healthy after stopping server 1: %v", err)
	}
	_ = resp.Body.Close()
}

// TestFlueInstancesIsolated confirms two agent instances on one server keep
// separate conversation history (no cross-talk).
func TestFlueInstancesIsolated(t *testing.T) {
	requireFlueE2E(t)
	ctx := context.Background()
	srv, err := flue.DefaultManager().StartServer(ctx, slog.Default(), t.TempDir(), resolveFlueModel())
	if err != nil {
		t.Fatalf("StartServer: %v", err)
	}
	defer srv.Stop()

	var a bytes.Buffer
	if err := flueLeadPrompt(ctx, srv.URL(), "ws-aaaaaaaa", "Remember this: the secret word is BANANA. Just acknowledge.", &a); err != nil {
		t.Fatalf("instance A: %v", err)
	}

	var b bytes.Buffer
	if err := flueLeadPrompt(ctx, srv.URL(), "ws-bbbbbbbb", "What is the secret word? If you were never told, reply exactly: UNKNOWN.", &b); err != nil {
		t.Fatalf("instance B: %v", err)
	}
	if strings.Contains(strings.ToUpper(b.String()), "BANANA") {
		t.Fatalf("instance B leaked instance A's history: %q", b.String())
	}
	t.Logf("instance B (isolated) said: %s", strings.TrimSpace(b.String()))
}
