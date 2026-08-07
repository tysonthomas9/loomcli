// Binding run-input tests live in the external trigger_test package so the cron
// dispatch assertion can drive the real memstore dispatch path (memstore
// imports trigger, so an in-package test would cycle).
package trigger_test

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/modules/automation"

	"github.com/tysonthomas9/loomcli/internal/modules/workflowcatalog"

	trigger "github.com/tysonthomas9/loomcli/internal/infra/automationruntime"
	"github.com/tysonthomas9/loomcli/internal/infra/memstore"
	"github.com/tysonthomas9/loomcli/internal/store"
)

func TestBindingRunInputParses(t *testing.T) {
	tests := []struct {
		name      string
		configRef string
		wantKeys  []string
	}{
		{name: "json object", configRef: `{"roleName":"docs-assistant","backend":"codex"}`, wantKeys: []string{"roleName", "backend"}},
		{name: "empty", configRef: "", wantKeys: nil},
		{name: "bare ref token (webhook source config)", configRef: "source-config-abc", wantKeys: nil},
		{name: "json array not object", configRef: `["a","b"]`, wantKeys: nil},
		{name: "malformed json", configRef: `{"roleName":`, wantKeys: nil},
		{name: "empty object", configRef: `{}`, wantKeys: nil},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := trigger.BindingRunInput(&automation.Binding{SourceConfigRef: tc.configRef})
			if len(got) != len(tc.wantKeys) {
				t.Fatalf("BindingRunInput(%q) = %v, want keys %v", tc.configRef, got, tc.wantKeys)
			}
			for _, k := range tc.wantKeys {
				if _, ok := got[k]; !ok {
					t.Fatalf("BindingRunInput(%q) missing key %q; got %v", tc.configRef, k, got)
				}
			}
		})
	}
}

func TestMergeRunInputPayload(t *testing.T) {
	t.Run("merges config under event; event wins collisions", func(t *testing.T) {
		runInput := map[string]json.RawMessage{
			"roleName": json.RawMessage(`"docs-assistant"`),
			"tick":     json.RawMessage(`"stale"`), // collides with the event field
		}
		out, err := trigger.MergeRunInputPayload(runInput, map[string]any{"tick": "2026-07-01T00:00:00Z"})
		if err != nil {
			t.Fatalf("MergeRunInputPayload: %v", err)
		}
		var got map[string]any
		if err := json.Unmarshal(out, &got); err != nil {
			t.Fatalf("decode merged: %v", err)
		}
		if got["roleName"] != "docs-assistant" {
			t.Fatalf("roleName = %v, want docs-assistant (from run-input)", got["roleName"])
		}
		if got["tick"] != "2026-07-01T00:00:00Z" {
			t.Fatalf("tick = %v, want the event value (event wins collisions)", got["tick"])
		}
	})

	t.Run("nil run-input returns the event payload unchanged", func(t *testing.T) {
		out, err := trigger.MergeRunInputPayload(nil, map[string]any{"tick": "T"})
		if err != nil {
			t.Fatalf("MergeRunInputPayload: %v", err)
		}
		var got map[string]any
		if err := json.Unmarshal(out, &got); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if len(got) != 1 || got["tick"] != "T" {
			t.Fatalf("payload = %v, want just the tick", got)
		}
	})
}

