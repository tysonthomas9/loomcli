package agentdef

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"

	"github.com/tysonthomas9/loomcli/internal/cli/managementapi"
	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/modules/agents"
	"github.com/tysonthomas9/loomcli/internal/platform/authority"
)

func TestRunAgentAddRoutesCanonicalIdentityThroughBoundary(t *testing.T) {
	resetAgentdefFlags(t)
	definitions := &agentDefinitionBoundaryStub{}
	installAgentdefTestRuntime(t, agentdefRuntime{definitions: definitions})

	agentAddRole = "coder"
	agentAddAuto = true
	agentAddMode = "service"
	agentAddProfile = "coder-profile"
	agentAddMaxConc = 3
	agentAddBudget = "bounded"

	command := &cobra.Command{}
	if err := runAgentAdd(command, []string{"coder-one"}); err != nil {
		t.Fatalf("runAgentAdd: %v", err)
	}
	if len(definitions.creates) != 1 {
		t.Fatalf("create calls = %d, want 1", len(definitions.creates))
	}
	got := definitions.creates[0]
	if got.Canonical.WorkspaceKey != "TEST" ||
		got.Canonical.AgentID != "coder-one" ||
		got.Canonical.Name != "coder-one" ||
		got.Canonical.Behavior.RoleName != "coder" ||
		got.Canonical.Kind != agents.AgentKindAlwaysOn ||
		got.Canonical.DesiredState != agents.DesiredRunning ||
		got.Canonical.ProfileName != "coder-profile" ||
		got.Canonical.MaxInstances != 3 ||
		got.Canonical.RestartPolicy != "always" ||
		got.Canonical.BudgetPolicy != "bounded" {
		t.Fatalf("create command = %+v, want canonical service agent", got)
	}
}

func TestRunAgentStartStopAndRemoveUseAtomicLifecycleBoundary(t *testing.T) {
	tests := []struct {
		name      string
		run       func() error
		want      agents.LifecycleAction
		requestID string
	}{
		{
			name: "start",
			run: func() error {
				agentStartRequestID = testBoundLifecycleRequestID("start-request-1")
				return runAgentStart(&cobra.Command{}, []string{"worker-one"})
			},
			want:      agents.LifecycleEnable,
			requestID: testBoundLifecycleRequestID("start-request-1"),
		},
		{
			name: "stop",
			run: func() error {
				agentStopRequestID = testBoundLifecycleRequestID("stop-request-1")
				return runAgentStop(&cobra.Command{}, []string{"worker-one"})
			},
			want:      agents.LifecycleDisable,
			requestID: testBoundLifecycleRequestID("stop-request-1"),
		},
		{
			name: "remove",
			run: func() error {
				agentRemoveRequestID = testBoundLifecycleRequestID("remove-request-1")
				return runAgentRemove(&cobra.Command{}, []string{"worker-one"})
			},
			want:      agents.LifecycleDelete,
			requestID: testBoundLifecycleRequestID("remove-request-1"),
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			resetAgentdefFlags(t)
			definitions := &agentDefinitionBoundaryStub{}
			installAgentdefTestRuntime(t, agentdefRuntime{definitions: definitions})

			if err := test.run(); err != nil {
				t.Fatalf("run command: %v", err)
			}
			if len(definitions.lifecycle) != 1 {
				t.Fatalf("lifecycle calls = %d, want 1", len(definitions.lifecycle))
			}
			got := definitions.lifecycle[0]
			if got.WorkspaceKey != "TEST" ||
				got.AgentID != "worker-one" ||
				got.Action != test.want ||
				got.RequestID != test.requestID {
				t.Fatalf("lifecycle command = %+v", got)
			}
		})
	}
}

func TestRunAgentRemoveDoesNotInventReceiptLookupForMissingAgent(t *testing.T) {
	resetAgentdefFlags(t)
	definitions := &agentDefinitionBoundaryStub{getErr: agents.ErrNotFound}
	installAgentdefTestRuntime(t, agentdefRuntime{definitions: definitions})

	err := runAgentRemove(&cobra.Command{}, []string{"missing-agent"})
	if !errors.Is(err, agents.ErrNotFound) ||
		!strings.Contains(err.Error(), "requires the generation-bound request-id") {
		t.Fatalf("runAgentRemove error = %v", err)
	}
	if len(definitions.lifecycle) != 0 {
		t.Fatalf("lifecycle calls = %d, want no receipt lookup", len(definitions.lifecycle))
	}
}

func TestAgentdefRejectsInheritedBackendFlagBeforeCommandRun(t *testing.T) {
	root := &cobra.Command{Use: "loom"}
	parent := &cobra.Command{Use: "agentdef"}
	add := &cobra.Command{Use: "add"}
	root.PersistentFlags().String("backend", "", "global backend")
	root.AddCommand(parent)
	parent.AddCommand(add)
	if err := root.PersistentFlags().Set("backend", "codex"); err != nil {
		t.Fatal(err)
	}
	if err := rejectAgentdefBackendFlag(add); err == nil ||
		!strings.Contains(err.Error(), "loom role set") ||
		!strings.Contains(err.Error(), "loom worker profile add") {
		t.Fatalf("backend guard error = %v", err)
	}
}

