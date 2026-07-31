package agentprovisioning

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/platform/authority"
)

func TestManagerRecoversCrashAfterEveryExternalCommit(t *testing.T) {
	tests := []struct {
		name string
		step Step
		key  string
	}{
		{"role", StepRole, ""},
		{"agent", StepAgent, ""},
		{"binding", StepBinding, ""},
		{"first grant", StepGrant, "grant-read"},
		{"second grant", StepGrant, "grant-write"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			store := newMemoryProgressStore()
			operations := newIdempotentOperations()
			now := time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC)
			manager := newTestManager(t, store, operations, &failOnceAfter{step: test.step, key: test.key}, &now)
			spec := validSpec()
			if _, err := manager.begin(t, spec); err != nil {
				t.Fatalf("Begin: %v", err)
			}
			if _, err := manager.Run(t.Context(), spec.WorkspaceKey, spec.ProvisioningID); err == nil {
				t.Fatal("Run succeeded despite injected process crash")
			}

			// A fresh process has no in-memory fault state. It reads the durable
			// record and safely replays the potentially committed command.
			restarted := newTestManager(t, store, operations, nil, &now)
			record, err := restarted.Run(t.Context(), spec.WorkspaceKey, spec.ProvisioningID)
			if err != nil {
				t.Fatalf("restart Run: %v", err)
			}
			if record.State != StateCompleted || record.CompletedAt == nil ||
				record.UnusedRolePolicy != UnusedRoleRetain ||
				!reflect.DeepEqual(record.CompletedSteps, []Step{StepRole, StepAgent, StepBinding, StepGrant}) ||
				!reflect.DeepEqual(record.CompletedGrants, []string{"grant-read", "grant-write"}) {
				t.Fatalf("completed record = %+v", record)
			}
			for _, commandID := range []string{
				"provision-1:role",
				"provision-1:agent",
				"provision-1:binding",
				"provision-1:grant:grant-read",
				"provision-1:grant:grant-write",
			} {
				if operations.unique[commandID] != 1 {
					t.Fatalf("unique durable mutation count for %s = %d, want 1", commandID, operations.unique[commandID])
				}
			}
			faultID := commandID(spec.ProvisioningID, test.step, test.key)
			if operations.calls[faultID] != 2 {
				t.Fatalf("fault-step invocations for %s = %d, want replayed twice", faultID, operations.calls[faultID])
			}
		})
	}
}

func TestManagerRetainsIndependentlyCommittedUnusedRole(t *testing.T) {
	store := newMemoryProgressStore()
	operations := newIdempotentOperations()
	operations.failStep = StepBinding
	now := time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC)
	manager := newTestManager(t, store, operations, nil, &now)
	spec := validSpec()
	if _, err := manager.begin(t, spec); err != nil {
		t.Fatalf("Begin: %v", err)
	}
	record, err := manager.Run(t.Context(), spec.WorkspaceKey, spec.ProvisioningID)
	if err == nil {
		t.Fatal("Run succeeded with binding failure")
	}
	if record == nil || record.State != StateRetryableFailed ||
		record.LastErrorClass != "binding_unavailable" ||
		record.UnusedRolePolicy != UnusedRoleRetain ||
		!slices.Contains(record.CompletedSteps, StepRole) ||
		operations.unique["provision-1:role"] != 1 {
		t.Fatalf("failed record/role outcome = %+v, operations=%+v", record, operations.unique)
	}
	if _, exists := operations.unique["provision-1:grant:grant-read"]; exists {
		t.Fatal("grant mutated after binding failure")
	}
}

func TestBeginRejectsDivergentIdempotencyIntent(t *testing.T) {
	store := newMemoryProgressStore()
	operations := newIdempotentOperations()
	now := time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC)
	manager := newTestManager(t, store, operations, nil, &now)
	spec := validSpec()
	first, err := manager.begin(t, spec)
	if err != nil {
		t.Fatalf("first Begin: %v", err)
	}
	replayed, err := manager.begin(t, spec)
	if err != nil || replayed.SpecFingerprint != first.SpecFingerprint {
		t.Fatalf("same Begin = %+v, %v", replayed, err)
	}
	spec.Role.Prompt = "divergent prompt"
	if _, err := manager.begin(t, spec); !errors.Is(err, ErrConflict) {
		t.Fatalf("divergent Begin error = %v, want ErrConflict", err)
	}
}

