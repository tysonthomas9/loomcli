package git

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/tysonthomas9/loomcli/internal/cli"
	"github.com/tysonthomas9/loomcli/internal/cli/config"
	"github.com/tysonthomas9/loomcli/internal/loomgit/publish"
)

var prWorkspace string
var prStackWorkspace string
var prStackResolver = cli.NewResolver
var prStackPublish = publish.PublishStackLocal

// agentEnvMarkers are set in every Loom agent session; a command run with one
// is the lead's unless a task-agent marker is also set (D42). Advisory in
// local mode (D28).
var agentEnvMarkers = []string{"LOOM_AGENT_NAME", "LOOM_ORCHESTRATOR_SESSION_ID", "LOOM_AGENT_TERMINAL_ID"}

var prCmd = &cobra.Command{
	Use:     "pr <lead> <change>",
	Short:   "Publish an approved change as a GitHub PR",
	GroupID: "git",
	Hidden:  true, // plumbing for tests and repair (S3)
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
}

var prStackCmd = &cobra.Command{
	Use:     "pr-stack <stack> <lead> [change...]",
	Short:   "Publish approved working-area layers as a linear PR stack",
	GroupID: "git",
	Hidden:  true, // plumbing for tests and repair (S3)
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
	return allWorkspacesResolver()
}

// allWorkspacesResolver loads every configured workspace without requiring an
// active one, for commands that select or iterate workspaces themselves.
func allWorkspacesResolver() (*cli.Resolver, error) {
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
