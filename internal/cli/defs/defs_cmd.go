package defs

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/tysonthomas9/loomcli/internal/bootstrap"
	"github.com/tysonthomas9/loomcli/internal/cli"
	"github.com/tysonthomas9/loomcli/internal/cli/cmdstore"
	defspkg "github.com/tysonthomas9/loomcli/internal/defs"
)

var (
	defsDir  string
	defsJSON bool
)

var defsCmd = &cobra.Command{
	Use:     "defs",
	Short:   "Check, plan, and apply code-defined Loom modules",
	GroupID: "workspace",
}

var defsCheckCmd = &cobra.Command{
	Use:   "check",
	Short: "Parse and validate .loom definitions without writing state",
	Args:  cobra.NoArgs,
	RunE:  runDefsCheck,
}

var defsPlanCmd = &cobra.Command{
	Use:   "plan",
	Short: "Show code-defined changes that would be applied",
	Args:  cobra.NoArgs,
	RunE:  runDefsPlan,
}

var defsApplyCmd = &cobra.Command{
	Use:   "apply",
	Short: "Apply code-defined agents, workflows, runtimes, routes, and triggers",
	Args:  cobra.NoArgs,
	RunE:  runDefsApply,
}

func init() {
	defsCmd.PersistentFlags().StringVar(&defsDir, "dir", ".", "Directory containing optional .loom definitions")
	defsCmd.PersistentFlags().BoolVar(&defsJSON, "json", false, "JSON output")
	defsCmd.AddCommand(defsCheckCmd, defsPlanCmd, defsApplyCmd)
	cli.RegisterCommand(defsCmd)
}

func runDefsCheck(_ *cobra.Command, _ []string) error {
	plan, err := defspkg.Load(defsDir)
	if err != nil {
		return err
	}
	if defsJSON {
		return cmdstore.WriteJSON(plan)
	}
	fmt.Printf("Definitions OK: %s\n", defspkg.Summary(plan))
	return nil
}

func runDefsPlan(_ *cobra.Command, _ []string) error {
	plan, err := defspkg.Load(defsDir)
	if err != nil {
		return err
	}
	if defsJSON {
		return cmdstore.WriteJSON(plan)
	}
	printPlan(plan)
	return nil
}

func runDefsApply(_ *cobra.Command, _ []string) error {
	plan, err := defspkg.Load(defsDir)
	if err != nil {
		return err
	}
	return cmdstore.WithActiveWorkspace(func(ctx context.Context, h *bootstrap.StoreHandle, ws string) error {
		if err := defspkg.Apply(ctx, h.Store, ws, actorName(), plan); err != nil {
			return err
		}
		if defsJSON {
			return cmdstore.WriteJSON(plan)
		}
		fmt.Printf("Applied definitions to %s: %s\n", ws, defspkg.Summary(plan))
		return nil
	})
}

func printPlan(plan *defspkg.Plan) {
	fmt.Printf("Definition plan for %s\n", plan.Root)
	if len(plan.Agents) == 0 && len(plan.Workflows) == 0 && len(plan.Runtimes) == 0 {
		fmt.Println("  no .loom definitions found")
		return
	}
	for _, agent := range plan.Agents {
		fmt.Printf("  agent    %-28s %s\n", agent.Name, agent.Version)
		if len(agent.Tools) > 0 {
			fmt.Printf("           model tools: %s\n", strings.Join(agent.Tools, ", "))
		}
		if len(agent.Skills) > 0 {
			fmt.Printf("           skills: %s\n", strings.Join(agent.Skills, ", "))
		}
		if len(agent.AllowedCommands) > 0 {
			fmt.Printf("           sandbox allow: %s\n", strings.Join(agent.AllowedCommands, ", "))
		}
		if len(agent.DeniedCommands) > 0 {
			fmt.Printf("           sandbox deny: %s\n", strings.Join(agent.DeniedCommands, ", "))
		}
		if len(agent.Repos) > 0 {
			fmt.Printf("           repos: %s\n", strings.Join(agent.Repos, ", "))
		}
		if len(agent.Env) > 0 {
			fmt.Printf("           env: %s\n", strings.Join(agent.Env, ", "))
		}
	}
	for _, wf := range plan.Workflows {
		fmt.Printf("  workflow %-28s %s\n", wf.Name, wf.Version)
		if wf.Builtin != "" {
			fmt.Printf("           runner: %s\n", wf.Builtin)
		}
		if len(wf.Tools) > 0 {
			fmt.Printf("           workflow tools: %s\n", strings.Join(wf.Tools, ", "))
		}
		if wf.RoutePath != "" {
			fmt.Printf("           route: POST %s auth=%s\n", wf.RoutePath, wf.RouteAuth)
		}
		if wf.TriggerEvent != "" {
			fmt.Printf("           trigger: %s\n", wf.TriggerEvent)
		}
		if len(wf.Repos) > 0 {
			fmt.Printf("           repos: %s\n", strings.Join(wf.Repos, ", "))
		}
		if len(wf.Env) > 0 {
			fmt.Printf("           env: %s\n", strings.Join(wf.Env, ", "))
		}
	}
	for _, rt := range plan.Runtimes {
		fmt.Printf("  runtime  %-28s %s provider=%s\n", rt.Name, rt.Version, rt.Provider)
	}
}

func actorName() string {
	if actor := strings.TrimSpace(os.Getenv("LOOM_ACTOR")); actor != "" {
		return actor
	}
	if actor := strings.TrimSpace(os.Getenv("USER")); actor != "" {
		return actor
	}
	return "loom"
}
