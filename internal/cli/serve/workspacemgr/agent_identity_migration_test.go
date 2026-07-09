package workspacemgr

import (
	"context"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/infra/memstore"
	"github.com/tysonthomas9/loomcli/internal/store"
	workflowdefs "github.com/tysonthomas9/loomcli/internal/workflows"
)

func TestEnsurePromptAgentIdentityRecordsIdempotentAndDoesNotClobber(t *testing.T) {
	ctx := context.Background()
	st := newAgentIdentityMigrationStore(t)
	if _, err := st.TriggerBindings().Create(ctx, store.TriggerBindingCreate{
		WorkspaceKey: "WS", BindingID: "docs-agent", Name: "Docs agent",
		SourceKind: store.InternalSourceKind, DriverID: workflowdefs.BuiltinPromptAgentWorkflowName,
		DriverVersionID: "prompt-agent-version-1", SourceConfigRef: `{"roleName":"docs-assistant","backend":"codex"}`,
		Enabled: true,
	}); err != nil {
		t.Fatalf("create prompt binding: %v", err)
	}
	if _, err := st.TriggerBindings().Create(ctx, store.TriggerBindingCreate{
		WorkspaceKey: "WS", BindingID: "scripted-agent", Name: "Scripted agent",
		SourceKind: store.CronSourceKind, DriverID: "scripted-driver", DriverVersionID: "scripted-version-1",
		SourceConfigRef: `{"roleName":"docs-assistant"}`, Schedule: "*/5 * * * *", Enabled: true,
	}); err != nil {
		t.Fatalf("create scripted binding: %v", err)
	}

	if err := EnsurePromptAgentIdentityRecords(ctx, st); err != nil {
		t.Fatalf("EnsurePromptAgentIdentityRecords: %v", err)
	}
	record, err := st.AgentServices().Get(ctx, "WS", "docs-agent")
	if err != nil {
		t.Fatalf("get migrated record: %v", err)
	}
	if record.Name != "Docs agent" || record.RoleName != "docs-assistant" ||
		record.Kind != domain.AgentServiceKindEvent || record.DesiredState != domain.AgentServiceDesiredRunning {
		t.Fatalf("migrated record = %+v", record)
	}
	binding, err := st.TriggerBindings().Get(ctx, "WS", "docs-agent")
	if err != nil {
		t.Fatalf("get migrated binding: %v", err)
	}
	if binding.TargetAgentServiceID != "docs-agent" {
		t.Fatalf("binding target = %q, want docs-agent", binding.TargetAgentServiceID)
	}
	scripted, err := st.TriggerBindings().Get(ctx, "WS", "scripted-agent")
	if err != nil {
		t.Fatalf("get scripted binding: %v", err)
	}
	if scripted.TargetAgentServiceID != "" {
		t.Fatalf("scripted binding target = %q, want untouched", scripted.TargetAgentServiceID)
	}

	customName := "Operator renamed"
	if _, err := st.AgentServices().Update(ctx, "WS", "docs-agent", store.AgentServiceUpdate{Name: &customName}); err != nil {
		t.Fatalf("customize record: %v", err)
	}
	if err := EnsurePromptAgentIdentityRecords(ctx, st); err != nil {
		t.Fatalf("second EnsurePromptAgentIdentityRecords: %v", err)
	}
	record, err = st.AgentServices().Get(ctx, "WS", "docs-agent")
	if err != nil {
		t.Fatalf("get record after second run: %v", err)
	}
	if record.Name != customName {
		t.Fatalf("record name after second run = %q, want customized %q", record.Name, customName)
	}
	records, err := st.AgentServices().List(ctx, "WS", store.AgentServiceFilter{})
	if err != nil {
		t.Fatalf("list records: %v", err)
	}
	if len(records) != 1 {
		t.Fatalf("records after second run = %+v, want one", records)
	}
}

func TestEnsurePromptAgentIdentityRecordsPrefixesAgentdefNameCollision(t *testing.T) {
	ctx := context.Background()
	st := newAgentIdentityMigrationStore(t)
	if _, err := st.Agents().Create(ctx, store.AgentCreate{WorkspaceKey: "WS", Name: "docs-agent", RoleName: "task"}); err != nil {
		t.Fatalf("create supervised agent: %v", err)
	}
	if _, err := st.TriggerBindings().Create(ctx, store.TriggerBindingCreate{
		WorkspaceKey: "WS", BindingID: "docs-agent", Name: "Docs agent",
		SourceKind: store.CronSourceKind, DriverID: workflowdefs.BuiltinPromptAgentWorkflowName,
		DriverVersionID: "prompt-agent-version-1", SourceConfigRef: `{"roleName":"docs-assistant"}`,
		Schedule: "*/10 * * * *", Enabled: false,
	}); err != nil {
		t.Fatalf("create prompt binding: %v", err)
	}

	if err := EnsurePromptAgentIdentityRecords(ctx, st); err != nil {
		t.Fatalf("EnsurePromptAgentIdentityRecords: %v", err)
	}
	record, err := st.AgentServices().Get(ctx, "WS", "agt-docs-agent")
	if err != nil {
		t.Fatalf("get prefixed record: %v", err)
	}
	if record.Kind != domain.AgentServiceKindCron || record.DesiredState != domain.AgentServiceDesiredPaused {
		t.Fatalf("prefixed record = %+v", record)
	}
	binding, err := st.TriggerBindings().Get(ctx, "WS", "docs-agent")
	if err != nil {
		t.Fatalf("get binding: %v", err)
	}
	if binding.TargetAgentServiceID != "agt-docs-agent" {
		t.Fatalf("binding target = %q, want agt-docs-agent", binding.TargetAgentServiceID)
	}
}

func newAgentIdentityMigrationStore(t *testing.T) *memstore.Store {
	t.Helper()
	st := memstore.New()
	ctx := context.Background()
	if _, err := st.Workspaces().Create(ctx, store.WorkspaceCreate{Key: "WS", Name: "WS"}); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	if _, err := st.Roles().Create(ctx, store.RoleCreate{WorkspaceKey: "WS", Name: "docs-assistant"}); err != nil {
		t.Fatalf("create role: %v", err)
	}
	seedMigrationDriver(t, st, workflowdefs.BuiltinPromptAgentWorkflowName, "prompt-agent-version-1")
	seedMigrationDriver(t, st, "scripted-driver", "scripted-version-1")
	return st
}

func seedMigrationDriver(t *testing.T, st store.Store, driverID, versionID string) {
	t.Helper()
	ctx := context.Background()
	if _, err := st.Drivers().Create(ctx, store.DriverCreate{
		WorkspaceKey: "WS", DriverID: driverID, Name: driverID, ActiveVersionID: versionID,
		Status: domain.DriverStatusActive, OwnerType: domain.DriverOwnerSystem,
	}); err != nil {
		t.Fatalf("create driver %s: %v", driverID, err)
	}
	if _, err := st.DriverVersions().Create(ctx, store.DriverVersionCreate{
		WorkspaceKey: "WS", VersionID: versionID, DriverID: driverID, Version: 1,
		SourceDigest: "src-" + versionID, BundleDigest: "bundle-" + versionID,
		ValidationStatus: domain.DriverVersionValidationPassed,
	}); err != nil {
		t.Fatalf("create driver version %s: %v", versionID, err)
	}
}
