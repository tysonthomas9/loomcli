package data

import (
	"os"

	"github.com/spf13/cobra"
)

// claimActor is captured for command-line parity with the local-mode claim
// command. Backend implementations derive the effective actor from their
// configured environment/session.
var claimActor string

var claimCmd = &cobra.Command{
	Use:   "claim <issue-id>",
	Short: "Atomically claim an issue",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := cmd.Context()
		ib, err := getIssueBackend(ctx)
		if err != nil {
			return err
		}
		if err := ib.ClaimIssue(ctx, args[0], 0); err != nil {
			return err
		}
		return printMessageResult(os.Stdout, "claimed "+args[0], outputFormat)
	},
}

func init() {
	claimCmd.Flags().StringVar(&claimActor, "actor", "", "Actor attempting the claim")
}
