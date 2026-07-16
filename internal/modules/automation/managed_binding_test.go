package automation

import (
	"errors"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/platform/authority"
)

func TestManagedBindingCommandsRequireExactOwnerAndDedicatedAuthority(t *testing.T) {
	h := newTestHarness(t)
	definition := BindingDefinition{
		BindingID: "managed-agent-1", Name: "agent one", SourceKind: SourceKindInternal,
		RouteKey: "internal:managed-agent-1", DriverID: "driver-a",
		TargetAgentServiceID: "agent-1", Enabled: true,
	}

	if _, err := h.service.CreateManagedBinding(t.Context(), h.issueOperator(ActionCreateBinding), CreateManagedBindingCommand{
		WorkspaceKey: "ws", AgentServiceID: "agent-1", Definition: definition,
	}); !errors.Is(err, authority.ErrAdmissionDenied) {
		t.Fatalf("ordinary action error = %v, want admission denial", err)
	}
	if h.persistence.bindings[bindingMapKey("ws", definition.BindingID)] != nil {
		t.Fatal("wrong-action managed create reached persistence")
	}

	forged := definition
	forged.BindingID = "forged"
	forged.RouteKey = "internal:forged"
	forged.TargetAgentServiceID = "agent-2"
	if _, err := h.service.CreateManagedBinding(t.Context(), h.issueOperator(ActionCreateManagedBinding), CreateManagedBindingCommand{
		WorkspaceKey: "ws", AgentServiceID: "agent-1", Definition: forged,
	}); !errors.Is(err, ErrManagedBinding) {
		t.Fatalf("forged owner create error = %v, want %v", err, ErrManagedBinding)
	}
	if h.persistence.bindings[bindingMapKey("ws", forged.BindingID)] != nil {
		t.Fatal("forged owner managed create reached persistence")
	}

	created, err := h.service.CreateManagedBinding(t.Context(), h.issueOperator(ActionCreateManagedBinding), CreateManagedBindingCommand{
		WorkspaceKey: "ws", AgentServiceID: "agent-1", Definition: definition,
	})
	if err != nil {
		t.Fatalf("CreateManagedBinding: %v", err)
	}
	if created.TargetAgentServiceID != "agent-1" || !created.Enabled {
		t.Fatalf("created = %+v", created)
	}

	name := "renamed"
	if _, err := h.service.UpdateManagedBinding(t.Context(), h.issueOperator(ActionUpdateManagedBinding), UpdateManagedBindingCommand{
		WorkspaceKey: "ws", BindingID: created.BindingID, AgentServiceID: "agent-2", Patch: BindingPatch{Name: &name},
	}); !errors.Is(err, ErrManagedBinding) {
		t.Fatalf("forged owner update error = %v, want %v", err, ErrManagedBinding)
	}
	forgedTarget := "agent-2"
	if _, err := h.service.UpdateManagedBinding(t.Context(), h.issueOperator(ActionUpdateManagedBinding), UpdateManagedBindingCommand{
		WorkspaceKey: "ws", BindingID: created.BindingID, AgentServiceID: "agent-1",
		Patch: BindingPatch{TargetAgentServiceID: &forgedTarget},
	}); !errors.Is(err, ErrManagedBinding) {
		t.Fatalf("owner-transfer update error = %v, want %v", err, ErrManagedBinding)
	}
	updated, err := h.service.UpdateManagedBinding(t.Context(), h.issueOperator(ActionUpdateManagedBinding), UpdateManagedBindingCommand{
		WorkspaceKey: "ws", BindingID: created.BindingID, AgentServiceID: "agent-1", Patch: BindingPatch{Name: &name},
	})
	if err != nil || updated.Name != name || updated.TargetAgentServiceID != "agent-1" {
		t.Fatalf("UpdateManagedBinding = %+v, %v", updated, err)
	}

	ordinary := BindingCommand{WorkspaceKey: "ws", BindingID: created.BindingID}
	if _, err := h.service.UpdateBinding(t.Context(), h.issueOperator(ActionUpdateBinding), UpdateBindingCommand{
		WorkspaceKey: "ws", BindingID: created.BindingID, Patch: BindingPatch{Name: &name},
	}); !errors.Is(err, ErrManagedBinding) {
		t.Fatalf("ordinary update error = %v, want %v", err, ErrManagedBinding)
	}
	if _, err := h.service.EnableBinding(t.Context(), h.issueOperator(ActionEnableBinding), ordinary); !errors.Is(err, ErrManagedBinding) {
		t.Fatalf("ordinary enable error = %v, want %v", err, ErrManagedBinding)
	}
	if _, err := h.service.DisableBinding(t.Context(), h.issueOperator(ActionDisableBinding), ordinary); !errors.Is(err, ErrManagedBinding) {
		t.Fatalf("ordinary disable error = %v, want %v", err, ErrManagedBinding)
	}
	if err := h.service.DeleteBinding(t.Context(), h.issueOperator(ActionDeleteBinding), ordinary); !errors.Is(err, ErrManagedBinding) {
		t.Fatalf("ordinary delete error = %v, want %v", err, ErrManagedBinding)
	}

	managed := ManagedBindingCommand{WorkspaceKey: "ws", BindingID: created.BindingID, AgentServiceID: "agent-1"}
	if _, err := h.service.DisableManagedBinding(t.Context(), h.issueOperator(ActionDisableManagedBinding), ManagedBindingCommand{
		WorkspaceKey: "ws", BindingID: created.BindingID, AgentServiceID: "agent-2",
	}); !errors.Is(err, ErrManagedBinding) {
		t.Fatalf("forged owner disable error = %v, want %v", err, ErrManagedBinding)
	}
	disabled, err := h.service.DisableManagedBinding(t.Context(), h.issueOperator(ActionDisableManagedBinding), managed)
	if err != nil || disabled.Enabled {
		t.Fatalf("DisableManagedBinding = %+v, %v", disabled, err)
	}
	if err := h.service.DeleteManagedBinding(t.Context(), h.issueOperator(ActionDeleteManagedBinding), ManagedBindingCommand{
		WorkspaceKey: "ws", BindingID: created.BindingID, AgentServiceID: "agent-2",
	}); !errors.Is(err, ErrManagedBinding) {
		t.Fatalf("forged owner delete error = %v, want %v", err, ErrManagedBinding)
	}
	if err := h.service.DeleteManagedBinding(t.Context(), h.issueOperator(ActionDeleteManagedBinding), managed); err != nil {
		t.Fatalf("DeleteManagedBinding: %v", err)
	}
	if h.persistence.bindings[bindingMapKey("ws", created.BindingID)] != nil {
		t.Fatal("managed binding still exists after delete")
	}
}