func TestResolveLifecycleRequestID(t *testing.T) {
	t.Run("explicit generation-bound id", func(t *testing.T) {
		want := testBoundLifecycleRequestID("operation:one_2.3-4")
		got, err := resolveLifecycleRequestID("  " + want + "  ")
		if err != nil {
			t.Fatalf("resolve explicit request ID: %v", err)
		}
		if got != want {
			t.Fatalf("request ID = %q", got)
		}
	})

	t.Run("explicit unbound id rejected", func(t *testing.T) {
		if _, err := resolveLifecycleRequestID("operation:one_2.3-4"); err == nil ||
			!strings.Contains(err.Error(), "omit the flag") {
			t.Fatalf("unbound explicit request ID error = %v", err)
		}
	})

	t.Run("generated when absent", func(t *testing.T) {
		got, err := resolveLifecycleRequestID("")
		if err != nil {
			t.Fatalf("generate request ID: %v", err)
		}
		if !strings.HasPrefix(got, "req-") || len(got) != len("req-")+32 {
			t.Fatalf("generated request ID = %q", got)
		}
		if normalized, err := normalizeLifecycleRequestID(got); err != nil || normalized != got {
			t.Fatalf("generated request ID is not canonical: normalized=%q err=%v", normalized, err)
		}
	})

	for _, test := range []struct {
		name  string
		value string
	}{
		{name: "too long", value: strings.Repeat("a", lifecycleRequestIDMaxLength+1)},
		{name: "newline", value: "operation\nforged-output"},
		{name: "space", value: "operation one"},
		{name: "non ascii", value: "opération"},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, err := resolveLifecycleRequestID(test.value); err == nil {
				t.Fatalf("resolveLifecycleRequestID(%q) succeeded", test.value)
			}
		})
	}
}

func TestRetiredCrossCapabilityFlagsAreAbsentAndDocumented(t *testing.T) {
	for _, name := range []string{
		"backend",
		"repos",
		"repo-groups",
		"cross-repo",
		"parent",
		"task-filter",
		"task",
		"orchestrator",
	} {
		if flag := agentAddCmd.Flags().Lookup(name); flag != nil {
			t.Errorf("agentdef add still exposes retired --%s flag", name)
		}
	}
	if flag := agentStopCmd.Flags().Lookup("force"); flag != nil {
		t.Error("agentdef stop still exposes retired --force flag")
	}
	for _, guidance := range []string{
		"loom role",
		"loom worker profile",
		"--repo-groups and --cross-repo",
		"loom data agent stop <NAME> --force",
	} {
		if !strings.Contains(agentdefCmd.Long+"\n"+agentStopCmd.Long, guidance) {
			t.Errorf("agentdef help missing migration guidance %q", guidance)
		}
	}
}

func TestCanonicalBoundaryUsesAgentsCreateAuthority(t *testing.T) {
	api := &canonicalAgentsAPIStub{}
	resolver := &operatorAuthorityResolverStub{}
	boundary, err := newCanonicalAgentDefinitionBoundary(api, resolver)
	if err != nil {
		t.Fatalf("newCanonicalAgentDefinitionBoundary: %v", err)
	}

	created, err := boundary.CreateAgentDefinition(t.Context(), AgentDefinitionCreateCommand{
		Canonical: canonicalCreateCommand("TEST", "worker-two", agents.DesiredRunning),
	})
	if err != nil {
		t.Fatalf("CreateAgentDefinition: %v", err)
	}
	if created.AgentID != "worker-two" || len(api.creates) != 1 {
		t.Fatalf("created = %+v, calls = %d", created, len(api.creates))
	}
	if len(resolver.actions) != 1 ||
		resolver.actions[0] != agents.ActionCreateAgent {
		t.Fatalf("resolved actions = %v", resolver.actions)
	}
}

func TestCanonicalBoundaryUsesSingleAtomicLifecycleCommand(t *testing.T) {
	now := time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC)
	api := &canonicalAgentsAPIStub{
		current: canonicalAgent(canonicalCreateCommand("TEST", "worker-one", agents.DesiredStopped), now),
	}
	resolver := &operatorAuthorityResolverStub{}
	boundary, err := newCanonicalAgentDefinitionBoundary(api, resolver)
	if err != nil {
		t.Fatalf("newCanonicalAgentDefinitionBoundary: %v", err)
	}

	record, err := boundary.ApplyAgentLifecycle(t.Context(), AgentLifecycleCommand{
		WorkspaceKey: "TEST",
		AgentID:      "worker-one",
		Action:       agents.LifecycleEnable,
		RequestID:    testBoundLifecycleRequestID("enable-request-1"),
	})
	if err != nil {
		t.Fatalf("ApplyAgentLifecycle: %v", err)
	}
	if record.DesiredState != agents.DesiredRunning || len(api.lifecycle) != 1 {
		t.Fatalf("record/calls = %+v/%d", record, len(api.lifecycle))
	}
	command := api.lifecycle[0]
	if command.WorkspaceKey != "TEST" ||
		command.AgentID != "worker-one" ||
		command.Action != agents.LifecycleEnable ||
		!command.ExpectedUpdatedAt.Equal(now) ||
		!strings.HasPrefix(command.IdempotencyKey, "agentdef-enable-") {
		t.Fatalf("lifecycle command = %+v", command)
	}
	if len(resolver.actions) != 1 || resolver.actions[0] != agents.ActionApplyLifecycle {
		t.Fatalf("resolved actions = %v", resolver.actions)
	}
}

