package main

import (
	"testing"

	"github.com/spf13/cobra"

	"github.com/tysonthomas9/loomcli/internal/cli"
)

// TestEveryRunECommandIsUsageSilenced walks the fully assembled command tree —
// this is package main, so every blank import in main.go has already registered
// its commands — and fails on any RunE command the walk in
// registerPendingCommands missed. It is the guard for a future command
// registered outside that walk, and for the subtrees that replace root's
// PersistentPreRunE.
//
// It must run before anything calls cli.Execute() in-process: SilenceUsage is
// sticky on the package-level command structs.
func TestEveryRunECommandIsUsageSilenced(t *testing.T) {
	var missed []string

	var walk func(*cobra.Command)
	walk = func(cmd *cobra.Command) {
		if cmd.RunE != nil && !cli.IsUsageSilenced(cmd) {
			missed = append(missed, cmd.CommandPath())
		}
		for _, sub := range cmd.Commands() {
			walk(sub)
		}
	}
	walk(cli.BuildRootCommand())

	for _, path := range missed {
		t.Errorf("command %q has a RunE but was not wrapped by SilenceUsageOnRunErrors", path)
	}
}
