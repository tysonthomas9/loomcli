package memstore

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/store"
)

func TestControlPlaneStores(t *testing.T) {
	st := New()
	ctx := t.Context()

	node, err := st.Nodes().Create(ctx, store.NodeCreate{WorkspaceKey: "WS", NodeID: "node-1", RuntimeProvider: domain.RuntimeProviderLocal, TTL: time.Minute})
	if err != nil {
		t.Fatalf("create node: %v", err)
	}
	if node.NodeID != "node-1" || node.ExpiresAt.IsZero() {
		t.Fatalf("node = %+v", node)
	}
	drain := domain.NodeDrainDraining
	updated, err := st.Nodes().Update(ctx, "WS", "node-1", store.NodeUpdate{DrainState: &drain})
	if err != nil {
		t.Fatalf("update node: %v", err)
	}
	if updated.DrainState != drain {
		t.Fatalf("drain state = %q", updated.DrainState)
	}

	session, err := st.AgentSessions().Create(ctx, store.AgentSessionCreate{
		WorkspaceKey: "WS",
		SessionID:    "sess-1",
		AgentID:      "agent-1",
		NodeID:       "node-1",
		Status:       domain.AgentSessionRunning,
		TaskID:       "T-1",
	})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	if session.SessionID != "sess-1" {
		t.Fatalf("session = %+v", session)
	}
	sessions, err := st.AgentSessions().List(ctx, "WS", store.AgentSessionFilter{NodeID: "node-1", Status: domain.AgentSessionRunning})
	if err != nil {
		t.Fatalf("list sessions: %v", err)
	}
	if len(sessions) != 1 || sessions[0].SessionID != "sess-1" {
		t.Fatalf("sessions = %+v", sessions)
	}
}

