package sessioncoord

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/app/query/runcapture"
	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/infra/memstore"
	"github.com/tysonthomas9/loomcli/internal/modules/artifacts"
	"github.com/tysonthomas9/loomcli/internal/modules/interaction"
	"github.com/tysonthomas9/loomcli/internal/store"
	"github.com/tysonthomas9/loomcli/internal/webui/apperrors"
)

type historyReaderFake struct {
	records []interaction.SessionHistoryRecord
	err     error
}

func (fake *historyReaderFake) List(context.Context, string, string) ([]interaction.SessionHistoryRecord, error) {
	return append([]interaction.SessionHistoryRecord(nil), fake.records...), fake.err
}

type runCaptureFake struct {
	capture   *runcapture.RunCapture
	getErr    error
	result    *runcapture.EvidenceContent
	err       error
	query     runcapture.Query
	kind      artifacts.EvidenceKind
	readCalls int
	getCalls  int
}

func (fake *runCaptureFake) Get(_ context.Context, query runcapture.Query) (*runcapture.RunCapture, error) {
	fake.getCalls++
	fake.query = query
	if fake.getErr != nil {
		return nil, fake.getErr
	}
	if fake.capture == nil {
		return nil, runcapture.ErrNotFound
	}
	return fake.capture, nil
}

func (*runCaptureFake) List(context.Context, runcapture.ArchiveQuery) ([]*runcapture.RunCapture, error) {
	return nil, nil
}

func (fake *runCaptureFake) ReadEvidence(_ context.Context, query runcapture.Query, kind artifacts.EvidenceKind) (*runcapture.EvidenceContent, error) {
	fake.readCalls++
	fake.query = query
	fake.kind = kind
	return fake.result, fake.err
}

func (*runCaptureFake) Transcript(context.Context, runcapture.Query) (*runcapture.TranscriptEvidence, error) {
	return nil, runcapture.ErrNotFound
}

func TestSessionProjectionNeverReadsLegacyArchive(t *testing.T) {
	runtimeDir := t.TempDir()
	legacy := filepath.Join(runtimeDir, "sessions", "session-1")
	if err := os.MkdirAll(legacy, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(legacy, "agent_transcript.jsonl"), []byte("legacy bytes"), 0o600); err != nil {
		t.Fatal(err)
	}
	service := NewSessionService(nil, nil, nil)
	_, err := service.GetSessionTranscript(t.Context(), "WS", "TASK-1", "session-1")
	var serviceErr *apperrors.ServiceError
	if !errors.As(err, &serviceErr) || serviceErr.Kind != apperrors.KindUnavailable {
		t.Fatalf("GetSessionTranscript error = %v, want unavailable", err)
	}
	if got, err := os.ReadFile(filepath.Join(legacy, "agent_transcript.jsonl")); err != nil || string(got) != "legacy bytes" {
		t.Fatalf("legacy evidence changed: %q, %v", got, err)
	}
}

func TestSessionStatusMappingsRemainOwnerDerived(t *testing.T) {
	if sessionStatusFromTaskRun("completed") != StatusCompleted ||
		sessionStatusFromAgentSession("failed") != StatusFailed {
		t.Fatal("owner lifecycle status mapping changed")
	}
}

func TestTaskSessionProjectionKeepsRunOutcomeSeparateFromEvidenceFailure(t *testing.T) {
	ctx := t.Context()
	st := memstore.New()
	if _, err := st.TaskRuns().Create(ctx, store.TaskRunCreate{
		WorkspaceKey: "WS", TaskRunID: "run-1", TaskID: "TASK-1",
		Status: domain.TaskRunCompleted, Runner: "codex",
	}); err != nil {
		t.Fatal(err)
	}
	captures := &runCaptureFake{capture: &runcapture.RunCapture{
		WorkspaceKey: "WS", OwnerKind: runcapture.OwnerExecution, OwnerID: "run-1",
		WorkItemID: "TASK-1", Status: string(domain.TaskRunCompleted),
		Evidence: []runcapture.Evidence{
			{Kind: artifacts.EvidenceTranscript, State: runcapture.EvidenceCaptureFailed, FailureClass: "capture_unavailable"},
			{Kind: artifacts.EvidenceDiff, State: runcapture.EvidenceTruncated, ArtifactID: "patch-1"},
		},
	}}
	service := NewSessionService(st, nil, captures)

	items, err := service.ListTaskSessions(ctx, "WS", "TASK-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].Status != StatusCompleted {
		t.Fatalf("items = %+v, want one completed run", items)
	}
	item := items[0]
	if item.TranscriptEvidenceStatus != string(runcapture.EvidenceCaptureFailed) ||
		item.TranscriptFailureClass != "capture_unavailable" || item.HasTranscript {
		t.Fatalf("transcript projection = %+v", item)
	}
	if item.DiffEvidenceStatus != string(runcapture.EvidenceTruncated) || !item.HasDiff {
		t.Fatalf("diff projection = %+v", item)
	}
}

func TestSessionDiffReadsRunCaptureInsteadOfArtifactOrFilesystemFallback(t *testing.T) {
	ctx := t.Context()
	st := memstore.New()
	if _, err := st.TaskRuns().Create(ctx, store.TaskRunCreate{
		WorkspaceKey: "WS", TaskRunID: "run-1", TaskID: "TASK-1",
		Status: domain.TaskRunCompleted, Runner: "codex",
	}); err != nil {
		t.Fatal(err)
	}
	captures := &runCaptureFake{result: &runcapture.EvidenceContent{
		Evidence: runcapture.Evidence{Kind: artifacts.EvidenceDiff, State: runcapture.EvidenceFinalized},
		Content:  []byte("diff --git a/a b/a\n"),
	}}
	service := NewSessionService(st, nil, captures)

	got, err := service.GetSessionDiff(ctx, "WS", "TASK-1", "run-1")
	if err != nil {
		t.Fatal(err)
	}
	if got != "diff --git a/a b/a\n" || captures.readCalls != 1 || captures.kind != artifacts.EvidenceDiff ||
		captures.query != (runcapture.Query{
			WorkspaceKey: "WS", OwnerKind: runcapture.OwnerExecution,
			OwnerID: "run-1", WorkItemID: "TASK-1",
		}) {
		t.Fatalf("diff/query/calls = %q/%+v/%d", got, captures.query, captures.readCalls)
	}
}

