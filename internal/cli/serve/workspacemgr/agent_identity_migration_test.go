package workspacemgr

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/infra/memstore"
	"github.com/tysonthomas9/loomcli/internal/modules/workflowcatalog"
	"github.com/tysonthomas9/loomcli/internal/store"
)

type agentServiceCreateRaceStore struct {
	store.Store
	agentServices store.AgentServiceStore
}

func (s agentServiceCreateRaceStore) AgentServices() store.AgentServiceStore {
	return s.agentServices
}

type alreadyExistsAfterWinnerAgentServiceStore struct {
	store.AgentServiceStore
	winner store.AgentServiceCreate
	once   sync.Once
	err    error
}

func (s *alreadyExistsAfterWinnerAgentServiceStore) Create(ctx context.Context, in store.AgentServiceCreate) (*domain.AgentService, error) {
	s.once.Do(func() {
		_, s.err = s.AgentServiceStore.Create(ctx, s.winner)
	})
	if s.err != nil {
		return nil, fmt.Errorf("create concurrent winner: %w", s.err)
	}
	return s.AgentServiceStore.Create(ctx, in)
}

func TestEnsurePromptAgentIdentityRecordsIdempotentAndDoesNotClobber(t *testing.T) {
	ctx := context.Background()
	st := newAgentIdentityMigrationStore(t)
	if _, err := st.TriggerBindings().Create(ctx, store.TriggerBindingCreate{
		WorkspaceKey: "WS", BindingID: "docs-agent", Name: "Docs agent",
		SourceKind: store.InternalSourceKind, DriverID: workflowcatalog.BuiltinPromptAgentWorkflowName,
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
		SourceKind: store.CronSourceKind, DriverID: workflowcatalog.BuiltinPromptAgentWorkflowName,
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

func TestEnsurePromptAgentIdentityRecordsDoesNotAdoptUnrelatedRecordID(t *testing.T) {
	ctx := context.Background()
	st := newAgentIdentityMigrationStore(t)
	if _, err := st.Roles().Create(ctx, store.RoleCreate{WorkspaceKey: "WS", Name: "other-role"}); err != nil {
		t.Fatalf("create other role: %v", err)
	}
	if _, err := st.AgentServices().Create(ctx, store.AgentServiceCreate{
		WorkspaceKey: "WS", ServiceID: "docs-agent", Name: "Unrelated record",
		Kind: domain.AgentServiceKindEvent, DesiredState: domain.AgentServiceDesiredRunning,
		RoleName: "other-role",
	}); err != nil {
		t.Fatalf("create unrelated record: %v", err)
	}
	if _, err := st.TriggerBindings().Create(ctx, store.TriggerBindingCreate{
		WorkspaceKey: "WS", BindingID: "docs-agent", Name: "Docs agent",
		SourceKind: store.InternalSourceKind, DriverID: workflowcatalog.BuiltinPromptAgentWorkflowName,
		DriverVersionID: "prompt-agent-version-1", SourceConfigRef: `{"roleName":"docs-assistant"}`,
		Enabled: true,
	}); err != nil {
		t.Fatalf("create prompt binding: %v", err)
	}

	if err := EnsurePromptAgentIdentityRecords(ctx, st); err != nil {
		t.Fatalf("EnsurePromptAgentIdentityRecords: %v", err)
	}
	binding, err := st.TriggerBindings().Get(ctx, "WS", "docs-agent")
	if err != nil {
		t.Fatalf("get migrated binding: %v", err)
	}
	if binding.TargetAgentServiceID != "agt-docs-agent" {
		t.Fatalf("binding target = %q, want collision-safe agt-docs-agent", binding.TargetAgentServiceID)
	}
	unrelated, err := st.AgentServices().Get(ctx, "WS", "docs-agent")
	if err != nil || unrelated.RoleName != "other-role" || unrelated.Name != "Unrelated record" {
		t.Fatalf("unrelated record was adopted or mutated: %+v err=%v", unrelated, err)
	}
	migrated, err := st.AgentServices().Get(ctx, "WS", "agt-docs-agent")
	if err != nil || migrated.RoleName != "docs-assistant" || migrated.Name != "Docs agent" {
		t.Fatalf("collision-safe migrated record = %+v err=%v", migrated, err)
	}
}

func TestEnsurePromptAgentIdentityRecordsReusesExactCrashResidue(t *testing.T) {
	ctx := context.Background()
	st := newAgentIdentityMigrationStore(t)
	if _, err := st.AgentServices().Create(ctx, store.AgentServiceCreate{
		WorkspaceKey: "WS", ServiceID: "docs-agent", Name: "Docs agent",
		Kind: domain.AgentServiceKindEvent, DesiredState: domain.AgentServiceDesiredRunning,
		RoleName: "docs-assistant",
	}); err != nil {
		t.Fatalf("create crash-residue record: %v", err)
	}
	if _, err := st.TriggerBindings().Create(ctx, store.TriggerBindingCreate{
		WorkspaceKey: "WS", BindingID: "docs-agent", Name: "Docs agent",
		SourceKind: store.InternalSourceKind, DriverID: workflowcatalog.BuiltinPromptAgentWorkflowName,
		DriverVersionID: "prompt-agent-version-1", SourceConfigRef: `{"roleName":"docs-assistant"}`,
		Enabled: true,
	}); err != nil {
		t.Fatalf("create prompt binding: %v", err)
	}

	if err := EnsurePromptAgentIdentityRecords(ctx, st); err != nil {
		t.Fatalf("EnsurePromptAgentIdentityRecords: %v", err)
	}
	binding, err := st.TriggerBindings().Get(ctx, "WS", "docs-agent")
	if err != nil || binding.TargetAgentServiceID != "docs-agent" {
		t.Fatalf("binding target = %+v err=%v, want exact residue reuse", binding, err)
	}
	records, err := st.AgentServices().List(ctx, "WS", store.AgentServiceFilter{})
	if err != nil || len(records) != 1 {
		t.Fatalf("records = %+v err=%v, want one reused record", records, err)
	}
}

func TestEnsurePromptAgentIdentityRecordsRejectsConcurrentUnrelatedWinner(t *testing.T) {
	ctx := context.Background()
	base := newAgentIdentityMigrationStore(t)
	if _, err := base.Roles().Create(ctx, store.RoleCreate{WorkspaceKey: "WS", Name: "other-role"}); err != nil {
		t.Fatalf("create other role: %v", err)
	}
	createPromptAgentMigrationBinding(t, base)

	racingServices := &alreadyExistsAfterWinnerAgentServiceStore{
		AgentServiceStore: base.AgentServices(),
		winner: store.AgentServiceCreate{
			WorkspaceKey: "WS", ServiceID: "docs-agent", Name: "Concurrent unrelated record",
			Kind: domain.AgentServiceKindEvent, DesiredState: domain.AgentServiceDesiredRunning,
			RoleName: "other-role",
		},
	}
	racingStore := agentServiceCreateRaceStore{Store: base, agentServices: racingServices}

	err := EnsurePromptAgentIdentityRecords(ctx, racingStore)
	if !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("EnsurePromptAgentIdentityRecords error = %v, want conflict", err)
	}
	binding, getErr := base.TriggerBindings().Get(ctx, "WS", "docs-agent")
	if getErr != nil {
		t.Fatalf("get binding after conflict: %v", getErr)
	}
	if binding.TargetAgentServiceID != "" {
		t.Fatalf("binding target = %q, want unattached after concurrent identity conflict", binding.TargetAgentServiceID)
	}
	winner, getErr := base.AgentServices().Get(ctx, "WS", "docs-agent")
	if getErr != nil || winner.RoleName != "other-role" || winner.Name != "Concurrent unrelated record" {
		t.Fatalf("concurrent unrelated winner changed: %+v err=%v", winner, getErr)
	}
}

func TestEnsurePromptAgentIdentityRecordsAdoptsConcurrentMatchingWinner(t *testing.T) {
	ctx := context.Background()
	base := newAgentIdentityMigrationStore(t)
	createPromptAgentMigrationBinding(t, base)

	racingServices := &alreadyExistsAfterWinnerAgentServiceStore{
		AgentServiceStore: base.AgentServices(),
		winner: store.AgentServiceCreate{
			WorkspaceKey: "WS", ServiceID: "docs-agent", Name: "Docs agent",
			Kind: domain.AgentServiceKindEvent, DesiredState: domain.AgentServiceDesiredRunning,
			RoleName: "docs-assistant",
		},
	}
	racingStore := agentServiceCreateRaceStore{Store: base, agentServices: racingServices}

	if err := EnsurePromptAgentIdentityRecords(ctx, racingStore); err != nil {
		t.Fatalf("EnsurePromptAgentIdentityRecords: %v", err)
	}
	binding, err := base.TriggerBindings().Get(ctx, "WS", "docs-agent")
	if err != nil {
		t.Fatalf("get attached binding: %v", err)
	}
	if binding.TargetAgentServiceID != "docs-agent" {
		t.Fatalf("binding target = %q, want concurrently created matching record", binding.TargetAgentServiceID)
	}
	records, err := base.AgentServices().List(ctx, "WS", store.AgentServiceFilter{})
	if err != nil || len(records) != 1 {
		t.Fatalf("records = %+v err=%v, want one matching winner", records, err)
	}
}

func createPromptAgentMigrationBinding(t *testing.T, st store.Store) {
	t.Helper()
	if _, err := st.TriggerBindings().Create(context.Background(), store.TriggerBindingCreate{
		WorkspaceKey: "WS", BindingID: "docs-agent", Name: "Docs agent",
		SourceKind: store.InternalSourceKind, DriverID: workflowcatalog.BuiltinPromptAgentWorkflowName,
		DriverVersionID: "prompt-agent-version-1", SourceConfigRef: `{"roleName":"docs-assistant"}`,
		Enabled: true,
	}); err != nil {
		t.Fatalf("create prompt binding: %v", err)
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
	seedMigrationDriver(t, st, workflowcatalog.BuiltinPromptAgentWorkflowName, "prompt-agent-version-1")
	seedMigrationDriver(t, st, "scripted-driver", "scripted-version-1")
	return st
}

func seedMigrationDriver(t *testing.T, st *memstore.Store, driverID, versionID string) {
	t.Helper()
	ctx := context.Background()
	if _, err := st.Drivers().Create(ctx, store.DriverCreate{
		WorkspaceKey: "WS", DriverID: driverID, Name: driverID,
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
	if _, err := st.ApproveDriverVersionForTest(ctx, "WS", driverID, versionID); err != nil {
		t.Fatalf("approve driver version %s: %v", versionID, err)
	}
	if _, err := st.ActivateDriverVersionForTest(ctx, "WS", driverID, versionID); err != nil {
		t.Fatalf("activate driver version %s: %v", versionID, err)
	}
}