func TestManagedBindingConditionalMutationRejectsConcurrentOwnerRevisionAndABA(t *testing.T) {
	create := func(t *testing.T, enabled bool) (*testHarness, *Binding) {
		t.Helper()
		h := newTestHarness(t)
		created, err := h.service.CreateManagedBinding(t.Context(), h.issueOperator(ActionCreateManagedBinding), CreateManagedBindingCommand{
			WorkspaceKey: "ws", AgentServiceID: "agent-1",
			Definition: BindingDefinition{
				BindingID: "managed-agent-1", Name: "agent one", SourceKind: SourceKindInternal,
				RouteKey: "internal:managed-agent-1", DriverID: "driver-a",
				TargetAgentServiceID: "agent-1", Enabled: enabled,
			},
		})
		if err != nil {
			t.Fatalf("CreateManagedBinding: %v", err)
		}
		return h, created
	}

	t.Run("concurrent owner update", func(t *testing.T) {
		h, created := create(t, true)
		h.persistence.managedMutationHook = func(p *fakePersistence) {
			p.mu.Lock()
			defer p.mu.Unlock()
			current := p.bindings[bindingMapKey("ws", created.BindingID)]
			current.Name = "concurrent owner"
			current.TargetAgentServiceID = "agent-2"
			current.UpdatedAt = current.UpdatedAt.Add(time.Microsecond)
		}
		name := "stale rename"
		_, err := h.service.UpdateManagedBinding(t.Context(), h.issueOperator(ActionUpdateManagedBinding), UpdateManagedBindingCommand{
			WorkspaceKey: "ws", BindingID: created.BindingID, AgentServiceID: "agent-1", Patch: BindingPatch{Name: &name},
		})
		if !errors.Is(err, ErrManagedBinding) {
			t.Fatalf("UpdateManagedBinding error = %v, want %v", err, ErrManagedBinding)
		}
		current := h.persistence.bindings[bindingMapKey("ws", created.BindingID)]
		if current.Name != "concurrent owner" || current.TargetAgentServiceID != "agent-2" {
			t.Fatalf("stale command retargeted concurrent row: %+v", current)
		}
	})

	t.Run("delete recreate ABA", func(t *testing.T) {
		h, created := create(t, true)
		h.persistence.managedMutationHook = func(p *fakePersistence) {
			p.mu.Lock()
			defer p.mu.Unlock()
			key := bindingMapKey("ws", created.BindingID)
			recreated := cloneBinding(p.bindings[key])
			recreated.Name = "recreated generation"
			recreated.CreatedAt = recreated.CreatedAt.Add(time.Second)
			recreated.UpdatedAt = recreated.UpdatedAt.Add(time.Second)
			p.bindings[key] = recreated
		}
		_, err := h.service.DisableManagedBinding(t.Context(), h.issueOperator(ActionDisableManagedBinding), ManagedBindingCommand{
			WorkspaceKey: "ws", BindingID: created.BindingID, AgentServiceID: "agent-1",
		})
		if !errors.Is(err, ErrManagedBinding) {
			t.Fatalf("DisableManagedBinding error = %v, want %v", err, ErrManagedBinding)
		}
		current := h.persistence.bindings[bindingMapKey("ws", created.BindingID)]
		if current.Name != "recreated generation" || !current.Enabled {
			t.Fatalf("stale command mutated recreated row: %+v", current)
		}
	})

	t.Run("delete rechecks disabled atomically", func(t *testing.T) {
		h, created := create(t, false)
		h.persistence.managedMutationHook = func(p *fakePersistence) {
			p.mu.Lock()
			defer p.mu.Unlock()
			current := p.bindings[bindingMapKey("ws", created.BindingID)]
			current.Enabled = true
			current.UpdatedAt = current.UpdatedAt.Add(time.Microsecond)
		}
		err := h.service.DeleteManagedBinding(t.Context(), h.issueOperator(ActionDeleteManagedBinding), ManagedBindingCommand{
			WorkspaceKey: "ws", BindingID: created.BindingID, AgentServiceID: "agent-1",
		})
		if !errors.Is(err, ErrManagedBinding) {
			t.Fatalf("DeleteManagedBinding error = %v, want %v", err, ErrManagedBinding)
		}
		if current := h.persistence.bindings[bindingMapKey("ws", created.BindingID)]; current == nil || !current.Enabled {
			t.Fatalf("concurrently enabled row was deleted: %+v", current)
		}
	})

	t.Run("same-state enable still consumes a conditional revision", func(t *testing.T) {
		h, created := create(t, true)
		enabled, err := h.service.EnableManagedBinding(t.Context(), h.issueOperator(ActionEnableManagedBinding), ManagedBindingCommand{
			WorkspaceKey: "ws", BindingID: created.BindingID, AgentServiceID: "agent-1",
		})
		if err != nil {
			t.Fatalf("EnableManagedBinding: %v", err)
		}
		if h.persistence.managedReplaceCalls != 1 || !enabled.UpdatedAt.After(created.UpdatedAt) {
			t.Fatalf("conditional calls/timestamp = %d / %s -> %s", h.persistence.managedReplaceCalls, created.UpdatedAt, enabled.UpdatedAt)
		}
	})
}

