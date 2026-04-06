package cli

import "github.com/spf13/cobra"

// pendingCmds accumulates commands registered by sub-packages via init().
var pendingCmds []*cobra.Command

// RegisterCommand adds a command to the pending list.
// Sub-packages call this in their init() functions.
func RegisterCommand(cmd *cobra.Command) {
	pendingCmds = append(pendingCmds, cmd)
}

// registerPendingCommands adds all pending commands to rootCmd.
func registerPendingCommands() {
	rootCmd.AddCommand(pendingCmds...)
}

// GetRootCmd returns the root cobra command for sub-packages that need
// to add command groups or other root-level configuration.
func GetRootCmd() *cobra.Command {
	return rootCmd
}
