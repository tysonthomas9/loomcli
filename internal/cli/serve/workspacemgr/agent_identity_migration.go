package workspacemgr

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"github.com/tysonthomas9/loomcli/internal/domain"
	storepkg "github.com/tysonthomas9/loomcli/internal/store"
	workflowdefs "github.com/tysonthomas9/loomcli/internal/workflows"
)

func EnsurePromptAgentIdentityRecords(ctx context.Context, s storepkg.Store) error {
	if s == nil {
		return fmt.Errorf("store is required: %w", domain.ErrInvalid)
	}
	workspaces, err := s.Workspaces().List(ctx)
	if err != nil {
		return fmt.Errorf("list workspaces for agent identity backfill: %w", err)
	}
	for _, ws := range workspaces {
		if ws == nil || strings.TrimSpace(ws.Key) == "" {
			continue
		}
		if err := ensurePromptAgentIdentityRecordsForWorkspace(ctx, s, ws.Key); err != nil {
			return err
		}
	}
	return nil
}

func ensurePromptAgentIdentityRecordsForWorkspace(ctx context.Context, s storepkg.Store, ws string) error {
	agentNames, err := existingAgentNames(ctx, s, ws)
	if err != nil {
		return err
	}
	bindings, err := s.TriggerBindings().List(ctx, ws, storepkg.TriggerBindingFilter{
		DriverID: workflowdefs.BuiltinPromptAgentWorkflowName,
	})
	if err != nil {
		return fmt.Errorf("list prompt-agent bindings in workspace %q: %w", ws, err)
	}
	for _, binding := range bindings {
		if binding == nil {
			continue
		}
		if target := strings.TrimSpace(binding.TargetAgentServiceID); target != "" {
			// Already attached: reconcile the binding's enabled flag to the
			// record's desired_state (§7 crash-recovery mitigation). The
			// enable/disable fan-out writes the record FIRST, so a crash
			// mid-fan-out leaves record=paused with bindings still enabled —
			// and the attached-binding 409 guard blocks repairing that through
			// the binding route. This loop is the reconciler that heals it at
			// the next serve start. Best-effort per binding.
			reconcilePromptAgentBindingEnabled(ctx, s, ws, target, binding)
			continue
		}
		roleName, ok := promptAgentBindingRoleName(binding.SourceConfigRef)
		if !ok {
			slog.Warn("skip prompt-agent identity backfill; binding has no roleName",
				"workspace", ws, "binding_id", binding.BindingID)
			continue
		}
		serviceID := migratedAgentServiceID(binding.BindingID, agentNames)
		if err := ensurePromptAgentRecordForBinding(ctx, s, ws, serviceID, roleName, binding); err != nil {
			return err
		}
		target := serviceID
		if _, err := s.TriggerBindings().Update(ctx, ws, binding.BindingID, storepkg.TriggerBindingUpdate{
			TargetAgentServiceID: &target,
		}); err != nil {
			return fmt.Errorf("patch binding %q target agent service: %w", binding.BindingID, err)
		}
	}
	return nil
}

// reconcilePromptAgentBindingEnabled aligns one attached binding's enabled
// flag with its agent record's desired_state (running ⇔ enabled). A missing or
// archived record leaves the binding untouched — deletion/archival flows own
// those states. Errors are logged, never fatal: this is a self-healing sweep,
// not a gate on serve start.
func reconcilePromptAgentBindingEnabled(ctx context.Context, s storepkg.Store, ws, serviceID string, binding *domain.TriggerBinding) {
	record, err := s.AgentServices().Get(ctx, ws, serviceID)
	if err != nil || record == nil {
		if err != nil && !errors.Is(err, domain.ErrNotFound) {
			slog.Warn("agent identity reconcile: get record failed",
				"workspace", ws, "service_id", serviceID, "binding_id", binding.BindingID, "err", err)
		}
		return
	}
	if record.DesiredState == domain.AgentServiceDesiredStopped {
		// Archived/stopped records are the delete flow's business.
		return
	}
	wantEnabled := record.DesiredState == domain.AgentServiceDesiredRunning
	if binding.Enabled == wantEnabled {
		return
	}
	if _, err := s.TriggerBindings().Update(ctx, ws, binding.BindingID, storepkg.TriggerBindingUpdate{
		Enabled: &wantEnabled,
	}); err != nil {
		slog.Warn("agent identity reconcile: patch binding enabled failed",
			"workspace", ws, "binding_id", binding.BindingID, "want_enabled", wantEnabled, "err", err)
		return
	}
	slog.Info("agent identity reconcile: aligned binding enabled to record desired_state",
		"workspace", ws, "binding_id", binding.BindingID, "service_id", serviceID, "enabled", wantEnabled)
}

func existingAgentNames(ctx context.Context, s storepkg.Store, ws string) (map[string]struct{}, error) {
	agents, err := s.Agents().List(ctx, ws)
	if err != nil {
		return nil, fmt.Errorf("list supervised agents in workspace %q: %w", ws, err)
	}
	names := make(map[string]struct{}, len(agents))
	for _, agent := range agents {
		if agent == nil {
			continue
		}
		if name := strings.TrimSpace(agent.Name); name != "" {
			names[name] = struct{}{}
		}
	}
	return names, nil
}

func ensurePromptAgentRecordForBinding(ctx context.Context, s storepkg.Store, ws, serviceID, roleName string, binding *domain.TriggerBinding) error {
	if existing, err := s.AgentServices().Get(ctx, ws, serviceID); err == nil && existing != nil {
		return nil
	} else if err != nil && !errors.Is(err, domain.ErrNotFound) {
		return fmt.Errorf("get agent service %q in workspace %q: %w", serviceID, ws, err)
	}
	desired := domain.AgentServiceDesiredPaused
	if binding.Enabled {
		desired = domain.AgentServiceDesiredRunning
	}
	if _, err := s.AgentServices().Create(ctx, storepkg.AgentServiceCreate{
		WorkspaceKey: ws,
		ServiceID:    serviceID,
		Name:         firstNonEmpty(strings.TrimSpace(binding.Name), binding.BindingID),
		Kind:         agentServiceKindForBinding(binding),
		DesiredState: desired,
		RoleName:     roleName,
	}); err != nil {
		if errors.Is(err, domain.ErrAlreadyExists) {
			return nil
		}
		return fmt.Errorf("create agent service %q in workspace %q: %w", serviceID, ws, err)
	}
	return nil
}

func migratedAgentServiceID(seed string, agentNames map[string]struct{}) string {
	id := strings.TrimSpace(seed)
	if id == "" {
		id = "agent"
	}
	for {
		if _, conflict := agentNames[id]; !conflict {
			return id
		}
		id = "agt-" + id
	}
}

func agentServiceKindForBinding(binding *domain.TriggerBinding) domain.AgentServiceKind {
	if binding != nil && binding.SourceKind == storepkg.CronSourceKind {
		return domain.AgentServiceKindCron
	}
	return domain.AgentServiceKindEvent
}

func promptAgentBindingRoleName(sourceConfigRef string) (string, bool) {
	var obj map[string]json.RawMessage
	if err := json.Unmarshal([]byte(strings.TrimSpace(sourceConfigRef)), &obj); err != nil {
		return "", false
	}
	for _, key := range []string{"roleName", "role_name"} {
		raw, ok := obj[key]
		if !ok {
			continue
		}
		var roleName string
		if err := json.Unmarshal(raw, &roleName); err != nil {
			return "", false
		}
		if roleName = strings.TrimSpace(roleName); roleName != "" {
			return roleName, true
		}
	}
	return "", false
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}
