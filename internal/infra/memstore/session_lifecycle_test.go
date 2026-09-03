package memstore

import (
	"context"
	"errors"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/store"
)

func TestSessionLifecycleRejectsInvalidInvocationKeys(t *testing.T) {
	st, run := lifecycleTestStore(t, domain.TaskRunRunning)
	for _, key := range []string{"", "Agent", "agent_1", "-agent", "agent/one"} {
		t.Run(key, func(t *testing.T) {
			_, err := st.AgentSessions().Open(t.Context(), run, lifecycleDescriptor(key))
			if !errors.Is(err, domain.ErrInvalid) {
				t.Fatalf("Open(%q) err = %v, want invalid", key, err)
			}
		})
	}
}

func TestSessionLifecycleIDComposition(t *testing.T) {
	if got := store.SessionID("run-1", 3, "review"); got != "run-1-a3-review" {
		t.Fatalf("SessionID = %q", got)
	}
	if got := store.TranscriptArtifactID("run-1", 3, "review"); got != "transcript-run-1-a3-review" {
		t.Fatalf("TranscriptArtifactID = %q", got)
	}
}

func TestSessionLifecyclePinsSchedulerAttempt(t *testing.T) {
	st := New()
	if _, err := st.TaskRuns().Create(t.Context(), store.TaskRunCreate{WorkspaceKey: "WS", TaskRunID: "run-3", TaskID: "WS-1", Status: domain.TaskRunRunning, RuntimeMetadata: map[string]string{"scheduler_attempt": "2"}}); err != nil {
		t.Fatalf("Create task run: %v", err)
	}
	run := store.SessionRunContext{WorkspaceKey: "WS", TaskRunID: "run-3", Attempt: 3}
	ref, err := st.AgentSessions().Open(t.Context(), run, lifecycleDescriptor("agent"))
	if err != nil || ref.SessionID != "run-3-a3-agent" {
		t.Fatalf("Open = %#v, %v", ref, err)
	}
	run.Attempt = 2
	_, err = st.AgentSessions().Open(t.Context(), run, lifecycleDescriptor("other"))
	assertLifecycleError(t, err, store.SessionLifecycleErrAttemptMismatch)
}