func TestBeginCanonicalizesDuplicateEventPatterns(t *testing.T) {
	store := newMemoryProgressStore()
	operations := newIdempotentOperations()
	now := time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC)
	manager := newTestManager(t, store, operations, nil, &now)
	spec := validSpec()
	spec.Binding.EventPatterns = []string{
		" internal.task.review ",
		"internal.task.open",
		"internal.task.review",
	}

	first, err := manager.begin(t, spec)
	if err != nil {
		t.Fatalf("first Begin: %v", err)
	}
	want := []string{"internal.task.open", "internal.task.review"}
	if !slices.Equal(first.Spec.Binding.EventPatterns, want) {
		t.Fatalf("canonical event patterns = %q, want %q", first.Spec.Binding.EventPatterns, want)
	}

	spec.Binding.EventPatterns = []string{"internal.task.review", "internal.task.open"}
	replayed, err := manager.begin(t, spec)
	if err != nil {
		t.Fatalf("canonical replay Begin: %v", err)
	}
	if replayed.SpecFingerprint != first.SpecFingerprint {
		t.Fatalf(
			"canonical replay fingerprint = %q, want %q",
			replayed.SpecFingerprint,
			first.SpecFingerprint,
		)
	}
}

func TestBeginCanonicalizesOwnerVisibleBindingDefaults(t *testing.T) {
	for _, sourceKind := range []string{"internal", "cron"} {
		t.Run(sourceKind, func(t *testing.T) {
			store := newMemoryProgressStore()
			operations := newIdempotentOperations()
			now := time.Date(2026, 7, 30, 12, 0, 0, 0, time.UTC)
			manager := newTestManager(t, store, operations, nil, &now)
			spec := validSpec()
			spec.Binding.SourceKind = sourceKind
			spec.Binding.Name = ""
			spec.Binding.RouteKey = ""
			if sourceKind == "cron" {
				spec.Binding.Schedule = "0 * * * *"
			}

			first, err := manager.begin(t, spec)
			if err != nil {
				t.Fatalf("Begin: %v", err)
			}
			wantRoute := sourceKind + ":" + spec.Binding.BindingID
			if first.Spec.Binding.RouteKey != wantRoute ||
				first.Spec.Binding.Name != wantRoute {
				t.Fatalf(
					"canonical Binding = name:%q route:%q, want %q",
					first.Spec.Binding.Name,
					first.Spec.Binding.RouteKey,
					wantRoute,
				)
			}

			spec.Binding.Name = wantRoute
			spec.Binding.RouteKey = wantRoute
			replayed, err := manager.begin(t, spec)
			if err != nil {
				t.Fatalf("explicit-default replay Begin: %v", err)
			}
			if replayed.SpecFingerprint != first.SpecFingerprint {
				t.Fatalf(
					"explicit-default replay fingerprint=%q, want %q",
					replayed.SpecFingerprint,
					first.SpecFingerprint,
				)
			}
		})
	}
}

func TestBeginRejectsGrantWithoutResourcePattern(t *testing.T) {
	store := newMemoryProgressStore()
	operations := newIdempotentOperations()
	now := time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC)
	manager := newTestManager(t, store, operations, nil, &now)
	spec := validSpec()
	spec.Grants[0].ResourcePattern = " "

	if _, err := manager.begin(t, spec); !errors.Is(err, ErrInvalid) {
		t.Fatalf("Begin grant without resource pattern = %v, want ErrInvalid", err)
	}
	if len(store.records) != 0 {
		t.Fatal("invalid grant intent was persisted")
	}
}

