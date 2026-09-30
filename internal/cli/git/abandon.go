package git

import (
	"errors"
	"fmt"
	"os/user"

	"github.com/spf13/cobra"

	"github.com/tysonthomas9/loomcli/internal/cli"
	"github.com/tysonthomas9/loomcli/internal/cli/cmdstore"
	"github.com/tysonthomas9/loomcli/internal/loomgit/abandon"
)

func newAbandonCommand() *cobra.Command {
	var req abandon.Request
	var yes bool
	cmd := &cobra.Command{
		Use:     "abandon <task>",
		Short:   "Capture and abandon a Loom change",
		GroupID: "git",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runAbandonCommand(cmd, args[0], req, yes)
		},
	}
	cmd.Flags().StringVar(&req.Workspace, "workspace-id", "", "Loom workspace ID")
	cmd.Flags().StringVar(&req.Repo, "repo-name", "", "Recorded repository name")
	cmd.Flags().StringVar(&req.Attempt, "attempt", "", "Task-copy attempt ID")
	cmd.Flags().StringVar(&req.Worktree, "task-copy", "", "Task-copy path")
	cmd.Flags().StringVar(&req.SourceRepo, "source-repo", "", "Source repository path")
	cmd.Flags().StringVar(&req.Reason, "reason", "", "Reason for abandonment")
	cmd.Flags().BoolVar(&req.ClosePR, "close-pr", false, "Close an open PR even if capture is incomplete")
	cmd.Flags().BoolVar(&req.DeleteRemote, "delete-remote", false, "Delete the remote change branch even if capture is incomplete")
	cmd.Flags().BoolVar(&yes, "yes", false, "Confirm listed ignored files")
	_ = cmd.MarkFlagRequired("workspace-id")
	_ = cmd.MarkFlagRequired("repo-name")
	_ = cmd.MarkFlagRequired("attempt")
	_ = cmd.MarkFlagRequired("task-copy")
	_ = cmd.MarkFlagRequired("reason")
	return cmd
}

func runAbandonCommand(cmd *cobra.Command, task string, req abandon.Request, yes bool) error {
	requester, err := user.Current()
	if err != nil {
		return fmt.Errorf("identify requesting user: %w", err)
	}
	handle, err := cmdstore.OpenStore(cmd.Context())
	if err != nil {
		return err
	}
	defer func() { _ = handle.Close() }()
	req.Task = task
	req.RequestedBy = requester.Username
	req.ConfirmIgnored = yes
	service := abandon.New()
	claims, ok := cli.GetDeps(cmd).IssueBackend.(abandon.ClaimReleaser)
	if !ok {
		return errors.New("issue backend cannot read current claim holder")
	}
	service.Claims = claims
	service.Sessions = handle.Store.AgentSessions()
	result, err := service.Run(cmd.Context(), req)
	var confirmation *abandon.ConfirmationRequired
	if errors.As(err, &confirmation) {
		if _, writeErr := fmt.Fprintln(cmd.ErrOrStderr(), "Ignored files in the task copy:"); writeErr != nil {
			return writeErr
		}
		for _, entry := range confirmation.Ignored {
			if _, writeErr := fmt.Fprintf(cmd.ErrOrStderr(), "  %s (%d bytes)\n", entry.Path, entry.Size); writeErr != nil {
				return writeErr
			}
		}
		return fmt.Errorf("confirm these files with --yes: %w", err)
	}
	if err != nil {
		return err
	}
	if _, err := fmt.Fprintf(cmd.OutOrStdout(), "Abandoned change %s at revision %d; capture complete: %t; retention eligible: %t\n",
		result.Revision.Change, result.Revision.Number, result.Complete, result.RetentionEligible); err != nil {
		return err
	}
	for _, dependent := range result.Dependents {
		if _, err := fmt.Fprintf(cmd.OutOrStdout(), "Dependent %s (%s): dependency_abandoned\n", dependent.Task, dependent.Repo); err != nil {
			return err
		}
	}
	return nil
}

func init() { cli.RegisterCommand(newAbandonCommand()) }
