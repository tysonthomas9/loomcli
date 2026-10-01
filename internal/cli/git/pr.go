package git

import (
	"bufio"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/tysonthomas9/loomcli/internal/cli"
	"github.com/tysonthomas9/loomcli/internal/cli/config"
	"github.com/tysonthomas9/loomcli/internal/loomgit/publish"
)

var prWorkspace string
var prStackWorkspace string
var prStackResolver = cli.NewResolver
var prStackPublish = publish.PublishStackLocal
var prMergePreview = publish.MergeStackPreviewLocal
var prMergeRequest = publish.MergeStackLocal
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
	mergeUpToCmd.Flags().StringVarP(&prStackWorkspace, "workspace", "W", "", "Workspace to operate on")
	mergeUpToCmd.Flags().Bool("status", false, "Show the recorded merge state without requesting a merge")
	cli.RegisterCommand(mergeUpToCmd)
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

var mergeUpToCmd = &cobra.Command{
	Use:     "merge-up-to <stack> <lead> <layer>",
	Short:   "Request a confirmed merge through a chosen stack layer",
	GroupID: "git",
	Args:    cobra.ExactArgs(3),
	RunE:    runMergeUpTo,
}

func runMergeUpTo(cmd *cobra.Command, args []string) error {
	resolver, err := resolvePRStackWorkspace()
	if err != nil {
		return err
	}
	workspace := resolver.Config.Workspaces[resolver.WorkspaceName()]
	view, err := prMergePreview(cmd.Context(), workspace.ID, args[1], args[0], args[2])
	if err != nil {
		return err
	}
	for _, layer := range view.Layers {
		if _, err := fmt.Fprintf(cmd.OutOrStdout(), "%s %s %s %s\n", layer.Change, layer.Head, layer.State, layer.PRURL); err != nil {
			return err
		}
	}
	if _, err := fmt.Fprintf(cmd.OutOrStdout(), "merge: %s %s\n", view.Phase, view.Reason); err != nil {
		return err
	}
	statusOnly, err := cmd.Flags().GetBool("status")
	if err != nil || statusOnly {
		return err
	}
	if err := confirmMergeUpTo(cmd, args[2]); err != nil {
		return err
	}
	heads := make([]string, len(view.Layers))
	for index, layer := range view.Layers {
		heads[index] = layer.Head
	}
	result, err := prMergeRequest(cmd.Context(), workspace.ID, args[1], args[0], args[2], heads)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(cmd.OutOrStdout(), "merge request: %s\n", result.Phase)
	return err
}

func confirmMergeUpTo(cmd *cobra.Command, target string) error {
	if _, err := fmt.Fprintf(cmd.OutOrStdout(), "Type 'merge %s' to confirm these exact heads: ", target); err != nil {
		return err
	}
	line, err := bufio.NewReader(cmd.InOrStdin()).ReadString('\n')
	if err != nil {
		return err
	}
	if strings.TrimSpace(line) != "merge "+target {
		return fmt.Errorf("merge not confirmed")
	}
	return nil
}

var prStackCmd = &cobra.Command{
	Use:     "pr-stack <stack> <lead> [change...]",
	Short:   "Publish approved working-area layers as a linear PR stack",
	GroupID: "git",
	Args:    cobra.MinimumNArgs(2),
	RunE:    runPRStack,
}

func runPRStack(cmd *cobra.Command, args []string) error {
	resolver, err := resolvePRStackWorkspace()
	if err != nil {
		return err
	}
	workspace := resolver.Config.Workspaces[resolver.WorkspaceName()]
	results, err := prStackPublish(cmd.Context(), workspace.ID, args[0], args[1], args[2:])
	if err != nil {
		return err
	}
	if len(results) > 0 && results[0].StatusReason != "" {
		if _, err := fmt.Fprintln(cmd.ErrOrStderr(), results[0].StatusReason); err != nil {
			return err
		}
	}
	for _, result := range results {
		if _, err := fmt.Fprintln(cmd.OutOrStdout(), result.PRURL); err != nil {
			return err
		}
	}
	return nil
}

func resolvePRStackWorkspace() (*cli.Resolver, error) {
	if prStackWorkspace == "" {
		return prStackResolver()
	}
	cfg, err := config.LoadConfigCached()
	if err != nil {
		return nil, err
	}
	resolver := &cli.Resolver{Mode: cli.ModeWorkspace, Config: cfg}
	if err := resolver.SetWorkspace(prStackWorkspace); err != nil {
		return nil, err
	}
	return resolver, nil
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
	result, err := CreatePRResult(cmd.Context(), workspace.ID, args[0], args[1])
	if err != nil {
		return err
	}
	_, err = fmt.Fprintln(cmd.OutOrStdout(), result.URL)
	return err
}