func TestCanonicalBoundaryDeleteReplaysAfterIdentityNotFound(t *testing.T) {
	now := time.Date(2026, 7, 30, 12, 0, 0, 0, time.UTC)
	api := &canonicalAgentsAPIStub{
		current: canonicalAgent(
			canonicalCreateCommand("TEST", "worker-one", agents.DesiredRunning),
			now,
		),
	}
	boundary, err := newCanonicalAgentDefinitionBoundary(api, &operatorAuthorityResolverStub{})
	if err != nil {
		t.Fatalf("newCanonicalAgentDefinitionBoundary: %v", err)
	}
	command := AgentLifecycleCommand{
		WorkspaceKey: "TEST",
		AgentID:      "worker-one",
		Action:       agents.LifecycleDelete,
		RequestID:    testBoundLifecycleRequestID("delete-worker-one"),
	}

	if _, err := boundary.ApplyAgentLifecycle(t.Context(), command); err != nil {
		t.Fatalf("first delete: %v", err)
	}
	api.getErr = agents.ErrNotFound
	if _, err := boundary.ApplyAgentLifecycle(t.Context(), command); err != nil {
		t.Fatalf("replayed delete after identity disappeared: %v", err)
	}
	if len(api.lifecycle) != 2 {
		t.Fatalf("lifecycle calls = %d, want 2", len(api.lifecycle))
	}
	first, replay := api.lifecycle[0], api.lifecycle[1]
	if first.IdempotencyKey != replay.IdempotencyKey {
		t.Fatalf("delete keys differ across lost-response replay: %q != %q", first.IdempotencyKey, replay.IdempotencyKey)
	}
	if !first.ExpectedUpdatedAt.Equal(now) ||
		!replay.ExpectedUpdatedAt.Equal(deleteReplayRevisionSentinel) ||
		replay.ExpectedUpdatedAt.IsZero() {
		t.Fatalf("delete revisions first=%s replay=%s", first.ExpectedUpdatedAt, replay.ExpectedUpdatedAt)
	}

	neverExisting := &canonicalAgentsAPIStub{
		getErr:       agents.ErrNotFound,
		lifecycleErr: agents.ErrNotFound,
	}
	missingBoundary, err := newCanonicalAgentDefinitionBoundary(
		neverExisting,
		&operatorAuthorityResolverStub{},
	)
	if err != nil {
		t.Fatalf("new missing canonical boundary: %v", err)
	}
	if _, err := missingBoundary.ApplyAgentLifecycle(t.Context(), AgentLifecycleCommand{
		WorkspaceKey: "TEST",
		AgentID:      "never-existed",
		Action:       agents.LifecycleDelete,
		RequestID:    testBoundLifecycleRequestID("delete-never-existed"),
	}); !errors.Is(err, agents.ErrNotFound) {
		t.Fatalf("never-existing delete error = %v, want agents.ErrNotFound", err)
	}
	if len(neverExisting.lifecycle) != 1 {
		t.Fatalf("never-existing delete lifecycle calls = %d, want receipt lookup", len(neverExisting.lifecycle))
	}
}

func TestManagementBoundaryDeleteReplaysAfterIdentityNotFound(t *testing.T) {
	now := time.Date(2026, 7, 30, 13, 0, 0, 0, time.UTC)
	client := &agentManagementClientStub{
		workspace: "TEST",
		current: canonicalAgent(
			canonicalCreateCommand("TEST", "worker-one", agents.DesiredRunning),
			now,
		),
	}
	boundary := &managementAgentDefinitionBoundary{client: client}
	command := AgentLifecycleCommand{
		WorkspaceKey: "TEST",
		AgentID:      "worker-one",
		Action:       agents.LifecycleDelete,
		RequestID:    testBoundLifecycleRequestID("delete-worker-one"),
	}

	if _, err := boundary.ApplyAgentLifecycle(t.Context(), command); err != nil {
		t.Fatalf("first management delete: %v", err)
	}
	client.getErr = domain.ErrNotFound
	if _, err := boundary.ApplyAgentLifecycle(t.Context(), command); err != nil {
		t.Fatalf("management replay after identity disappeared: %v", err)
	}
	if len(client.lifecycle) != 2 {
		t.Fatalf("management lifecycle calls = %d, want 2", len(client.lifecycle))
	}
	first, replay := client.lifecycle[0], client.lifecycle[1]
	if first.IdempotencyKey != replay.IdempotencyKey {
		t.Fatalf("management delete keys differ: %q != %q", first.IdempotencyKey, replay.IdempotencyKey)
	}
	if !replay.ExpectedUpdatedAt.Equal(deleteReplayRevisionSentinel) ||
		replay.ExpectedUpdatedAt.IsZero() {
		t.Fatalf("management replay revision = %s", replay.ExpectedUpdatedAt)
	}

	client.lifecycleErr = domain.ErrNotFound
	if _, err := boundary.ApplyAgentLifecycle(t.Context(), AgentLifecycleCommand{
		WorkspaceKey: "TEST",
		AgentID:      "never-existed",
		Action:       agents.LifecycleDelete,
		RequestID:    testBoundLifecycleRequestID("delete-never-existed"),
	}); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("never-existing management delete error = %v, want domain.ErrNotFound", err)
	}
}

func TestLifecycleIdempotencyKeysAreOperationScoped(t *testing.T) {
	var firstActionKey string
	for _, action := range []agents.LifecycleAction{
		agents.LifecycleEnable,
		agents.LifecycleDisable,
		agents.LifecycleDelete,
	} {
		command := AgentLifecycleCommand{
			WorkspaceKey: "TEST",
			AgentID:      "worker-one",
			Action:       action,
			RequestID:    testBoundLifecycleRequestID("operation-one"),
		}
		firstKey := lifecycleIdempotencyKey(command)
		if firstKey != lifecycleIdempotencyKey(command) {
			t.Fatalf("%s idempotency key is not stable for one operation", action)
		}
		command.RequestID = testBoundLifecycleRequestID("operation-two")
		if firstKey == lifecycleIdempotencyKey(command) {
			t.Fatalf("%s idempotency key did not change for a new operation", action)
		}
		if firstActionKey != "" && firstActionKey == firstKey {
			t.Fatalf("enable and disable share idempotency key %q", firstKey)
		}
		firstActionKey = firstKey
	}
}

