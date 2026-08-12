package runcapture

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/modules/artifacts"
	"github.com/tysonthomas9/loomcli/internal/modules/artifacts/transcript"
	"github.com/tysonthomas9/loomcli/internal/modules/execution"
	"github.com/tysonthomas9/loomcli/internal/modules/interaction"
)

type executionQueriesFake struct {
	run   *execution.TaskRun
	err   error
	calls int
}

func (fake *executionQueriesFake) GetTaskRun(context.Context, string, string) (*execution.TaskRun, error) {
	fake.calls++
	return fake.run, fake.err
}
func (*executionQueriesFake) ListActiveTaskRuns(context.Context, execution.ActiveTaskRunQuery) ([]*execution.TaskRun, error) {
	return nil, nil
}
func (*executionQueriesFake) ListTaskRunEvents(context.Context, execution.TaskRunEventQuery) ([]*execution.TaskRunEvent, error) {
	return nil, nil
}

type interactionQueriesFake struct {
	session *interaction.AgentSession
	err     error
	calls   int
}

func (fake *interactionQueriesFake) GetSession(context.Context, string, string) (*interaction.AgentSession, error) {
	fake.calls++
	return fake.session, fake.err
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
	result := make([]*artifacts.Artifact, len(fake.values))
	copy(result, fake.values)
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
	value, err := json.Marshal(transcript.Event{
		Seq: 1, Timestamp: time.Date(2026, 8, 12, 10, 0, 0, 0, time.UTC),
		Role: transcript.RoleAssistant, Type: transcript.EventText, Text: text,
	})
	if err != nil {
		t.Fatal(err)
	}
	return append(value, '\n')
}