func TestDeleteManagedBindingReconcilesUncertainOutcome(t *testing.T) {
	createDisabled := func(t *testing.T) (*testHarness, *Binding) {
		t.Helper()
		h := newTestHarness(t)
		created, err := h.service.CreateManagedBinding(t.Context(), h.issueOperator(ActionCreateManagedBinding), CreateManagedBindingCommand{
			WorkspaceKey: "ws", AgentServiceID: "agent-1",
			Definition: BindingDefinition{
				BindingID: "managed-agent-1", Name: "agent one", SourceKind: SourceKindInternal,
				RouteKey: "internal:managed-agent-1", DriverID: "driver-a",
				TargetAgentServiceID: "agent-1", Enabled: false,
			},
		})
		if err != nil {
			t.Fatalf("CreateManagedBinding: %v", err)
		}
		return h, created
	}
	command := ManagedBindingCommand{WorkspaceKey: "ws", BindingID: "managed-agent-1", AgentServiceID: "agent-1"}

	t.Run("initial absence is idempotent success", func(t *testing.T) {
		h := newTestHarness(t)
		if err := h.service.DeleteManagedBinding(t.Context(), h.issueOperator(ActionDeleteManagedBinding), command); err != nil {
			t.Fatalf("DeleteManagedBinding: %v", err)
		}
		if h.persistence.managedDeleteCalls != 0 {
			t.Fatalf("conditional delete calls = %d, want 0", h.persistence.managedDeleteCalls)
		}
	})

	t.Run("committed delete with lost response is success", func(t *testing.T) {
		h, _ := createDisabled(t)
		h.persistence.managedDeleteErr = errors.New("response lost")
		h.persistence.managedDeleteCommitOnErr = true
		if err := h.service.DeleteManagedBinding(t.Context(), h.issueOperator(ActionDeleteManagedBinding), command); err != nil {
			t.Fatalf("DeleteManagedBinding: %v", err)
		}
		if current := h.persistence.bindings[bindingMapKey("ws", command.BindingID)]; current != nil {
			t.Fatalf("committed binding still exists: %+v", current)
		}
	})

	t.Run("same snapshot preserves transport failure", func(t *testing.T) {
		h, created := createDisabled(t)
		transportErr := errors.New("storage unavailable")
		h.persistence.managedDeleteErr = transportErr
		err := h.service.DeleteManagedBinding(t.Context(), h.issueOperator(ActionDeleteManagedBinding), command)
		if !errors.Is(err, transportErr) {
			t.Fatalf("DeleteManagedBinding error = %v, want %v", err, transportErr)
		}
		current := h.persistence.bindings[bindingMapKey("ws", command.BindingID)]
		if !managedBindingMatchesSnapshot(current, managedBindingSnapshot(created, command.AgentServiceID)) {
			t.Fatalf("unchanged binding = %+v, want original %+v", current, created)
		}
	})

	t.Run("recreated generation after lost response is managed conflict", func(t *testing.T) {
		h, created := createDisabled(t)
		recreated := cloneBinding(created)
		recreated.Name = "recreated generation"
		recreated.CreatedAt = recreated.CreatedAt.Add(time.Second)
		recreated.UpdatedAt = recreated.UpdatedAt.Add(time.Second)
		h.persistence.managedDeleteErr = errors.New("response lost")
		h.persistence.managedDeleteCommitOnErr = true
		h.persistence.managedDeleteRecreate = recreated
		err := h.service.DeleteManagedBinding(t.Context(), h.issueOperator(ActionDeleteManagedBinding), command)
		if !errors.Is(err, ErrManagedBinding) {
			t.Fatalf("DeleteManagedBinding error = %v, want %v", err, ErrManagedBinding)
		}
		current := h.persistence.bindings[bindingMapKey("ws", command.BindingID)]
		if current == nil || current.Name != recreated.Name || !current.CreatedAt.Equal(recreated.CreatedAt) {
			t.Fatalf("recreated binding was not preserved: %+v", current)
		}
	})
}
