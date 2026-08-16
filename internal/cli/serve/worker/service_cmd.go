package worker

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/tysonthomas9/loomcli/internal/modules/agents"

	"github.com/spf13/cobra"

	"github.com/tysonthomas9/loomcli/internal/bootstrap"
	"github.com/tysonthomas9/loomcli/internal/cli"
	"github.com/tysonthomas9/loomcli/internal/cli/cmdstore"
	"github.com/tysonthomas9/loomcli/internal/cli/managementapi"
)

var (
	workerServiceAddName            string
	workerServiceAddKind            string
	workerServiceAddDesiredState    string
	workerServiceAddRoleName        string
	workerServiceAddProfileName     string
	workerServiceAddScheduleID      string
	workerServiceAddEventSources    []string
	workerServiceAddTriggerRefs     []string
	workerServiceAddPlacementPolicy string
	workerServiceAddMaxInstances    int
	workerServiceAddLeaseID         string
	workerServiceAddRestartPolicy   string
	workerServiceAddPermissions     []string
	workerServiceAddBudgetPolicy    string
	workerServiceAddStateRef        string
	workerServiceAddMetadata        []string

	workerServiceListKind         string
	workerServiceListDesiredState string
	workerServiceListRoleName     string
	workerServiceListProfileName  string
	workerServiceListLimit        int
	workerServiceListJSON         bool
	workerServiceShowJSON         bool
)

var workerServiceCmd = &cobra.Command{
	Use:   "service",
	Short: "Manage long-running agent services in the active workspace",
}

var workerServiceAddCmd = &cobra.Command{
	Use:               "add <SERVICE_ID>",
	Short:             "Create an agent service",
	Args:              cobra.ExactArgs(1),
	PersistentPreRunE: cli.PrepareStandaloneHTTPCommand,
	RunE:              runWorkerServiceAdd,
}

var workerServiceListCmd = &cobra.Command{
	Use:   "list",
	Short: "List agent services",
	Args:  cobra.NoArgs,
	RunE:  runWorkerServiceList,
}

var workerServiceShowCmd = &cobra.Command{
	Use:   "show <SERVICE_ID>",
	Short: "Show agent service details",
	Args:  cobra.ExactArgs(1),
	RunE:  runWorkerServiceShow,
}

var workerServiceSetCmd = &cobra.Command{
	Use:   "set <SERVICE_ID> <KEY> <VALUE>",
	Short: "Set an agent service field",
	Long: `Set an agent service field by key. Supported keys:
  name              string
  kind              lead|support|triage|on_call|scheduled|maintenance|orchestrator|always_on|cron|event|campaign_orchestrator
  desired_state     running|stopped|paused
  role_name         string
  profile_name      string
  schedule_id       string
  placement_policy  string
  max_instances     positive integer
  lease_id          string
  restart_policy    string
  budget_policy     string
  state_ref         string
  event_sources     comma-separated list
  trigger_refs      comma-separated list
  permissions       comma-separated list
  metadata          comma-separated key=value list`,
	Args:              cobra.ExactArgs(3),
	PersistentPreRunE: cli.PrepareStandaloneHTTPCommand,
	RunE:              runWorkerServiceSet,
}

var workerServiceUnsetCmd = &cobra.Command{
	Use:   "unset <SERVICE_ID> <KEY>",
	Short: "Clear an agent service field",
	Long: `Clear an agent service field. Supported keys:
  profile_name
  schedule_id
  placement_policy
  lease_id
  restart_policy
  budget_policy
  state_ref
  event_sources
  trigger_refs
  permissions
  metadata`,
	Args:              cobra.ExactArgs(2),
	PersistentPreRunE: cli.PrepareStandaloneHTTPCommand,
	RunE:              runWorkerServiceUnset,
}

var workerServiceRemoveCmd = &cobra.Command{
	Use:               "remove <SERVICE_ID>",
	Short:             "Delete an agent service",
	Args:              cobra.ExactArgs(1),
	PersistentPreRunE: cli.PrepareStandaloneHTTPCommand,
	RunE:              runWorkerServiceRemove,
}

