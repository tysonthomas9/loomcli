package driver

import (
	"context"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/tysonthomas9/loomcli/internal/bootstrap"
	"github.com/tysonthomas9/loomcli/internal/cli/cmdstore"
	"github.com/tysonthomas9/loomcli/internal/domain"
	driverpkg "github.com/tysonthomas9/loomcli/internal/driver"
	"github.com/tysonthomas9/loomcli/internal/epicrunner"
	"github.com/tysonthomas9/loomcli/internal/leadcontrol"
	"github.com/tysonthomas9/loomcli/internal/store"
)

var (
	driverListAgentsWorkspaceKey string
	driverListAgentsDriverRunID  string
	driverListAgentsNodeID       string
	driverListAgentsLeaseID      string
	driverListAgentsFence        int64
	driverListAgentsJSON         bool

	driverAgentSessionWorkspaceKey string
	driverAgentSessionDriverRunID  string
	driverAgentSessionNodeID       string
	driverAgentSessionLeaseID      string
	driverAgentSessionFence        int64
	driverAgentSessionName         string
	driverAgentSessionJSON         bool

	driverUpdateAgentParentWorkspaceKey string
	driverUpdateAgentParentDriverRunID  string
	driverUpdateAgentParentNodeID       string
	driverUpdateAgentParentLeaseID      string
	driverUpdateAgentParentFence        int64
	driverUpdateAgentParentName         string
	driverUpdateAgentParentParent       string
	driverUpdateAgentParentExpectParent string
	driverUpdateAgentParentJSON         bool

	driverDeliverLeadWorkspaceKey string
	driverDeliverLeadDriverRunID  string
	driverDeliverLeadNodeID       string
	driverDeliverLeadLeaseID      string
	driverDeliverLeadFence        int64
	driverDeliverLeadName         string
	driverDeliverLeadJSON         bool

	driverDeliverAgentMessageWorkspaceKey string
	driverDeliverAgentMessageDriverRunID  string
	driverDeliverAgentMessageNodeID       string
	driverDeliverAgentMessageLeaseID      string
	driverDeliverAgentMessageFence        int64
	driverDeliverAgentMessageName         string
	driverDeliverAgentMessageText         string
	driverDeliverAgentMessageJSON         bool
)

var driverListAgentsCmd = &cobra.Command{
	Use:    "list-agents",
	Short:  "List agents for a driver runtime",
	Hidden: true,
	Args:   cobra.NoArgs,
	RunE:   runDriverListAgents,
}

var driverAgentOrchestrationSessionCmd = &cobra.Command{
	Use:    "agent-orchestration-session",
	Short:  "Resolve the active orchestration session for an agent",
	Hidden: true,
	Args:   cobra.NoArgs,
	RunE:   runDriverAgentOrchestrationSession,
}

var driverUpdateAgentParentCmd = &cobra.Command{
	Use:    "update-agent-parent",
	Short:  "Update an agent parent for a driver runtime",
	Hidden: true,
	Args:   cobra.NoArgs,
	RunE:   runDriverUpdateAgentParent,
}

var driverDeliverLeadAssignmentCmd = &cobra.Command{
	Use:    "deliver-lead-assignment",
	Short:  "Deliver the current backend epic assignment to a lead runtime",
	Hidden: true,
	Args:   cobra.NoArgs,
	RunE:   runDriverDeliverLeadAssignment,
}

var driverDeliverAgentMessageCmd = &cobra.Command{
	Use:    "deliver-agent-message",
	Short:  "Queue or deliver a workflow message to an agent runtime",
	Hidden: true,
	Args:   cobra.NoArgs,
	RunE:   runDriverDeliverAgentMessage,
}

func bindDriverListAgentsFlags(cmd *cobra.Command) {
	cmd.Flags().StringVar(&driverListAgentsWorkspaceKey, "workspace-key", "", "Workspace key (default: LOOM_DRIVER_WORKSPACE or active workspace)")
	cmd.Flags().StringVar(&driverListAgentsDriverRunID, "driver-run-id", "", "Parent DriverRun ID (default: LOOM_DRIVER_RUN_ID)")
	cmd.Flags().StringVar(&driverListAgentsNodeID, "node-id", "", "Parent DriverRun node ID")
	cmd.Flags().StringVar(&driverListAgentsLeaseID, "lease-id", "", "Parent DriverRun lease ID")
	cmd.Flags().Int64Var(&driverListAgentsFence, "fencing-token", 0, "Parent DriverRun fencing token")
	cmd.Flags().BoolVar(&driverListAgentsJSON, "json", false, "JSON output")
}

