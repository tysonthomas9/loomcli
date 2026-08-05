// Package agentdef registers the `loom agentdef` noun-verb commands over the
// Phase 5 Agents capability.
//
// Distinct from `loom agent <worktree>`, which runs an actual agent process
// with a custom prompt.
package agentdef

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/tysonthomas9/loomcli/internal/cli"
	"github.com/tysonthomas9/loomcli/internal/cli/cmdstore"
	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/modules/agents"
)

var (
	agentAddRole         string
	agentAddAuto         bool
	agentAddMode         string
	agentAddProfile      string
	agentAddMaxConc      int
	agentAddBudget       string
	agentStartRequestID  string
	agentStopRequestID   string
	agentRemoveRequestID string
	agentListJSON        bool
	agentShowJSON        bool
)

var agentdefCmd = &cobra.Command{
	Use:     "agentdef",
	Short:   "Manage agent assignments within the active workspace",
	GroupID: "workspace",
	Long: `Define long-lived agent assignments through the Agents capability.

Distinct from 'loom agent <worktree>' which runs an actual agent process.
Phase 6 will unify these surfaces.

Agent definitions own identity and desired lifecycle only. Configure shared
behavior fields such as backend and task_filter with 'loom role'. Configure
execution placement such as repository and parent-epic scope with
'loom worker profile', then attach that profile with 'agentdef add --profile'.

The legacy --task and --orchestrator launch flags are retired; dispatch work
through an Execution workflow such as 'loom epic run'. The legacy
--repo-groups and --cross-repo flags have no Phase 5 replacement and are
rejected rather than silently broadening repository access.`,
}

var agentAddCmd = &cobra.Command{
	Use:   "add <NAME>",
	Short: "Register an agent assignment in the active workspace",
	Args:  cobra.ExactArgs(1),
	RunE:  runAgentAdd,
}

var agentListCmd = &cobra.Command{
	Use:   "list",
	Short: "List agent assignments in the active workspace",
	Args:  cobra.NoArgs,
	RunE:  runAgentList,
}

var agentShowCmd = &cobra.Command{
	Use:   "show <NAME>",
	Short: "Show agent details",
	Args:  cobra.ExactArgs(1),
	RunE:  runAgentShow,
}

var agentRemoveCmd = &cobra.Command{
	Use:   "remove <NAME>",
	Short: "Delete an agent assignment from the active workspace",
	Long: `Delete an agent assignment from the active workspace.

The command prints a generation-bound request ID before issuing the mutation.
After an ambiguous response, pass that exact value to --request-id to replay
the same Fleet receipt. Omit the flag for a fresh operation; arbitrary
caller-created request IDs are rejected.`,
	Args: cobra.ExactArgs(1),
	RunE: runAgentRemove,
}

var agentStartCmd = &cobra.Command{
	Use:   "start <NAME>",
	Short: "Request an agent assignment to start",
	Long: `Request an agent assignment to start accepting work.

The command prints a generation-bound request ID before issuing the mutation.
After an ambiguous response, pass that exact value to --request-id to replay
the same Fleet receipt. Omit the flag for a fresh operation.`,
	Args: cobra.ExactArgs(1),
	RunE: runAgentStart,
}

var agentStopCmd = &cobra.Command{
	Use:   "stop <NAME>",
	Short: "Request an agent assignment to stop",
	Long: `Request an agent assignment to stop accepting new work.

This changes durable desired state; it does not interrupt an active terminal
session. For the transitional runtime-control operation use:
  loom data agent stop <NAME> --force

The command prints a generation-bound request ID before issuing the mutation.
After an ambiguous response, pass that exact value to --request-id to replay
the same Fleet receipt. Omit the flag for a fresh operation.`,
	Args: cobra.ExactArgs(1),
	RunE: runAgentStop,
}

