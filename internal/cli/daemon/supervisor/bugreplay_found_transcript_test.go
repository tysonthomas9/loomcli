//go:build daemon_bugreplay

package supervisor

import (
	"context"
	"errors"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/cli/config"
	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/infra/memstore"
	"github.com/tysonthomas9/loomcli/internal/store"
)

type foundTranscriptUploadStore struct {
	*memstore.Store
	artifacts store.ArtifactStore
}

func (s *foundTranscriptUploadStore) Artifacts() store.ArtifactStore { return s.artifacts }

type foundFailingArtifactStore struct{ store.ArtifactStore }

func (s foundFailingArtifactStore) Create(context.Context, store.ArtifactCreate) (*domain.Artifact, error) {
	return nil, errors.New("transcript storage unavailable")
}

// Found bug #16: an exit-zero session must not report completion if its
// provided transcript could not be stored in the control plane.
func TestBugReplay_Found16_TranscriptUploadFailureDoesNotComplete(t *testing.T) {
	base := memstore.New()
	if _, err := base.AgentSessions().Create(t.Context(), store.AgentSessionCreate{
		WorkspaceKey: "WS", SessionID: "session-1", AgentID: "worker-1",
		Kind: domain.AgentSessionKindTask, Status: domain.AgentSessionRunning,
	}); err != nil {
		t.Fatal(err)
	}
	s := newControlPlaneTestSupervisor(base)
	s.ControlStore = &foundTranscriptUploadStore{Store: base, artifacts: foundFailingArtifactStore{base.Artifacts()}}
	ap := &AgentProcess{Entry: config.AgentEntry{Worktree: "worker-1", Role: "task"}}
	s.completeControlPlaneAgentSession(ap, agentSessionCompletionInput{
		sessionID: "session-1", exitCode: 0, transcriptData: []byte(`{"event":"done"}`),
	})
	session, err := base.AgentSessions().Get(t.Context(), "WS", "session-1")
	if err != nil {
		t.Fatal(err)
	}
	if session.Status == domain.AgentSessionCompleted {
		t.Fatalf("session status = %q after transcript upload failed; completion requires transcript evidence", session.Status)
	}
}
