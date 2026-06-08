package hooks

import (
	"fmt"
	"io"
	"os"

	"github.com/spf13/cobra"

	hwharness "github.com/olesho/harness-wrapper/pkg/harness"
	_ "github.com/olesho/harness-wrapper/pkg/harness/all" // register built-in profiles (claude/gemini/...)
)

// hooksDispatchCmd is the GENERIC, wrapper-driven hook entrypoint:
//
//	loom hooks dispatch <harness> <event>
//
// The orchestrator templates `<loom> hooks dispatch <harness> <event>` into a
// harness's hook config (loom sets HookCommand={loomExe,"hooks","dispatch"}); the
// fired hook reads its payload on stdin and delegates to harness-wrapper's
// HandleHookEvent. loom does NO payload parsing / no path encoding here — it is a
// pure forwarder. This coexists with the legacy `loom hooks claude-code <event>`
// handlers (a different, named subcommand) so nothing is removed during rollout.
var hooksDispatchCmd = &cobra.Command{
	Use:    "dispatch <harness> <event>",
	Short:  "Dispatch a harness lifecycle hook to harness-wrapper",
	Hidden: true,
	Args:   cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		return runDispatch(cmd, args[0], args[1])
	},
}

// runDispatch reads stdin, hands it to harness.HandleHookEvent, and translates
// the outcome: a Block result prints the harness's block decision and exits 2
// (so the harness blocks the tool — cooperative yield); everything else exits 0.
// Parse/spool errors are logged to stderr but NEVER fail the hook — a failing
// hook must not break the harness.
func runDispatch(cmd *cobra.Command, harnessName, event string) error {
	stdin, _ := io.ReadAll(cmd.InOrStdin())
	outcome, err := hwharness.HandleHookEvent(harnessName, event, os.Environ(), stdin)
	if err != nil {
		fmt.Fprintf(os.Stderr, "loom hooks dispatch %s/%s: %v\n", harnessName, event, err)
		return nil // exit 0 — never break the harness on a hook error
	}
	if outcome.Block {
		_, _ = fmt.Fprint(cmd.OutOrStdout(), outcome.BlockOutput)
		os.Exit(2) //nolint:gocritic // exit 2 is the harness's "block this tool" signal
	}
	return nil
}
