package hooks

import (
	"bytes"
	"encoding/json"
	"io"
	"testing"

	"github.com/spf13/cobra"

	hwharness "github.com/olesho/harness-wrapper/pkg/harness"
)

func TestRunDispatchForwardsToSpool(t *testing.T) {
	spool := t.TempDir()
	t.Setenv("HW_EVENT_SPOOL", spool)
	t.Setenv("HW_HOME", t.TempDir())

	stdin, _ := json.Marshal(map[string]string{"session_id": "s-1", "transcript_path": "/ignored"})
	cmd := &cobra.Command{}
	cmd.SetIn(bytes.NewReader(stdin))
	cmd.SetOut(io.Discard)

	// session-start emits a session marker (no transcript read), spooled.
	if err := runDispatch(cmd, "claude", "session-start"); err != nil {
		t.Fatalf("runDispatch: %v", err)
	}
	evs, err := hwharness.DrainSpool(spool)
	if err != nil {
		t.Fatalf("DrainSpool: %v", err)
	}
	if len(evs) != 1 || evs[0].HarnessSessionID != "s-1" {
		t.Fatalf("expected one spooled session marker for s-1, got %+v", evs)
	}
}

func TestRunDispatchInertWithoutSpool(t *testing.T) {
	// No HW_EVENT_SPOOL ⇒ HandleHookEvent is inert; runDispatch returns nil and
	// writes nothing (a leftover hook on a non-loom run can't perturb it).
	cmd := &cobra.Command{}
	cmd.SetIn(bytes.NewReader([]byte(`{"session_id":"s"}`)))
	if err := runDispatch(cmd, "claude", "stop"); err != nil {
		t.Errorf("inert dispatch should return nil, got %v", err)
	}
}

func TestRunDispatchErrorIsNonFatal(t *testing.T) {
	t.Setenv("HW_EVENT_SPOOL", t.TempDir())
	cmd := &cobra.Command{}
	cmd.SetIn(bytes.NewReader([]byte("{}")))
	cmd.SetErr(io.Discard)
	// Unknown harness ⇒ HandleHookEvent errors; runDispatch logs and returns nil
	// (exit 0) so a hook failure never breaks the harness.
	if err := runDispatch(cmd, "nonesuch", "stop"); err != nil {
		t.Errorf("unknown harness should be non-fatal, got %v", err)
	}
}
