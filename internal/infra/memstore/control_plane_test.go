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

func TestNodePlacementCopyIndependence(t *testing.T) {
	st := New()
	ctx := t.Context()
	attached := time.Date(2026, 8, 6, 12, 0, 0, 0, time.UTC)
	nextDelete := time.Date(2026, 8, 6, 12, 5, 0, 0, time.UTC)
	lostAt := time.Date(2026, 8, 6, 12, 1, 0, 0, time.UTC)
	ambiguousAt := time.Date(2026, 8, 6, 12, 2, 0, 0, time.UTC)

	created, err := st.Nodes().Create(ctx, store.NodeCreate{
		WorkspaceKey:    "WS",
		NodeID:          "node-placed",
		RuntimeProvider: domain.RuntimeProviderDaytona,
		Placement: &domain.NodePlacement{
			SandboxID:                "sandbox-1",
			Generation:               1,
			ReservedVCPU:             2,
			ReservedMemGiB:           4,
			State:                    domain.PlacementStateActive,
			FirstAttachedAt:          &attached,
			LostAt:                   &lostAt,
			ProvisionAmbiguousAt:     &ambiguousAt,
			CreateAbsenceConfirmedAt: &ambiguousAt,
			SnapshotRef:              "snapshot-1",
			DeleteAttempts:           2,
			LastDeleteError:          "delete failed",
			NextDeleteAt:             nextDelete,
		},
		TTL: time.Minute,
	})
	if err != nil {
		t.Fatalf("create node: %v", err)
	}
	created.Placement.SandboxID = "mutated-return"
	*created.Placement.FirstAttachedAt = attached.Add(time.Hour)
	*created.Placement.LostAt = lostAt.Add(time.Hour)
	*created.Placement.ProvisionAmbiguousAt = ambiguousAt.Add(time.Hour)
	*created.Placement.CreateAbsenceConfirmedAt = ambiguousAt.Add(time.Hour)

	got, err := st.Nodes().Get(ctx, "WS", "node-placed")
	if err != nil {
		t.Fatalf("get node: %v", err)
	}
	if got.Placement == nil {
		t.Fatal("get placement = nil, want placement")
	}
	if got.Placement.SandboxID != "sandbox-1" ||
		!got.Placement.FirstAttachedAt.Equal(attached) ||
		!got.Placement.LostAt.Equal(lostAt) ||
		!got.Placement.ProvisionAmbiguousAt.Equal(ambiguousAt) ||
		!got.Placement.CreateAbsenceConfirmedAt.Equal(ambiguousAt) ||
		got.Placement.DeleteAttempts != 2 ||
		got.Placement.LastDeleteError != "delete failed" ||
		!got.Placement.NextDeleteAt.Equal(nextDelete) {
		t.Fatalf("stored placement mutated through create result: %+v", got.Placement)
	}

	got.Placement.SandboxID = "mutated-get"
	list, err := st.Nodes().List(ctx, "WS")
	if err != nil {
		t.Fatalf("list nodes: %v", err)
	}
	if len(list) != 1 || list[0].Placement == nil || list[0].Placement.SandboxID != "sandbox-1" {
		t.Fatalf("stored placement mutated through get result: %+v", list)
	}

	replacement := &domain.NodePlacement{
		SandboxID:       "sandbox-2",
		Generation:      2,
		State:           domain.PlacementStateProvisioning,
		DeleteAttempts:  3,
		LastDeleteError: "retry later",
		NextDeleteAt:    nextDelete.Add(time.Minute),
	}
	updated, err := st.Nodes().Update(ctx, "WS", "node-placed", store.NodeUpdate{Placement: &replacement})
	if err != nil {
		t.Fatalf("update placement: %v", err)
	}
	if updated.Placement == nil || updated.Placement.SandboxID != "sandbox-2" || updated.Placement.DeleteAttempts != 3 {
		t.Fatalf("updated placement = %+v, want sandbox-2", updated.Placement)
	}
	replacement.SandboxID = "mutated-patch"
	replacement.LastDeleteError = "mutated-error"

	got, err = st.Nodes().Get(ctx, "WS", "node-placed")
	if err != nil {
		t.Fatalf("get after update: %v", err)
	}
	if got.Placement == nil || got.Placement.SandboxID != "sandbox-2" || got.Placement.LastDeleteError != "retry later" {
		t.Fatalf("stored placement mutated through patch input: %+v", got.Placement)
	}

	var clear *domain.NodePlacement
	updated, err = st.Nodes().Update(ctx, "WS", "node-placed", store.NodeUpdate{Placement: &clear})
	if err != nil {
		t.Fatalf("clear placement: %v", err)
	}
	if updated.Placement != nil {
		t.Fatalf("cleared placement = %+v, want nil", updated.Placement)
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
