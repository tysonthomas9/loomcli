package driverapi

import (
	"context"
	"fmt"
	"net/http"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/store"
)

// bindConfigHeaders creates a binding carrying run-input plus a driver run
// stamped with it, claims the run, and returns owner headers for that run.
func (h *testHarness) bindConfigHeaders(t *testing.T, bindingID, sourceConfigRef, runID string) map[string]string {
	t.Helper()
	ctx := context.Background()
	if _, err := h.store.TriggerBindings().Create(ctx, store.TriggerBindingCreate{
		WorkspaceKey:    "WS",
		BindingID:       bindingID,
		Name:            bindingID,
		SourceKind:      "internal",
		RouteKey:        "internal.task.ready." + bindingID,
		DriverID:        "driver-1",
		DriverVersionID: "version-1",
		Enabled:         true,
		SourceConfigRef: sourceConfigRef,
	}); err != nil {
		t.Fatalf("create binding %q: %v", bindingID, err)
	}
	if _, err := h.store.DriverRuns().Create(ctx, store.DriverRunCreate{
		WorkspaceKey:     "WS",
		RunID:            runID,
		DriverID:         "driver-1",
		DriverVersionID:  "version-1",
		TriggerBindingID: bindingID,
	}); err != nil {
		t.Fatalf("create stamped run %q: %v", runID, err)
	}
	claimed, err := h.store.DriverRuns().Claim(ctx, "WS", runID, "node-"+runID, "lease-"+runID)
	if err != nil {
		t.Fatalf("claim run %q: %v", runID, err)
	}
	return map[string]string{
		HeaderDriverRunID:        claimed.RunID,
		HeaderDriverNodeID:       claimed.NodeID,
		HeaderDriverLeaseID:      claimed.LeaseID,
		HeaderDriverFencingToken: fmt.Sprintf("%d", claimed.FencingToken),
	}
}

// A run stamped with its binding resolves that binding's config by reference,
// and a body-supplied binding id is IGNORED (server derives it from provenance).
func TestBindingConfigResolvesFromStampedProvenance(t *testing.T) {
	h := newTestHarness(t, "")
	// A DIFFERENT binding the caller will try to smuggle via the request body.
	// If the body were honored, the response would carry "evil-role".
	if _, err := h.store.TriggerBindings().Create(context.Background(), store.TriggerBindingCreate{
		WorkspaceKey: "WS", BindingID: "attacker-binding", Name: "attacker",
		SourceKind: "internal", RouteKey: "internal.attacker",
		DriverID: "driver-1", DriverVersionID: "version-1", Enabled: true,
		SourceConfigRef: `{"roleName":"evil-role"}`,
	}); err != nil {
		t.Fatalf("create attacker binding: %v", err)
	}
	headers := h.bindConfigHeaders(t, "prompt-agent-onready",
		`{"roleName":"docs-assistant","backend":"codex"}`, "run-bound")

	resp, decoded := h.do(t, opRequest{
		op:      "binding-config",
		headers: headers,
		body:    map[string]any{"bindingId": "attacker-binding"}, // must be ignored
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %v)", resp.StatusCode, decoded)
	}
	if decoded["roleName"] != "docs-assistant" {
		t.Fatalf("roleName = %v, want docs-assistant (body bindingId must be ignored)", decoded["roleName"])
	}
	if decoded["backend"] != "codex" {
		t.Fatalf("backend = %v, want codex", decoded["backend"])
	}
	if decoded["bindingId"] != "prompt-agent-onready" {
		t.Fatalf("bindingId = %v, want prompt-agent-onready", decoded["bindingId"])
	}
	if decoded["sourceKind"] != "internal" {
		t.Fatalf("sourceKind = %v, want internal", decoded["sourceKind"])
	}
}

// A run with no binding lineage gets a clean not_found — NOT the connector
// path's grant_denied (reading a nonexistent config is not an escalation).
func TestBindingConfigNoBindingNotFound(t *testing.T) {
	h := newTestHarness(t, "") // run-1 carries no binding, no source ref
	resp, decoded := h.do(t, opRequest{op: "binding-config", headers: h.ownerHeaders()})
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 (body %v)", resp.StatusCode, decoded)
	}
	if code := errorCode(t, decoded); code != "not_found" {
		t.Fatalf("error code = %q, want not_found", code)
	}
}

// binding-config authenticates like every other op: no run headers → 401.
func TestBindingConfigRequiresRunOwnership(t *testing.T) {
	h := newTestHarness(t, "")
	resp, decoded := h.do(t, opRequest{op: "binding-config"})
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", resp.StatusCode)
	}
	if code := errorCode(t, decoded); code != "unauthenticated" {
		t.Fatalf("error code = %q, want unauthenticated", code)
	}
}
