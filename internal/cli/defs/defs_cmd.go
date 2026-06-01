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
	"github.com/tysonthomas9/loomcli/internal/cli/sourceagent"
	defspkg "github.com/tysonthomas9/loomcli/internal/defs"
	"github.com/tysonthomas9/loomcli/internal/domain"
)

var (
	defsDir           string
	defsJSON          bool
	defsFromWorkspace bool
	defsExportForce   bool
	defsExportState   bool
	defsApplyStart    bool

	defsWithActiveWorkspace = cmdstore.WithActiveWorkspace
	defsWriteJSON           = cmdstore.WriteJSON
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

var defsExportSourceCmd = &cobra.Command{
	Use:   "export-source",
	Short: "Write durable workspace definitions back to .loom TypeScript source",
	Args:  cobra.NoArgs,
	RunE:  runDefsExportSource,
}

func init() {
	defsCmd.PersistentFlags().StringVar(&defsDir, "dir", ".", "Directory containing optional .loom definitions")
	defsCmd.PersistentFlags().BoolVar(&defsJSON, "json", false, "JSON output")
	defsPlanCmd.Flags().BoolVar(&defsFromWorkspace, "from-workspace", false, "Read durable definitions from the active Loom workspace instead of local source")
	defsApplyCmd.Flags().BoolVar(&defsApplyStart, "start", false, "Create or update one running agent instance per source-defined agent")
	defsExportSourceCmd.Flags().BoolVar(&defsExportForce, "force", false, "Overwrite existing generated source files")
	defsExportSourceCmd.Flags().BoolVar(&defsExportState, "include-state", false, "Also write reviewable mutable runtime state snapshots under .loom/state")
	defsCmd.AddCommand(defsCheckCmd, defsPlanCmd, defsApplyCmd, defsExportSourceCmd)
	cli.RegisterCommand(defsCmd)
}

func runDefsCheck(_ *cobra.Command, _ []string) error {
	plan, err := defspkg.Load(defsDir)
	if err != nil {
		return err
	}
	if defsJSON {
		return defsWriteJSON(plan)
	}
	fmt.Printf("Definitions OK: %s\n", defspkg.Summary(plan))
	return nil
}

func runDefsPlan(_ *cobra.Command, _ []string) error {
	if defsFromWorkspace {
		return defsWithActiveWorkspace(func(ctx context.Context, h *bootstrap.StoreHandle, ws string) error {
			plan, err := defspkg.PlanFromWorkspace(ctx, h.Store, ws)
			if err != nil {
				return err
			}
			if defsJSON {
				return defsWriteJSON(plan)
			}
			printPlan(plan)
			return nil
		})
	}
	plan, err := defspkg.Load(defsDir)
	if err != nil {
		return err
	}
	if defsJSON {
		return defsWriteJSON(plan)
	}
	printPlan(plan)
	return nil
}

func runDefsApply(_ *cobra.Command, _ []string) error {
	plan, err := defspkg.Load(defsDir)
	if err != nil {
		return err
	}
	return defsWithActiveWorkspace(func(ctx context.Context, h *bootstrap.StoreHandle, ws string) error {
		if err := defspkg.Apply(ctx, h.Store, ws, actorName(), plan); err != nil {
			return err
		}
		var started []*domain.Agent
		var worktrees []defsApplyWorktree
		if defsApplyStart {
			var err error
			started, err = defspkg.ApplyAgentInstancesForPlan(ctx, h.Store, ws, plan, true)
			if err != nil {
				return err
			}
			worktrees, err = provisionStartedAgentWorktrees(plan, started)
			if err != nil {
				return err
			}
		}
		if defsJSON {
			if defsApplyStart {
				return defsWriteJSON(map[string]any{
					"plan":                    plan,
					"started_agent_instances": started,
					"prepared_worktrees":      worktrees,
				})
			}
			return defsWriteJSON(plan)
		}
		fmt.Printf("Applied definitions to %s: %s\n", ws, defspkg.Summary(plan))
		if defsApplyStart {
			fmt.Printf("Started %d agent instance(s)\n", len(started))
			for _, worktree := range worktrees {
				fmt.Printf("Prepared worktree for %s repo %s: %s\n", worktree.Instance, worktree.Repo, worktree.Path)
			}
		}
		return nil
	})
}

type defsApplyWorktree struct {
	Agent    string `json:"agent"`
	Instance string `json:"instance"`
	Repo     string `json:"repo"`
	Path     string `json:"path"`
}

func provisionStartedAgentWorktrees(plan *defspkg.Plan, started []*domain.Agent) ([]defsApplyWorktree, error) {
	if plan == nil || len(started) == 0 {
		return nil, nil
	}
	startedByRole := make(map[string]*domain.Agent, len(started))
	for _, instance := range started {
		if instance == nil {
			continue
		}
		role := strings.TrimSpace(instance.RoleName)
		if role == "" {
			role = instance.Name
		}
		startedByRole[role] = instance
	}
	worktrees := make([]defsApplyWorktree, 0, len(plan.Agents))
	for _, agent := range plan.Agents {
		instance := startedByRole[agent.Name]
		if instance == nil {
			continue
		}
		worktree, err := sourceagent.ProvisionWorktree(instance.Name, agent.Repos)
		if err != nil {
			return nil, err
		}
		if worktree == nil {
			continue
		}
		worktrees = append(worktrees, defsApplyWorktree{
			Agent:    agent.Name,
			Instance: worktree.Instance,
			Repo:     worktree.Repo,
			Path:     worktree.Path,
		})
	}
	return worktrees, nil
}

func runDefsExportSource(_ *cobra.Command, _ []string) error {
	return defsWithActiveWorkspace(func(ctx context.Context, h *bootstrap.StoreHandle, ws string) error {
		plan, err := defspkg.PlanFromWorkspace(ctx, h.Store, ws)
		if err != nil {
			return err
		}
		files, err := defspkg.WriteSourceExport(defsDir, plan, defsExportForce)
		if err != nil {
			return err
		}
		if defsExportState {
			stateFiles, err := defspkg.WriteRuntimeStateExport(defsDir, plan, defsExportForce)
			if err != nil {
				return err
			}
			files = append(files, stateFiles...)
		}
		if defsJSON {
			return defsWriteJSON(files)
		}
		fmt.Printf("Exported %d files to %s\n", len(files), defsDir)
		for _, file := range files {
			fmt.Printf("  %s\n", file.Path)
		}
		return nil
	})
}

//nolint:gocognit,cyclop,funlen // Plan output intentionally groups several definition kinds in one stable CLI view.
func printPlan(plan *defspkg.Plan) {
	fmt.Printf("Definition plan for %s\n", plan.Root)
	if len(plan.Agents) == 0 && len(plan.Workflows) == 0 && len(plan.Runtimes) == 0 && len(plan.Skills) == 0 && len(plan.Tools) == 0 {
		fmt.Println("  no .loom definitions found")
		return
	}
	for _, skill := range plan.Skills {
		fmt.Printf("  skill    %-28s %s\n", skill.Name, skill.Version)
		if len(skill.Resources) > 0 {
			fmt.Printf("           resources: %s\n", strings.Join(skill.Resources, ", "))
		}
	}
	for _, tool := range plan.Tools {
		fmt.Printf("  tool     %-28s %s\n", tool.Name, tool.Version)
		if tool.Handler != "" {
			fmt.Printf("           handler: %s\n", tool.Handler)
		}
		if len(tool.Repos) > 0 {
			fmt.Printf("           repos: %s\n", strings.Join(tool.Repos, ", "))
		}
		if len(tool.Env) > 0 {
			fmt.Printf("           env: %s\n", strings.Join(tool.Env, ", "))
		}
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
