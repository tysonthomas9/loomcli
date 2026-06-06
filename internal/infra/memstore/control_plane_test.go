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
