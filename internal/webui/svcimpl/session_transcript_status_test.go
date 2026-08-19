package svcimpl

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/infra/memstore"
	"github.com/tysonthomas9/loomcli/internal/store"
	"github.com/tysonthomas9/loomcli/internal/webui/service"
)

// artifactErrStore wraps a memstore so the artifact content read fails with a
// chosen error, exercising the transcript reader's upstream-failure mapping.
type artifactErrStore struct {
	*memstore.Store
	readErr error
	getErr  error
}

func (s artifactErrStore) Artifacts() store.ArtifactStore {
	return failingArtifacts{ArtifactStore: s.Store.Artifacts(), readErr: s.readErr, getErr: s.getErr}
}

type failingArtifacts struct {
	store.ArtifactStore
	readErr error
	getErr  error
}

func (f failingArtifacts) ReadContent(context.Context, string, string) ([]byte, error) {
	return nil, f.readErr
}

func (f failingArtifacts) Get(ctx context.Context, workspaceKey, artifactID string) (*domain.Artifact, error) {
	if f.getErr != nil {
		return nil, f.getErr
	}
	return f.ArtifactStore.Get(ctx, workspaceKey, artifactID)
}

func seedTranscriptRefSession(t *testing.T, st *memstore.Store) {
	t.Helper()
	if _, err := st.AgentSessions().Create(t.Context(), store.AgentSessionCreate{
		WorkspaceKey: "WS",
		SessionID:    "flue-task-run-1",
		AgentID:      "flue-task-agent",
		Kind:         domain.AgentSessionKindTask,
		TaskID:       "TASK-FLUE-1",
		Status:       domain.AgentSessionCompleted,
		Metadata: map[string]string{
			"runtime":        "flue",
			"task_run_id":    "task-run-1",
			"transcript_ref": "artifact://transcript-task-run-1",
		},
	}); err != nil {
		t.Fatalf("create control-plane session: %v", err)
	}
}

// A transcript store that is down is a retryable upstream failure, not a bug in
// this service: it must surface as 503 so clients (and the UI) can retry,
// instead of being laundered into an opaque 500.
func TestGetSessionTranscriptUpstreamUnavailableMapsToServiceUnavailable(t *testing.T) {
	st := memstore.New()
	seedTranscriptRefSession(t, st)
	createFinalizedArtifact(t, st, "transcript-task-run-1", "flue-task-run-1", "TASK-FLUE-1", "task-run-1", "transcript", "application/x-ndjson", []byte("{}\n"))

	wrapped := artifactErrStore{
		Store:   st,
		readErr: fmt.Errorf("fleetdb: GET /artifacts/x/content: HTTP 502: %w", domain.ErrUnavailable),
	}
	svc := NewSessionService(wrapped, nil)

	_, err := svc.GetSessionTranscript(t.Context(), "WS", "TASK-FLUE-1", "flue-task-run-1")
	assertServiceErrorKind(t, err, service.KindUnavailable)
}

// A transcript_ref pointing at an artifact that no longer exists is a missing
// resource (404), not an internal error.
func TestGetSessionTranscriptMissingManagedContentMapsToNotFound(t *testing.T) {
	st := memstore.New()
	seedTranscriptRefSession(t, st)

	wrapped := artifactErrStore{
		Store:   st,
		readErr: fmt.Errorf("read content: %w", domain.ErrNotFound),
		getErr:  fmt.Errorf("get artifact: %w", domain.ErrNotFound),
	}
	svc := NewSessionService(wrapped, nil)

	_, err := svc.GetSessionTranscript(t.Context(), "WS", "TASK-FLUE-1", "flue-task-run-1")
	assertServiceErrorKind(t, err, service.KindNotFound)
}

// Anything genuinely unclassified still reports 500 — the typed mappings must
// not swallow real bugs.
func TestGetSessionTranscriptUnclassifiedFailureStaysInternal(t *testing.T) {
	st := memstore.New()
	seedTranscriptRefSession(t, st)
	createFinalizedArtifact(t, st, "transcript-task-run-1", "flue-task-run-1", "TASK-FLUE-1", "task-run-1", "transcript", "application/x-ndjson", []byte("{}\n"))

	wrapped := artifactErrStore{Store: st, readErr: errors.New("corrupt content index")}
	svc := NewSessionService(wrapped, nil)

	_, err := svc.GetSessionTranscript(t.Context(), "WS", "TASK-FLUE-1", "flue-task-run-1")
	assertServiceErrorKind(t, err, service.KindInternal)
}