func TestLifecycleBoundaryNeverRebindsRetryTokenToReplacementGeneration(t *testing.T) {
	now := time.Date(2026, 7, 30, 15, 0, 0, 0, time.UTC)
	const replacementGeneration = "ffeeddccbbaa99887766554433221100"
	oldToken := testBoundLifecycleRequestID("old-operation")
	freshToken, err := bindLifecycleRequestID("fresh-operation", replacementGeneration)
	if err != nil {
		t.Fatal(err)
	}
	replacement := canonicalAgent(
		canonicalCreateCommand("TEST", "worker-one", agents.DesiredRunning),
		now,
	)
	replacement.GenerationID = replacementGeneration

	t.Run("canonical", func(t *testing.T) {
		api := &canonicalAgentsAPIStub{
			current:      replacement,
			lifecycleErr: agents.ErrConflict,
		}
		boundary, err := newCanonicalAgentDefinitionBoundary(
			api,
			&operatorAuthorityResolverStub{},
		)
		if err != nil {
			t.Fatal(err)
		}
		oldCommand := AgentLifecycleCommand{
			WorkspaceKey: "TEST", AgentID: "worker-one",
			Action: agents.LifecycleDisable, RequestID: oldToken,
		}
		if _, err := boundary.ApplyAgentLifecycle(t.Context(), oldCommand); !errors.Is(
			err,
			agents.ErrConflict,
		) {
			t.Fatalf("old-token replacement error=%v, want conflict", err)
		}
		if got := api.lifecycle[0].ExpectedGenerationID; got != testAgentdefGenerationID {
			t.Fatalf("old token rebound to generation %q", got)
		}
		api.lifecycleErr = nil
		oldCommand.RequestID = freshToken
		result, err := boundary.ApplyAgentLifecycle(t.Context(), oldCommand)
		if err != nil || result.GenerationID != replacementGeneration {
			t.Fatalf("fresh replacement operation=%+v err=%v", result, err)
		}
	})

	t.Run("management", func(t *testing.T) {
		client := &agentManagementClientStub{
			workspace:    "TEST",
			current:      replacement,
			lifecycleErr: domain.ErrConflict,
		}
		boundary := &managementAgentDefinitionBoundary{client: client}
		oldCommand := AgentLifecycleCommand{
			WorkspaceKey: "TEST", AgentID: "worker-one",
			Action: agents.LifecycleDisable, RequestID: oldToken,
		}
		if _, err := boundary.ApplyAgentLifecycle(t.Context(), oldCommand); !errors.Is(
			err,
			domain.ErrConflict,
		) {
			t.Fatalf("old-token replacement error=%v, want conflict", err)
		}
		if got := client.lifecycle[0].ExpectedGenerationID; got != testAgentdefGenerationID {
			t.Fatalf("old token rebound to generation %q", got)
		}
		client.lifecycleErr = nil
		oldCommand.RequestID = freshToken
		result, err := boundary.ApplyAgentLifecycle(t.Context(), oldCommand)
		if err != nil || result.GenerationID != replacementGeneration {
			t.Fatalf("fresh replacement operation=%+v err=%v", result, err)
		}
	})
}

func TestCanonicalBoundaryRetriesLostLifecycleResponseAndReplaysRequest(t *testing.T) {
	initialRevision := time.Date(2026, 7, 30, 14, 0, 0, 0, time.UTC)
	responseLost := errors.New("response lost after commit")
	api := &canonicalAgentsAPIStub{
		current: canonicalAgent(
			canonicalCreateCommand("TEST", "worker-one", agents.DesiredStopped),
			initialRevision,
		),
		lifecycleErrors: []error{responseLost},
	}
	boundary, err := newCanonicalAgentDefinitionBoundary(
		api,
		&operatorAuthorityResolverStub{},
	)
	if err != nil {
		t.Fatalf("new canonical boundary: %v", err)
	}
	command := AgentLifecycleCommand{
		WorkspaceKey: "TEST",
		AgentID:      "worker-one",
		Action:       agents.LifecycleEnable,
		RequestID:    testBoundLifecycleRequestID("enable-operation-one"),
	}

	record, err := boundary.ApplyAgentLifecycle(t.Context(), command)
	if err != nil {
		t.Fatalf("retry lost response: %v", err)
	}
	if record.DesiredState != agents.DesiredRunning || len(api.lifecycle) != 2 {
		t.Fatalf("record/calls after bounded retry = %+v/%d", record, len(api.lifecycle))
	}
	first, retry := api.lifecycle[0], api.lifecycle[1]
	if first != retry {
		t.Fatalf("bounded retry changed command:\nfirst=%+v\nretry=%+v", first, retry)
	}

	advancedRevision := api.current.UpdatedAt
	if _, err := boundary.ApplyAgentLifecycle(t.Context(), command); err != nil {
		t.Fatalf("rerun same request ID: %v", err)
	}
	if len(api.lifecycle) != 3 {
		t.Fatalf("calls after rerun = %d, want 3", len(api.lifecycle))
	}
	replay := api.lifecycle[2]
	if replay.IdempotencyKey != first.IdempotencyKey {
		t.Fatalf("rerun key = %q, want %q", replay.IdempotencyKey, first.IdempotencyKey)
	}
	if !replay.ExpectedUpdatedAt.Equal(advancedRevision) ||
		replay.ExpectedUpdatedAt.Equal(first.ExpectedUpdatedAt) {
		t.Fatalf(
			"rerun revision = %s, first = %s, advanced = %s",
			replay.ExpectedUpdatedAt,
			first.ExpectedUpdatedAt,
			advancedRevision,
		)
	}

	command.RequestID = testBoundLifecycleRequestID("enable-operation-two")
	if _, err := boundary.ApplyAgentLifecycle(t.Context(), command); err != nil {
		t.Fatalf("new enable generation: %v", err)
	}
	next := api.lifecycle[3]
	if next.IdempotencyKey == first.IdempotencyKey {
		t.Fatalf("new operation reused key %q", next.IdempotencyKey)
	}
}