func initWorkerServiceCommands() {
	workerServiceAddCmd.Flags().StringVar(&workerServiceAddName, "name", "", "Display name (default: service ID)")
	workerServiceAddCmd.Flags().StringVar(&workerServiceAddKind, "kind", "", "Service kind")
	workerServiceAddCmd.Flags().StringVar(&workerServiceAddDesiredState, "desired-state", "", "Desired state running|stopped|paused (default: stopped)")
	workerServiceAddCmd.Flags().StringVar(&workerServiceAddRoleName, "role", "", "Role name")
	workerServiceAddCmd.Flags().StringVar(&workerServiceAddProfileName, "profile", "", "Worker profile name")
	workerServiceAddCmd.Flags().StringVar(&workerServiceAddScheduleID, "schedule-id", "", "Schedule identifier")
	workerServiceAddCmd.Flags().StringSliceVar(&workerServiceAddEventSources, "event-source", nil, "Event source (comma-separated or repeat flag)")
	workerServiceAddCmd.Flags().StringSliceVar(&workerServiceAddTriggerRefs, "trigger-ref", nil, "Trigger binding reference (comma-separated or repeat flag)")
	workerServiceAddCmd.Flags().StringVar(&workerServiceAddPlacementPolicy, "placement-policy", "", "Placement policy")
	workerServiceAddCmd.Flags().IntVar(&workerServiceAddMaxInstances, "max-instances", 0, "Maximum instances (0 = default)")
	workerServiceAddCmd.Flags().StringVar(&workerServiceAddLeaseID, "lease-id", "", "Service lease identifier")
	workerServiceAddCmd.Flags().StringVar(&workerServiceAddRestartPolicy, "restart-policy", "", "Restart policy")
	workerServiceAddCmd.Flags().StringSliceVar(&workerServiceAddPermissions, "permission", nil, "Permission (comma-separated or repeat flag)")
	workerServiceAddCmd.Flags().StringVar(&workerServiceAddBudgetPolicy, "budget-policy", "", "Budget policy")
	workerServiceAddCmd.Flags().StringVar(&workerServiceAddStateRef, "state-ref", "", "Persistent state reference")
	workerServiceAddCmd.Flags().StringArrayVar(&workerServiceAddMetadata, "metadata", nil, "Metadata key=value (repeatable)")
	_ = workerServiceAddCmd.MarkFlagRequired("kind")
	_ = workerServiceAddCmd.MarkFlagRequired("role")

	workerServiceListCmd.Flags().StringVar(&workerServiceListKind, "kind", "", "Filter by service kind")
	workerServiceListCmd.Flags().StringVar(&workerServiceListDesiredState, "desired-state", "", "Filter by desired state")
	workerServiceListCmd.Flags().StringVar(&workerServiceListRoleName, "role", "", "Filter by role name")
	workerServiceListCmd.Flags().StringVar(&workerServiceListProfileName, "profile", "", "Filter by profile name")
	workerServiceListCmd.Flags().IntVar(&workerServiceListLimit, "limit", 0, "Maximum rows")
	workerServiceListCmd.Flags().BoolVar(&workerServiceListJSON, "json", false, "JSON output")
	workerServiceShowCmd.Flags().BoolVar(&workerServiceShowJSON, "json", false, "JSON output")

	workerServiceCmd.AddCommand(workerServiceAddCmd, workerServiceListCmd, workerServiceShowCmd, workerServiceSetCmd, workerServiceUnsetCmd, workerServiceRemoveCmd)
	workerCmd.AddCommand(workerServiceCmd)
}

func runWorkerServiceAdd(cmd *cobra.Command, args []string) error {
	metadata, err := parseWorkerProfileMetadata(workerServiceAddMetadata)
	if err != nil {
		return err
	}
	kind, err := parseAgentServiceKind(workerServiceAddKind)
	if err != nil {
		return err
	}
	desiredState, err := parseAgentServiceDesiredState(workerServiceAddDesiredState, true)
	if err != nil {
		return err
	}
	client, err := managementapi.New(cmd.Context(), "loom worker service add")
	if err != nil {
		return err
	}
	name := strings.TrimSpace(workerServiceAddName)
	if name == "" {
		name = args[0]
	}
	record, err := client.CreateAgent(cmd.Context(), agents.CreateAgentCommand{
		WorkspaceKey: client.Workspace(), AgentID: args[0], Name: name,
		Kind: kind, DesiredState: desiredState,
		Behavior:    agents.BehaviorReference{RoleName: workerServiceAddRoleName},
		ProfileName: workerServiceAddProfileName, ScheduleID: workerServiceAddScheduleID,
		EventSources: workerServiceAddEventSources, TriggerRefs: workerServiceAddTriggerRefs,
		PlacementPolicy: workerServiceAddPlacementPolicy, MaxInstances: workerServiceAddMaxInstances,
		LeaseID: workerServiceAddLeaseID, RestartPolicy: workerServiceAddRestartPolicy,
		Permissions: workerServiceAddPermissions, BudgetPolicy: workerServiceAddBudgetPolicy,
		StateRef: workerServiceAddStateRef, Metadata: metadata,
	})
	if err != nil {
		return fmt.Errorf("create agent service: %w", err)
	}
	fmt.Printf("Created agent service %s/%s\n", record.WorkspaceKey, record.AgentID)
	return nil
}

