package git

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/tysonthomas9/loomcli/internal/cli"
	"github.com/tysonthomas9/loomcli/internal/loomgit/publish"
)

var prWorkspace string
var prStackWorkspace string
var prStackResolver = cli.NewResolver
var prStackPublish = publish.PublishStackLocal
var deliveryModeCmd *cobra.Command

var prCmd = &cobra.Command{
	Use:     "pr <lead> <change>",
	Short:   "Publish an approved change as a GitHub PR",
	GroupID: "git",
	Long: `Publish a recorded and approved change from a lead's working area.

The repository and target branch come from the workspace's recorded Git state.
Use -W to select a workspace when the current directory does not identify one.`,
	Args: cobra.ExactArgs(2),
	RunE: runPR,
}

func init() {
	prCmd.Flags().StringVarP(&prWorkspace, "workspace", "W", "", "Workspace to operate on")
	cli.RegisterCommand(prCmd)
	prStackCmd.Flags().StringVarP(&prStackWorkspace, "workspace", "W", "", "Workspace to operate on")
	cli.RegisterCommand(prStackCmd)
	deliveryModeCmd = &cobra.Command{
		Use:     "delivery-mode [stack|trunk]",
		Short:   "Show or set the workspace's Git delivery mode",
		GroupID: "git",
		Long:    "Trunk mode publishes one PR per change against trunk. Add a feature-flag:<name> label to a task to name its flag in the PR body.",
		Args:    cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			resolver, err := cli.NewResolver()
			if err != nil {
				return err
			}
			selected, _ := cmd.Flags().GetString("workspace")
			if selected != "" {
				if err := resolver.SetWorkspace(selected); err != nil {
					return err
				}
			}
			workspace := resolver.Config.Workspaces[resolver.WorkspaceName()]
			if len(args) == 1 {
				if err := publish.SetDeliveryModeLocal(cmd.Context(), workspace.ID, args[0]); err != nil {
					return err
				}
			}
			mode, err := publish.DeliveryModeLocal(cmd.Context(), workspace.ID)
			if err != nil {
				return err
			}
			_, err = fmt.Fprintln(cmd.OutOrStdout(), mode)
			return err
		},
	}
	deliveryModeCmd.Flags().StringP("workspace", "W", "", "Workspace to operate on")
	cli.RegisterCommand(deliveryModeCmd)
}

var prStackCmd = &cobra.Command{
	Use:     "pr-stack <stack> <lead> [change...]",
	Short:   "Publish approved working-area layers as a linear PR stack",
	GroupID: "git",
	Args:    cobra.MinimumNArgs(2),
	RunE:    runPRStack,
}

func runPRStack(cmd *cobra.Command, args []string) error {
	resolver, err := prStackResolver()
	if err != nil {
		return err
	}
	if prStackWorkspace != "" {
		if err := resolver.SetWorkspace(prStackWorkspace); err != nil {
			return err
		}
	}
	workspace := resolver.Config.Workspaces[resolver.WorkspaceName()]
	results, err := prStackPublish(cmd.Context(), workspace.ID, args[0], args[1], args[2:])
	if err != nil {
		return err
	}
	for _, result := range results {
		if _, err := fmt.Fprintln(cmd.OutOrStdout(), result.PRURL); err != nil {
			return err
		}
	}
	return nil
}

func runPR(cmd *cobra.Command, args []string) error {
	resolver, err := cli.NewResolver()
	if err != nil {
		return err
	}
	if prWorkspace != "" {
		if err := resolver.SetWorkspace(prWorkspace); err != nil {
			return err
		}
	}
	workspace := resolver.Config.Workspaces[resolver.WorkspaceName()]
	result, err := publish.PublishLocal(cmd.Context(), workspace.ID, args[0], args[1])
	if err != nil {
		return err
	}
	_, err = fmt.Fprintln(cmd.OutOrStdout(), result.PRURL)
	return err
}
