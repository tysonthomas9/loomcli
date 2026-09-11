package daemon

import (
	"fmt"

	"github.com/spf13/cobra"

	cliagent "github.com/tysonthomas9/loomcli/internal/cli/agent"
)

var (
	seedLogWorkspace string
	seedLogAgent     string
	seedLogFile      string
)

// daemonSeedLogCmd is part of the TEST-ONLY seeding seam (docs/adr/0001). It
// appends content to an agent's archive log through cliagent.OpenAgentArchiveLog
// — the exact writer the supervisor uses and the resolver the web UI "Logs"
// tab reads — so harnesses never construct log paths by hand. Hidden: never in
// help output; refuses to run without LOOM_TESTSUPPORT=1.
var daemonSeedLogCmd = &cobra.Command{
	Use:    "seed-log",
	Short:  "TEST-ONLY: append content to an agent's archive log via the product's own writer",
	Hidden: true,
	Args:   cobra.NoArgs,
	RunE:   runDaemonSeedLog,
}

func init() {
	f := daemonSeedLogCmd.Flags()
	f.StringVar(&seedLogWorkspace, "workspace", "", "Workspace key (required)")
	f.StringVar(&seedLogAgent, "agent", "", "Agent name (required)")
	f.StringVar(&seedLogFile, "content", "", "Log content file (default: stdin)")
	daemonCmd.AddCommand(daemonSeedLogCmd)
}

func runDaemonSeedLog(_ *cobra.Command, _ []string) error {
	if err := requireTestSupport(); err != nil {
		return err
	}
	if seedLogWorkspace == "" || seedLogAgent == "" {
		return fmt.Errorf("--workspace and --agent are required")
	}
	data, err := readSeedContent(seedLogFile)
	if err != nil {
		return fmt.Errorf("read log content: %w", err)
	}
	if len(data) == 0 {
		return fmt.Errorf("log content is empty")
	}
	f, err := cliagent.OpenAgentArchiveLog(seedLogWorkspace, seedLogAgent)
	if err != nil {
		return err
	}
	defer f.Close() //nolint:errcheck // best-effort close after explicit write check
	if _, err := f.Write(data); err != nil {
		return fmt.Errorf("append agent archive log: %w", err)
	}
	fmt.Printf("seeded log: ws=%s agent=%s bytes=%d path=%s\n", seedLogWorkspace, seedLogAgent, len(data), f.Name())
	return nil
}
