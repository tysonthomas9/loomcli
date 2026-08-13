package runcapture

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/modules/artifacts"
	"github.com/tysonthomas9/loomcli/internal/modules/execution"
	"github.com/tysonthomas9/loomcli/internal/modules/interaction"
)

type executionQueriesFake struct {
	run       *execution.TaskRun
	runs      []*execution.TaskRun
	err       error
	calls     int
	listCalls int
}

func (fake *executionQueriesFake) GetTaskRun(context.Context, string, string) (*execution.TaskRun, error) {
	fake.calls++
	return fake.run, fake.err
}
func (fake *executionQueriesFake) ListTaskRuns(context.Context, execution.TaskRunArchiveQuery) ([]*execution.TaskRun, error) {
	fake.listCalls++
	if fake.err != nil {
		return nil, fake.err
	}
	return append([]*execution.TaskRun(nil), fake.runs...), nil
}
func (*executionQueriesFake) ListActiveTaskRuns(context.Context, execution.ActiveTaskRunQuery) ([]*execution.TaskRun, error) {
	return nil, nil
}
func (*executionQueriesFake) ListTaskRunEvents(context.Context, execution.TaskRunEventQuery) ([]*execution.TaskRunEvent, error) {
	return nil, nil
}

type interactionQueriesFake struct {
	session   *interaction.AgentSession
	sessions  []*interaction.AgentSession
	err       error
	calls     int
	listCalls int
}

func (fake *interactionQueriesFake) GetSession(context.Context, string, string) (*interaction.AgentSession, error) {
	fake.calls++
	return fake.session, fake.err
}
func (fake *interactionQueriesFake) ListSessions(context.Context, interaction.SessionArchiveQuery) ([]*interaction.AgentSession, error) {
	fake.listCalls++
	if fake.err != nil {
		return nil, fake.err
	}
	return append([]*interaction.AgentSession(nil), fake.sessions...), nil
}

type artifactQueriesFake struct {
	values       []*artifacts.Artifact
	content      map[string][]byte
	contentErr   error
	listCalls    int
	contentCalls int
}

func (fake *artifactQueriesFake) GetArtifact(context.Context, artifacts.Query) (*artifacts.Artifact, error) {
	return nil, artifacts.ErrNotFound
}
func (fake *artifactQueriesFake) ListArtifacts(_ context.Context, query artifacts.SearchQuery) ([]*artifacts.Artifact, error) {
	fake.listCalls++
	result := make([]*artifacts.Artifact, 0, len(fake.values))
	for _, value := range fake.values {
		if value.OwnerType != query.Filter.OwnerType || value.OwnerID != query.Filter.OwnerID {
			continue
		}
		result = append(result, value)
	}
	return result, nil
}
func (fake *artifactQueriesFake) ReadArtifactContent(_ context.Context, query artifacts.Query) ([]byte, error) {
	fake.contentCalls++
	if fake.contentErr != nil {
		return nil, fake.contentErr
	}
	return append([]byte(nil), fake.content[query.ArtifactID]...), nil
}

func TestRunCaptureRejectsRelationshipMismatchBeforeArtifactRead(t *testing.T) {
	now := time.Date(2026, 8, 12, 10, 0, 0, 0, time.UTC)
	executions := &executionQueriesFake{run: &execution.TaskRun{
		WorkspaceKey: "WS", TaskRunID: "run-1", WorkItemID: "TASK-1",
		Status: execution.StatusSucceeded, CreatedAt: now, UpdatedAt: now,
	}}
	interactions := &interactionQueriesFake{session: validSession(now)}
	artifactQueries := &artifactQueriesFake{}
	service := newService(t, executions, interactions, artifactQueries)
	if _, err := service.Get(t.Context(), Query{
		WorkspaceKey: "WS", OwnerKind: OwnerExecution, OwnerID: "run-1", WorkItemID: "TASK-2",
	}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("execution mismatch error = %v", err)
	}
	if artifactQueries.listCalls != 0 {
		t.Fatal("execution mismatch reached Artifacts")
	}
	if _, err := service.Get(t.Context(), Query{
		WorkspaceKey: "WS", OwnerKind: OwnerInteraction, OwnerID: "session-1", AgentID: "other",
	}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("interaction mismatch error = %v", err)
	}
	if artifactQueries.listCalls != 0 {
		t.Fatal("interaction mismatch reached Artifacts")
	}
}