func runWorkerServiceList(cmd *cobra.Command, _ []string) error {
	return cmdstore.WithActiveWorkspace(cmd.Context(), func(ctx context.Context, h *bootstrap.StoreHandle, ws string) error {
		desiredState, err := parseAgentServiceDesiredState(workerServiceListDesiredState, true)
		if err != nil {
			return err
		}
		var kind agents.AgentKind
		if strings.TrimSpace(workerServiceListKind) != "" {
			kind, err = parseAgentServiceKind(workerServiceListKind)
			if err != nil {
				return err
			}
		}
		services, err := h.Store.AgentServices().List(ctx, ws, agents.AgentServiceFilter{
			Kind:         kind,
			DesiredState: desiredState,
			RoleName:     workerServiceListRoleName,
			ProfileName:  workerServiceListProfileName,
			Limit:        workerServiceListLimit,
		})
		if err != nil {
			return fmt.Errorf("list agent services: %w", err)
		}
		if workerServiceListJSON {
			return cmdstore.WriteJSON(services)
		}
		if len(services) == 0 {
			fmt.Printf("No agent services in workspace %s\n", ws)
			return nil
		}
		for _, svc := range services {
			fmt.Printf("%-24s %-14s %-10s %-14s %-14s %d\n", svc.ServiceID, svc.Kind, svc.DesiredState, svc.RoleName, svc.ProfileName, svc.MaxInstances)
		}
		return nil
	})
}

func runWorkerServiceShow(cmd *cobra.Command, args []string) error {
	return cmdstore.WithActiveWorkspace(cmd.Context(), func(ctx context.Context, h *bootstrap.StoreHandle, ws string) error {
		svc, err := h.Store.AgentServices().Get(ctx, ws, args[0])
		if err != nil {
			return fmt.Errorf("get agent service: %w", err)
		}
		if workerServiceShowJSON {
			return cmdstore.WriteJSON(svc)
		}
		printAgentService(svc)
		return nil
	})
}

func runWorkerServiceSet(cmd *cobra.Command, args []string) error {
	return runWorkerServiceMutation(cmd, args[0], args[1], args[2], false)
}

func runWorkerServiceUnset(cmd *cobra.Command, args []string) error {
	return runWorkerServiceMutation(cmd, args[0], args[1], "", true)
}

func runWorkerServiceRemove(cmd *cobra.Command, args []string) error {
	client, err := managementapi.New(cmd.Context(), "loom worker service remove")
	if err != nil {
		return err
	}
	current, err := client.GetAgent(cmd.Context(), args[0])
	if err != nil {
		return fmt.Errorf("get agent service: %w", err)
	}
	if _, err := client.ArchiveAgent(cmd.Context(), args[0], current.UpdatedAt); err != nil {
		return fmt.Errorf("remove agent service: %w", err)
	}
	fmt.Printf("Removed agent service %s/%s\n", client.Workspace(), args[0])
	return nil
}

func runWorkerServiceMutation(
	cmd *cobra.Command,
	serviceID, key, value string,
	unset bool,
) error {
	patch, err := buildAgentServicePatch(key, value, unset)
	if err != nil {
		return err
	}
	client, err := managementapi.New(cmd.Context(), "loom worker service update")
	if err != nil {
		return err
	}
	current, err := client.GetAgent(cmd.Context(), serviceID)
	if err != nil {
		return fmt.Errorf("get agent service: %w", err)
	}
	if patch.DesiredState != nil {
		if _, err := client.SetAgentDesiredState(cmd.Context(), serviceID, managementapi.SetAgentDesiredStateRequest{
			ExpectedState: current.DesiredState,
			DesiredState:  *patch.DesiredState, ExpectedUpdatedAt: current.UpdatedAt,
		}); err != nil {
			return fmt.Errorf("update agent service: %w", err)
		}
	} else if _, err := client.UpdateAgent(cmd.Context(), serviceID, current.UpdatedAt, agentPatchFromStore(patch)); err != nil {
		return fmt.Errorf("update agent service: %w", err)
	}
	if unset {
		fmt.Printf("Cleared %s/%s.%s\n", client.Workspace(), serviceID, key)
	} else {
		fmt.Printf("Set %s/%s.%s = %s\n", client.Workspace(), serviceID, key, value)
	}
	return nil
}

