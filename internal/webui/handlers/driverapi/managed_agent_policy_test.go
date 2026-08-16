package driverapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/infra/memstore"
	agentsowner "github.com/tysonthomas9/loomcli/internal/modules/agents"
	"github.com/tysonthomas9/loomcli/internal/modules/execution"
)

func TestSnapshotManagedAgentPolicyOverridesSpoofedPolicy(t *testing.T) {
	h := newManagedAgentPolicyHarness(t)
	parent := seedManagedAgentPolicy(t, h)
	input := json.RawMessage(`{
		"taskPrompt": "keep caller input",
		"loomAgentPolicy": {
			"version": 999,
			"agentServiceId": "attacker",
			"roleName": "attacker",
			"backend": "shell",
			"readOnly": false,
			"allowedTools": ["Bash"],
			"attackerOnly": true
		}
	}`)

	snapshot, err := h.module.snapshotManagedAgentPolicy(t.Context(), "WS", parent, input)
	if err != nil {
		t.Fatalf("snapshotManagedAgentPolicy: %v", err)
	}
	object, policy, policyObject := decodeManagedAgentPolicy(t, snapshot)
	if got := decodeJSONString(t, object["taskPrompt"]); got != "keep caller input" {
		t.Fatalf("taskPrompt = %q, want preserved caller input", got)
	}
	if policy.Version != 1 ||
		policy.AgentServiceID != "managed-agent" ||
		policy.RoleName != "managed-role" ||
		policy.Backend != "service-backend" ||
		policy.Model != "server-model" ||
		policy.Effort != "high" ||
		!policy.ReadOnly {
		t.Fatalf("policy = %#v, want server-derived managed policy", policy)
	}
	if len(policy.AllowedTools) != 2 || policy.AllowedTools[0] != "Read" || policy.AllowedTools[1] != "Grep" {
		t.Fatalf("allowedTools = %v, want [Read Grep]", policy.AllowedTools)
	}
	if len(policy.DeniedTools) != 1 || policy.DeniedTools[0] != "Bash" {
		t.Fatalf("deniedTools = %v, want [Bash]", policy.DeniedTools)
	}
	if policy.MaxBudgetUSD == nil || *policy.MaxBudgetUSD != 3.25 {
		t.Fatalf("maxBudgetUsd = %v, want 3.25", policy.MaxBudgetUSD)
	}
	if _, ok := policyObject["attackerOnly"]; ok {
		t.Fatal("server-derived loomAgentPolicy retained an attacker-only field")
	}
}

func TestSnapshotManagedAgentPolicyBytesRemainImmutableAcrossStoreEdits(t *testing.T) {
	h := newManagedAgentPolicyHarness(t)
	parent := seedManagedAgentPolicy(t, h)

	first, err := h.module.snapshotManagedAgentPolicy(
		t.Context(),
		"WS",
		parent,
		json.RawMessage(`{"taskPrompt":"implement"}`),
	)
	if err != nil {
		t.Fatalf("first snapshotManagedAgentPolicy: %v", err)
	}
	frozenFirst := append(json.RawMessage(nil), first...)

	model := "server-model-v2"
	effort := "low"
	readOnly := false
	allowedTools := []string{"Read"}
	deniedTools := []string{"Bash", "Write"}
	if _, err := h.store.Roles().Update(t.Context(), "WS", "managed-role", agentsowner.RoleRecordUpdate{
		Model:        &model,
		Effort:       &effort,
		ReadOnly:     &readOnly,
		AllowedTools: &allowedTools,
		DeniedTools:  &deniedTools,
	}); err != nil {
		t.Fatalf("update managed role: %v", err)
	}
	metadata := map[string]string{"backend": "service-backend-v2"}
	if _, err := h.store.AgentServices().Update(
		t.Context(),
		"WS",
		"managed-agent",
		agentsowner.AgentServiceUpdate{Metadata: &metadata},
	); err != nil {
		t.Fatalf("update managed agent service: %v", err)
	}

	second, err := h.module.snapshotManagedAgentPolicy(
		t.Context(),
		"WS",
		parent,
		json.RawMessage(`{"taskPrompt":"implement"}`),
	)
	if err != nil {
		t.Fatalf("second snapshotManagedAgentPolicy: %v", err)
	}
	if !bytes.Equal(first, frozenFirst) {
		t.Fatalf("first snapshot mutated after store edits:\n got %s\nwant %s", first, frozenFirst)
	}

	_, firstPolicy, _ := decodeManagedAgentPolicy(t, first)
	if firstPolicy.Backend != "service-backend" ||
		firstPolicy.Model != "server-model" ||
		firstPolicy.Effort != "high" ||
		!firstPolicy.ReadOnly ||
		len(firstPolicy.AllowedTools) != 2 {
		t.Fatalf("first policy changed after store edits: %#v", firstPolicy)
	}
	_, secondPolicy, _ := decodeManagedAgentPolicy(t, second)
	if secondPolicy.Backend != "service-backend-v2" ||
		secondPolicy.Model != "server-model-v2" ||
		secondPolicy.Effort != "low" ||
		secondPolicy.ReadOnly {
		t.Fatalf("second policy did not reflect store edits: %#v", secondPolicy)
	}
	if len(secondPolicy.AllowedTools) != 1 || secondPolicy.AllowedTools[0] != "Read" {
		t.Fatalf("second allowedTools = %v, want [Read]", secondPolicy.AllowedTools)
	}
	if len(secondPolicy.DeniedTools) != 2 ||
		secondPolicy.DeniedTools[0] != "Bash" ||
		secondPolicy.DeniedTools[1] != "Write" {
		t.Fatalf("second deniedTools = %v, want [Bash Write]", secondPolicy.DeniedTools)
	}
}