func TestBeginRequiresOperatorAuthorityAndDelegatesItsVerifiedSubject(t *testing.T) {
	store := newMemoryProgressStore()
	operations := newIdempotentOperations()
	now := time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC)
	manager := newTestManager(t, store, operations, nil, &now)
	spec := validSpec()

	auth := issueBeginAuthority(t, manager.issuer, spec.WorkspaceKey, "operator:trusted")
	record, err := manager.Begin(t.Context(), auth, spec)
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}
	if record.RequestedBy != "operator:trusted" {
		t.Fatalf("requester = %q, want verified delegated operator", record.RequestedBy)
	}

	foreignIssuer := authority.NewIssuer()
	foreign := issueBeginAuthority(t, foreignIssuer, spec.WorkspaceKey, "operator:foreign")
	foreignSpec := spec
	foreignSpec.ProvisioningID = "provision-foreign"
	if _, err := manager.Begin(t.Context(), foreign, foreignSpec); !errors.Is(err, authority.ErrAdmissionDenied) {
		t.Fatalf("foreign authority error = %v, want admission denied", err)
	}

	wrongWorkspace := issueBeginAuthority(t, manager.issuer, "OTHER", "operator:trusted")
	wrongWorkspaceSpec := spec
	wrongWorkspaceSpec.ProvisioningID = "provision-other"
	if _, err := manager.Begin(t.Context(), wrongWorkspace, wrongWorkspaceSpec); !errors.Is(err, authority.ErrAdmissionDenied) {
		t.Fatalf("wrong-workspace authority error = %v, want admission denied", err)
	}
}

func TestPermanentFailureIsTerminalWhileUnavailableFailureRecovers(t *testing.T) {
	store := newMemoryProgressStore()
	operations := newIdempotentOperations()
	now := time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC)
	manager := newTestManager(t, store, operations, nil, &now)

	permanent := validSpec()
	operations.failures[commandID(permanent.ProvisioningID, StepBinding, "")] =
		fmt.Errorf("binding definition rejected: %w", ErrInvalid)
	if _, err := manager.begin(t, permanent); err != nil {
		t.Fatalf("Begin permanent: %v", err)
	}
	record, err := manager.Run(t.Context(), permanent.WorkspaceKey, permanent.ProvisioningID)
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("permanent Run error = %v, want ErrInvalid", err)
	}
	if record == nil || record.State != StatePermanentFailed || record.LastErrorClass != "binding_invalid" {
		t.Fatalf("permanent record = %+v", record)
	}
	delete(operations.failures, commandID(permanent.ProvisioningID, StepBinding, ""))
	callsBefore := len(operations.payloads)
	record, err = manager.Run(t.Context(), permanent.WorkspaceKey, permanent.ProvisioningID)
	if !errors.Is(err, ErrPermanentFailure) || record == nil || record.State != StatePermanentFailed {
		t.Fatalf("terminal replay = %+v, %v", record, err)
	}
	if len(operations.payloads) != callsBefore {
		t.Fatal("terminal replay invoked a capability operation")
	}

	retryable := validSpec()
	retryable.ProvisioningID = "provision-retryable"
	retryable.Agent.AgentID = "agent-retryable"
	retryable.Binding.BindingID = "binding-retryable"
	operations.failures[commandID(retryable.ProvisioningID, StepBinding, "")] =
		fmt.Errorf("binding backend unavailable: %w", ErrUnavailable)
	if _, err := manager.begin(t, retryable); err != nil {
		t.Fatalf("Begin retryable: %v", err)
	}
	record, err = manager.Run(t.Context(), retryable.WorkspaceKey, retryable.ProvisioningID)
	if !errors.Is(err, ErrUnavailable) || record == nil || record.State != StateRetryableFailed ||
		record.LastErrorClass != "binding_unavailable" {
		t.Fatalf("retryable record = %+v, %v", record, err)
	}
	delete(operations.failures, commandID(retryable.ProvisioningID, StepBinding, ""))
	count, err := manager.Recover(t.Context(), retryable.WorkspaceKey, 10)
	if err != nil || count != 1 {
		t.Fatalf("Recover retryable = %d, %v", count, err)
	}
	recovered, err := store.Get(t.Context(), retryable.WorkspaceKey, retryable.ProvisioningID)
	if err != nil || recovered.State != StateCompleted {
		t.Fatalf("recovered retryable record = %+v, %v", recovered, err)
	}
}

