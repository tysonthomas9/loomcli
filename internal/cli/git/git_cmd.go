package git

import (
	"github.com/spf13/cobra"

	"github.com/tysonthomas9/loomcli/internal/cli"
)

var gitCmd = NewCommand()

func init() {
	cli.RegisterCommand(gitCmd)
}

// NewCommand builds the git-oriented namespace command.
func NewCommand() *cobra.Command {
	return &cobra.Command{
		Use:     "git",
		Short:   "Git-oriented commands",
		GroupID: "git",
	}
}

// RegisterSubcommand adds a command under `loom git`.
func RegisterSubcommand(cmd *cobra.Command) {
	gitCmd.AddCommand(cmd)
}