func TestRunCaptureHidesMismatchedSelectedTranscript(t *testing.T) {
	now := time.Date(2026, 8, 12, 10, 0, 0, 0, time.UTC)
	session := validSession(now)
	session.TranscriptArtifactID = "foreign-transcript"
	queries := &artifactQueriesFake{values: []*artifacts.Artifact{
		sessionArtifactRow(now, "transcript-session-1", artifacts.StatusFinalized),
	}}
	service := newService(t, &executionQueriesFake{}, &interactionQueriesFake{session: session}, queries)

	_, err := service.Transcript(t.Context(), Query{
		WorkspaceKey: "WS", OwnerKind: OwnerInteraction, OwnerID: "session-1", AgentID: "agent-1",
	})
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("error = %v, want not found", err)
	}
	if queries.contentCalls != 0 {
		t.Fatalf("content reads = %d, want 0", queries.contentCalls)
	}
}

func TestRunCaptureProjectsIndependentEvidenceStates(t *testing.T) {
	now := time.Date(2026, 8, 12, 10, 0, 0, 0, time.UTC)
	artifactQueries := &artifactQueriesFake{values: []*artifacts.Artifact{
		artifactRow(now, "transcript-1", "transcript", artifacts.StatusFinalized, map[string]string{artifacts.MetadataEvidenceTruncated: "true"}),
		artifactRow(now, "diff-1", "patch", artifacts.StatusFinalized, nil),
		artifactRow(now, "logs-1", "logs", artifacts.StatusUploading, nil),
		artifactRow(now, "report-1", "report", artifacts.StatusFailed, map[string]string{"loom.evidence.failure_class": "redaction_failed"}),
	}}
	service := newService(t, &executionQueriesFake{run: &execution.TaskRun{
		WorkspaceKey: "WS", TaskRunID: "run-1", WorkItemID: "TASK-1",
		Status: execution.StatusSucceeded, CreatedAt: now, UpdatedAt: now,
	}}, &interactionQueriesFake{session: validSession(now)}, artifactQueries)
	capture, err := service.Get(t.Context(), Query{WorkspaceKey: "WS", OwnerKind: OwnerExecution, OwnerID: "run-1"})
	if err != nil {
		t.Fatal(err)
	}
	want := []EvidenceState{EvidenceTruncated, EvidenceFinalized, EvidencePending, EvidenceCaptureFailed}
	if len(capture.Evidence) != len(want) {
		t.Fatalf("evidence = %+v", capture.Evidence)
	}
	for index := range want {
		if capture.Evidence[index].State != want[index] {
			t.Fatalf("evidence %d state = %q, want %q", index, capture.Evidence[index].State, want[index])
		}
	}
	if capture.Evidence[3].FailureClass != "redaction_failed" {
		t.Fatalf("failure class = %q", capture.Evidence[3].FailureClass)
	}
}

