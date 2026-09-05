package backends

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// A bare harness exit sets Turn.Reason="exit code 1" and no text. That is
// non-empty but uninformative, so it classifies as [Unknown] and burns the
// task's no-progress budget. The rendered screen is the only thing that says
// WHY — here a folder-trust dialog — so it must reach OutputTail.
func TestErroredTurnCarriesRenderedScreen(t *testing.T) {
	const screen = "Quick safety check: Is this a project you created or one you trust?\n> No, exit\n  Yes, I trust this folder"

	orig := claudeRunTurn
	t.Cleanup(func() { claudeRunTurn = orig })
	claudeRunTurn = func(_ context.Context, cfg claudeRunTurnConfig) (claudeRunTurnResult, error) {
		if cfg.Output != nil {
			_, _ = cfg.Output.Write([]byte(screen))
		}
		var res claudeRunTurnResult
		res.Turn.Reason = "exit code 1"
		return res, errors.New("harness: turn errored")
	}

	res, err := invokeClaudeRunTurn(context.Background(), t.TempDir(), "p", "agent", "", nil, nil)
	if err == nil {
		t.Fatal("want error")
	}
	if !strings.Contains(claudeRunTurnEvidence(res, ""), "Yes, I trust this folder") {
		t.Fatalf("screen did not reach evidence; got %q", claudeRunTurnEvidence(res, ""))
	}
}

// A successful turn must not be polluted with screen noise.
func TestSuccessfulTurnKeepsTextClean(t *testing.T) {
	orig := claudeRunTurn
	t.Cleanup(func() { claudeRunTurn = orig })
	claudeRunTurn = func(_ context.Context, cfg claudeRunTurnConfig) (claudeRunTurnResult, error) {
		if cfg.Output != nil {
			_, _ = cfg.Output.Write([]byte("ANSI noise"))
		}
		var res claudeRunTurnResult
		res.Turn.Text = "OK"
		return res, nil
	}
	res, err := invokeClaudeRunTurn(context.Background(), t.TempDir(), "p", "agent", "", nil, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Turn.Text != "OK" {
		t.Fatalf("Turn.Text = %q, want %q", res.Turn.Text, "OK")
	}
}
