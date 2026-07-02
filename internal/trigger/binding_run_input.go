package trigger

import (
	"encoding/json"
	"strings"

	"github.com/tysonthomas9/loomcli/internal/domain"
)

// Binding run-input (Phase 4 prompt-agent packaging).
//
// PROBLEM. A dispatched DriverRun receives the trigger source's payload verbatim
// as its input (fleet-db copies dispatch.Payload onto the run, and the workflow
// runtime hands it to the workflow as ctx.payload — see
// internal/driver/executor.go flueRuntimeEnv → LOOM_FLUE_INVOKE_PAYLOAD). A cron
// tick therefore delivers only {"tick": ...}. There is no per-binding config on
// the run: a prompt agent (the prompt-agent builtin CONFIGURED WITH A ROLE) has
// no way to learn which role to wear.
//
// WHY NOT A NEW BINDING FIELD. The platform store is always the fleet-db HTTP
// client (embedded subprocess in local mode, remote in cloud —
// internal/bootstrap/openstore.go); fleet-db owns run creation and copies
// dispatch.Payload onto the run unchanged, has NO free-form binding config
// field, and STRICT-DECODES every request body (DisallowUnknownFields), and it
// is a SEPARATE repository — so a new binding field or a server-side
// binding→payload merge would require fleet-db changes that are out of scope.
//
// MECHANISM. The binding's source_config_ref is a free-form, unvalidated string
// that round-trips through fleet-db (and loomcli's domain) untouched. When it
// holds a JSON object, that object is the binding's RUN-INPUT: static config the
// loomcli-controlled dispatch source (CronScheduler) merges UNDER the source
// event payload before dispatch, so the run's input carries both. This benefits
// EVERY cron workflow agent (generic run-input), not just prompt-agent: a prompt
// agent stores {"roleName":"docs-assistant","backend":"codex"} and the fired run
// receives {"roleName":..,"backend":..,"tick":..}. A non-JSON source_config_ref
// (a real webhook source-config ref) parses to nil and is left untouched.
//
// FAN-OUT SCOPE. Cron dispatch is 1:1 — a cron binding owns a unique route key
// (store.WithDerivedRoute) and no pattern binding matches a cron route — so
// merging THIS binding's run-input into the (single-leg) dispatch payload is
// always correct. A shared-payload fan-out route (one dispatch, many matched
// bindings) could not carry per-binding run-input this way; that path is not
// used for cron and is documented where it matters.

// BindingRunInput parses a binding's source_config_ref as a JSON object of
// run-input fields. A non-object / empty / malformed source_config_ref yields a
// nil map and no error (back-compat: webhook source-config refs and legacy
// bindings simply carry no run-input). The values are kept as json.RawMessage so
// arbitrary JSON scalars/objects survive the merge without a lossy re-encode.
func BindingRunInput(binding *domain.TriggerBinding) map[string]json.RawMessage {
	if binding == nil {
		return nil
	}
	return parseRunInputObject(binding.SourceConfigRef)
}

// parseRunInputObject decodes a string as a JSON object; anything that is not a
// JSON object (a bare ref token, empty, an array, malformed JSON) yields nil.
func parseRunInputObject(raw string) map[string]json.RawMessage {
	raw = strings.TrimSpace(raw)
	if raw == "" || raw[0] != '{' {
		return nil
	}
	var obj map[string]json.RawMessage
	if err := json.Unmarshal([]byte(raw), &obj); err != nil {
		return nil
	}
	if len(obj) == 0 {
		return nil
	}
	return obj
}

// MergeRunInputPayload merges a binding's run-input UNDER an event payload: the
// run-input supplies static per-binding config (e.g. roleName) and the event
// payload supplies occurrence-specific fields (e.g. tick, taskId), which WIN on
// a key collision so an event can never be shadowed by stale config. The
// returned bytes are a JSON object suitable for use as a DriverRun payload.
//
// runInput nil/empty returns eventPayload unchanged (marshaled), so a binding
// with no run-input keeps the exact legacy payload byte-for-byte where it
// matters (the tick-only cron payload).
func MergeRunInputPayload(runInput map[string]json.RawMessage, eventPayload map[string]any) (json.RawMessage, error) {
	if len(runInput) == 0 {
		return json.Marshal(eventPayload)
	}
	merged := make(map[string]json.RawMessage, len(runInput)+len(eventPayload))
	for k, v := range runInput {
		merged[k] = v
	}
	for k, v := range eventPayload {
		enc, err := json.Marshal(v)
		if err != nil {
			return nil, err
		}
		merged[k] = enc
	}
	return json.Marshal(merged)
}