func TestManagementBoundaryRetriesLostLifecycleResponseAndReplaysRequest(t *testing.T) {
	initialRevision := time.Date(2026, 7, 30, 15, 0, 0, 0, time.UTC)
	responseLost := errors.New("response lost after commit")
	client := &agentManagementClientStub{
		workspace: "TEST",
		current: canonicalAgent(
			canonicalCreateCommand("TEST", "worker-one", agents.DesiredRunning),
			initialRevision,
		),
		lifecycleErrors: []error{responseLost},
	}
	boundary := &managementAgentDefinitionBoundary{client: client}
	command := AgentLifecycleCommand{
		WorkspaceKey: "TEST",
		AgentID:      "worker-one",
		Action:       agents.LifecycleDisable,
		RequestID:    testBoundLifecycleRequestID("disable-operation-one"),
	}

	record, err := boundary.ApplyAgentLifecycle(t.Context(), command)
	if err != nil {
		t.Fatalf("retry lost response: %v", err)
	}
	if record.DesiredState != agents.DesiredPaused || len(client.lifecycle) != 2 {
		t.Fatalf("record/calls after bounded retry = %+v/%d", record, len(client.lifecycle))
	}
	first, retry := client.lifecycle[0], client.lifecycle[1]
	if first != retry {
		t.Fatalf("bounded retry changed request:\nfirst=%+v\nretry=%+v", first, retry)
	}

	advancedRevision := client.current.UpdatedAt
	if _, err := boundary.ApplyAgentLifecycle(t.Context(), command); err != nil {
		t.Fatalf("rerun same request ID: %v", err)
	}
	if len(client.lifecycle) != 3 {
		t.Fatalf("calls after rerun = %d, want 3", len(client.lifecycle))
	}
	replay := client.lifecycle[2]
	if replay.IdempotencyKey != first.IdempotencyKey {
		t.Fatalf("rerun key = %q, want %q", replay.IdempotencyKey, first.IdempotencyKey)
	}
	if !replay.ExpectedUpdatedAt.Equal(advancedRevision) ||
		replay.ExpectedUpdatedAt.Equal(first.ExpectedUpdatedAt) {
		t.Fatalf(
			"rerun revision = %s, first = %s, advanced = %s",
			replay.ExpectedUpdatedAt,
			first.ExpectedUpdatedAt,
			advancedRevision,
		)
	}

	command.RequestID = testBoundLifecycleRequestID("disable-operation-two")
	if _, err := boundary.ApplyAgentLifecycle(t.Context(), command); err != nil {
		t.Fatalf("new disable generation: %v", err)
	}
	next := client.lifecycle[3]
	if next.IdempotencyKey == first.IdempotencyKey {
		t.Fatalf("new operation reused key %q", next.IdempotencyKey)
	}
}

func TestLifecycleBoundaryReturnsDefinitiveErrorsWithoutPanicking(t *testing.T) {
	now := time.Date(2026, 7, 30, 16, 0, 0, 0, time.UTC)
	command := AgentLifecycleCommand{
		WorkspaceKey: "TEST",
		AgentID:      "worker-one",
		Action:       agents.LifecycleEnable,
		RequestID:    testBoundLifecycleRequestID("enable-operation-one"),
	}

	canonicalAPI := &canonicalAgentsAPIStub{
		current: canonicalAgent(
			canonicalCreateCommand("TEST", "worker-one", agents.DesiredStopped),
			now,
		),
		lifecycleErr: agents.ErrConflict,
	}
	canonicalBoundary, err := newCanonicalAgentDefinitionBoundary(
		canonicalAPI,
		&operatorAuthorityResolverStub{},
	)
	if err != nil {
		t.Fatalf("new canonical boundary: %v", err)
	}
	if _, err := canonicalBoundary.ApplyAgentLifecycle(t.Context(), command); !errors.Is(err, agents.ErrConflict) {
		t.Fatalf("canonical lifecycle error = %v, want conflict", err)
	}
	if len(canonicalAPI.lifecycle) != 1 {
		t.Fatalf("canonical definitive-error calls = %d, want 1", len(canonicalAPI.lifecycle))
	}

	managementClient := &agentManagementClientStub{
		workspace: "TEST",
		current: canonicalAgent(
			canonicalCreateCommand("TEST", "worker-one", agents.DesiredStopped),
			now,
		),
		lifecycleErr: domain.ErrConflict,
	}
	managementBoundary := &managementAgentDefinitionBoundary{client: managementClient}
	if _, err := managementBoundary.ApplyAgentLifecycle(t.Context(), command); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("management lifecycle error = %v, want conflict", err)
	}
	if len(managementClient.lifecycle) != 1 {
		t.Fatalf("management definitive-error calls = %d, want 1", len(managementClient.lifecycle))
	}
}