func bindDriverAgentOrchestrationSessionFlags(cmd *cobra.Command) {
	cmd.Flags().StringVar(&driverAgentSessionWorkspaceKey, "workspace-key", "", "Workspace key (default: LOOM_DRIVER_WORKSPACE or active workspace)")
	cmd.Flags().StringVar(&driverAgentSessionDriverRunID, "driver-run-id", "", "Parent DriverRun ID (default: LOOM_DRIVER_RUN_ID)")
	cmd.Flags().StringVar(&driverAgentSessionNodeID, "node-id", "", "Parent DriverRun node ID")
	cmd.Flags().StringVar(&driverAgentSessionLeaseID, "lease-id", "", "Parent DriverRun lease ID")
	cmd.Flags().Int64Var(&driverAgentSessionFence, "fencing-token", 0, "Parent DriverRun fencing token")
	cmd.Flags().StringVar(&driverAgentSessionName, "agent", "", "Agent name")
	cmd.Flags().BoolVar(&driverAgentSessionJSON, "json", false, "JSON output")
	_ = cmd.MarkFlagRequired("agent")
}

func bindDriverUpdateAgentParentFlags(cmd *cobra.Command) {
	cmd.Flags().StringVar(&driverUpdateAgentParentWorkspaceKey, "workspace-key", "", "Workspace key (default: LOOM_DRIVER_WORKSPACE or active workspace)")
	cmd.Flags().StringVar(&driverUpdateAgentParentDriverRunID, "driver-run-id", "", "Parent DriverRun ID (default: LOOM_DRIVER_RUN_ID)")
	cmd.Flags().StringVar(&driverUpdateAgentParentNodeID, "node-id", "", "Parent DriverRun node ID")
	cmd.Flags().StringVar(&driverUpdateAgentParentLeaseID, "lease-id", "", "Parent DriverRun lease ID")
	cmd.Flags().Int64Var(&driverUpdateAgentParentFence, "fencing-token", 0, "Parent DriverRun fencing token")
	cmd.Flags().StringVar(&driverUpdateAgentParentName, "agent", "", "Agent name")
	cmd.Flags().StringVar(&driverUpdateAgentParentParent, "parent", "", "New parent epic ID")
	cmd.Flags().StringVar(&driverUpdateAgentParentExpectParent, "expect-parent", "", "Expected current parent before update")
	cmd.Flags().BoolVar(&driverUpdateAgentParentJSON, "json", false, "JSON output")
	_ = cmd.MarkFlagRequired("agent")
	_ = cmd.MarkFlagRequired("parent")
}

func bindDriverDeliverLeadAssignmentFlags(cmd *cobra.Command) {
	cmd.Flags().StringVar(&driverDeliverLeadWorkspaceKey, "workspace-key", "", "Workspace key (default: LOOM_DRIVER_WORKSPACE or active workspace)")
	cmd.Flags().StringVar(&driverDeliverLeadDriverRunID, "driver-run-id", "", "Parent DriverRun ID (default: LOOM_DRIVER_RUN_ID)")
	cmd.Flags().StringVar(&driverDeliverLeadNodeID, "node-id", "", "Parent DriverRun node ID")
	cmd.Flags().StringVar(&driverDeliverLeadLeaseID, "lease-id", "", "Parent DriverRun lease ID")
	cmd.Flags().Int64Var(&driverDeliverLeadFence, "fencing-token", 0, "Parent DriverRun fencing token")
	cmd.Flags().StringVar(&driverDeliverLeadName, "agent", "", "Lead agent name")
	cmd.Flags().BoolVar(&driverDeliverLeadJSON, "json", false, "JSON output")
	_ = cmd.MarkFlagRequired("agent")
}