// TestCronSchedulerMergesBindingRunInput proves ITEM D end-to-end through the
// real memstore dispatch: a cron binding whose source_config_ref carries a
// run-input object fires a run whose payload merges that config with the tick,
// so a prompt agent's configured roleName reaches input.roleName.
func TestCronSchedulerMergesBindingRunInput(t *testing.T) {
	st := memstore.New()
	ctx := t.Context()
	if _, err := st.Drivers().Create(ctx, store.DriverCreate{
		WorkspaceKey: "WS", DriverID: "prompt-agent", Name: "prompt-agent",
		OwnerType: workflowcatalog.DriverOwnerSystem, Status: workflowcatalog.DriverStatusActive,
	}); err != nil {
		t.Fatalf("Create driver: %v", err)
	}
	if _, err := st.DriverVersions().Create(ctx, store.DriverVersionCreate{
		WorkspaceKey: "WS", VersionID: "v1", DriverID: "prompt-agent", Version: 1,
		SourceDigest: "sha256:s", BundleDigest: "sha256:b", ValidationStatus: workflowcatalog.DriverVersionValidationPassed,
	}); err != nil {
		t.Fatalf("Create driver version: %v", err)
	}
	if _, err := st.TriggerBindings().Create(ctx, store.TriggerBindingCreate{
		WorkspaceKey: "WS", BindingID: "docs-agent", Name: "docs-agent",
		SourceKind: trigger.CronSourceKind, RouteKey: "cron:docs-agent", Schedule: "* * * * *",
		SourceConfigRef: `{"roleName":"docs-assistant","backend":"codex"}`,
		DriverID:        "prompt-agent", DriverVersionID: "v1", TargetEntrypoint: "run", Enabled: true,
	}); err != nil {
		t.Fatalf("Create trigger binding: %v", err)
	}

	scheduler := &trigger.CronScheduler{Store: st, WorkspaceKey: "WS"}
	t0 := time.Date(2026, 7, 1, 10, 0, 30, 0, time.UTC)
	if _, err := scheduler.RunOnce(ctx, t0); err != nil { // prime window
		t.Fatalf("prime sweep: %v", err)
	}
	t1 := t0.Add(45 * time.Second) // a tick at 10:01:00 is now due
	res, err := scheduler.RunOnce(ctx, t1)
	if err != nil {
		t.Fatalf("due sweep: %v", err)
	}
	if res.Fired != 1 {
		t.Fatalf("fired = %d, want 1", res.Fired)
	}

	runs, err := st.DriverRuns().List(ctx, "WS", store.DriverRunFilter{})
	if err != nil || len(runs) != 1 {
		t.Fatalf("List runs = %v, %v; want 1 run", runs, err)
	}
	var payload map[string]any
	if err := json.Unmarshal(runs[0].Payload, &payload); err != nil {
		t.Fatalf("decode run payload %q: %v", runs[0].Payload, err)
	}
	if payload["roleName"] != "docs-assistant" {
		t.Fatalf("run payload roleName = %v, want docs-assistant", payload["roleName"])
	}
	if payload["backend"] != "codex" {
		t.Fatalf("run payload backend = %v, want codex", payload["backend"])
	}
	if _, ok := payload["tick"]; !ok {
		t.Fatalf("run payload missing tick; got %v", payload)
	}
}

// TestCronSchedulerNoRunInputKeepsTickOnlyPayload proves back-compat: a binding
// with no run-input (empty source_config_ref) dispatches the tick-only payload
// exactly as before.
func TestCronSchedulerNoRunInputKeepsTickOnlyPayload(t *testing.T) {
	st := memstore.New()
	ctx := t.Context()
	if _, err := st.Drivers().Create(ctx, store.DriverCreate{
		WorkspaceKey: "WS", DriverID: "nightly", Name: "nightly",
		OwnerType: workflowcatalog.DriverOwnerSystem, Status: workflowcatalog.DriverStatusActive,
	}); err != nil {
		t.Fatalf("Create driver: %v", err)
	}
	if _, err := st.DriverVersions().Create(ctx, store.DriverVersionCreate{
		WorkspaceKey: "WS", VersionID: "v1", DriverID: "nightly", Version: 1,
		SourceDigest: "sha256:s", BundleDigest: "sha256:b", ValidationStatus: workflowcatalog.DriverVersionValidationPassed,
	}); err != nil {
		t.Fatalf("Create driver version: %v", err)
	}
	if _, err := st.TriggerBindings().Create(ctx, store.TriggerBindingCreate{
		WorkspaceKey: "WS", BindingID: "nightly", Name: "nightly",
		SourceKind: trigger.CronSourceKind, RouteKey: "cron:nightly", Schedule: "* * * * *",
		DriverID: "nightly", DriverVersionID: "v1", TargetEntrypoint: "run", Enabled: true,
	}); err != nil {
		t.Fatalf("Create trigger binding: %v", err)
	}
	scheduler := &trigger.CronScheduler{Store: st, WorkspaceKey: "WS"}
	t0 := time.Date(2026, 7, 1, 10, 0, 30, 0, time.UTC)
	if _, err := scheduler.RunOnce(ctx, t0); err != nil {
		t.Fatalf("prime sweep: %v", err)
	}
	if _, err := scheduler.RunOnce(ctx, t0.Add(45*time.Second)); err != nil {
		t.Fatalf("due sweep: %v", err)
	}
	runs, err := st.DriverRuns().List(ctx, "WS", store.DriverRunFilter{})
	if err != nil || len(runs) != 1 {
		t.Fatalf("List runs = %v, %v; want 1 run", runs, err)
	}
	var payload map[string]any
	if err := json.Unmarshal(runs[0].Payload, &payload); err != nil {
		t.Fatalf("decode run payload: %v", err)
	}
	if len(payload) != 1 {
		t.Fatalf("payload = %v, want tick-only", payload)
	}
	if _, ok := payload["tick"]; !ok {
		t.Fatalf("payload missing tick; got %v", payload)
	}
}