func TestSessionLifecycleOpenIsIdempotentAndStampsContext(t *testing.T) {
	st, run := lifecycleTestStore(t, domain.TaskRunRunning)
	descriptor := lifecycleDescriptor("agent")
	first, err := st.AgentSessions().Open(t.Context(), run, descriptor)
	if err != nil {
		t.Fatalf("first Open: %v", err)
	}
	second, err := st.AgentSessions().Open(t.Context(), run, descriptor)
	if err != nil {
		t.Fatalf("second Open: %v", err)
	}
	if first != second || first.SessionID != "run-1-a1-agent" {
		t.Fatalf("open refs = %#v / %#v", first, second)
	}
	session, err := st.AgentSessions().Get(t.Context(), "WS", first.SessionID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if session.Metadata[store.SessionMetadataFencingToken] != "42" || session.Metadata[store.SessionMetadataDriverRunID] != "driver-1" || session.Metadata[store.SessionMetadataDriverStepID] != "step-1" {
		t.Fatalf("open metadata = %#v", session.Metadata)
	}
}

func TestSessionLifecycleRejectsConflictingOpen(t *testing.T) {
	st, run := lifecycleTestStore(t, domain.TaskRunRunning)
	if _, err := st.AgentSessions().Open(t.Context(), run, lifecycleDescriptor("agent")); err != nil {
		t.Fatalf("Open: %v", err)
	}
	descriptor := lifecycleDescriptor("agent")
	descriptor.Model = "gpt-6"
	_, err := st.AgentSessions().Open(t.Context(), run, descriptor)
	assertLifecycleError(t, err, store.SessionLifecycleErrDescriptorConflict)
}

func TestSessionLifecycleFinalizeFirstTerminalWins(t *testing.T) {
	st, run := lifecycleTestStore(t, domain.TaskRunRunning)
	ref, err := st.AgentSessions().Open(t.Context(), run, lifecycleDescriptor("agent"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	exit := 0
	outcome := store.SessionOutcome{Status: domain.AgentSessionCompleted, ExitCode: &exit, Summary: "done", TranscriptRef: "artifact://transcript-run-1-a1-agent", DriverRunnerSessionID: "provider-1"}
	settled, err := st.AgentSessions().Finalize(t.Context(), ref, outcome)
	if err != nil {
		t.Fatalf("first Finalize: %v", err)
	}
	replayed, err := st.AgentSessions().Finalize(t.Context(), ref, outcome)
	if err != nil || replayed.UpdatedAt != settled.UpdatedAt {
		t.Fatalf("replayed Finalize = %#v, %v", replayed, err)
	}
	reopened, err := st.AgentSessions().Open(t.Context(), run, lifecycleDescriptor("agent"))
	if err != nil || reopened != ref {
		t.Fatalf("Open after finalize = %#v, %v", reopened, err)
	}
	omittedCloseMetadata := outcome
	omittedCloseMetadata.DriverRunnerSessionID = ""
	_, err = st.AgentSessions().Finalize(t.Context(), ref, omittedCloseMetadata)
	assertLifecycleError(t, err, store.SessionLifecycleErrOutcomeConflict)
	conflict := outcome
	conflict.Status = domain.AgentSessionFailed
	_, err = st.AgentSessions().Finalize(t.Context(), ref, conflict)
	assertLifecycleError(t, err, store.SessionLifecycleErrOutcomeConflict)
	stored, err := st.AgentSessions().Get(t.Context(), "WS", ref.SessionID)
	if err != nil || stored.Status != domain.AgentSessionCompleted || stored.Summary != "done" {
		t.Fatalf("settled session = %#v, %v", stored, err)
	}
}

func TestSessionLifecycleFinalizeUsagePreservesExistingValuesOnZero(t *testing.T) {
	st, run := lifecycleTestStore(t, domain.TaskRunRunning)
	ref, err := st.AgentSessions().Open(t.Context(), run, lifecycleDescriptor("agent"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	metadata := map[string]string{
		store.SessionMetadataInputTokens:      "100",
		store.SessionMetadataOutputTokens:     "50",
		store.SessionMetadataEstimatedCostUSD: "1.5",
	}
	if _, err := st.AgentSessions().Update(t.Context(), "WS", ref.SessionID, store.AgentSessionUpdate{Metadata: &metadata}); err != nil {
		t.Fatalf("seed usage metadata: %v", err)
	}
	zero := int64(0)
	zeroCost := 0.0
	session, err := st.AgentSessions().Finalize(t.Context(), ref, store.SessionOutcome{
		Status: domain.AgentSessionCompleted,
		Usage:  store.SessionUsage{InputTokens: &zero, OutputTokens: &zero, CostUSD: &zeroCost},
	})
	if err != nil {
		t.Fatalf("Finalize: %v", err)
	}
	if session.Metadata[store.SessionMetadataInputTokens] != "100" || session.Metadata[store.SessionMetadataOutputTokens] != "50" || session.Metadata[store.SessionMetadataEstimatedCostUSD] != "1.5" {
		t.Fatalf("zero usage clobbered existing metadata: %#v", session.Metadata)
	}
}

func TestAgentSessionUpdateTerminalReplayChecksAllSuppliedFields(t *testing.T) {
	st, run := lifecycleTestStore(t, domain.TaskRunRunning)
	ref, err := st.AgentSessions().Open(t.Context(), run, lifecycleDescriptor("agent"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	completed := domain.AgentSessionCompleted
	summary := "leaf closed"
	patch := store.AgentSessionUpdate{Status: &completed, Summary: &summary}
	if _, err := st.AgentSessions().Update(t.Context(), "WS", ref.SessionID, patch); err != nil {
		t.Fatalf("first Update: %v", err)
	}
	if _, err := st.AgentSessions().Update(t.Context(), "WS", ref.SessionID, patch); err != nil {
		t.Fatalf("replayed Update: %v", err)
	}
	changed := "reconciler overwrite"
	_, err = st.AgentSessions().Update(t.Context(), "WS", ref.SessionID, store.AgentSessionUpdate{Status: &completed, Summary: &changed})
	assertLifecycleError(t, err, store.SessionLifecycleErrOutcomeConflict)
	stored, _ := st.AgentSessions().Get(t.Context(), "WS", ref.SessionID)
	if stored.Summary != summary {
		t.Fatalf("conflicting Update changed record: %#v", stored)
	}
}

func TestAgentSessionUpdateTerminalCoreAndAdvisoryFields(t *testing.T) {
	st, run := lifecycleTestStore(t, domain.TaskRunRunning)
	ref, err := st.AgentSessions().Open(t.Context(), run, lifecycleDescriptor("agent"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if _, err := st.AgentSessions().Finalize(t.Context(), ref, store.SessionOutcome{Status: domain.AgentSessionCompleted, Summary: "leaf"}); err != nil {
		t.Fatalf("Finalize: %v", err)
	}
	summary, errorClass := "overwrite", "reconciler"
	_, err = st.AgentSessions().Update(t.Context(), "WS", ref.SessionID, store.AgentSessionUpdate{Summary: &summary, ErrorClass: &errorClass})
	assertLifecycleError(t, err, store.SessionLifecycleErrOutcomeConflict)
	metadata := map[string]string{"advisory": "seen"}
	updated, err := st.AgentSessions().Update(t.Context(), "WS", ref.SessionID, store.AgentSessionUpdate{Metadata: &metadata})
	if err != nil || updated.Metadata["advisory"] != "seen" || updated.Summary != "leaf" {
		t.Fatalf("metadata-only Update = %#v, %v", updated, err)
	}
}

func TestAgentSessionUpdateProtectsLegacyTerminalOutcome(t *testing.T) {
	st := New()
	legacy, err := st.AgentSessions().Create(t.Context(), store.AgentSessionCreate{
		WorkspaceKey: "WS", SessionID: "legacy-run", AgentID: "flue", Status: domain.AgentSessionCompleted,
	})
	if err != nil {
		t.Fatalf("Create legacy session: %v", err)
	}
	legacySummary := "legacy overwrite"
	_, err = st.AgentSessions().Update(t.Context(), "WS", legacy.SessionID, store.AgentSessionUpdate{Summary: &legacySummary})
	assertLifecycleError(t, err, store.SessionLifecycleErrOutcomeConflict)
	running := domain.AgentSessionRunning
	_, err = st.AgentSessions().Update(t.Context(), "WS", legacy.SessionID, store.AgentSessionUpdate{Status: &running})
	assertLifecycleError(t, err, store.SessionLifecycleErrOutcomeConflict)
}

func TestSessionLifecycleRejectsOpenAfterTaskRunTerminal(t *testing.T) {
	st, run := lifecycleTestStore(t, domain.TaskRunCompleted)
	_, err := st.AgentSessions().Open(t.Context(), run, lifecycleDescriptor("agent"))
	assertLifecycleError(t, err, store.SessionLifecycleErrTaskRunTerminal)
}

func TestSessionLifecycleReconcilerStampCannotClobberLeafClose(t *testing.T) {
	st, run := lifecycleTestStore(t, domain.TaskRunRunning)
	ref, err := st.AgentSessions().Open(t.Context(), run, lifecycleDescriptor("agent"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	leaf := store.SessionOutcome{Status: domain.AgentSessionCompleted, Summary: "leaf closed"}
	if _, err := st.AgentSessions().Finalize(t.Context(), ref, leaf); err != nil {
		t.Fatalf("leaf Finalize: %v", err)
	}
	_, err = st.AgentSessions().Finalize(t.Context(), ref, store.SessionOutcome{Status: domain.AgentSessionFailed, ErrorClass: "agent_session_unclosed"})
	assertLifecycleError(t, err, store.SessionLifecycleErrOutcomeConflict)
	stored, _ := st.AgentSessions().Get(context.Background(), "WS", ref.SessionID)
	if stored.Status != domain.AgentSessionCompleted || stored.Summary != "leaf closed" {
		t.Fatalf("reconciler changed settled leaf session: %#v", stored)
	}
}

func lifecycleTestStore(t *testing.T, status domain.TaskRunStatus) (*Store, store.SessionRunContext) {
	t.Helper()
	st := New()
	if _, err := st.TaskRuns().Create(t.Context(), store.TaskRunCreate{WorkspaceKey: "WS", TaskRunID: "run-1", TaskID: "WS-1", Status: status}); err != nil {
		t.Fatalf("Create task run: %v", err)
	}
	return st, store.SessionRunContext{WorkspaceKey: "WS", TaskRunID: "run-1", Attempt: 1, FencingToken: 42, DriverRunID: "driver-1", DriverStepID: "step-1"}
}

func lifecycleDescriptor(invocationKey string) store.SessionDescriptor {
	return store.SessionDescriptor{InvocationKey: invocationKey, Backend: "codex", Model: "gpt-5", Metadata: map[string]string{"source": "test"}}
}

func assertLifecycleError(t *testing.T, err error, want string) {
	t.Helper()
	var lifecycleErr *store.SessionLifecycleError
	if !errors.As(err, &lifecycleErr) || lifecycleErr.Code != want || !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("error = %v, want lifecycle %q conflict", err, want)
	}
}