func TestLifecycleBoundaryReturnsAmbiguousErrorsAfterBoundedRetry(t *testing.T) {
	now := time.Date(2026, 7, 30, 17, 0, 0, 0, time.UTC)
	responseLost := errors.New("connection closed before response")
	command := AgentLifecycleCommand{
		WorkspaceKey: "TEST",
		AgentID:      "worker-one",
		Action:       agents.LifecycleDisable,
		RequestID:    testBoundLifecycleRequestID("disable-operation-one"),
	}

	canonicalAPI := &canonicalAgentsAPIStub{
		current: canonicalAgent(
			canonicalCreateCommand("TEST", "worker-one", agents.DesiredRunning),
			now,
		),
		lifecycleErr: responseLost,
	}
	canonicalBoundary, err := newCanonicalAgentDefinitionBoundary(
		canonicalAPI,
		&operatorAuthorityResolverStub{},
	)
	if err != nil {
		t.Fatalf("new canonical boundary: %v", err)
	}
	if _, err := canonicalBoundary.ApplyAgentLifecycle(t.Context(), command); !errors.Is(err, responseLost) {
		t.Fatalf("canonical lifecycle error = %v, want response-lost error", err)
	}
	if len(canonicalAPI.lifecycle) != lifecycleAttempts {
		t.Fatalf(
			"canonical ambiguous-error calls = %d, want %d",
			len(canonicalAPI.lifecycle),
			lifecycleAttempts,
		)
	}

	managementClient := &agentManagementClientStub{
		workspace: "TEST",
		current: canonicalAgent(
			canonicalCreateCommand("TEST", "worker-one", agents.DesiredRunning),
			now,
		),
		lifecycleErr: responseLost,
	}
	managementBoundary := &managementAgentDefinitionBoundary{client: managementClient}
	if _, err := managementBoundary.ApplyAgentLifecycle(t.Context(), command); !errors.Is(err, responseLost) {
		t.Fatalf("management lifecycle error = %v, want response-lost error", err)
	}
	if len(managementClient.lifecycle) != lifecycleAttempts {
		t.Fatalf(
			"management ambiguous-error calls = %d, want %d",
			len(managementClient.lifecycle),
			lifecycleAttempts,
		)
	}
}

func TestAgentDefinitionBoundariesRejectNilProjectionsWithoutPanicking(t *testing.T) {
	create := AgentDefinitionCreateCommand{
		Canonical: canonicalCreateCommand("TEST", "worker-one", agents.DesiredRunning),
	}
	assertInvalid := func(t *testing.T, call func() error) {
		t.Helper()
		if err := call(); !errors.Is(err, agents.ErrInvalidPersistedState) {
			t.Fatalf("nil projection error=%v, want ErrInvalidPersistedState", err)
		}
	}

	t.Run("canonical", func(t *testing.T) {
		api := &canonicalAgentsAPIStub{
			createNil: true,
			getNil:    true,
			list:      []*agents.Agent{nil},
		}
		boundary, err := newCanonicalAgentDefinitionBoundary(
			api,
			&operatorAuthorityResolverStub{},
		)
		if err != nil {
			t.Fatal(err)
		}
		assertInvalid(t, func() error {
			_, err := boundary.CreateAgentDefinition(t.Context(), create)
			return err
		})
		assertInvalid(t, func() error {
			_, err := boundary.GetAgentDefinition(t.Context(), "TEST", "worker-one")
			return err
		})
		assertInvalid(t, func() error {
			_, err := boundary.ListAgentDefinitions(t.Context(), "TEST")
			return err
		})
	})

	t.Run("management", func(t *testing.T) {
		client := &agentManagementClientStub{
			workspace: "TEST",
			createNil: true,
			getNil:    true,
			list:      []*agents.Agent{nil},
		}
		boundary := &managementAgentDefinitionBoundary{client: client}
		assertInvalid(t, func() error {
			_, err := boundary.CreateAgentDefinition(t.Context(), create)
			return err
		})
		assertInvalid(t, func() error {
			_, err := boundary.GetAgentDefinition(t.Context(), "TEST", "worker-one")
			return err
		})
		assertInvalid(t, func() error {
			_, err := boundary.ListAgentDefinitions(t.Context(), "TEST")
			return err
		})
	})
}

func TestAgentdefRuntimeRequiresExplicitManagementEndpoint(t *testing.T) {
	t.Setenv("LOOM_WORKSPACE", "TEST")
	t.Setenv("LOOM_SERVER_URL", "")
	_, err := resolveAgentdefRuntime(t.Context(), "TEST")
	if err == nil || !strings.Contains(err.Error(), "LOOM_SERVER_URL") {
		t.Fatalf("resolveAgentdefRuntime error = %v, want explicit management endpoint requirement", err)
	}
}

type agentDefinitionBoundaryStub struct {
	creates   []AgentDefinitionCreateCommand
	lifecycle []AgentLifecycleCommand
	getErr    error
}

func (stub *agentDefinitionBoundaryStub) CreateAgentDefinition(
	_ context.Context,
	command AgentDefinitionCreateCommand,
) (*AgentDefinition, error) {
	stub.creates = append(stub.creates, command)
	return agentDefinitionFromCanonical(canonicalAgent(command.Canonical, testAgentdefTime())), nil
}

func (stub *agentDefinitionBoundaryStub) GetAgentDefinition(
	_ context.Context,
	workspace string,
	agentID string,
) (*AgentDefinition, error) {
	if stub.getErr != nil {
		return nil, stub.getErr
	}
	return agentDefinitionFromCanonical(canonicalAgent(
		canonicalCreateCommand(workspace, agentID, agents.DesiredRunning),
		testAgentdefTime(),
	)), nil
}

func (*agentDefinitionBoundaryStub) ListAgentDefinitions(
	context.Context,
	string,
) ([]*AgentDefinition, error) {
	return nil, nil
}

