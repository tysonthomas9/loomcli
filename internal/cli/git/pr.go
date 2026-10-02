package git

import (
	"bufio"
	"fmt"
	"os"
	"os/user"
	"strings"

	"github.com/spf13/cobra"

	"github.com/tysonthomas9/loomcli/internal/cli"
	"github.com/tysonthomas9/loomcli/internal/cli/config"
	"github.com/tysonthomas9/loomcli/internal/loomgit/publish"
	"github.com/tysonthomas9/loomcli/internal/loomgit/review"
)

var prWorkspace string
var prStackWorkspace string
var prStackResolver = cli.NewResolver
var prStackPublish = publish.PublishStackLocal
var prMergePreview = publish.MergeStackPreviewLocal
var prMergeRequest = publish.MergeStackLocal
var prRequestMerge = publish.RequestMergeLocal
var prMergeRequests = publish.MergeRequestsLocal
var prConfirmMerge = publish.ConfirmMergeRequestLocal

// agentEnvMarkers are set in every Loom agent session. A human-only merge
// confirmation refuses them; this is advisory in local mode (D28).
var agentEnvMarkers = []string{"LOOM_AGENT_NAME", "LOOM_ORCHESTRATOR_SESSION_ID", "LOOM_AGENT_TERMINAL_ID"}
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
	requestMergeCmd.Flags().StringVarP(&prStackWorkspace, "workspace", "W", "", "Workspace to operate on")
	cli.RegisterCommand(requestMergeCmd)
	confirmMergeCmd.Flags().StringVarP(&prStackWorkspace, "workspace", "W", "", "Workspace to operate on")
	cli.RegisterCommand(confirmMergeCmd)
	deliveryModeCmd = &cobra.Command{
		Use:     "delivery-mode [stack|trunk]",
		Short:   "Show or set the workspace's Git delivery mode",
		GroupID: "git",
		Long:    "Trunk mode publishes one PR per change against trunk. Add a feature-flag:<name> label to a task to name its flag in the PR body.",
		Args:    cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			selected, _ := cmd.Flags().GetString("workspace")
			resolver, err := resolverFor(selected, cli.NewResolver)
			if err != nil {
				return err
			}
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
	leadMayMergeCmd.Flags().StringVarP(&prStackWorkspace, "workspace", "W", "", "Workspace to operate on")
	cli.RegisterCommand(leadMayMergeCmd)
}

var setWorkspacePolicy = publish.SetWorkspacePolicyLocal

var leadMayMergeCmd = &cobra.Command{
	Use:     "lead-may-merge [off|when_green]",
	Short:   "Show or set whether the lead may merge a stack once the provider shows it green",
	GroupID: "git",
	Long: `With when_green, Reconcile lets the lead merge a Loom stack up to the highest layer whose
required checks and reviews pass on the provider, with no confirmation. Loom follows the repo's
branch protection and adds no review requirement of its own. Only a human can change this setting.`,
	Args: cobra.MaximumNArgs(1),
	RunE: runLeadMayMerge,
}

func runLeadMayMerge(cmd *cobra.Command, args []string) error {
	resolver, err := resolvePRStackWorkspace()
	if err != nil {
		return err
	}
	workspace := resolver.Config.Workspaces[resolver.WorkspaceName()]
	if len(args) == 1 {
		name := "local-user"
		if current, err := user.Current(); err == nil && current.Username != "" {
			name = current.Username
		}
		warning, err := setWorkspacePolicy(cmd.Context(), workspace.ID, args[0], review.Actor{Kind: "human", ID: name}, os.Environ())
		if err != nil {
			return err
		}
		if warning != "" {
			if _, err := fmt.Fprintln(cmd.ErrOrStderr(), "warning: "+warning); err != nil {
				return err
			}
		}
	}
	policy, err := publish.LeadMayMergeLocal(cmd.Context(), workspace.ID)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintln(cmd.OutOrStdout(), policy.Value)
	return err
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
	human, err := humanMergeActor()
	if err != nil {
		return err
	}
	if err := confirmMergeUpTo(cmd, args[2]); err != nil {
		return err
	}
	heads := make([]string, len(view.Layers))
	for index, layer := range view.Layers {
		heads[index] = layer.Head
	}
	result, err := prMergeRequest(cmd.Context(), workspace.ID, args[1], args[0], args[2], heads, human)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(cmd.OutOrStdout(), "merge request: %s\n", result.Phase)
	return err
}

