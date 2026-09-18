package backends

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"
)

// TestSpawnRepro reproduces a live agent spawn through the SAME function the
// daemon calls (invokeClaudeRunTurn), without going through the supervisor — so
// no task is claimed and no no-progress kill is recorded against one. That
// matters: before this existed, the only way to test a failing agent was to
// start it, and three crashes quarantine whatever it claimed (PUPPET-451).
//
// Opt in with the agent's own environment, e.g.:
//
//	CLAUDE_CONFIG_DIR=<profile> CLAUDE_CODE_OAUTH_TOKEN=$(cat <profile>/oauth-token) \
//	LOOM_ROLE=integrator LOOM_ROLE_INPUT_POLICY='{"default":"deny",...}' \
//	REPRO_WORKDIR=<worktree> REPRO_AGENT=<agent> \
//	go test ./internal/cli/backends/ -run TestSpawnRepro -v
func TestSpawnRepro(t *testing.T) {
	wd := os.Getenv("REPRO_WORKDIR")
	if wd == "" {
		t.Skip("set REPRO_WORKDIR")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	res, err := invokeClaudeRunTurn(ctx, wd, "Reply with exactly: OK", os.Getenv("REPRO_AGENT"), "", nil, nil)

	ev := claudeRunTurnEvidence(res, "")
	t.Logf("---- err        : %v", err)
	t.Logf("---- turn.Reason: %q", res.Turn.Reason)
	t.Logf("---- evidence   : %d bytes", len(ev))
	for _, ln := range strings.Split(ev, "\n") {
		if s := strings.TrimSpace(ln); s != "" {
			t.Logf("  | %s", s)
		}
	}
}
