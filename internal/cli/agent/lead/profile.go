package lead

import (
	"fmt"
	"os"

	"github.com/tysonthomas9/loomcli/internal/cli"
	"github.com/tysonthomas9/loomcli/internal/harnessprofile"
)

// enforceLeadProfile points `loom lead` at its per-agent harness profiles and
// refuses to start when one it is about to use does not verify.
//
// The supervisor resolves, verifies and exports a profile root before it hands
// the environment to an agent it spawns (see supervisor.AppendProfileEnv).
// `loom lead` is one of the two agents that do not come from the supervisor —
// the standalone `loom agent` path is the other — so without this call it gets
// a profile only when something outside loom exports one: the workspace
// launcher script did, and every other way of starting a lead (bare
// `loom lead`, the WebUI terminal) silently ran the operator's own ~/.claude
// and ~/.codex. So lead injects what it inherited nothing for, and verifies
// what it did inherit.
//
// It refuses rather than falling back: unsetting the variable and continuing
// against the operator's ~/.claude is the exact leak per-agent profiles close.
//
// The policy itself lives in internal/harnessprofile, shared verbatim with the
// supervisor's spawn path and with `loom agent`. This function is only lead's
// wiring into it: which workspace root, which agent id, and how a refusal is
// reported.
func enforceLeadProfile() {
	if err := harnessprofile.Enforce(cli.GetWorkspaceRuntimeDir(), resolveLeadAgentID()); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		fmt.Fprintf(os.Stderr, "Repair: %s\n", harnessprofile.Repair(err, harnessprofile.FailedDir(err)))
		os.Exit(1)
	}
}