func TestRecoverConvergesEveryPendingRecord(t *testing.T) {
	store := newMemoryProgressStore()
	operations := newIdempotentOperations()
	now := time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC)
	manager := newTestManager(t, store, operations, nil, &now)
	for index := 1; index <= 2; index++ {
		spec := validSpec()
		spec.ProvisioningID = fmt.Sprintf("provision-%d", index)
		spec.Agent.AgentID = fmt.Sprintf("agent-%d", index)
		spec.Binding.BindingID = fmt.Sprintf("binding-%d", index)
		for grantIndex := range spec.Grants {
			spec.Grants[grantIndex].GrantID += fmt.Sprintf("-%d", index)
		}
		if _, err := manager.begin(t, spec); err != nil {
			t.Fatalf("Begin(%d): %v", index, err)
		}
	}
	count, err := manager.Recover(t.Context(), "TEST", 10)
	if err != nil {
		t.Fatalf("Recover: %v", err)
	}
	if count != 2 {
		t.Fatalf("Recover count = %d, want 2", count)
	}
}

func TestRecoverContinuesAcrossCorruptAndPermanentRecordsAndJoinsFailures(t *testing.T) {
	store := newMemoryProgressStore()
	operations := newIdempotentOperations()
	now := time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC)
	manager := newTestManager(t, store, operations, nil, &now)

	specs := make([]Spec, 3)
	for index := range specs {
		specs[index] = validSpec()
		specs[index].ProvisioningID = fmt.Sprintf("provision-%d", index+1)
		specs[index].Agent.AgentID = fmt.Sprintf("agent-%d", index+1)
		specs[index].Binding.BindingID = fmt.Sprintf("binding-%d", index+1)
		if _, err := manager.begin(t, specs[index]); err != nil {
			t.Fatalf("Begin(%d): %v", index+1, err)
		}
	}

	corruptKey := recordKey(specs[0].WorkspaceKey, specs[0].ProvisioningID)
	store.records[corruptKey].State = StateRunning
	store.records[corruptKey].CompletedSteps = []Step{StepAgent}
	operations.failures[commandID(specs[1].ProvisioningID, StepBinding, "")] =
		fmt.Errorf("invalid binding: %w", ErrInvalid)

	count, err := manager.Recover(t.Context(), "TEST", 10)
	if count != 1 {
		t.Fatalf("Recover count = %d, want one successful record", count)
	}
	if !errors.Is(err, ErrConflict) || !errors.Is(err, ErrInvalid) {
		t.Fatalf("Recover joined error = %v, want corrupt and invalid causes", err)
	}

	permanent, getErr := store.Get(t.Context(), "TEST", specs[1].ProvisioningID)
	if getErr != nil || permanent.State != StatePermanentFailed {
		t.Fatalf("permanent record = %+v, %v", permanent, getErr)
	}
	completed, getErr := store.Get(t.Context(), "TEST", specs[2].ProvisioningID)
	if getErr != nil || completed.State != StateCompleted {
		t.Fatalf("later record did not complete = %+v, %v", completed, getErr)
	}
	pending, listErr := store.ListPending(t.Context(), "TEST", 10)
	if listErr != nil || len(pending) != 1 || pending[0].ProvisioningID != specs[0].ProvisioningID {
		t.Fatalf("pending records after recovery = %+v, %v", pending, listErr)
	}
}

