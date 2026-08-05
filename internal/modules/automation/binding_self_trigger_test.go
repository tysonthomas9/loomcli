package automation

import (
	"errors"
	"strings"
	"testing"
)

func TestBindingDefinitionRequiresWorkflowExclusionForInternalIssueEvents(t *testing.T) {
	base := BindingDefinition{
		BindingID: "binding", SourceKind: SourceKindInternal, RouteKey: "internal:binding", DriverID: "driver-a",
	}
	tests := []struct {
		name      string
		mutate    func(*BindingDefinition)
		wantError bool
	}{
		{
			name: "exact issue route without filter",
			mutate: func(definition *BindingDefinition) {
				definition.RouteKey = "internal.issue.created"
			},
			wantError: true,
		},
		{
			name: "issue wildcard without filter",
			mutate: func(definition *BindingDefinition) {
				definition.EventTypePatterns = []string{"internal.issue.*"}
			},
			wantError: true,
		},
		{
			name: "broad wildcard without filter",
			mutate: func(definition *BindingDefinition) {
				definition.EventTypePatterns = []string{"*.*.*"}
			},
			wantError: true,
		},
		{
			name: "alternation including issue without filter",
			mutate: func(definition *BindingDefinition) {
				definition.EventTypePatterns = []string{"internal.{issue,task}.*"}
			},
			wantError: true,
		},
		{
			name: "allow list does not replace exclusion",
			mutate: func(definition *BindingDefinition) {
				definition.EventTypePatterns = []string{"internal.issue.created"}
				definition.ActorFilter = &ActorFilter{AllowActors: []string{"driver-run:trusted"}}
			},
			wantError: true,
		},
		{
			name: "workflow exclusion permits issue wildcard",
			mutate: func(definition *BindingDefinition) {
				definition.EventTypePatterns = []string{"internal.issue.*"}
				definition.ActorFilter = &ActorFilter{ExcludeActorKinds: []string{" WORKFLOW "}}
			},
		},
		{
			name: "non issue internal binding remains compatible",
			mutate: func(definition *BindingDefinition) {
				definition.EventTypePatterns = []string{"internal.task.ready"}
			},
		},
		{
			name: "external route remains compatible",
			mutate: func(definition *BindingDefinition) {
				definition.SourceKind = "github"
				definition.RouteKey = "github.issue.created"
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			definition := base
			test.mutate(&definition)
			prepared, err := prepareBindingDefinition(definition)
			if test.wantError {
				if !errors.Is(err, ErrInvalid) || !strings.Contains(err.Error(), "actor_filter.exclude_actor_kinds") {
					t.Fatalf("prepareBindingDefinition error = %v, want actor-filter validation", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("prepareBindingDefinition: %v", err)
			}
			if prepared.ActorFilter != nil && len(prepared.ActorFilter.ExcludeActorKinds) > 0 &&
				prepared.ActorFilter.ExcludeActorKinds[0] != "workflow" {
				t.Fatalf("normalized actor filter = %+v", prepared.ActorFilter)
			}
		})
	}
}

func TestBindingCommandsPreserveInternalIssueSelfTriggerInvariant(t *testing.T) {
	tests := []struct {
		name    string
		managed bool
	}{
		{name: "unmanaged"},
		{name: "managed", managed: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			h := newTestHarness(t)
			unsafe := BindingDefinition{
				BindingID: "unsafe", SourceKind: SourceKindInternal, RouteKey: "internal:unsafe",
				EventTypePatterns: []string{"internal.issue.created"}, DriverID: "driver-a", Enabled: true,
			}
			if test.managed {
				unsafe.TargetAgentServiceID = "agent-1"
				_, err := h.service.CreateManagedBinding(t.Context(), h.issueOperator(ActionCreateManagedBinding), CreateManagedBindingCommand{
					WorkspaceKey: "ws", AgentServiceID: "agent-1", Definition: unsafe,
				})
				assertUnsafeBindingError(t, err)
			} else {
				_, err := h.service.CreateBinding(t.Context(), h.issueOperator(ActionCreateBinding), CreateBindingCommand{
					WorkspaceKey: "ws", Definition: unsafe,
				})
				assertUnsafeBindingError(t, err)
			}
			if h.persistence.bindings[bindingMapKey("ws", unsafe.BindingID)] != nil {
				t.Fatal("unsafe create reached persistence")
			}

			safe := unsafe
			safe.BindingID = "safe"
			safe.RouteKey = "internal:safe"
			safe.ActorFilter = &ActorFilter{ExcludeActorKinds: []string{"workflow"}}
			var created *Binding
			var err error
			if test.managed {
				created, err = h.service.CreateManagedBinding(t.Context(), h.issueOperator(ActionCreateManagedBinding), CreateManagedBindingCommand{
					WorkspaceKey: "ws", AgentServiceID: "agent-1", Definition: safe,
				})
			} else {
				created, err = h.service.CreateBinding(t.Context(), h.issueOperator(ActionCreateBinding), CreateBindingCommand{
					WorkspaceKey: "ws", Definition: safe,
				})
			}
			if err != nil {
				t.Fatalf("create safe binding: %v", err)
			}

			if test.managed {
				_, err = h.service.UpdateManagedBinding(t.Context(), h.issueOperator(ActionUpdateManagedBinding), UpdateManagedBindingCommand{
					WorkspaceKey: "ws", BindingID: created.BindingID, AgentServiceID: "agent-1",
					Patch: BindingPatch{ClearActorFilter: true},
				})
			} else {
				_, err = h.service.UpdateBinding(t.Context(), h.issueOperator(ActionUpdateBinding), UpdateBindingCommand{
					WorkspaceKey: "ws", BindingID: created.BindingID, Patch: BindingPatch{ClearActorFilter: true},
				})
			}
			assertUnsafeBindingError(t, err)
			persisted := h.persistence.bindings[bindingMapKey("ws", created.BindingID)]
			if persisted == nil || !excludesWorkflowActor(persisted.ActorFilter) {
				t.Fatalf("rejected update changed persisted filter: %+v", persisted)
			}

			name := "safe rename"
			if test.managed {
				created, err = h.service.UpdateManagedBinding(t.Context(), h.issueOperator(ActionUpdateManagedBinding), UpdateManagedBindingCommand{
					WorkspaceKey: "ws", BindingID: created.BindingID, AgentServiceID: "agent-1", Patch: BindingPatch{Name: &name},
				})
			} else {
				created, err = h.service.UpdateBinding(t.Context(), h.issueOperator(ActionUpdateBinding), UpdateBindingCommand{
					WorkspaceKey: "ws", BindingID: created.BindingID, Patch: BindingPatch{Name: &name},
				})
			}
			if err != nil || created.Name != name || !excludesWorkflowActor(created.ActorFilter) {
				t.Fatalf("safe update = %+v, %v", created, err)
			}
		})
	}
}

func assertUnsafeBindingError(t *testing.T, err error) {
	t.Helper()
	if !errors.Is(err, ErrInvalid) || !strings.Contains(err.Error(), "actor_filter.exclude_actor_kinds") {
		t.Fatalf("error = %v, want internal issue-event actor-filter validation", err)
	}
}