// TestAgentSessionFilter_KindAndParent guards the new filter dimensions
// added for the Agent.OrchestratorSessionID migration. Without these,
// callers couldn't ask "give me the orchestration session whose child is
// task session T" via the store interface — they had to list everything
// and filter client-side, which is what motivated keeping the
// denormalized OrchestratorSessionID cache on Agent in the first place.
func TestAgentSessionFilter_KindAndParent(t *testing.T) {
	st := New()
	ctx := t.Context()

	if _, err := st.AgentSessions().Create(ctx, store.AgentSessionCreate{
		WorkspaceKey: "WS", SessionID: "orch-1", AgentID: "nova",
		Kind: domain.AgentSessionKindOrchestration, Status: domain.AgentSessionRunning,
	}); err != nil {
		t.Fatalf("create orch: %v", err)
	}
	if _, err := st.AgentSessions().Create(ctx, store.AgentSessionCreate{
		WorkspaceKey: "WS", SessionID: "task-1a", AgentID: "worker-a",
		Kind: domain.AgentSessionKindTask, ParentSessionID: "orch-1",
		Status: domain.AgentSessionRunning,
	}); err != nil {
		t.Fatalf("create task-1a: %v", err)
	}
	if _, err := st.AgentSessions().Create(ctx, store.AgentSessionCreate{
		WorkspaceKey: "WS", SessionID: "task-1b", AgentID: "worker-b",
		Kind: domain.AgentSessionKindTask, ParentSessionID: "orch-1",
		Status: domain.AgentSessionRunning,
	}); err != nil {
		t.Fatalf("create task-1b: %v", err)
	}
	if _, err := st.AgentSessions().Create(ctx, store.AgentSessionCreate{
		WorkspaceKey: "WS", SessionID: "task-x", AgentID: "worker-x",
		Kind: domain.AgentSessionKindTask, ParentSessionID: "orch-other",
		Status: domain.AgentSessionRunning,
	}); err != nil {
		t.Fatalf("create task-x: %v", err)
	}

	// Kind-only filter
	got, err := st.AgentSessions().List(ctx, "WS", store.AgentSessionFilter{Kind: domain.AgentSessionKindOrchestration})
	if err != nil {
		t.Fatalf("list kind=orch: %v", err)
	}
	if len(got) != 1 || got[0].SessionID != "orch-1" {
		t.Fatalf("kind=orch results: want [orch-1], got %v", sessionIDs(got))
	}

	// Parent-only filter
	got, err = st.AgentSessions().List(ctx, "WS", store.AgentSessionFilter{ParentSessionID: "orch-1"})
	if err != nil {
		t.Fatalf("list parent=orch-1: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("parent=orch-1 results: want 2, got %v", sessionIDs(got))
	}

	// Combined: kind + parent
	got, err = st.AgentSessions().List(ctx, "WS", store.AgentSessionFilter{
		Kind: domain.AgentSessionKindTask, ParentSessionID: "orch-1",
	})
	if err != nil {
		t.Fatalf("list kind=task,parent=orch-1: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("kind+parent results: want 2, got %v", sessionIDs(got))
	}

	// Mismatch returns empty
	got, err = st.AgentSessions().List(ctx, "WS", store.AgentSessionFilter{
		Kind: domain.AgentSessionKindOrchestration, ParentSessionID: "orch-1",
	})
	if err != nil {
		t.Fatalf("list mismatch: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("mismatch results: want empty, got %v", sessionIDs(got))
	}
}

func TestAgentSessionListPage_TimeFilterSortTotalLimit(t *testing.T) {
	st := New()
	ctx := t.Context()
	base := time.Date(2026, 7, 16, 12, 0, 0, 0, time.UTC)

	create := func(id string, startedAt time.Time, kind domain.AgentSessionKind, parent string) {
		t.Helper()
		if _, err := st.AgentSessions().Create(ctx, store.AgentSessionCreate{
			WorkspaceKey:    "WS",
			SessionID:       id,
			AgentID:         "worker-a",
			Kind:            kind,
			ParentSessionID: parent,
			Status:          domain.AgentSessionCompleted,
			StartedAt:       startedAt,
		}); err != nil {
			t.Fatalf("create %s: %v", id, err)
		}
	}
	create("too-old", base.Add(-2*time.Hour), domain.AgentSessionKindTask, "orch-1")
	create("oldest-match", base.Add(-50*time.Minute), domain.AgentSessionKindTask, "orch-1")
	create("middle-match", base.Add(-30*time.Minute), domain.AgentSessionKindTask, "orch-1")
	create("newest-match", base.Add(-10*time.Minute), domain.AgentSessionKindTask, "orch-1")
	create("kind-mismatch", base.Add(-5*time.Minute), domain.AgentSessionKindOrchestration, "orch-1")
	create("parent-mismatch", base.Add(-4*time.Minute), domain.AgentSessionKindTask, "other")
	create("too-new", base.Add(10*time.Minute), domain.AgentSessionKindTask, "orch-1")

	since := base.Add(-time.Hour)
	until := base
	got, total, err := st.AgentSessions().ListPage(ctx, "WS", store.AgentSessionFilter{
		Kind:            domain.AgentSessionKindTask,
		ParentSessionID: "orch-1",
		Since:           &since,
		Until:           &until,
		Limit:           2,
	})
	if err != nil {
		t.Fatalf("ListPage: %v", err)
	}
	if total != 3 {
		t.Fatalf("total = %d, want 3", total)
	}
	if ids := sessionIDs(got); len(ids) != 2 || ids[0] != "newest-match" || ids[1] != "middle-match" {
		t.Fatalf("ids = %v, want [newest-match middle-match]", ids)
	}
}

func sessionIDs(sessions []*domain.AgentSession) []string {
	ids := make([]string, 0, len(sessions))
	for _, s := range sessions {
		if s != nil {
			ids = append(ids, s.SessionID)
		}
	}
	return ids
}

func TestArtifactUploadContent(t *testing.T) {
	st := New()
	ctx := t.Context()
	if _, err := st.Artifacts().Create(ctx, store.ArtifactCreate{WorkspaceKey: "WS", ArtifactID: "artifact-1", Type: "patch"}); err != nil {
		t.Fatalf("create artifact: %v", err)
	}

	body := []byte("diff --git a/file b/file\n")
	artifact, err := st.Artifacts().UploadContent(ctx, "WS", "artifact-1", store.ArtifactContentUpload{
		Body:     bytes.NewReader(body),
		MIMEType: "text/x-diff",
	})
	if err != nil {
		t.Fatalf("upload content: %v", err)
	}
	sum := sha256.Sum256(body)
	expectedHash := "sha256:" + hex.EncodeToString(sum[:])
	if artifact.DurableStatus != "uploading" || artifact.SizeBytes != int64(len(body)) || artifact.ContentHash != expectedHash || artifact.Checksum != expectedHash {
		t.Fatalf("artifact = %+v, want uploading with size/hash", artifact)
	}
	if artifact.MIMEType != "text/x-diff" || !strings.HasPrefix(artifact.URI, "mem://artifacts/WS/artifact-1/") {
		t.Fatalf("artifact mime/uri = %q/%q", artifact.MIMEType, artifact.URI)
	}

	finalized, err := st.Artifacts().Finalize(ctx, "WS", "artifact-1", store.ArtifactFinalize{})
	if err != nil {
		t.Fatalf("finalize artifact: %v", err)
	}
	if finalized.DurableStatus != "finalized" || finalized.FinalizedAt == nil || finalized.ContentHash != expectedHash {
		t.Fatalf("finalized artifact = %+v, want finalized with original hash", finalized)
	}
	refinalized, err := st.Artifacts().Finalize(ctx, "WS", "artifact-1", store.ArtifactFinalize{ContentHash: &expectedHash})
	if err != nil {
		t.Fatalf("re-finalize artifact: %v", err)
	}
	if refinalized.DurableStatus != "finalized" || refinalized.ContentHash != expectedHash {
		t.Fatalf("re-finalized artifact = %+v, want finalized with original hash", refinalized)
	}
	if _, err := st.Artifacts().UploadContent(ctx, "WS", "artifact-1", store.ArtifactContentUpload{Body: bytes.NewReader(body)}); !errors.Is(err, domain.ErrInvalidTransition) {
		t.Fatalf("upload finalized error = %v, want ErrInvalidTransition", err)
	}
}

func TestArtifactFinalizeRejectsHashMismatch(t *testing.T) {
	st := New()
	ctx := t.Context()
	if _, err := st.Artifacts().Create(ctx, store.ArtifactCreate{WorkspaceKey: "WS", ArtifactID: "artifact-1", Type: "patch"}); err != nil {
		t.Fatalf("create artifact: %v", err)
	}
	body := []byte("patch bytes")
	uploaded, err := st.Artifacts().UploadContent(ctx, "WS", "artifact-1", store.ArtifactContentUpload{Body: bytes.NewReader(body)})
	if err != nil {
		t.Fatalf("upload content: %v", err)
	}
	badHash := "sha256:bad"
	if _, err := st.Artifacts().Finalize(ctx, "WS", "artifact-1", store.ArtifactFinalize{ContentHash: &badHash}); !errors.Is(err, domain.ErrInvalidTransition) {
		t.Fatalf("finalize mismatch error = %v, want ErrInvalidTransition", err)
	}
	artifact, err := st.Artifacts().Get(ctx, "WS", "artifact-1")
	if err != nil {
		t.Fatalf("get artifact: %v", err)
	}
	if artifact.DurableStatus != "uploading" || artifact.ContentHash != uploaded.ContentHash || artifact.FinalizedAt != nil {
		t.Fatalf("artifact after rejected finalize = %+v, want original uploading artifact", artifact)
	}
}

func TestArtifactFinalizeRejectsMissingURI(t *testing.T) {
	st := New()
	ctx := t.Context()
	if _, err := st.Artifacts().Create(ctx, store.ArtifactCreate{WorkspaceKey: "WS", ArtifactID: "artifact-1", Type: "patch"}); err != nil {
		t.Fatalf("create artifact: %v", err)
	}
	if _, err := st.Artifacts().Finalize(ctx, "WS", "artifact-1", store.ArtifactFinalize{}); !errors.Is(err, domain.ErrInvalidTransition) {
		t.Fatalf("finalize missing uri error = %v, want ErrInvalidTransition", err)
	}
}