func TestSnapshotManagedAgentPolicyStripsReservedKeyForUnmanagedRun(t *testing.T) {
	h := newManagedAgentPolicyHarness(t)
	input := json.RawMessage(`{
		"taskPrompt": "ordinary unmanaged input",
		"loomAgentPolicy": {"backend": "attacker"}
	}`)

	snapshot, err := h.module.snapshotManagedAgentPolicy(
		t.Context(),
		"WS",
		&execution.DriverRun{},
		input,
	)
	if err != nil {
		t.Fatalf("snapshotManagedAgentPolicy: %v", err)
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal(snapshot, &object); err != nil {
		t.Fatalf("decode unmanaged snapshot: %v (raw=%s)", err, snapshot)
	}
	if _, ok := object[managedAgentPolicyInputKey]; ok {
		t.Fatalf("unmanaged snapshot retained reserved key: %s", snapshot)
	}
	if got := decodeJSONString(t, object["taskPrompt"]); got != "ordinary unmanaged input" {
		t.Fatalf("taskPrompt = %q, want preserved unmanaged input", got)
	}
}

func TestSnapshotManagedAgentPolicyFailsClosedWhenRoleIsMissing(t *testing.T) {
	h := newManagedAgentPolicyHarness(t)
	parent := seedManagedAgentPolicy(t, h)
	h.module.agentRoles = snapshotMissingRoleQueries{}

	snapshot, err := h.module.snapshotManagedAgentPolicy(
		t.Context(),
		"WS",
		parent,
		json.RawMessage(`{"loomAgentPolicy":{"backend":"attacker"}}`),
	)
	if err == nil {
		t.Fatalf("snapshotManagedAgentPolicy returned snapshot %s, want missing-role error", snapshot)
	}
	if snapshot != nil {
		t.Fatalf("snapshot = %s, want nil on missing managed role", snapshot)
	}
	if !errors.Is(err, agentsowner.ErrNotFound) {
		t.Fatalf("error = %v, want Agents ErrNotFound", err)
	}
	if !strings.Contains(err.Error(), `resolve managed TaskRun role "managed-role"`) {
		t.Fatalf("error = %q, want managed-role resolution context", err)
	}
}

type managedAgentPolicyHarness struct {
	module *Module
	store  *memstore.Store
}

func newManagedAgentPolicyHarness(t *testing.T) *managedAgentPolicyHarness {
	t.Helper()
	st := memstore.New()
	t.Cleanup(func() {
		if err := st.Close(); err != nil {
			t.Errorf("close managed policy store: %v", err)
		}
	})
	return &managedAgentPolicyHarness{
		module: NewModule(Config{
			AgentIdentities: testStoreAgentIdentities{store: st.AgentServices()},
			AgentRoles:      testStoreAgentRoles{store: st.Roles()},
		}),
		store: st,
	}
}

func seedManagedAgentPolicy(t *testing.T, h *managedAgentPolicyHarness) *execution.DriverRun {
	t.Helper()
	budget := 3.25
	if _, err := h.store.Roles().Create(t.Context(), agentsowner.RoleRecordCreate{
		WorkspaceKey: "WS",
		Name:         "managed-role",
		Model:        "server-model",
		Backend:      "role-backend",
		Effort:       "high",
		ReadOnly:     true,
		AllowedTools: []string{"Read", "Grep"},
		DeniedTools:  []string{"Bash"},
		MaxBudgetUSD: &budget,
	}); err != nil {
		t.Fatalf("create managed role: %v", err)
	}
	if _, err := h.store.AgentServices().Create(t.Context(), agentsowner.AgentServiceCreate{
		WorkspaceKey: "WS",
		ServiceID:    "managed-agent",
		Kind:         agentsowner.AgentKindEvent,
		RoleName:     "managed-role",
		Metadata:     map[string]string{"backend": "service-backend"},
	}); err != nil {
		t.Fatalf("create managed agent service: %v", err)
	}
	return &execution.DriverRun{AgentServiceID: "managed-agent"}
}

func decodeManagedAgentPolicy(
	t *testing.T,
	raw json.RawMessage,
) (map[string]json.RawMessage, managedAgentPolicyInput, map[string]json.RawMessage) {
	t.Helper()
	var object map[string]json.RawMessage
	if err := json.Unmarshal(raw, &object); err != nil {
		t.Fatalf("decode TaskRun input: %v (raw=%s)", err, raw)
	}
	rawPolicy, ok := object[managedAgentPolicyInputKey]
	if !ok {
		t.Fatalf("TaskRun input missing %q: %s", managedAgentPolicyInputKey, raw)
	}
	var policy managedAgentPolicyInput
	if err := json.Unmarshal(rawPolicy, &policy); err != nil {
		t.Fatalf("decode managed policy: %v (raw=%s)", err, rawPolicy)
	}
	var policyObject map[string]json.RawMessage
	if err := json.Unmarshal(rawPolicy, &policyObject); err != nil {
		t.Fatalf("decode managed policy object: %v (raw=%s)", err, rawPolicy)
	}
	return object, policy, policyObject
}

func decodeJSONString(t *testing.T, raw json.RawMessage) string {
	t.Helper()
	var value string
	if err := json.Unmarshal(raw, &value); err != nil {
		t.Fatalf("decode JSON string: %v (raw=%s)", err, raw)
	}
	return value
}

type snapshotMissingRoleQueries struct{}

func (snapshotMissingRoleQueries) GetRole(context.Context, string, string) (*agentsowner.Role, error) {
	return nil, agentsowner.ErrNotFound
}

func (snapshotMissingRoleQueries) ListRoles(context.Context, string) ([]*agentsowner.Role, error) {
	return nil, agentsowner.ErrNotFound
}
