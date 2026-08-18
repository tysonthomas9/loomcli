// Package agents implements scripted-role agent-instance CRUD commands.
package agents

import (
	"context"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/tysonthomas9/loomcli/internal/agentprovision"
	"github.com/tysonthomas9/loomcli/internal/bootstrap"
	"github.com/tysonthomas9/loomcli/internal/cli"
	"github.com/tysonthomas9/loomcli/internal/cli/cmdstore"
	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/store"
	"github.com/tysonthomas9/loomcli/internal/trigger"
)

var (
	agentsWithActiveWorkspace           = cmdstore.WithActiveWorkspace
	agentsWorkspaceDir                  = resolveAgentsWorkspaceDir
	agentsOutput              io.Writer = os.Stdout

	agentsAddRole     string
	agentsAddSchedule string
	agentsAddName     string
	agentsAddTimezone string
)

var agentsCmd = &cobra.Command{
	Use:     "agents",
	Short:   "Manage scripted-role agent instances",
	GroupID: "workspace",
}

var agentsListCmd = &cobra.Command{
	Use: "list", Short: "List agent instances", Args: cobra.NoArgs, RunE: runAgentsList,
}

var agentsAddCmd = &cobra.Command{
	Use: "add <id>", Short: "Create a cron-scheduled scripted-role instance", Args: cobra.ExactArgs(1), RunE: runAgentsAdd,
}

var agentsEnableCmd = &cobra.Command{
	Use: "enable <id>", Short: "Enable an agent instance", Args: cobra.ExactArgs(1), RunE: runAgentsEnable,
}

var agentsDisableCmd = &cobra.Command{
	Use: "disable <id>", Short: "Disable an agent instance", Args: cobra.ExactArgs(1), RunE: runAgentsDisable,
}

var agentsRemoveCmd = &cobra.Command{
	Use: "remove <id>", Short: "Remove an agent instance and its bindings", Args: cobra.ExactArgs(1), RunE: runAgentsRemove,
}

func init() {
	agentsAddCmd.Flags().StringVar(&agentsAddRole, "role", "", "Scripted role name")
	agentsAddCmd.Flags().StringVar(&agentsAddSchedule, "schedule", "", "Cron expression")
	agentsAddCmd.Flags().StringVar(&agentsAddName, "name", "", "Display name")
	agentsAddCmd.Flags().StringVar(&agentsAddTimezone, "timezone", "", "IANA schedule timezone (default UTC)")
	_ = agentsAddCmd.MarkFlagRequired("role")
	_ = agentsAddCmd.MarkFlagRequired("schedule")
	agentsCmd.AddCommand(agentsListCmd, agentsAddCmd, agentsEnableCmd, agentsDisableCmd, agentsRemoveCmd)
	cli.RegisterCommand(agentsCmd)
}

func runAgentsAdd(_ *cobra.Command, args []string) error {
	if len(args) != 1 {
		return fmt.Errorf("agent service id is required")
	}
	if strings.TrimSpace(agentsAddRole) == "" {
		return fmt.Errorf("--role is required")
	}
	if strings.TrimSpace(agentsAddSchedule) == "" {
		return fmt.Errorf("--schedule is required")
	}
	return agentsWithActiveWorkspace(func(ctx context.Context, h *bootstrap.StoreHandle, ws string) error {
		svc, binding, err := agentprovision.CreateAgentInstance(ctx, h.Store, ws, agentsWorkspaceDir(ws), agentprovision.AgentInstanceCreate{
			ServiceID: args[0], Name: agentsAddName, RoleName: agentsAddRole, CreatedBy: "cli",
			Binding: agentprovision.AgentInstanceBinding{
				Kind: trigger.CronSourceKind, Schedule: agentsAddSchedule,
				Timezone: agentsAddTimezone, Enabled: true,
			},
		})
		if err != nil {
			return fmt.Errorf("add agent instance: %w", err)
		}
		_, err = fmt.Fprintf(agentsOutput, "Added agent %s (%s) on %s\n", svc.ServiceID, svc.RoleName, binding.Schedule)
		return err
	})
}

func runAgentsList(_ *cobra.Command, _ []string) error {
	return agentsWithActiveWorkspace(func(ctx context.Context, h *bootstrap.StoreHandle, ws string) error {
		services, err := h.Store.AgentServices().List(ctx, ws, store.AgentServiceFilter{})
		if err != nil {
			return fmt.Errorf("list agent instances: %w", err)
		}
		sort.Slice(services, func(i, j int) bool { return services[i].ServiceID < services[j].ServiceID })
		tw := tabwriter.NewWriter(agentsOutput, 0, 4, 2, ' ', 0)
		if _, err := fmt.Fprintln(tw, "ID\tROLE\tTRIGGER KIND\tSCHEDULE\tSTATE"); err != nil {
			return err
		}
		for _, svc := range services {
			if svc == nil || svc.DeletedAt != nil {
				continue
			}
			schedule := ""
			bindings, listErr := h.Store.TriggerBindings().List(ctx, ws, store.TriggerBindingFilter{TargetAgentServiceID: svc.ServiceID})
			if listErr != nil {
				return fmt.Errorf("list bindings for agent %s: %w", svc.ServiceID, listErr)
			}
			sort.Slice(bindings, func(i, j int) bool { return bindings[i].BindingID < bindings[j].BindingID })
			if len(bindings) > 0 && bindings[0] != nil {
				schedule = bindings[0].Schedule
			}
			if _, err := fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n", svc.ServiceID, svc.RoleName, svc.TriggerKind, schedule, svc.DesiredState); err != nil {
				return err
			}
		}
		return tw.Flush()
	})
}

func runAgentsEnable(_ *cobra.Command, args []string) error {
	return setAgentsState(args, domain.AgentServiceDesiredRunning, "Enabled")
}

func runAgentsDisable(_ *cobra.Command, args []string) error {
	return setAgentsState(args, domain.AgentServiceDesiredStopped, "Disabled")
}

func setAgentsState(args []string, state domain.AgentServiceDesiredState, verb string) error {
	if len(args) != 1 {
		return fmt.Errorf("agent service id is required")
	}
	return agentsWithActiveWorkspace(func(ctx context.Context, h *bootstrap.StoreHandle, ws string) error {
		svc, err := agentprovision.SetAgentInstanceDesiredState(ctx, h.Store, ws, args[0], state)
		if err != nil {
			return fmt.Errorf("%s agent instance: %w", strings.ToLower(verb), err)
		}
		_, err = fmt.Fprintf(agentsOutput, "%s agent %s\n", verb, svc.ServiceID)
		return err
	})
}

func runAgentsRemove(_ *cobra.Command, args []string) error {
	if len(args) != 1 {
		return fmt.Errorf("agent service id is required")
	}
	return agentsWithActiveWorkspace(func(ctx context.Context, h *bootstrap.StoreHandle, ws string) error {
		if err := agentprovision.DeleteAgentInstance(ctx, h.Store, ws, args[0]); err != nil {
			return fmt.Errorf("remove agent instance: %w", err)
		}
		_, err := fmt.Fprintf(agentsOutput, "Removed agent %s\n", args[0])
		return err
	})
}

func resolveAgentsWorkspaceDir(ws string) string {
	if cache, err := bootstrap.LoadStateCache(); err == nil && cache != nil {
		if dir := strings.TrimSpace(cache.Workspaces[ws].Path); dir != "" {
			return dir
		}
	}
	if dir := strings.TrimSpace(os.Getenv("LOOM_WORKSPACE_RUNTIME_DIR")); dir != "" {
		return dir
	}
	dir, _ := os.Getwd()
	return dir
}