func TestSessionHistoryProjectsDurableScrollbackState(t *testing.T) {
	history := &historyReaderFake{records: []interaction.SessionHistoryRecord{{
		ID: "history-record-1", IssueID: "TASK-1", Status: "completed",
	}}}
	captures := &runCaptureFake{capture: &runcapture.RunCapture{
		WorkspaceKey: "WS", OwnerKind: runcapture.OwnerInteraction, OwnerID: "history-record-1",
		WorkItemID: "TASK-1", Evidence: []runcapture.Evidence{{
			Kind: artifacts.EvidenceScrollback, State: runcapture.EvidenceCaptureFailed,
			FailureClass: "capture_unavailable",
		}},
	}}
	service := NewSessionService(nil, history, captures)
	items, err := service.ListSessionHistory(t.Context(), "WS", "TASK-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].Status != "completed" ||
		items[0].ScrollbackEvidenceStatus != string(runcapture.EvidenceCaptureFailed) ||
		items[0].ScrollbackFailureClass != "capture_unavailable" {
		t.Fatalf("history = %+v", items)
	}
	if captures.getCalls != 1 || captures.query.OwnerID != "history-record-1" || captures.query.WorkItemID != "TASK-1" {
		t.Fatalf("capture query = %+v calls=%d", captures.query, captures.getCalls)
	}
}

func TestSessionScrollbackReadsAuthorizedRunCaptureEvidence(t *testing.T) {
	history := &historyReaderFake{records: []interaction.SessionHistoryRecord{{
		ID: "history-record-1", IssueID: "TASK-1", SessionName: "terminal-1",
		Status: "completed", StartedAt: time.Date(2026, 8, 12, 10, 0, 0, 0, time.UTC),
	}}}
	captures := &runCaptureFake{result: &runcapture.EvidenceContent{
		Evidence: runcapture.Evidence{Kind: artifacts.EvidenceScrollback, State: runcapture.EvidenceFinalized},
		Content:  []byte("first\nsecond"),
	}}
	service := NewSessionService(nil, history, captures)

	result, err := service.GetSessionScrollback(t.Context(), "WS", "TASK-1", "history-record-1")
	if err != nil {
		t.Fatal(err)
	}
	if result.Content != "first\nsecond" || result.Lines != 2 {
		t.Fatalf("scrollback = %+v", result)
	}
	if captures.readCalls != 1 || captures.kind != artifacts.EvidenceScrollback {
		t.Fatalf("capture call = %d kind %q", captures.readCalls, captures.kind)
	}
	want := runcapture.Query{
		WorkspaceKey: "WS", OwnerKind: runcapture.OwnerInteraction,
		OwnerID: "history-record-1", WorkItemID: "TASK-1",
	}
	if captures.query != want {
		t.Fatalf("capture query = %+v, want %+v", captures.query, want)
	}
}

func TestSessionScrollbackMapsDurableEvidenceStates(t *testing.T) {
	history := &historyReaderFake{records: []interaction.SessionHistoryRecord{{ID: "record-1", IssueID: "TASK-1"}}}
	tests := []struct {
		name  string
		state runcapture.EvidenceState
		err   error
		kind  apperrors.ErrorKind
	}{
		{name: "missing", state: runcapture.EvidenceMissing, kind: apperrors.KindNotFound},
		{name: "pending", state: runcapture.EvidencePending, kind: apperrors.KindUnavailable},
		{name: "failed", state: runcapture.EvidenceCaptureFailed, kind: apperrors.KindUnavailable},
		{name: "content unavailable", state: runcapture.EvidenceContentUnavailable, kind: apperrors.KindUnavailable},
		{name: "corrupt", state: runcapture.EvidenceCorrupt, kind: apperrors.KindInternal},
		{name: "archive unavailable", err: runcapture.ErrUnavailable, kind: apperrors.KindUnavailable},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			captures := &runCaptureFake{err: test.err}
			if test.err == nil {
				captures.result = &runcapture.EvidenceContent{Evidence: runcapture.Evidence{State: test.state}}
			}
			service := NewSessionService(nil, history, captures)
			_, err := service.GetSessionScrollback(t.Context(), "WS", "TASK-1", "record-1")
			var serviceErr *apperrors.ServiceError
			if !errors.As(err, &serviceErr) || serviceErr.Kind != test.kind {
				t.Fatalf("error = %v, want kind %q", err, test.kind)
			}
		})
	}
}

func TestSessionScrollbackRejectsRecordMismatchBeforeEvidenceRead(t *testing.T) {
	captures := &runCaptureFake{}
	service := NewSessionService(nil, &historyReaderFake{}, captures)
	_, err := service.GetSessionScrollback(t.Context(), "WS", "TASK-1", "foreign-record")
	var serviceErr *apperrors.ServiceError
	if !errors.As(err, &serviceErr) || serviceErr.Kind != apperrors.KindNotFound {
		t.Fatalf("error = %v, want not found", err)
	}
	if captures.readCalls != 0 {
		t.Fatalf("evidence reads = %d, want 0", captures.readCalls)
	}
}