func init() {
	agentAddCmd.Flags().StringVar(&agentAddRole, "role", "", "Role name (required)")
	_ = agentAddCmd.MarkFlagRequired("role")
	agentAddCmd.Flags().BoolVar(&agentAddAuto, "auto", false, "Keep the agent's desired state running across controller restarts")
	agentAddCmd.Flags().StringVar(&agentAddMode, "mode", "", "Agent mode: ephemeral or service")
	agentAddCmd.Flags().StringVar(&agentAddProfile, "profile", "", "Execution worker profile name")
	agentAddCmd.Flags().IntVar(&agentAddMaxConc, "max-concurrency", 0, "Maximum concurrent runs for orchestrator/service agents")
	agentAddCmd.Flags().StringVar(&agentAddBudget, "budget-policy", "", "Budget/retry policy name")

	agentListCmd.Flags().BoolVar(&agentListJSON, "json", false, "JSON output")
	agentShowCmd.Flags().BoolVar(&agentShowJSON, "json", false, "JSON output")
	agentStartCmd.Flags().StringVar(&agentStartRequestID, "request-id", "", "Generation-bound retry ID printed by a prior attempt")
	agentStopCmd.Flags().StringVar(&agentStopRequestID, "request-id", "", "Generation-bound retry ID printed by a prior attempt")
	agentRemoveCmd.Flags().StringVar(&agentRemoveRequestID, "request-id", "", "Generation-bound retry ID printed by a prior attempt")

	agentdefCmd.AddCommand(agentAddCmd, agentListCmd, agentShowCmd, agentRemoveCmd, agentStartCmd, agentStopCmd)
	_ = cli.RegisterPreBackendCommandGuard(rejectAgentdefBackendFlag)
	cli.RegisterCommand(agentdefCmd)
}

func rejectAgentdefBackendFlag(cmd *cobra.Command) error {
	if cmd == nil || !isAgentdefCommand(cmd) {
		return nil
	}
	flag := cmd.Flags().Lookup("backend")
	if flag == nil {
		flag = cmd.InheritedFlags().Lookup("backend")
	}
	if flag == nil || !flag.Changed {
		return nil
	}
	return errors.New(
		"agentdef does not accept --backend; use " +
			"`loom role set ROLE backend VALUE` for behavior or " +
			"`loom worker profile add PROFILE --backend VALUE ...` for execution placement",
	)
}

func isAgentdefCommand(cmd *cobra.Command) bool {
	for current := cmd; current != nil; current = current.Parent() {
		if current.Name() == agentdefCmd.Name() {
			return true
		}
	}
	return false
}

func runAgentAdd(cmd *cobra.Command, args []string) error {
	return withAgentdefRuntime(cmd.Context(), func(ctx context.Context, runtime agentdefRuntime, ws string) error {
		create, err := agentCreateFromFlags(ws, args[0], agentAddMode)
		if err != nil {
			return err
		}
		a, err := runtime.definitions.CreateAgentDefinition(ctx, create)
		if err != nil {
			return fmt.Errorf("create agent: %w", err)
		}
		fmt.Printf("Created agent %s/%s (role=%s)\n", a.WorkspaceKey, a.Name, a.RoleName)
		return nil
	})
}

func agentCreateFromFlags(
	workspace string,
	name string,
	mode string,
) (AgentDefinitionCreateCommand, error) {
	mode = strings.TrimSpace(mode)
	kind := agents.AgentKindMaintenance
	switch mode {
	case "", "ephemeral":
	case "service":
		kind = agents.AgentKindAlwaysOn
	default:
		return AgentDefinitionCreateCommand{}, fmt.Errorf(
			"invalid agent mode %q (want ephemeral or service)",
			mode,
		)
	}
	maxInstances := agentAddMaxConc
	if maxInstances == 0 {
		maxInstances = 1
	}
	restartPolicy := ""
	if agentAddAuto {
		restartPolicy = "always"
	}
	return AgentDefinitionCreateCommand{
		Canonical: agents.CreateAgentCommand{
			WorkspaceKey: workspace,
			AgentID:      name,
			Name:         name,
			Kind:         kind,
			Behavior: agents.BehaviorReference{
				RoleName: agentAddRole,
			},
			DesiredState:  agents.DesiredRunning,
			ProfileName:   strings.TrimSpace(agentAddProfile),
			MaxInstances:  maxInstances,
			RestartPolicy: restartPolicy,
			BudgetPolicy:  agentAddBudget,
		},
	}, nil
}

