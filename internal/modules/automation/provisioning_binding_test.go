package automation

import (
	"errors"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/platform/authority"
)

func TestEnsureManagedBindingCreatesExactIntentAndReplaysAfterCatalogDrift(t *testing.T) {
	h := newTestHarness(t)
	command := EnsureManagedBindingCommand{
		RequestID: "provision-1:binding", WorkspaceKey: "ws", AgentServiceID: "agent-1",
		Definition: BindingDefinition{
			BindingID: "docs-review-1", Name: "Docs review",
			SourceKind: SourceKindInternal, SourceConfigRef: "role://docs?backend=codex",
			RouteKey:          "internal:docs-review-1",
			EventTypePatterns: []string{"internal.task.review"},
			DriverID:          "driver-a", DriverVersionID: "version-active",
			TargetEntrypoint: "run", TargetAgentServiceID: "agent-1",
			ConcurrencyPolicy: ConcurrencyOneActivePerEpic, Enabled: true,
		},
	}
	auth := h.issueSystem(ActionEnsureManagedBinding)
	first, err := h.service.EnsureManagedBinding(t.Context(), auth, command)
	if err != nil {
		t.Fatal(err)
	}
	if first.DriverVersionID != "version-active" ||
		first.TargetAgentServiceID != "agent-1" ||
		first.RetryMaxAttempts != DefaultRetryMaxAttempts ||
		first.RetryBackoffSeconds != DefaultRetryBackoffSeconds {
		t.Fatalf("first = %+v", first)
	}

	// Replay is tied to the persisted immutable definition, not whichever
	// version became active after the external binding commit.
	h.catalog.values["driver-a"] = effectiveVersion("ws", "driver-a", "version-new")
	replayed, err := h.service.EnsureManagedBinding(t.Context(), auth, command)
	if err != nil {
		t.Fatal(err)
	}
	if replayed.DriverVersionID != "version-active" ||
		!replayed.CreatedAt.Equal(first.CreatedAt) {
		t.Fatalf("replay = %+v, first = %+v", replayed, first)
	}

	divergent := command
	divergent.Definition.Name = "Different binding authority"
	if _, err := h.service.EnsureManagedBinding(t.Context(), auth, divergent); !errors.Is(err, ErrConflict) {
		t.Fatalf("divergent replay = %v, want ErrConflict", err)
	}
}

func TestEnsureManagedBindingFailsClosedWhenPinnedVersionIsNoLongerEffective(t *testing.T) {
	h := newTestHarness(t)
	h.catalog.values["driver-a"] = effectiveVersion("ws", "driver-a", "version-new")
	command := EnsureManagedBindingCommand{
		RequestID: "provision-2:binding", WorkspaceKey: "ws", AgentServiceID: "agent-2",
		Definition: BindingDefinition{
			BindingID: "docs-review-2", SourceKind: SourceKindInternal,
			RouteKey: "internal:docs-review-2",
			DriverID: "driver-a", DriverVersionID: "version-old",
			TargetEntrypoint: "run", TargetAgentServiceID: "agent-2",
		},
	}
	if _, err := h.service.EnsureManagedBinding(
		t.Context(),
		h.issueSystem(ActionEnsureManagedBinding),
		command,
	); !errors.Is(err, ErrConflict) {
		t.Fatalf("version drift = %v, want ErrConflict", err)
	}
	if h.persistence.bindings[bindingMapKey("ws", command.Definition.BindingID)] != nil {
		t.Fatal("version-drifted provisioning intent reached persistence")
	}
}

func TestEnsureManagedBindingRequiresDedicatedSystemAuthority(t *testing.T) {
	h := newTestHarness(t)
	command := EnsureManagedBindingCommand{
		RequestID: "provision-1:binding", WorkspaceKey: "ws", AgentServiceID: "agent-1",
		Definition: BindingDefinition{
			BindingID: "docs-review-1", SourceKind: SourceKindInternal,
			RouteKey: "internal:docs-review-1", DriverID: "driver-a",
			DriverVersionID: "version-active", TargetAgentServiceID: "agent-1",
		},
	}
	if _, err := h.service.EnsureManagedBinding(
		t.Context(),
		h.issueSystem(ActionSweepCron),
		command,
	); !errors.Is(err, authority.ErrAdmissionDenied) {
		t.Fatalf("wrong action = %v, want admission denied", err)
	}
	if h.persistence.bindings[bindingMapKey("ws", command.Definition.BindingID)] != nil {
		t.Fatal("wrong-action ensure reached persistence")
	}
}
