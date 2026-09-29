//go:build sessionsreplay

package cleanup

import (
	"io"
	"os"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/tysonthomas9/loomcli/internal/sessions"
)

// #558: loom usage must read the session index that daemon/fleet runs write.
func TestSessionsReplay558UsageReadsSessionIndex(t *testing.T) {
	runtimeDir := t.TempDir()
	t.Setenv("LOOM_WORKSPACE_RUNTIME_DIR", runtimeDir)
	store, err := sessions.NewStore(runtimeDir)
	if err != nil {
		t.Fatal(err)
	}
	session, err := store.CreateSession(sessions.CreateOptions{AgentName: "usage-replay-worker", Backend: "codex"})
	if err != nil {
		t.Fatal(err)
	}
	if err := session.Finalize(sessions.FinalizeOptions{TaskID: "TASK-558", ExitCode: 0, InputTokens: 12}); err != nil {
		t.Fatal(err)
	}
	previousFormat := usageFormat
	usageFormat = "json"
	t.Cleanup(func() { usageFormat = previousFormat })
	read, write, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	previousStdout := os.Stdout
	os.Stdout = write
	defer func() { os.Stdout = previousStdout }()
	runUsage(&cobra.Command{}, nil)
	os.Stdout = previousStdout
	if err := write.Close(); err != nil {
		t.Fatal(err)
	}
	output, err := io.ReadAll(read)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(output), session.SessionID()) {
		t.Fatalf("#558: usage output omitted indexed session %s: %s", session.SessionID(), output)
	}
}