func runAgentList(cmd *cobra.Command, _ []string) error {
	return withAgentdefRuntime(cmd.Context(), func(ctx context.Context, runtime agentdefRuntime, ws string) error {
		definitions, err := runtime.definitions.ListAgentDefinitions(ctx, ws)
		if err != nil {
			return fmt.Errorf("list agents: %w", err)
		}
		if agentListJSON {
			return cmdstore.WriteJSON(definitions)
		}
		if len(definitions) == 0 {
			fmt.Printf("No agents in workspace %s\n", ws)
			return nil
		}
		for _, a := range definitions {
			state := a.State
			if state == "" {
				state = "unknown"
			}
			auto := ""
			if a.Auto {
				auto = " auto"
			}
			mode := ""
			if a.Mode != "" {
				mode = " mode=" + a.Mode
			}
			fmt.Printf(
				"%-20s role=%-10s state=%s desired=%s%s%s\n",
				a.Name,
				a.RoleName,
				state,
				a.DesiredState,
				mode,
				auto,
			)
		}
		return nil
	})
}

func runAgentShow(cmd *cobra.Command, args []string) error { //nolint:funlen // The command renders one compatibility view with explicit legacy fields and stable output ordering.
	return withAgentdefRuntime(cmd.Context(), func(ctx context.Context, runtime agentdefRuntime, ws string) error {
		a, err := runtime.definitions.GetAgentDefinition(ctx, ws, args[0])
		if err != nil {
			return fmt.Errorf("get agent: %w", err)
		}
		if agentShowJSON {
			return cmdstore.WriteJSON(a)
		}
		fmt.Printf("Workspace:    %s\n", a.WorkspaceKey)
		fmt.Printf("Name:         %s\n", a.Name)
		fmt.Printf("Role:         %s\n", a.RoleName)
		state := a.State
		if state == "" {
			state = "unknown"
		}
		fmt.Printf("State:        %s\n", state)
		fmt.Printf("Desired:      %s\n", a.DesiredState)
		fmt.Printf("Auto-start:   %t\n", a.Auto)
		if a.Backend != "" {
			fmt.Printf("Backend:      %s\n", a.Backend)
		}
		if len(a.Repos) > 0 {
			fmt.Printf("Repos:        %s\n", strings.Join(a.Repos, ", "))
		}
		if len(a.RepoGroups) > 0 {
			fmt.Printf("Repo groups:  %s\n", strings.Join(a.RepoGroups, ", "))
		}
		if a.CrossRepo {
			fmt.Printf("Cross-repo:   true\n")
		}
		if a.Parent != "" {
			fmt.Printf("Parent epic:  %s\n", a.Parent)
		}
		if a.OrchestratorSessionID != "" {
			fmt.Printf("Orchestrator: %s\n", a.OrchestratorSessionID)
		}
		if a.ProfileName != "" {
			fmt.Printf("Profile:      %s\n", a.ProfileName)
		}
		if a.Mode != "" {
			fmt.Printf("Mode:         %s\n", a.Mode)
		}
		if a.TaskFilter != "" {
			fmt.Printf("Task filter:  %s\n", a.TaskFilter)
		}
		if a.MaxConcurrency > 0 {
			fmt.Printf("Max conc:     %d\n", a.MaxConcurrency)
		}
		if a.BudgetPolicy != "" {
			fmt.Printf("Budget:       %s\n", a.BudgetPolicy)
		}
		return nil
	})
}

func runAgentRemove(cmd *cobra.Command, args []string) error {
	requestID, err := resolveLifecycleRequestID(agentRemoveRequestID)
	if err != nil {
		return err
	}
	return applyAgentLifecycle(
		cmd.Context(),
		args[0],
		agents.LifecycleDelete,
		"remove",
		requestID,
	)
}

