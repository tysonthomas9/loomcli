package data

import (
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/tysonthomas9/loomcli/internal/backend"
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
		id := args[0]
		if err := ib.ClaimIssue(ctx, backend.ClaimIssueParams{ID: id, OwnerActor: strings.TrimSpace(claimActor)}); err != nil {
			return err
		}
		return printMessageResult(os.Stdout, "claimed "+id, outputFormat)
	},
}

func init() {
	claimCmd.Flags().StringVar(&claimActor, "actor", "", "Override the actor identity used to acquire the claim lock (defaults to the backend's configured actor)")
}