func bindDriverDeliverAgentMessageFlags(cmd *cobra.Command) {
	cmd.Flags().StringVar(&driverDeliverAgentMessageWorkspaceKey, "workspace-key", "", "Workspace key (default: LOOM_DRIVER_WORKSPACE or active workspace)")
	cmd.Flags().StringVar(&driverDeliverAgentMessageDriverRunID, "driver-run-id", "", "Parent DriverRun ID (default: LOOM_DRIVER_RUN_ID)")
	cmd.Flags().StringVar(&driverDeliverAgentMessageNodeID, "node-id", "", "Parent DriverRun node ID")
	cmd.Flags().StringVar(&driverDeliverAgentMessageLeaseID, "lease-id", "", "Parent DriverRun lease ID")
	cmd.Flags().Int64Var(&driverDeliverAgentMessageFence, "fencing-token", 0, "Parent DriverRun fencing token")
	cmd.Flags().StringVar(&driverDeliverAgentMessageName, "agent", "", "Agent name")
	cmd.Flags().StringVar(&driverDeliverAgentMessageText, "message", "", "Message text to deliver")
	cmd.Flags().BoolVar(&driverDeliverAgentMessageJSON, "json", false, "JSON output")
	_ = cmd.MarkFlagRequired("agent")
	_ = cmd.MarkFlagRequired("message")
}

func runDriverListAgents(_ *cobra.Command, _ []string) error {
	return cmdstore.WithStore(func(ctx context.Context, h *bootstrap.StoreHandle) error {
		ws, _, err := resolveRunningDriverRun(ctx, h, driverListAgentsWorkspaceKey, driverListAgentsDriverRunID, driverListAgentsNodeID, driverListAgentsLeaseID, driverListAgentsFence)
		if err != nil {
			return err
		}
		agents, err := h.Store.Agents().List(ctx, ws)
		if err != nil {
			return fmt.Errorf("list agents: %w", err)
		}
		if driverListAgentsJSON {
			return cmdstore.WriteJSON(agents)
		}
		for _, agent := range agents {
			if agent == nil {
				continue
			}
			fmt.Printf("%s\t%s\t%s\n", agent.Name, agent.RoleName, agent.Parent)
		}
		return nil
	})
}

func runDriverAgentOrchestrationSession(_ *cobra.Command, _ []string) error {
	return cmdstore.WithStore(func(ctx context.Context, h *bootstrap.StoreHandle) error {
		ws, _, err := resolveRunningDriverRun(ctx, h, driverAgentSessionWorkspaceKey, driverAgentSessionDriverRunID, driverAgentSessionNodeID, driverAgentSessionLeaseID, driverAgentSessionFence)
		if err != nil {
			return err
		}
		agentName := strings.TrimSpace(driverAgentSessionName)
		if agentName == "" {
			return fmt.Errorf("agent required: %w", domain.ErrInvalid)
		}
		sessionID, err := store.OrchestrationSessionIDFor(ctx, h.Store, ws, agentName)
		if err != nil {
			return fmt.Errorf("resolve orchestration session: %w", err)
		}
		result := map[string]string{"agentName": agentName, "orchestratorSessionId": sessionID}
		if driverAgentSessionJSON {
			return cmdstore.WriteJSON(result)
		}
		fmt.Println(sessionID)
		return nil
	})
}

func runDriverUpdateAgentParent(_ *cobra.Command, _ []string) error {
	return cmdstore.WithStore(func(ctx context.Context, h *bootstrap.StoreHandle) error {
		ws, _, err := resolveRunningDriverRun(ctx, h, driverUpdateAgentParentWorkspaceKey, driverUpdateAgentParentDriverRunID, driverUpdateAgentParentNodeID, driverUpdateAgentParentLeaseID, driverUpdateAgentParentFence)
		if err != nil {
			return err
		}
		agentName := strings.TrimSpace(driverUpdateAgentParentName)
		parentID := strings.TrimSpace(driverUpdateAgentParentParent)
		if agentName == "" || parentID == "" {
			return fmt.Errorf("agent and parent required: %w", domain.ErrInvalid)
		}
		unlock, err := epicrunner.AcquireBindLock(ws, agentName)
		if err != nil {
			return fmt.Errorf("acquire agent parent lock: %w", err)
		}
		defer unlock()
		current, err := h.Store.Agents().Get(ctx, ws, agentName)
		if err != nil {
			return fmt.Errorf("get agent: %w", err)
		}
		if current.Parent != strings.TrimSpace(driverUpdateAgentParentExpectParent) {
			return fmt.Errorf("agent %q parent changed from %q to %q: %w", agentName, strings.TrimSpace(driverUpdateAgentParentExpectParent), current.Parent, domain.ErrConflict)
		}
		updated, err := h.Store.Agents().Update(ctx, ws, agentName, store.AgentUpdate{Parent: &parentID})
		if err != nil {
			return fmt.Errorf("update agent parent: %w", err)
		}
		if driverUpdateAgentParentJSON {
			return cmdstore.WriteJSON(updated)
		}
		fmt.Printf("Updated agent %s parent to %s\n", agentName, parentID)
		return nil
	})
}