func agentPatchFromStore(patch agents.AgentServiceUpdate) agents.AgentPatch {
	out := agents.AgentPatch{
		Name: patch.Name, ProfileName: patch.ProfileName, ScheduleID: patch.ScheduleID,
		EventSources: patch.EventSources, TriggerRefs: patch.TriggerRefs,
		PlacementPolicy: patch.PlacementPolicy, MaxInstances: patch.MaxInstances,
		LeaseID: patch.LeaseID, RestartPolicy: patch.RestartPolicy,
		Permissions: patch.Permissions, BudgetPolicy: patch.BudgetPolicy,
		StateRef: patch.StateRef, Metadata: patch.Metadata,
	}
	if patch.Kind != nil {
		value := *patch.Kind
		out.Kind = &value
	}
	if patch.RoleName != nil || patch.DriverID != nil || patch.DriverVersionID != nil {
		behavior := agents.BehaviorReference{}
		if patch.RoleName != nil {
			behavior.RoleName = *patch.RoleName
		}
		if patch.DriverID != nil {
			behavior.DriverID = *patch.DriverID
		}
		if patch.DriverVersionID != nil {
			behavior.DriverVersionID = *patch.DriverVersionID
		}
		out.Behavior = &behavior
	}
	return out
}

func printAgentService(svc *agents.AgentServiceRecord) {
	fmt.Printf("Workspace:      %s\n", svc.WorkspaceKey)
	fmt.Printf("Service ID:     %s\n", svc.ServiceID)
	fmt.Printf("Name:           %s\n", svc.Name)
	fmt.Printf("Kind:           %s\n", svc.Kind)
	fmt.Printf("Desired state:  %s\n", svc.DesiredState)
	fmt.Printf("Role:           %s\n", svc.RoleName)
	if svc.ProfileName != "" {
		fmt.Printf("Profile:        %s\n", svc.ProfileName)
	}
	if svc.ScheduleID != "" {
		fmt.Printf("Schedule ID:    %s\n", svc.ScheduleID)
	}
	if len(svc.EventSources) > 0 {
		fmt.Printf("Event sources:  %s\n", strings.Join(svc.EventSources, ", "))
	}
	if len(svc.TriggerRefs) > 0 {
		fmt.Printf("Trigger refs:   %s\n", strings.Join(svc.TriggerRefs, ", "))
	}
	if svc.PlacementPolicy != "" {
		fmt.Printf("Placement:      %s\n", svc.PlacementPolicy)
	}
	fmt.Printf("Max instances:  %d\n", svc.MaxInstances)
	if svc.LeaseID != "" {
		fmt.Printf("Lease ID:       %s\n", svc.LeaseID)
	}
	if svc.RestartPolicy != "" {
		fmt.Printf("Restart policy: %s\n", svc.RestartPolicy)
	}
	if len(svc.Permissions) > 0 {
		fmt.Printf("Permissions:    %s\n", strings.Join(svc.Permissions, ", "))
	}
	if svc.BudgetPolicy != "" {
		fmt.Printf("Budget policy:  %s\n", svc.BudgetPolicy)
	}
	if svc.StateRef != "" {
		fmt.Printf("State ref:      %s\n", svc.StateRef)
	}
}

func buildAgentServicePatch(key, value string, unset bool) (agents.AgentServiceUpdate, error) {
	switch key {
	case "name", "role_name", "profile_name", "schedule_id", "placement_policy",
		"lease_id", "restart_policy", "budget_policy", "state_ref":
		return buildAgentServiceStringPatch(key, value, unset)
	case "kind", "desired_state", "max_instances":
		return buildAgentServiceTypedPatch(key, value, unset)
	case "event_sources", "trigger_refs", "permissions", "metadata":
		return buildAgentServiceListPatch(key, value, unset)
	default:
		var patch agents.AgentServiceUpdate
		return patch, fmt.Errorf("unsupported agent service field %q", key)
	}
}