func runAgentStart(cmd *cobra.Command, args []string) error {
	requestID, err := resolveLifecycleRequestID(agentStartRequestID)
	if err != nil {
		return err
	}
	return applyAgentLifecycle(cmd.Context(), args[0], agents.LifecycleEnable, "start", requestID)
}

func runAgentStop(cmd *cobra.Command, args []string) error {
	requestID, err := resolveLifecycleRequestID(agentStopRequestID)
	if err != nil {
		return err
	}
	return applyAgentLifecycle(cmd.Context(), args[0], agents.LifecycleDisable, "stop", requestID)
}

//nolint:funlen // Lifecycle token validation, request replay, and user-visible recovery instructions must remain one atomic CLI operation.
func applyAgentLifecycle(
	ctx context.Context,
	name string,
	action agents.LifecycleAction,
	commandType string,
	requestID string,
) error {
	return withAgentdefRuntime(ctx, func(ctx context.Context, runtime agentdefRuntime, ws string) error {
		current, getErr := runtime.definitions.GetAgentDefinition(ctx, ws, name)
		generationID := ""
		if getErr == nil {
			if current == nil || !agents.ValidGenerationID(current.GenerationID) {
				return agents.ErrInvalidPersistedState
			}
			generationID = current.GenerationID
		} else {
			notFound := errors.Is(getErr, domain.ErrNotFound) ||
				errors.Is(getErr, agents.ErrNotFound)
			if action != agents.LifecycleDelete || !notFound {
				return fmt.Errorf("read Agent generation before %s: %w", commandType, getErr)
			}
			if _, bound, parseErr := parseBoundLifecycleRequestID(requestID); parseErr != nil {
				return fmt.Errorf("parse lifecycle retry token: %w", parseErr)
			} else if !bound {
				return fmt.Errorf(
					"agent not found; a delete replay requires the generation-bound request-id printed by the prior attempt: %w",
					getErr,
				)
			}
		}
		boundRequestID, err := bindLifecycleRequestID(requestID, generationID)
		if err != nil {
			return fmt.Errorf("bind lifecycle request to Agent generation: %w", err)
		}
		fmt.Printf("Lifecycle request-id=%s\n", boundRequestID)
		if _, err := runtime.definitions.ApplyAgentLifecycle(ctx, AgentLifecycleCommand{
			WorkspaceKey: ws,
			AgentID:      name,
			Action:       action,
			RequestID:    boundRequestID,
		}); err != nil {
			return fmt.Errorf(
				"update agent desired state (request_id=%s; retry with --request-id %s): %w",
				boundRequestID,
				boundRequestID,
				err,
			)
		}
		if action == agents.LifecycleDelete {
			fmt.Printf("Removed agent %s/%s (request-id=%s)\n", ws, name, boundRequestID)
		} else {
			fmt.Printf("Requested agent %s/%s %s (request-id=%s)\n", ws, name, commandType, boundRequestID)
		}
		return nil
	})
}

var newLifecycleRequestID = func() (string, error) {
	random := make([]byte, 16)
	if _, err := rand.Read(random); err != nil {
		return "", fmt.Errorf("generate lifecycle request id: %w", err)
	}
	return "req-" + hex.EncodeToString(random), nil
}

func resolveLifecycleRequestID(explicit string) (string, error) {
	explicit = strings.TrimSpace(explicit)
	if explicit != "" {
		requestID, err := normalizeLifecycleRequestID(explicit)
		if err != nil {
			return "", fmt.Errorf("invalid lifecycle --request-id: %w", err)
		}
		if _, bound, err := parseBoundLifecycleRequestID(requestID); err != nil || !bound {
			if err == nil {
				err = errors.New("expected a generation-bound token printed by an earlier lifecycle attempt")
			}
			return "", fmt.Errorf(
				"invalid lifecycle --request-id: %w; omit the flag to start a fresh operation",
				err,
			)
		}
		return requestID, nil
	}
	requestID, err := newLifecycleRequestID()
	if err != nil {
		return "", err
	}
	requestID, err = normalizeLifecycleRequestID(requestID)
	if err != nil {
		return "", fmt.Errorf("generated invalid lifecycle request id: %w", err)
	}
	return requestID, nil
}