var requestMergeCmd = &cobra.Command{
	Use:     "request-merge <stack> <lead> <layer>",
	Short:   "Ask a human to confirm merging a stack through a chosen layer",
	GroupID: "git",
	Long: `Record a merge request pinned to the stack's current heads. Nothing merges
until a human confirms it in the UI or with 'loom confirm-merge' within 30 minutes.`,
	Args: cobra.ExactArgs(3),
	RunE: func(cmd *cobra.Command, args []string) error {
		resolver, err := resolvePRStackWorkspace()
		if err != nil {
			return err
		}
		requester := publish.MergeActor{Kind: "lead", ID: os.Getenv("LOOM_AGENT_NAME")}
		if requester.ID == "" {
			requester, err = humanMergeActor()
			if err != nil {
				return err
			}
		}
		workspace := resolver.Config.Workspaces[resolver.WorkspaceName()]
		request, err := prRequestMerge(cmd.Context(), workspace.ID, args[1], args[0], args[2], requester)
		if err != nil {
			return err
		}
		return printMergeRequest(cmd, request)
	},
}

var confirmMergeCmd = &cobra.Command{
	Use:     "confirm-merge <lead> <request-id>",
	Short:   "Confirm a lead's merge request for its exact heads",
	GroupID: "git",
	Args:    cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		human, err := humanMergeActor()
		if err != nil {
			return err
		}
		resolver, err := resolvePRStackWorkspace()
		if err != nil {
			return err
		}
		workspace := resolver.Config.Workspaces[resolver.WorkspaceName()]
		requests, err := prMergeRequests(cmd.Context(), workspace.ID, args[0])
		if err != nil {
			return err
		}
		for _, request := range requests {
			if request.ID != args[1] {
				continue
			}
			if err := printMergeRequest(cmd, request); err != nil {
				return err
			}
			if err := confirmMergeUpTo(cmd, request.Target); err != nil {
				return err
			}
			result, err := prConfirmMerge(cmd.Context(), workspace.ID, args[0], request.ID, human)
			if err != nil {
				return err
			}
			_, err = fmt.Fprintf(cmd.OutOrStdout(), "merge request: %s\n", result.Phase)
			return err
		}
		return fmt.Errorf("merge request %s not found for lead %s", args[1], args[0])
	},
}

func printMergeRequest(cmd *cobra.Command, request publish.MergeRequestView) error {
	out := cmd.OutOrStdout()
	if _, err := fmt.Fprintf(out, "merge request %s: %s up to %s, %s by %s %s, expires %s\n", request.ID, request.StackID,
		request.Target, request.Status, request.RequestedKind, request.RequestedBy, request.ExpiresAt.Format("15:04:05Z07:00")); err != nil {
		return err
	}
	for _, layer := range request.Layers {
		if _, err := fmt.Fprintf(out, "%s %s checks=%s review=%s %s\n", layer.Change, layer.Head, layer.Checks, layer.Review, layer.PRURL); err != nil {
			return err
		}
	}
	return nil
}

// humanMergeActor refuses agent sessions; local mode trusts the OS user (D28).
func humanMergeActor() (publish.MergeActor, error) {
	for _, name := range agentEnvMarkers {
		if os.Getenv(name) != "" {
			return publish.MergeActor{}, fmt.Errorf("merge confirmation refused: %s is set, so this looks like an agent session; a human must confirm", name)
		}
	}
	user := os.Getenv("USER")
	if user == "" {
		user = "local-user"
	}
	return publish.MergeActor{Kind: "human", ID: user}, nil
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

// resolverFor returns the fallback resolver when no workspace is selected.
// With a selection it skips active-workspace resolution, so -W works when no
// workspace is active; the caller then calls SetWorkspace(selected).
func resolverFor(selected string, fallback func() (*cli.Resolver, error)) (*cli.Resolver, error) {
	if selected == "" {
		return fallback()
	}
	cfg, err := config.LoadConfigCached()
	if err != nil {
		return nil, err
	}
	return &cli.Resolver{Mode: cli.ModeWorkspace, Config: cfg}, nil
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
	resolver, err := resolverFor(prWorkspace, cli.NewResolver)
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