// buildAgentServiceStringPatch handles the plain string agent service
// fields for buildAgentServicePatch.
func buildAgentServiceStringPatch(key, value string, unset bool) (agents.AgentServiceUpdate, error) {
	var patch agents.AgentServiceUpdate
	switch key {
	case "name":
		if unset {
			return patch, fmt.Errorf("name cannot be unset")
		}
		patch.Name = &value
	case "role_name":
		if unset {
			return patch, fmt.Errorf("role_name cannot be unset")
		}
		patch.RoleName = &value
	case "profile_name":
		patch.ProfileName = &value
	case "schedule_id":
		patch.ScheduleID = &value
	case "placement_policy":
		patch.PlacementPolicy = &value
	case "lease_id":
		patch.LeaseID = &value
	case "restart_policy":
		patch.RestartPolicy = &value
	case "budget_policy":
		patch.BudgetPolicy = &value
	case "state_ref":
		patch.StateRef = &value
	}
	return patch, nil
}

// buildAgentServiceTypedPatch handles the agent service fields that parse
// into typed values (enums, ints) for buildAgentServicePatch.
func buildAgentServiceTypedPatch(key, value string, unset bool) (agents.AgentServiceUpdate, error) {
	var patch agents.AgentServiceUpdate
	switch key {
	case "kind":
		if unset {
			return patch, fmt.Errorf("kind cannot be unset")
		}
		kind, err := parseAgentServiceKind(value)
		if err != nil {
			return patch, err
		}
		patch.Kind = &kind
	case "desired_state":
		if unset {
			return patch, fmt.Errorf("desired_state cannot be unset")
		}
		state, err := parseAgentServiceDesiredState(value, false)
		if err != nil {
			return patch, err
		}
		patch.DesiredState = &state
	case "max_instances":
		if unset {
			return patch, fmt.Errorf("max_instances cannot be unset")
		}
		n, err := strconv.Atoi(value)
		if err != nil {
			return patch, fmt.Errorf("max_instances must be an integer: %w", err)
		}
		if n < 1 {
			return patch, fmt.Errorf("max_instances must be positive")
		}
		patch.MaxInstances = &n
	}
	return patch, nil
}

// buildAgentServiceListPatch handles the list- and map-valued agent service
// fields for buildAgentServicePatch.
func buildAgentServiceListPatch(key, value string, unset bool) (agents.AgentServiceUpdate, error) {
	var patch agents.AgentServiceUpdate
	switch key {
	case "event_sources":
		list := splitWorkerProfileList(value)
		patch.EventSources = &list
	case "trigger_refs":
		list := splitWorkerProfileList(value)
		patch.TriggerRefs = &list
	case "permissions":
		list := splitWorkerProfileList(value)
		patch.Permissions = &list
	case "metadata":
		if unset {
			metadata := map[string]string{}
			patch.Metadata = &metadata
			return patch, nil
		}
		metadata, err := parseWorkerProfileMetadata(strings.Split(value, ","))
		if err != nil {
			return patch, err
		}
		patch.Metadata = &metadata
	}
	return patch, nil
}

func parseAgentServiceKind(raw string) (agents.AgentKind, error) {
	kind := agents.AgentKind(strings.TrimSpace(raw))
	switch kind {
	case agents.AgentKindLead, agents.AgentKindSupport, agents.AgentKindTriage,
		agents.AgentKindOnCall, agents.AgentKindScheduled, agents.AgentKindMaintenance,
		agents.AgentKindOrchestrator, agents.AgentKindAlwaysOn, agents.AgentKindCron,
		agents.AgentKindEvent, agents.AgentKindCampaignOrchestrator:
		return kind, nil
	default:
		return "", fmt.Errorf("unsupported agent service kind %q", raw)
	}
}

func parseAgentServiceDesiredState(raw string, allowEmpty bool) (agents.DesiredState, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" && allowEmpty {
		return "", nil
	}
	state := agents.DesiredState(raw)
	switch state {
	case agents.DesiredRunning, agents.DesiredStopped, agents.DesiredPaused:
		return state, nil
	default:
		return "", fmt.Errorf("unsupported agent service desired_state %q", raw)
	}
}
