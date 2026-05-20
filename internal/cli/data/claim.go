package data

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

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
		actor := strings.TrimSpace(claimActor)
		if actor != "" {
			actorBackend, ok := ib.(interface {
				ClaimIssueAsActor(context.Context, string, time.Duration, string) error
			})
			if !ok {
				return fmt.Errorf("--actor is not supported by the active backend (%s); the claim lock would be acquired under the CLI's identity instead", ib.BackendName())
			}
			if err := actorBackend.ClaimIssueAsActor(ctx, id, 0, actor); err != nil {
				return err
			}
		} else {
			if err := ib.ClaimIssue(ctx, id, 0); err != nil {
				return err
			}
		}
		return printMessageResult(os.Stdout, "claimed "+id, outputFormat)
	},
}

func init() {
	claimCmd.Flags().StringVar(&claimActor, "actor", "", "Override the actor identity used to acquire the claim lock (defaults to the CLI's configured actor)")
}