func TestProgressValidationRejectsRegressionAndIncompleteCompletion(t *testing.T) {
	store := newMemoryProgressStore()
	operations := newIdempotentOperations()
	now := time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC)
	manager := newTestManager(t, store, operations, nil, &now)
	pending, err := manager.begin(t, validSpec())
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}

	running := cloneRecord(pending)
	running.State = StateRunning
	if err := validateTransition(pending, running); err != nil {
		t.Fatalf("pending -> running transition: %v", err)
	}
	withRole := cloneRecord(running)
	withRole.CompletedSteps = []Step{StepRole}
	if err := validateTransition(running, withRole); err != nil {
		t.Fatalf("running progress transition: %v", err)
	}
	regressed := cloneRecord(withRole)
	regressed.CompletedSteps = nil
	if err := validateTransition(withRole, regressed); !errors.Is(err, ErrConflict) {
		t.Fatalf("regression error = %v, want ErrConflict", err)
	}

	incomplete := cloneRecord(withRole)
	incomplete.State = StateCompleted
	completedAt := now
	incomplete.CompletedAt = &completedAt
	if err := validateTransition(withRole, incomplete); !errors.Is(err, ErrConflict) {
		t.Fatalf("incomplete completion error = %v, want ErrConflict", err)
	}

	inconsistentFailure := cloneRecord(withRole)
	inconsistentFailure.State = StateRetryableFailed
	inconsistentFailure.LastErrorClass = "role_unavailable"
	if err := validateTransition(withRole, inconsistentFailure); !errors.Is(err, ErrConflict) {
		t.Fatalf("inconsistent failure error = %v, want ErrConflict", err)
	}

	changedGeneration := cloneRecord(withRole)
	changedGeneration.ProvisioningGenerationID = "fedcba9876543210fedcba9876543210"
	if err := validateTransition(withRole, changedGeneration); !errors.Is(err, ErrConflict) {
		t.Fatalf("generation mutation error = %v, want ErrConflict", err)
	}
}

func TestProgressTransitionTreatsEmptyJSONCollectionsAsCanonicalIntent(t *testing.T) {
	store := newMemoryProgressStore()
	operations := newIdempotentOperations()
	now := time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC)
	manager := newTestManager(t, store, operations, nil, &now)
	spec := validSpec()
	spec.Grants = nil
	pending, err := manager.begin(t, spec)
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}

	// JSON transports preserve [] as a non-nil empty slice, while clone helpers
	// may canonicalize it to nil. Both represent the same immutable intent.
	pending.Spec.Role.PathPatterns = []string{}
	pending.Spec.Binding.EventPatterns = []string{}
	pending.Spec.Grants = []GrantSpec{}
	running := cloneRecord(pending)
	running.State = StateRunning
	if err := validateTransition(pending, running); err != nil {
		t.Fatalf("empty collection representation changed canonical intent: %v", err)
	}
}

func TestManagerRejectsMissingServerMintedProvisioningGeneration(t *testing.T) {
	store := newMemoryProgressStore()
	store.generation = ""
	operations := newIdempotentOperations()
	now := time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC)
	manager := newTestManager(t, store, operations, nil, &now)

	if _, err := manager.begin(t, validSpec()); !errors.Is(err, ErrConflict) {
		t.Fatalf("Begin without server generation = %v, want ErrConflict", err)
	}
	if len(operations.calls) != 0 {
		t.Fatal("invalid durable generation reached a capability operation")
	}
}

