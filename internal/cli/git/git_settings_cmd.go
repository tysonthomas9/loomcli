package git

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/tysonthomas9/loomcli/internal/cli"
	"github.com/tysonthomas9/loomcli/internal/loomgit/publish"
)

var gitSettingsResolver = cli.NewResolver
var gitSettingsLocal = publish.GitSettingsLocal
var setGitSettingsLocal = publish.SetGitSettingsLocal

// gitSettingsCmd shows or changes the same three Git settings as the UI.
var gitSettingsCmd = &cobra.Command{
	Use:     "git-settings",
	Short:   "Show or change the workspace's delivery, auto-merge and lead approval settings",
	GroupID: "git",
	Long: `Show or change the workspace's Git settings, the same three controls as the UI.

  --delivery stack|pr-per-task   Stack approved tasks into one PR stack, or open one PR per task
  --auto-merge on|off            Merge PRs once their checks and reviews pass; also lets the lead run loom merge
  --lead-may-approve on|off      Let the lead approve tasks

Only a human can change --auto-merge and --lead-may-approve. With no flags it prints the settings.`,
	Args: cobra.NoArgs,
	RunE: runGitSettings,
}

func init() {
	gitSettingsCmd.Flags().StringP("workspace", "W", "", "Workspace to operate on")
	gitSettingsCmd.Flags().String("delivery", "", "stack or pr-per-task")
	gitSettingsCmd.Flags().String("auto-merge", "", "on or off")
	gitSettingsCmd.Flags().String("lead-may-approve", "", "on or off")
	cli.RegisterCommand(gitSettingsCmd)
}

func runGitSettings(cmd *cobra.Command, _ []string) error {
	change, err := gitSettingsChange(cmd)
	if err != nil {
		return err
	}
	selected, _ := cmd.Flags().GetString("workspace")
	resolver, err := resolverFor(selected, gitSettingsResolver)
	if err != nil {
		return err
	}
	if selected != "" {
		if err := resolver.SetWorkspace(selected); err != nil {
			return err
		}
	}
	workspace := resolver.Config.Workspaces[resolver.WorkspaceName()]
	var settings publish.GitSettings
	if change == (publish.GitSettingsChange{}) {
		settings, err = gitSettingsLocal(cmd.Context(), workspace.ID)
	} else {
		var warning string
		settings, warning, err = setGitSettingsLocal(cmd.Context(), workspace.ID, change, resolveCommandActor("lead").Actor, nil)
		if err == nil && warning != "" {
			_, err = fmt.Fprintln(cmd.ErrOrStderr(), "warning: "+warning)
		}
	}
	if err != nil {
		return err
	}
	delivery := "stack"
	if settings.DeliveryMode == "trunk" {
		delivery = "pr-per-task"
	}
	_, err = fmt.Fprintf(cmd.OutOrStdout(), "delivery: %s\nauto-merge: %s\nlead-may-approve: %s\n",
		delivery, onOff(settings.LeadMayMerge == "when_green"), onOff(settings.LeadMayApprovePublish))
	return err
}

// gitSettingsChange maps the flags given onto the stored settings.
func gitSettingsChange(cmd *cobra.Command) (publish.GitSettingsChange, error) {
	var change publish.GitSettingsChange
	if cmd.Flags().Changed("delivery") {
		value, _ := cmd.Flags().GetString("delivery")
		mode := map[string]string{"stack": "stack", "pr-per-task": "trunk"}[value]
		if mode == "" {
			return change, fmt.Errorf("--delivery must be stack or pr-per-task, not %q", value)
		}
		change.DeliveryMode = &mode
	}
	if cmd.Flags().Changed("auto-merge") {
		on, err := onOffFlag(cmd, "auto-merge")
		if err != nil {
			return change, err
		}
		policy := "off"
		if on {
			policy = "when_green"
		}
		change.LeadMayMerge = &policy
	}
	if cmd.Flags().Changed("lead-may-approve") {
		on, err := onOffFlag(cmd, "lead-may-approve")
		if err != nil {
			return change, err
		}
		change.LeadMayApprovePublish = &on
	}
	return change, nil
}

func onOffFlag(cmd *cobra.Command, name string) (bool, error) {
	value, _ := cmd.Flags().GetString(name)
	switch value {
	case "on":
		return true, nil
	case "off":
		return false, nil
	}
	return false, fmt.Errorf("--%s must be on or off, not %q", name, value)
}

func onOff(on bool) string {
	if on {
		return "on"
	}
	return "off"
}
