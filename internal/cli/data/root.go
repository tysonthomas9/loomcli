package data

import (
	"github.com/spf13/cobra"
)

// Persistent flag/env state for the `loom data` subtree. These are set by
// Cobra flag binding on dataRootCmd and read by every leaf command.
var (
	serverURL    string
	workspaceID  string
	outputFormat string // "text" | "json"
)

var dataRootCmd = &cobra.Command{
	Use:   "data",
	Short: "Data-only commands that talk to a loom server over HTTP",
	Long: `The 'loom data' subtree contains thin CLI commands that interact
with a running loom server via HTTP exclusively. These commands do NOT read
local files, do NOT auto-spawn the daemon, and require either the --server
flag or LOOM_SERVER_URL environment variable to point at a reachable server.

Use 'loom data' commands when:
  • You are on a machine without a local checkout.
  • You want to manage agents on a remote loom server.
  • You are scripting against a hosted loom deployment.

For local-mode operation, use the legacy top-level commands
(loom show, loom list, loom monitor, ...).

Examples:
  loom data show <id> --server http://localhost:8080
  loom data list --server http://localhost:8080 -o json | jq '.[].id'
  loom data monitor --server http://localhost:8080
  loom data agent stop falcon --server http://localhost:8080

Note: if you pass --server as a root-level flag (e.g.
'loom --server URL data show ID'), cli/data cannot read it — use
'loom data show ID --server URL' or set LOOM_SERVER_URL.`,
}

// Commands returns the data sub-tree for registration by cmd/loom/main.go.
// This is the sole entry point — the package does not call cli.RegisterCommand
// itself, because it cannot import internal/cli. See the package-level doc.
func Commands() []*cobra.Command {
	return []*cobra.Command{dataRootCmd}
}

func init() {
	dataRootCmd.PersistentFlags().StringVar(&serverURL, "server", "", "Loom server base URL (or LOOM_SERVER_URL env var)")
	dataRootCmd.PersistentFlags().StringVar(&workspaceID, "workspace", "", "Workspace ID (or LOOM_WORKSPACE env var; auto-discovers if unset)")
	dataRootCmd.PersistentFlags().StringVarP(&outputFormat, "output", "o", "text", "Output format: text|json")

	dataRootCmd.AddCommand(
		showCmd,
		listCmd,
		readyCmd,
		blockedCmd,
		claimCmd,
		closeCmd,
		commentCmd,
		monitorCmd,
		agentsCmd,
		agentCmd,
	)
}