func TestManagerPausedAfterRunningSaveCannotMutateSameKeyWorkspaceReplacement(t *testing.T) {
	store := newMemoryProgressStore()
	paused := &pauseAfterRunningProgressStore{
		memoryProgressStore: store,
		committed:           make(chan struct{}),
		resume:              make(chan struct{}),
	}
	operations := &generationGuardedOperations{store: store}
	now := time.Date(2026, 7, 30, 12, 0, 0, 0, time.UTC)
	issuer := authority.NewIssuer()
	admission, err := issuer.NewAdmission(OperationRules()...)
	if err != nil {
		t.Fatal(err)
	}
	manager, err := New(
		paused,
		operations,
		operations,
		operations,
		operations,
		admission,
		nil,
		func() time.Time { return now },
	)
	if err != nil {
		t.Fatal(err)
	}
	testManager := &testManager{Manager: manager, issuer: issuer}
	spec := validSpec()
	begun, err := testManager.begin(t, spec)
	if err != nil {
		t.Fatal(err)
	}

	runResult := make(chan error, 1)
	go func() {
		_, runErr := manager.Run(t.Context(), spec.WorkspaceKey, spec.ProvisioningID)
		runResult <- runErr
	}()
	select {
	case <-paused.committed:
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not pause after committing running progress")
	}

	const replacementGeneration = "fedcba9876543210fedcba9876543210"
	store.mu.Lock()
	replacement := cloneRecord(store.records[recordKey(spec.WorkspaceKey, spec.ProvisioningID)])
	replacement.ProvisioningGenerationID = replacementGeneration
	replacement.State = StatePending
	replacement.CompletedSteps = nil
	replacement.CompletedGrants = nil
	replacement.Version = 1
	replacement.CreatedAt = now.Add(time.Second)
	replacement.UpdatedAt = replacement.CreatedAt
	store.records[recordKey(spec.WorkspaceKey, spec.ProvisioningID)] = replacement
	store.mu.Unlock()
	close(paused.resume)

	select {
	case runErr := <-runResult:
		if !errors.Is(runErr, ErrConcurrentWrite) {
			t.Fatalf("stale Run error = %v, want concurrent write", runErr)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("stale Run did not return after replacement")
	}
	if operations.calls != 1 ||
		operations.lastGeneration != begun.ProvisioningGenerationID ||
		operations.mutations != 0 {
		t.Fatalf(
			"guarded operations = calls:%d generation:%q mutations:%d",
			operations.calls,
			operations.lastGeneration,
			operations.mutations,
		)
	}
	current, err := store.Get(t.Context(), spec.WorkspaceKey, spec.ProvisioningID)
	if err != nil {
		t.Fatal(err)
	}
	if current.ProvisioningGenerationID != replacementGeneration ||
		current.State != StatePending ||
		current.Version != 1 {
		t.Fatalf("replacement record was changed by stale Run: %+v", current)
	}
}

type testManager struct {
	*Manager
	issuer *authority.Issuer
}

func newTestManager(
	t *testing.T,
	store ProgressStore,
	operations *idempotentOperations,
	faults FaultInjector,
	now *time.Time,
) *testManager {
	t.Helper()
	issuer := authority.NewIssuer()
	admission, err := issuer.NewAdmission(OperationRules()...)
	if err != nil {
		t.Fatalf("NewAdmission: %v", err)
	}
	manager, err := New(
		store,
		operations,
		operations,
		operations,
		operations,
		admission,
		faults,
		func() time.Time { return *now },
	)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return &testManager{Manager: manager, issuer: issuer}
}

func (manager *testManager) begin(t *testing.T, spec Spec) (*Record, error) {
	t.Helper()
	return manager.Begin(
		t.Context(),
		issueBeginAuthority(t, manager.issuer, spec.WorkspaceKey, "operator:test"),
		spec,
	)
}

func issueBeginAuthority(
	t *testing.T,
	issuer *authority.Issuer,
	workspace,
	subject string,
) authority.OperatorAuthority {
	t.Helper()
	principal, err := issuer.DeriveVerifiedPrincipal(authority.PrincipalClaims{
		Subject: subject, Class: authority.ClassOperator, Workspace: workspace,
		Actions: []authority.Action{ActionBeginProvisioning}, ExpiresAt: time.Now().Add(time.Hour),
	})
	if err != nil {
		t.Fatalf("DeriveVerifiedPrincipal: %v", err)
	}
	auth, err := issuer.IssueOperator(principal, workspace, ActionBeginProvisioning)
	if err != nil {
		t.Fatalf("IssueOperator: %v", err)
	}
	return auth
}

func validSpec() Spec {
	maxPriority := 7
	maxConcurrency := 2
	maxBudgetUSD := 25.5
	return Spec{
		ProvisioningID: "provision-1", WorkspaceKey: "TEST",
		Role: RoleSpec{
			Name: "docs-review", Kind: "worker", Prompt: "Review and update documentation.",
			TaskFilter: "status=review", PathPatterns: []string{"docs/**", "README.md"},
			Skills: []string{"documentation", "review"}, MaxPriority: &maxPriority,
			MaxConcurrency: &maxConcurrency, AllowedTools: []string{"Read", "Edit"},
			DeniedTools: []string{"Shell"}, MaxBudgetUSD: &maxBudgetUSD,
		},
		Agent: AgentSpec{
			AgentID: "agent-1", Name: "Documentation Review", Kind: "event",
			DesiredState: "running", RoleName: "docs-review",
			Metadata: map[string]string{"backend": "codex"},
		},
		Binding: BindingSpec{
			BindingID: "binding-1", Name: "review transition", SourceKind: "internal",
			SourceConfigRef: "role://docs-review?backend=codex",
			EventPatterns:   []string{"internal.task.review"},
			DriverID:        "prompt-agent", DriverVersionID: "prompt-agent-v1",
			Entrypoint: "run", ConcurrencyPolicy: "one_active_per_epic", Enabled: true,
		},
		Grants: []GrantSpec{
			{
				GrantID: "grant-read", ConnectorID: "github",
				Action: "pull_request.read", ResourcePattern: "repo:acme/docs",
			},
			{
				GrantID: "grant-write", ConnectorID: "github",
				Action: "contents.write", ResourcePattern: "repo:acme/docs",
			},
		},
	}
}

type memoryProgressStore struct {
	mu         sync.Mutex
	records    map[string]*Record
	order      []string
	generation string
}

func newMemoryProgressStore() *memoryProgressStore {
	return &memoryProgressStore{
		records:    map[string]*Record{},
		generation: "0123456789abcdef0123456789abcdef",
	}
}

func recordKey(workspace, provisioningID string) string { return workspace + "/" + provisioningID }

func (store *memoryProgressStore) Begin(_ context.Context, spec Spec, requestedBy string) (*Record, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	spec = normalizeSpec(spec)
	key := recordKey(spec.WorkspaceKey, spec.ProvisioningID)
	if existing := store.records[key]; existing != nil {
		if !reflect.DeepEqual(existing.Spec, spec) {
			return nil, ErrConflict
		}
		return cloneRecord(existing), nil
	}
	encoded, err := json.Marshal(spec)
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256(encoded)
	now := time.Date(2026, 7, 29, 11, 59, 0, 0, time.UTC)
	value := &Record{
		ProvisioningID:           spec.ProvisioningID,
		ProvisioningGenerationID: store.generation,
		WorkspaceKey:             spec.WorkspaceKey,
		RequestedBy:              requestedBy,
		SpecFingerprint:          fmt.Sprintf("sha256:%x", sum),
		Spec:                     cloneSpec(spec),
		State:                    StatePending,
		UnusedRolePolicy:         UnusedRoleRetain,
		Version:                  1,
		CreatedAt:                now,
		UpdatedAt:                now,
	}
	store.records[key] = value
	store.order = append(store.order, key)
	return cloneRecord(value), nil
}

func (store *memoryProgressStore) Get(_ context.Context, workspace, provisioningID string) (*Record, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	value := store.records[recordKey(workspace, provisioningID)]
	if value == nil {
		return nil, ErrNotFound
	}
	return cloneRecord(value), nil
}

func (store *memoryProgressStore) Save(_ context.Context, record *Record, expected int64) (*Record, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	key := recordKey(record.WorkspaceKey, record.ProvisioningID)
	current := store.records[key]
	if current == nil {
		return nil, ErrNotFound
	}
	if current.Version != expected {
		return nil, ErrConcurrentWrite
	}
	if current.ProvisioningGenerationID != record.ProvisioningGenerationID {
		return nil, ErrConcurrentWrite
	}
	value := cloneRecord(record)
	value.Version = expected + 1
	store.records[key] = value
	return cloneRecord(value), nil
}

func (store *memoryProgressStore) ListPending(_ context.Context, workspace string, limit int) ([]*Record, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	out := []*Record{}
	for _, key := range store.order {
		record := store.records[key]
		if record.WorkspaceKey == workspace && record.State.pendingRecovery() {
			out = append(out, cloneRecord(record))
			if limit > 0 && len(out) >= limit {
				break
			}
		}
	}
	return out, nil
}

type pauseAfterRunningProgressStore struct {
	*memoryProgressStore
	committed chan struct{}
	resume    chan struct{}
	once      sync.Once
}

func (store *pauseAfterRunningProgressStore) Save(
	ctx context.Context,
	record *Record,
	expected int64,
) (*Record, error) {
	saved, err := store.memoryProgressStore.Save(ctx, record, expected)
	if err != nil {
		return nil, err
	}
	if saved.State == StateRunning && len(saved.CompletedSteps) == 0 {
		store.once.Do(func() {
			close(store.committed)
			<-store.resume
		})
	}
	return saved, nil
}

type generationGuardedOperations struct {
	store          *memoryProgressStore
	calls          int
	mutations      int
	lastGeneration string
}

func (operations *generationGuardedOperations) EnsureRole(
	_ context.Context,
	command EnsureRoleCommand,
) error {
	return operations.ensure(
		command.WorkspaceKey,
		command.ProvisioningID,
		command.ProvisioningGenerationID,
	)
}

func (operations *generationGuardedOperations) EnsureAgent(
	_ context.Context,
	command EnsureAgentCommand,
) error {
	return operations.ensure(
		command.WorkspaceKey,
		command.ProvisioningID,
		command.ProvisioningGenerationID,
	)
}

func (operations *generationGuardedOperations) EnsureBinding(
	_ context.Context,
	command EnsureBindingCommand,
) error {
	return operations.ensure(
		command.WorkspaceKey,
		command.ProvisioningID,
		command.ProvisioningGenerationID,
	)
}

func (operations *generationGuardedOperations) EnsureGrant(
	_ context.Context,
	command EnsureGrantCommand,
) error {
	return operations.ensure(
		command.WorkspaceKey,
		command.ProvisioningID,
		command.ProvisioningGenerationID,
	)
}

func (operations *generationGuardedOperations) ensure(
	workspace,
	provisioningID,
	generationID string,
) error {
	operations.store.mu.Lock()
	defer operations.store.mu.Unlock()
	operations.calls++
	operations.lastGeneration = generationID
	current := operations.store.records[recordKey(workspace, provisioningID)]
	if current == nil || current.ProvisioningGenerationID != generationID {
		return ErrConcurrentWrite
	}
	operations.mutations++
	return nil
}

type idempotentOperations struct {
	mu       sync.Mutex
	calls    map[string]int
	unique   map[string]int
	payloads map[string]string
	failStep Step
	failures map[string]error
}

func newIdempotentOperations() *idempotentOperations {
	return &idempotentOperations{
		calls: map[string]int{}, unique: map[string]int{}, payloads: map[string]string{},
		failures: map[string]error{},
	}
}

func (operations *idempotentOperations) EnsureRole(_ context.Context, command EnsureRoleCommand) error {
	if operations.failStep == StepRole {
		return errors.New("role unavailable")
	}
	if err := operations.failures[command.CommandID]; err != nil {
		return err
	}
	return operations.ensure(command.CommandID, command)
}

func (operations *idempotentOperations) EnsureAgent(_ context.Context, command EnsureAgentCommand) error {
	if operations.failStep == StepAgent {
		return errors.New("agent unavailable")
	}
	if err := operations.failures[command.CommandID]; err != nil {
		return err
	}
	return operations.ensure(command.CommandID, command)
}

func (operations *idempotentOperations) EnsureBinding(_ context.Context, command EnsureBindingCommand) error {
	if operations.failStep == StepBinding {
		return errors.New("binding unavailable")
	}
	if err := operations.failures[command.CommandID]; err != nil {
		return err
	}
	return operations.ensure(command.CommandID, command)
}

func (operations *idempotentOperations) EnsureGrant(_ context.Context, command EnsureGrantCommand) error {
	if operations.failStep == StepGrant {
		return errors.New("grant unavailable")
	}
	if err := operations.failures[command.CommandID]; err != nil {
		return err
	}
	return operations.ensure(command.CommandID, command)
}

func (operations *idempotentOperations) ensure(commandID string, command any) error {
	operations.mu.Lock()
	defer operations.mu.Unlock()
	encoded, err := json.Marshal(command)
	if err != nil {
		return err
	}
	operations.calls[commandID]++
	payload := string(encoded)
	if existing, ok := operations.payloads[commandID]; ok {
		if existing != payload {
			return ErrConflict
		}
		return nil
	}
	operations.payloads[commandID] = payload
	operations.unique[commandID]++
	return nil
}

type failOnceAfter struct {
	step  Step
	key   string
	fired bool
}

func (fault *failOnceAfter) AfterExternalCommit(step Step, key string) error {
	if !fault.fired && fault.step == step && fault.key == key {
		fault.fired = true
		return errors.New("simulated process crash")
	}
	return nil
}