func TestRunCaptureProjectsMalformedTerminalEvidenceAsCorrupt(t *testing.T) {
	now := time.Date(2026, 8, 12, 10, 0, 0, 0, time.UTC)
	finalized := artifactRow(now, "transcript-1", "transcript", artifacts.StatusFinalized, nil)
	delete(finalized.Metadata, artifacts.MetadataEvidenceCaptureStatus)
	failed := artifactRow(now, "diff-1", "patch", artifacts.StatusFailed, nil)
	delete(failed.Metadata, "loom.evidence.failure_class")
	service := newService(t, &executionQueriesFake{run: &execution.TaskRun{
		WorkspaceKey: "WS", TaskRunID: "run-1", WorkItemID: "TASK-1",
		Status: execution.StatusSucceeded, CreatedAt: now, UpdatedAt: now,
	}}, &interactionQueriesFake{}, &artifactQueriesFake{values: []*artifacts.Artifact{finalized, failed}})
	capture, err := service.Get(t.Context(), Query{WorkspaceKey: "WS", OwnerKind: OwnerExecution, OwnerID: "run-1"})
	if err != nil {
		t.Fatal(err)
	}
	if len(capture.Evidence) != 2 || capture.Evidence[0].State != EvidenceCorrupt || capture.Evidence[1].State != EvidenceCorrupt {
		t.Fatalf("evidence = %+v, want visible corrupt states", capture.Evidence)
	}
}