func runDriverDeliverLeadAssignment(_ *cobra.Command, _ []string) error {
	return cmdstore.WithStore(func(ctx context.Context, h *bootstrap.StoreHandle) error {
		ws, _, err := resolveRunningDriverRun(ctx, h, driverDeliverLeadWorkspaceKey, driverDeliverLeadDriverRunID, driverDeliverLeadNodeID, driverDeliverLeadLeaseID, driverDeliverLeadFence)
		if err != nil {
			return err
		}
		leadName := strings.TrimSpace(driverDeliverLeadName)
		if leadName == "" {
			return fmt.Errorf("agent required: %w", domain.ErrInvalid)
		}
		delivery, err := leadcontrol.DeliverCurrentAssignment(ctx, h.Store, ws, leadName)
		if err != nil {
			return fmt.Errorf("deliver lead assignment: %w", err)
		}
		result := newLeadDeliveryResult(leadName, delivery)
		if driverDeliverLeadJSON {
			return cmdstore.WriteJSON(result)
		}
		if result.Reason != "" {
			fmt.Printf("Lead assignment delivery for %s: %s (%s)\n", leadName, result.State, result.Reason)
		} else {
			fmt.Printf("Lead assignment delivery for %s: %s\n", leadName, result.State)
		}
		return nil
	})
}

func runDriverDeliverAgentMessage(_ *cobra.Command, _ []string) error {
	return cmdstore.WithStore(func(ctx context.Context, h *bootstrap.StoreHandle) error {
		ws, parent, err := resolveRunningDriverRun(ctx, h, driverDeliverAgentMessageWorkspaceKey, driverDeliverAgentMessageDriverRunID, driverDeliverAgentMessageNodeID, driverDeliverAgentMessageLeaseID, driverDeliverAgentMessageFence)
		if err != nil {
			return err
		}
		agentName := strings.TrimSpace(driverDeliverAgentMessageName)
		message := strings.TrimSpace(driverDeliverAgentMessageText)
		if agentName == "" || message == "" {
			return fmt.Errorf("agent and message required: %w", domain.ErrInvalid)
		}
		result, err := deliverAgentMessageForDriver(ctx, h.Store, ws, parent.RunID, agentName, message)
		if err != nil {
			return fmt.Errorf("deliver agent message: %w", err)
		}
		if driverDeliverAgentMessageJSON {
			return cmdstore.WriteJSON(result)
		}
		if result.Reason != "" {
			fmt.Printf("Agent message delivery for %s: %s (%s)\n", agentName, result.State, result.Reason)
		} else {
			fmt.Printf("Agent message delivery for %s: %s\n", agentName, result.State)
		}
		return nil
	})
}

type agentMessageDeliveryResult = driverpkg.AgentMessageDeliveryResult

func deliverAgentMessageForDriver(ctx context.Context, st store.Store, workspace, driverRunID, agentName, message string) (agentMessageDeliveryResult, error) {
	return driverpkg.DeliverAgentMessageForDriver(ctx, st, workspace, driverRunID, agentName, message)
}

func newLeadDeliveryResult(agentName string, delivery *leadcontrol.DeliveryResult) agentMessageDeliveryResult {
	return driverpkg.NewAgentMessageDeliveryResult(agentName, delivery)
}
