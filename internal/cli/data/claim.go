package data

import (
	"os"

	"github.com/spf13/cobra"

	"github.com/tysonthomas9/loomcli/internal/backend/api"
)

// claimActor is captured for forward compatibility. The loom server has no
// per-issue claim endpoint today (tracked in loomcli-j7qcq), so
// api.APIBackend.ClaimIssue returns ErrNotImplemented regardless of actor —
// but we keep the flag so the CLI surface matches the local-mode claim
// command and will Just Work when the server endpoint lands.
var claimActor string

var claimCmd = &cobra.Command{
	Use:   "claim <issue-id>",
	Short: "Atomically claim an issue (HTTP)",
	Long: `Claim an issue via the loom server. Currently returns an error
because the server does not expose a per-issue claim endpoint (tracked in
loomcli-j7qcq). The command is present so the cli/data surface mirrors the
full backend.IssueBackend API; it will work once the server endpoint lands.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := cmd.Context()
		cli, url, err := getHTTPClient()
		if err != nil {
			return err
		}
		wsID, err := resolveWorkspaceID(ctx, cli, url)
		if err != nil {
			return err
		}
		ab, err := api.New(api.Config{BaseURL: url, WorkspaceID: wsID, HTTPClient: cli})
		if err != nil {
			return err
		}
		if err := ab.ClaimIssue(ctx, args[0], 0); err != nil {
			return err
		}
		return printMessageResult(os.Stdout, "claimed "+args[0], outputFormat)
	},
}

func init() {
	claimCmd.Flags().StringVar(&claimActor, "actor", "", "Actor attempting the claim (forward-compat; server currently ignores)")
}