func (stub *agentDefinitionBoundaryStub) ApplyAgentLifecycle(
	_ context.Context,
	command AgentLifecycleCommand,
) (*AgentDefinition, error) {
	stub.lifecycle = append(stub.lifecycle, command)
	desired := agents.DesiredPaused
	if command.Action == agents.LifecycleEnable {
		desired = agents.DesiredRunning
	}
	record := canonicalAgent(
		canonicalCreateCommand(command.WorkspaceKey, command.AgentID, desired),
		testAgentdefTime(),
	)
	if command.Action == agents.LifecycleDelete {
		deleted := testAgentdefTime()
		record.DesiredState = agents.DesiredStopped
		record.DeletedAt = &deleted
	}
	return agentDefinitionFromCanonical(record), nil
}

type canonicalAgentsAPIStub struct {
	current         *agents.Agent
	getErr          error
	getNil          bool
	createNil       bool
	list            []*agents.Agent
	creates         []agents.CreateAgentCommand
	desired         []agents.SetDesiredStateCommand
	lifecycle       []agents.ApplyLifecycleCommand
	lifecycleErr    error
	lifecycleErrors []error
	receipts        map[string]*agents.LifecycleResult
}

func (stub *canonicalAgentsAPIStub) GetAgent(
	_ context.Context,
	workspace string,
	agentID string,
) (*agents.Agent, error) {
	if stub.getErr != nil {
		return nil, stub.getErr
	}
	if stub.getNil {
		return nil, nil
	}
	if stub.current != nil {
		out := *stub.current
		return &out, nil
	}
	return canonicalAgent(
		canonicalCreateCommand(workspace, agentID, agents.DesiredRunning),
		testAgentdefTime(),
	), nil
}

func (stub *canonicalAgentsAPIStub) ListAgents(
	context.Context,
	string,
	agents.AgentFilter,
) ([]*agents.Agent, error) {
	return stub.list, nil
}

func (stub *canonicalAgentsAPIStub) CreateAgent(
	_ context.Context,
	_ authority.OperatorAuthority,
	command agents.CreateAgentCommand,
) (*agents.Agent, error) {
	stub.creates = append(stub.creates, command)
	if stub.createNil {
		return nil, nil
	}
	return canonicalAgent(command, testAgentdefTime()), nil
}

func (stub *canonicalAgentsAPIStub) ArchiveAgent(
	_ context.Context,
	_ authority.OperatorAuthority,
	command agents.ArchiveAgentCommand,
) (*agents.Agent, error) {
	record := canonicalAgent(
		canonicalCreateCommand(command.WorkspaceKey, command.AgentID, agents.DesiredStopped),
		command.ExpectedUpdatedAt,
	)
	deleted := command.ExpectedUpdatedAt.Add(time.Second)
	record.DeletedAt = &deleted
	record.UpdatedAt = deleted
	return record, nil
}

func (stub *canonicalAgentsAPIStub) SetDesiredState(
	_ context.Context,
	_ authority.OperatorAuthority,
	command agents.SetDesiredStateCommand,
) (*agents.Agent, error) {
	stub.desired = append(stub.desired, command)
	record := canonicalAgent(
		canonicalCreateCommand(command.WorkspaceKey, command.AgentID, command.DesiredState),
		command.ExpectedUpdatedAt,
	)
	record.UpdatedAt = command.ExpectedUpdatedAt.Add(time.Second)
	stub.current = record
	return record, nil
}

func (stub *canonicalAgentsAPIStub) ApplyLifecycle(
	_ context.Context,
	_ authority.OperatorAuthority,
	command agents.ApplyLifecycleCommand,
) (*agents.LifecycleResult, error) {
	stub.lifecycle = append(stub.lifecycle, command)
	if stub.lifecycleErr != nil {
		return nil, stub.lifecycleErr
	}
	if receipt := stub.receipts[command.IdempotencyKey]; receipt != nil {
		return receipt, nil
	}
	desired := agents.DesiredPaused
	if command.Action == agents.LifecycleEnable {
		desired = agents.DesiredRunning
	}
	record := canonicalAgent(
		canonicalCreateCommand(command.WorkspaceKey, command.AgentID, desired),
		command.ExpectedUpdatedAt.Add(time.Second),
	)
	record.GenerationID = command.ExpectedGenerationID
	if command.Action == agents.LifecycleDelete {
		record.DesiredState = agents.DesiredStopped
		deleted := record.UpdatedAt
		record.DeletedAt = &deleted
	}
	result := &agents.LifecycleResult{
		WorkspaceKey: command.WorkspaceKey, AgentID: command.AgentID,
		IdempotencyKey: command.IdempotencyKey, Action: command.Action,
		Agent: record, CommittedAt: record.UpdatedAt,
	}
	stub.current = record
	if stub.receipts == nil {
		stub.receipts = make(map[string]*agents.LifecycleResult)
	}
	stub.receipts[command.IdempotencyKey] = result
	if len(stub.lifecycleErrors) > 0 {
		err := stub.lifecycleErrors[0]
		stub.lifecycleErrors = stub.lifecycleErrors[1:]
		if err != nil {
			return nil, err
		}
	}
	return result, nil
}

type agentManagementClientStub struct {
	workspace       string
	current         *agents.Agent
	getErr          error
	getNil          bool
	createNil       bool
	list            []*agents.Agent
	lifecycle       []managementapi.ApplyAgentLifecycleRequest
	lifecycleErr    error
	lifecycleErrors []error
	receipts        map[string]*agents.LifecycleResult
}

func (stub *agentManagementClientStub) Workspace() string {
	return stub.workspace
}

func (stub *agentManagementClientStub) CreateAgent(
	_ context.Context,
	command agents.CreateAgentCommand,
) (*agents.Agent, error) {
	if stub.createNil {
		return nil, nil
	}
	return canonicalAgent(command, testAgentdefTime()), nil
}

