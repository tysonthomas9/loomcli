// Config-by-reference on the run-scoped driver-op surface (unified-agent
// proposal, "Config-by-reference (2026-07-02, Tyson)").
//
// A trigger binding's per-binding config (a prompt agent's roleName/backend)
// used to travel BY VALUE — copied into every dispatched run's payload by each
// dispatch source, which is why the merge sites multiplied and the internal
// task.ready lane (which delivers only event data) went red. The binding-config
// op flips that to BY REFERENCE: the run reads its own binding's config at
// start, resolving the binding from the CALLING run's VERIFIED provenance —
// exactly the server-side derivation the connector-egress and actor-lock
// security fixes use. It is the source_config_ref wedge's ONE reader.
package driverapi

import (
	"context"
	"fmt"

	trigger "github.com/tysonthomas9/loomcli/internal/infra/automationruntime"
)

// bindingConfig is the "binding-config" op handler. Flow: verify the caller
// owns a running parent DriverRun, resolve that run's trigger binding from
// server-side provenance (lookupParentBindingID — direct TriggerBindingID first,
// then the delivery/route-key lineage the connector path uses), then return the
// binding's parsed run-input object plus binding/source/schedule context.
//
// SECURITY: the binding id is NEVER read from the request body. Accepting a
// caller-supplied binding id would reopen the confused-deputy class the actor
// derivation fix closed — a run could read another binding's config (and, since
// the same run-input carries a role prompt, wear a role it was never bound to).
// The request body is therefore ignored entirely.
func (m *Module) bindingConfig(ctx context.Context, ws string, id driverIdentity, _ []byte) (any, error) {
	parent, err := m.verifyParent(ctx, ws, id)
	if err != nil {
		return nil, err
	}
	bindingID, err := m.lookupParentBindingID(ctx, ws, parent)
	if err != nil {
		// ErrNotFound → a clean not_found envelope (a bare `loom workflow run`
		// with no binding legitimately has no config). Unlike connector egress
		// this op does NOT deny-by-default: reading a nonexistent config is not
		// a privilege escalation.
		return nil, err
	}
	binding, err := m.store.TriggerBindings().Get(ctx, ws, bindingID)
	if err != nil {
		return nil, fmt.Errorf("get trigger binding %q: %w", bindingID, err)
	}
	// The run-input object parsed from source_config_ref (trigger.BindingRunInput)
	// is returned at the top level so a prompt agent reads binding.config().roleName
	// directly; the reserved context keys below are stamped AFTER the copy so they
	// always win over any colliding run-input field.
	result := map[string]any{}
	for k, v := range trigger.BindingRunInput(binding) {
		result[k] = v
	}
	result["bindingId"] = binding.BindingID
	result["sourceKind"] = binding.SourceKind
	if binding.Schedule != "" {
		result["schedule"] = binding.Schedule
	}
	return result, nil
}
