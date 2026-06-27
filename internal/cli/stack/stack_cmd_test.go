package stack

import (
	"bytes"
	"slices"
	"testing"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	cligit "github.com/tysonthomas9/loomcli/internal/cli/git"
)

func TestStackCommandNamespacesExposeCoreCommands(t *testing.T) {
	rootStack := newStackCommand("workspace")
	gitCmd := cligit.NewCommand()
	gitCmd.AddCommand(newStackCommand(""))
	gitStack := requireChildCommand(t, gitCmd, "stack")

	for _, name := range []string{"publish", "status", "restack"} {
		t.Run(name, func(t *testing.T) {
			rootSub := requireChildCommand(t, rootStack, name)
			gitSub := requireChildCommand(t, gitStack, name)
			assertCommandShape(t, rootSub, gitSub)
		})
	}
}

func TestGitStackCoreCommandsRouteLikeTopLevelStack(t *testing.T) {
	t.Setenv("LOOM_WORKSPACE", "")

	for _, name := range []string{"publish", "status", "restack"} {
		t.Run(name, func(t *testing.T) {
			root := testRootCommand()
			root.AddCommand(newStackCommand("workspace"))

			nested := testRootCommand()
			gitCmd := cligit.NewCommand()
			gitCmd.AddCommand(newStackCommand(""))
			nested.AddCommand(gitCmd)

			rootErr := executeCommand(root, "stack", name, "stack-id")
			nestedErr := executeCommand(nested, "git", "stack", name, "stack-id")
			if rootErr == nil || nestedErr == nil {
				t.Fatalf("expected both commands to fail before store access, got root=%v nested=%v", rootErr, nestedErr)
			}
			if rootErr.Error() != nestedErr.Error() {
				t.Fatalf("errors differ:\nroot:   %v\nnested: %v", rootErr, nestedErr)
			}
		})
	}
}

func testRootCommand() *cobra.Command {
	c := &cobra.Command{
		Use:           "loom",
		SilenceErrors: true,
		SilenceUsage:  true,
	}
	c.AddGroup(&cobra.Group{ID: "git", Title: "Git Operations:"})
	c.AddGroup(&cobra.Group{ID: "workspace", Title: "Workspace Commands:"})
	return c
}

func executeCommand(cmd *cobra.Command, args ...string) error {
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs(args)
	return cmd.Execute()
}

func requireChildCommand(t *testing.T, parent *cobra.Command, name string) *cobra.Command {
	t.Helper()
	for _, child := range parent.Commands() {
		if child.Name() == name {
			return child
		}
	}
	t.Fatalf("%s missing child command %q", parent.CommandPath(), name)
	return nil
}

func assertCommandShape(t *testing.T, rootSub, gitSub *cobra.Command) {
	t.Helper()
	if rootSub.Use != gitSub.Use {
		t.Fatalf("Use differs: root=%q git=%q", rootSub.Use, gitSub.Use)
	}
	if rootSub.Short != gitSub.Short {
		t.Fatalf("Short differs: root=%q git=%q", rootSub.Short, gitSub.Short)
	}
	if (rootSub.RunE == nil) != (gitSub.RunE == nil) {
		t.Fatalf("RunE presence differs for %s", rootSub.Name())
	}
	if (rootSub.Args == nil) != (gitSub.Args == nil) {
		t.Fatalf("Args validator presence differs for %s", rootSub.Name())
	}
	rootFlags := flagSpecs(rootSub)
	gitFlags := flagSpecs(gitSub)
	if !slices.Equal(rootFlags, gitFlags) {
		t.Fatalf("flags differ for %s: root=%v git=%v", rootSub.Name(), rootFlags, gitFlags)
	}
}

func flagSpecs(cmd *cobra.Command) []string {
	flags := cmd.Flags()
	specs := make([]string, 0)
	flags.VisitAll(func(f *pflag.Flag) {
		specs = append(specs, f.Name+"|"+f.Shorthand+"|"+f.Value.Type()+"|"+f.DefValue)
	})
	slices.Sort(specs)
	return specs
}