func TestRunCaptureProjectsOwnerFailureWhenArtifactRowCouldNotBeCommitted(t *testing.T) {
	now := time.Date(2026, 8, 12, 10, 0, 0, 0, time.UTC)
	metadata := artifacts.OwnerEvidenceCaptureFailure(
		artifacts.EvidenceTranscript,
		artifacts.ErrUnavailable,
		2,
	)
	service := newService(t, &executionQueriesFake{run: &execution.TaskRun{
		WorkspaceKey: "WS", TaskRunID: "run-1", WorkItemID: "TASK-1",
		Status: execution.StatusSucceeded, RuntimeMetadata: metadata,
		CreatedAt: now, UpdatedAt: now,
	}}, &interactionQueriesFake{}, &artifactQueriesFake{})

	capture, err := service.Get(t.Context(), Query{
		WorkspaceKey: "WS", OwnerKind: OwnerExecution, OwnerID: "run-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if capture.Status != string(execution.StatusSucceeded) || len(capture.Evidence) != 1 {
		t.Fatalf("capture = %+v, want successful work plus one failed evidence facet", capture)
	}
	evidence := capture.Evidence[0]
	if evidence.Kind != artifacts.EvidenceTranscript || evidence.State != EvidenceCaptureFailed ||
		evidence.FailureClass != "capture_unavailable" || evidence.ArtifactID != "" {
		t.Fatalf("evidence = %+v, want owner-projected capture failure", evidence)
	}
}

func TestRunCaptureUsesNewestAttemptAcrossOwnerFailureAndArtifact(t *testing.T) {
	now := time.Date(2026, 8, 12, 10, 0, 0, 0, time.UTC)
	metadata := artifacts.OwnerEvidenceCaptureFailure(
		artifacts.EvidenceTranscript,
		artifacts.ErrUnavailable,
		2,
	)
	first := artifactRow(now, "transcript-1", "transcript", artifacts.StatusFinalized, map[string]string{
		"task_run_attempt": "1",
	})
	service := newService(t, &executionQueriesFake{run: &execution.TaskRun{
		WorkspaceKey: "WS", TaskRunID: "run-1", WorkItemID: "TASK-1",
		Status: execution.StatusSucceeded, RuntimeMetadata: metadata,
		CreatedAt: now, UpdatedAt: now,
	}}, &interactionQueriesFake{}, &artifactQueriesFake{values: []*artifacts.Artifact{first}})

	capture, err := service.Get(t.Context(), Query{
		WorkspaceKey: "WS", OwnerKind: OwnerExecution, OwnerID: "run-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(capture.Evidence) != 1 || capture.Evidence[0].State != EvidenceCaptureFailed {
		t.Fatalf("evidence = %+v, want attempt-2 failure to supersede attempt-1 content", capture.Evidence)
	}
}

func TestRunCaptureIgnoresArtifactsOutsideDurableEvidenceVocabulary(t *testing.T) {
	now := time.Date(2026, 8, 12, 10, 0, 0, 0, time.UTC)
	artifactQueries := &artifactQueriesFake{values: []*artifacts.Artifact{
		artifactRow(now, "runner-bundle-1", "artifact", artifacts.StatusFinalized, nil),
		artifactRow(now, "report-1", "report", artifacts.StatusFinalized, nil),
	}}
	service := newService(t, &executionQueriesFake{run: &execution.TaskRun{
		WorkspaceKey: "WS", TaskRunID: "run-1", WorkItemID: "TASK-1",
		Status: execution.StatusSucceeded, CreatedAt: now, UpdatedAt: now,
	}}, &interactionQueriesFake{}, artifactQueries)

	capture, err := service.Get(t.Context(), Query{
		WorkspaceKey: "WS", OwnerKind: OwnerExecution, OwnerID: "run-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(capture.Evidence) != 1 || capture.Evidence[0].Kind != artifacts.EvidenceReport ||
		capture.Evidence[0].ArtifactID != "report-1" {
		t.Fatalf("evidence = %+v, want only the report facet", capture.Evidence)
	}
}

func TestRunCaptureArchiveMergesOwnerQueriesWithoutReReadingOwners(t *testing.T) {
	now := time.Date(2026, 8, 12, 10, 0, 0, 0, time.UTC)
	run := &execution.TaskRun{
		WorkspaceKey: "WS", TaskRunID: "run-1", WorkItemID: "TASK-1",
		Status: execution.StatusSucceeded, StartedAt: timePointer(now.Add(-2 * time.Minute)),
		CreatedAt: now.Add(-3 * time.Minute), UpdatedAt: now,
	}
	session := validSession(now)
	session.StartedAt = now.Add(-time.Minute)
	executions := &executionQueriesFake{runs: []*execution.TaskRun{run}}
	interactions := &interactionQueriesFake{sessions: []*interaction.AgentSession{session}}
	artifactQueries := &artifactQueriesFake{values: []*artifacts.Artifact{
		artifactRow(now, "diff-1", "patch", artifacts.StatusFinalized, nil),
		sessionArtifactRow(now, "transcript-session-1", artifacts.StatusFinalized),
	}}
	service := newService(t, executions, interactions, artifactQueries)

	values, err := service.List(t.Context(), ArchiveQuery{WorkspaceKey: "WS", WorkItemID: "TASK-1", Limit: 2})
	if err != nil {
		t.Fatal(err)
	}
	if len(values) != 2 || values[0].OwnerKind != OwnerInteraction || values[0].OwnerID != "session-1" ||
		values[1].OwnerKind != OwnerExecution || values[1].OwnerID != "run-1" {
		t.Fatalf("archive order = %+v", values)
	}
	if executions.listCalls != 1 || interactions.listCalls != 1 || executions.calls != 0 || interactions.calls != 0 {
		t.Fatalf("owner calls = execution(list=%d get=%d) interaction(list=%d get=%d)",
			executions.listCalls, executions.calls, interactions.listCalls, interactions.calls)
	}
	if len(values[0].Evidence) != 1 || values[0].Evidence[0].Kind != artifacts.EvidenceTranscript ||
		len(values[1].Evidence) != 1 || values[1].Evidence[0].Kind != artifacts.EvidenceDiff {
		t.Fatalf("archive evidence = %+v", values)
	}
}

func TestRunCaptureArchiveAgentFilterSkipsExecutionAndBoundsRequests(t *testing.T) {
	now := time.Date(2026, 8, 12, 10, 0, 0, 0, time.UTC)
	executions := &executionQueriesFake{runs: []*execution.TaskRun{{WorkspaceKey: "WS", TaskRunID: "run-1"}}}
	interactions := &interactionQueriesFake{sessions: []*interaction.AgentSession{validSession(now)}}
	service := newService(t, executions, interactions, &artifactQueriesFake{})

	values, err := service.List(t.Context(), ArchiveQuery{WorkspaceKey: "WS", AgentID: "agent-1", Limit: 10})
	if err != nil || len(values) != 1 || values[0].OwnerKind != OwnerInteraction {
		t.Fatalf("agent archive = %+v, %v", values, err)
	}
	if executions.listCalls != 0 || interactions.listCalls != 1 {
		t.Fatalf("list calls = execution %d interaction %d", executions.listCalls, interactions.listCalls)
	}
	for _, invalid := range []ArchiveQuery{
		{},
		{WorkspaceKey: "WS", Limit: -1},
		{WorkspaceKey: "WS", Limit: maxArchiveLimit + 1},
		{WorkspaceKey: "WS", OwnerID: "run-1"},
		{WorkspaceKey: "WS", OwnerKind: OwnerExecution, AgentID: "agent-1"},
	} {
		if _, listErr := service.List(t.Context(), invalid); !errors.Is(listErr, ErrInvalid) {
			t.Fatalf("query %+v error = %v, want invalid", invalid, listErr)
		}
	}
}

func TestTranscriptEvidenceSurfacesMissingUnavailableCorruptAndRestartedContent(t *testing.T) {
	now := time.Date(2026, 8, 12, 10, 0, 0, 0, time.UTC)
	session := validSession(now)
	interactions := &interactionQueriesFake{session: session}
	artifactQueries := &artifactQueriesFake{content: map[string][]byte{}}
	service := newService(t, &executionQueriesFake{}, interactions, artifactQueries)
	query := Query{WorkspaceKey: "WS", OwnerKind: OwnerInteraction, OwnerID: "session-1", AgentID: "agent-1"}
	missing, err := service.Transcript(t.Context(), query)
	if err != nil || missing.Evidence.State != EvidenceMissing || artifactQueries.contentCalls != 0 {
		t.Fatalf("missing transcript = %+v, %v", missing, err)
	}

	artifactQueries.values = []*artifacts.Artifact{sessionArtifactRow(now, "transcript-session-1", artifacts.StatusFinalized)}
	session.TranscriptArtifactID = "transcript-session-1"
	artifactQueries.contentErr = artifacts.ErrContentUnavailable
	unavailable, err := service.Transcript(t.Context(), query)
	if err != nil || unavailable.Evidence.State != EvidenceContentUnavailable {
		t.Fatalf("unavailable transcript = %+v, %v", unavailable, err)
	}

	artifactQueries.contentErr = nil
	artifactQueries.content["transcript-session-1"] = []byte("not-json\n")
	corrupt, err := service.Transcript(t.Context(), query)
	if err != nil || corrupt.Evidence.State != EvidenceCorrupt || corrupt.Evidence.FailureClass != "canonical_transcript_invalid" {
		t.Fatalf("corrupt transcript = %+v, %v", corrupt, err)
	}

	artifactQueries.content["transcript-session-1"] = canonicalTranscript(t, "restart-safe")
	restarted := newService(t, &executionQueriesFake{}, interactions, artifactQueries)
	loaded, err := restarted.Transcript(t.Context(), query)
	if err != nil || loaded.Evidence.State != EvidenceFinalized || len(loaded.Events) != 1 || loaded.Events[0].Text != "restart-safe" {
		t.Fatalf("restarted transcript = %+v, %v", loaded, err)
	}
}

func TestReadEvidenceSurfacesDurableContentIntegrityFailure(t *testing.T) {
	now := time.Date(2026, 8, 12, 10, 0, 0, 0, time.UTC)
	artifact := artifactRow(now, "diff-1", "patch", artifacts.StatusFinalized, nil)
	queries := &artifactQueriesFake{values: []*artifacts.Artifact{artifact}, contentErr: artifacts.ErrEvidenceCorrupt}
	service := newService(t, &executionQueriesFake{run: &execution.TaskRun{
		WorkspaceKey: "WS", TaskRunID: "run-1", WorkItemID: "TASK-1",
		Status: execution.StatusSucceeded, CreatedAt: now, UpdatedAt: now,
	}}, &interactionQueriesFake{}, queries)
	result, err := service.ReadEvidence(t.Context(), Query{
		WorkspaceKey: "WS", OwnerKind: OwnerExecution, OwnerID: "run-1",
	}, artifacts.EvidenceDiff)
	if err != nil {
		t.Fatal(err)
	}
	if result.Evidence.State != EvidenceCorrupt || result.Evidence.FailureClass != "durable_content_integrity" || len(result.Content) != 0 {
		t.Fatalf("evidence = %+v", result)
	}
}

func TestReadEvidenceReturnsDurableScrollbackWithoutTranscriptParsing(t *testing.T) {
	now := time.Date(2026, 8, 12, 10, 0, 0, 0, time.UTC)
	session := validSession(now)
	session.SessionID = "history-record-1"
	artifact := sessionArtifactRow(now, "scrollback-1", artifacts.StatusFinalized)
	artifact.OwnerID = session.SessionID
	artifact.SessionID = session.SessionID
	artifact.Type = "scrollback"
	queries := &artifactQueriesFake{
		values:  []*artifacts.Artifact{artifact},
		content: map[string][]byte{"scrollback-1": []byte("first\nsecond")},
	}
	service := newService(t, &executionQueriesFake{}, &interactionQueriesFake{session: session}, queries)

	result, err := service.ReadEvidence(t.Context(), Query{
		WorkspaceKey: "WS", OwnerKind: OwnerInteraction, OwnerID: "history-record-1", WorkItemID: "TASK-1",
	}, artifacts.EvidenceScrollback)
	if err != nil {
		t.Fatal(err)
	}
	if result.Evidence.State != EvidenceFinalized || string(result.Content) != "first\nsecond" {
		t.Fatalf("scrollback = %+v", result)
	}
	if queries.contentCalls != 1 {
		t.Fatalf("content reads = %d, want 1", queries.contentCalls)
	}
}

func TestReadEvidenceSelectsLatestExecutionAttempt(t *testing.T) {
	now := time.Date(2026, 8, 12, 10, 0, 0, 0, time.UTC)
	first := artifactRow(now, "transcript-1", "transcript", artifacts.StatusFailed, map[string]string{
		"task_run_attempt": "1", "loom.evidence.failure_class": "evidence_corrupt",
	})
	second := artifactRow(now.Add(time.Minute), "transcript-2", "transcript", artifacts.StatusFinalized, map[string]string{
		"task_run_attempt": "2",
	})
	queries := &artifactQueriesFake{
		values:  []*artifacts.Artifact{first, second},
		content: map[string][]byte{"transcript-2": canonicalTranscript(t, "retry succeeded")},
	}
	service := newService(t, &executionQueriesFake{run: &execution.TaskRun{
		WorkspaceKey: "WS", TaskRunID: "run-1", WorkItemID: "TASK-1",
		Status: execution.StatusSucceeded, CreatedAt: now, UpdatedAt: now,
	}}, &interactionQueriesFake{}, queries)

	result, err := service.Transcript(t.Context(), Query{
		WorkspaceKey: "WS", OwnerKind: OwnerExecution, OwnerID: "run-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Evidence.ArtifactID != "transcript-2" || result.Evidence.State != EvidenceFinalized ||
		len(result.Events) != 1 || result.Events[0].Text != "retry succeeded" {
		t.Fatalf("transcript = %+v, want latest successful attempt", result)
	}
	if len(result.Capture.Evidence) != 1 {
		t.Fatalf("capture evidence = %+v, want one selected facet", result.Capture.Evidence)
	}
}

func TestReadEvidenceRejectsDuplicateKindsWithinOneExecutionAttempt(t *testing.T) {
	now := time.Date(2026, 8, 12, 10, 0, 0, 0, time.UTC)
	first := artifactRow(now, "scrollback-1", "scrollback", artifacts.StatusFinalized, map[string]string{"task_run_attempt": "2"})
	second := artifactRow(now, "scrollback-2", "scrollback", artifacts.StatusFinalized, map[string]string{"task_run_attempt": "2"})
	queries := &artifactQueriesFake{values: []*artifacts.Artifact{first, second}}
	service := newService(t, &executionQueriesFake{run: &execution.TaskRun{
		WorkspaceKey: "WS", TaskRunID: "run-1", WorkItemID: "TASK-1",
		Status: execution.StatusSucceeded, CreatedAt: now, UpdatedAt: now,
	}}, &interactionQueriesFake{}, queries)

	_, err := service.ReadEvidence(t.Context(), Query{
		WorkspaceKey: "WS", OwnerKind: OwnerExecution, OwnerID: "run-1",
	}, artifacts.EvidenceScrollback)
	if !errors.Is(err, ErrInvalidPersistedState) {
		t.Fatalf("error = %v, want invalid persisted state", err)
	}
	if queries.contentCalls != 0 {
		t.Fatalf("content reads = %d, want 0", queries.contentCalls)
	}
}

func newService(t *testing.T, executions execution.TaskRunQueries, interactions interaction.SessionQueries, artifactQueries artifacts.QueryAPI) *Service {
	t.Helper()
	service, err := New(executions, interactions, artifactQueries)
	if err != nil {
		t.Fatal(err)
	}
	return service
}

func validSession(now time.Time) *interaction.AgentSession {
	return &interaction.AgentSession{
		WorkspaceKey: "WS", SessionID: "session-1", AgentID: "agent-1", TaskID: "TASK-1",
		Status: interaction.SessionCompleted, StartedAt: now.Add(-time.Minute),
		CreatedAt: now.Add(-time.Hour), UpdatedAt: now,
	}
}

func artifactRow(now time.Time, id, kind string, status artifacts.DurableStatus, metadata map[string]string) *artifacts.Artifact {
	metadata = cloneMetadata(metadata)
	if metadata == nil {
		metadata = map[string]string{}
	}
	switch status {
	case artifacts.StatusFinalized:
		metadata[artifacts.MetadataEvidenceCaptureStatus] = "finalized"
		if _, ok := metadata[artifacts.MetadataEvidenceTruncated]; !ok {
			metadata[artifacts.MetadataEvidenceTruncated] = "false"
		}
		if metadata[artifacts.MetadataEvidenceTruncated] == "true" {
			metadata[artifacts.MetadataEvidenceTruncateReason] = "canonical_output_limit"
		}
	case artifacts.StatusFailed:
		metadata[artifacts.MetadataEvidenceCaptureStatus] = "capture_failed"
		if _, ok := metadata["loom.evidence.failure_class"]; !ok {
			metadata["loom.evidence.failure_class"] = "capture_failed"
		}
	}
	row := &artifacts.Artifact{
		WorkspaceKey: "WS", ArtifactID: id, OwnerType: artifacts.OwnerTaskRun, OwnerID: "run-1",
		TaskID: "TASK-1", Type: kind, DurableStatus: status, Metadata: metadata,
		CreatedAt: now, UpdatedAt: now,
	}
	if status == artifacts.StatusFinalized {
		row.FinalizedAt = &now
	}
	return row
}

func sessionArtifactRow(now time.Time, id string, status artifacts.DurableStatus) *artifacts.Artifact {
	row := artifactRow(now, id, "transcript", status, nil)
	row.OwnerType = artifacts.OwnerSession
	row.OwnerID = "session-1"
	row.SessionID = "session-1"
	row.AgentID = "agent-1"
	return row
}

func canonicalTranscript(t *testing.T, text string) []byte {
	t.Helper()
	value, err := json.Marshal(artifacts.TranscriptEvent{
		Seq: 1, Timestamp: time.Date(2026, 8, 12, 10, 0, 0, 0, time.UTC),
		Role: artifacts.TranscriptRoleAgent, Type: artifacts.TranscriptEventText, Text: text,
	})
	if err != nil {
		t.Fatal(err)
	}
	return append(value, '\n')
}

func timePointer(value time.Time) *time.Time { return &value }