func (stub *agentManagementClientStub) GetAgent(
	context.Context,
	string,
) (*agents.Agent, error) {
	if stub.getErr != nil {
		return nil, stub.getErr
	}
	if stub.getNil {
		return nil, nil
	}
	out := *stub.current
	return &out, nil
}

func (stub *agentManagementClientStub) ListAgents(context.Context) ([]*agents.Agent, error) {
	return stub.list, nil
}

func (stub *agentManagementClientStub) ApplyAgentLifecycle(
	_ context.Context,
	agentID string,
	request managementapi.ApplyAgentLifecycleRequest,
) (*agents.LifecycleResult, error) {
	stub.lifecycle = append(stub.lifecycle, request)
	if stub.lifecycleErr != nil {
		return nil, stub.lifecycleErr
	}
	if receipt := stub.receipts[request.IdempotencyKey]; receipt != nil {
		return receipt, nil
	}
	now := testAgentdefTime().Add(time.Minute)
	if stub.current != nil {
		now = stub.current.UpdatedAt.Add(time.Second)
	}
	desired := agents.DesiredPaused
	if request.Action == agents.LifecycleEnable {
		desired = agents.DesiredRunning
	}
	if request.Action == agents.LifecycleDelete {
		desired = agents.DesiredStopped
	}
	record := canonicalAgent(
		canonicalCreateCommand(stub.workspace, agentID, desired),
		now,
	)
	record.GenerationID = request.ExpectedGenerationID
	deleted := now
	if request.Action == agents.LifecycleDelete {
		record.DeletedAt = &deleted
	}
	result := &agents.LifecycleResult{
		WorkspaceKey:   stub.workspace,
		AgentID:        agentID,
		Action:         request.Action,
		IdempotencyKey: request.IdempotencyKey,
		Agent:          record,
		CommittedAt:    now,
	}
	stub.current = record
	if stub.receipts == nil {
		stub.receipts = make(map[string]*agents.LifecycleResult)
	}
	stub.receipts[request.IdempotencyKey] = result
	if len(stub.lifecycleErrors) > 0 {
		err := stub.lifecycleErrors[0]
		stub.lifecycleErrors = stub.lifecycleErrors[1:]
		if err != nil {
			return nil, err
		}
	}
	return result, nil
}

type operatorAuthorityResolverStub struct {
	actions []authority.Action
}

func (stub *operatorAuthorityResolverStub) ResolveOperatorAuthority(
	_ context.Context,
	_ string,
	action authority.Action,
) (authority.OperatorAuthority, error) {
	stub.actions = append(stub.actions, action)
	return authority.OperatorAuthority{}, nil
}

func canonicalCreateCommand(
	workspace string,
	agentID string,
	state agents.DesiredState,
) agents.CreateAgentCommand {
	return agents.CreateAgentCommand{
		WorkspaceKey: workspace,
		AgentID:      agentID,
		Name:         agentID,
		Kind:         agents.AgentKindMaintenance,
		Behavior:     agents.BehaviorReference{RoleName: "task"},
		DesiredState: state,
		MaxInstances: 1,
	}
}

func canonicalAgent(command agents.CreateAgentCommand, now time.Time) *agents.Agent {
	return &agents.Agent{
		WorkspaceKey:    command.WorkspaceKey,
		AgentID:         command.AgentID,
		GenerationID:    testAgentdefGenerationID,
		Name:            command.Name,
		Kind:            command.Kind,
		Behavior:        command.Behavior,
		DesiredState:    command.DesiredState,
		ProfileName:     command.ProfileName,
		PlacementPolicy: command.PlacementPolicy,
		MaxInstances:    command.MaxInstances,
		RestartPolicy:   command.RestartPolicy,
		BudgetPolicy:    command.BudgetPolicy,
		CreatedAt:       now,
		UpdatedAt:       now,
	}
}

const testAgentdefGenerationID = "00112233445566778899aabbccddeeff"

func testBoundLifecycleRequestID(seed string) string {
	value, err := bindLifecycleRequestID(seed, testAgentdefGenerationID)
	if err != nil {
		panic(err)
	}
	return value
}

func installAgentdefTestRuntime(t *testing.T, runtime agentdefRuntime) {
	t.Helper()
	previousWorkspaceResolver := resolveAgentdefWorkspace
	previousResolver := resolveAgentdefRuntime
	resolveAgentdefWorkspace = func() (string, error) {
		return "TEST", nil
	}
	resolveAgentdefRuntime = func(
		context.Context,
		string,
	) (agentdefRuntime, error) {
		return runtime, nil
	}
	t.Cleanup(func() {
		resolveAgentdefWorkspace = previousWorkspaceResolver
		resolveAgentdefRuntime = previousResolver
	})
}

func resetAgentdefFlags(t *testing.T) {
	t.Helper()
	agentAddRole = ""
	agentAddAuto = false
	agentAddMode = ""
	agentAddProfile = ""
	agentAddMaxConc = 0
	agentAddBudget = ""
	agentStartRequestID = ""
	agentStopRequestID = ""
	agentRemoveRequestID = ""
	agentListJSON = false
	agentShowJSON = false
	t.Cleanup(func() {
		agentAddRole = ""
		agentAddAuto = false
		agentAddMode = ""
		agentAddProfile = ""
		agentAddMaxConc = 0
		agentAddBudget = ""
		agentStartRequestID = ""
		agentStopRequestID = ""
		agentRemoveRequestID = ""
		agentListJSON = false
		agentShowJSON = false
	})
}

func testAgentdefTime() time.Time {
	return time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC)
}
