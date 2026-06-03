package backends

import (
	"bytes"
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/flue"
)

// TestFlueLeadServerE2E exercises the real lead-server Go path (StartServer →
// flueLeadPrompt → streamFlueSSE, multi-turn) against a live flue server. It is
// gated behind LOOM_FLUE_E2E because it needs Node, a built flue project
// (LOOM_FLUE_PROJECT_DIR or ~/.loom/flue), provider auth, and network.
func TestFlueLeadServerE2E(t *testing.T) {
	requireFlueE2E(t)
	workDir := t.TempDir()
	ctx := context.Background()

	srv, err := flue.DefaultManager().StartServer(ctx, slog.Default(), workDir, resolveFlueModel())
	if err != nil {
		t.Fatalf("StartServer: %v", err)
	}
	defer srv.Stop()

	id := leadInstanceID(workDir)

	var turn1 bytes.Buffer
	if err := flueLeadPrompt(ctx, srv.URL(), id, "Create a file named lead-e2e.txt containing exactly: lead e2e ok. Then briefly confirm.", &turn1); err != nil {
		t.Fatalf("turn 1: %v", err)
	}
	t.Logf("turn1 output: %s", strings.TrimSpace(turn1.String()))

	data, err := os.ReadFile(filepath.Join(workDir, "lead-e2e.txt"))
	if err != nil {
		t.Fatalf("agent did not create lead-e2e.txt: %v", err)
	}
	if !strings.Contains(string(data), "lead e2e ok") {
		t.Fatalf("file content = %q, want it to contain 'lead e2e ok'", string(data))
	}

	// Multi-turn continuity against the same instance id.
	var turn2 bytes.Buffer
	if err := flueLeadPrompt(ctx, srv.URL(), id, "What file did you just create? Answer in one line.", &turn2); err != nil {
		t.Fatalf("turn 2: %v", err)
	}
	t.Logf("turn2 output: %s", strings.TrimSpace(turn2.String()))
	if !strings.Contains(strings.ToLower(turn2.String()), "lead-e2e.txt") {
		t.Fatalf("turn 2 did not recall the file (no continuity): %q", turn2.String())
	}
}
