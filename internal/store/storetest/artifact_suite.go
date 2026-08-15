package storetest

import (
	"context"
	"errors"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/store"
)

// ArtifactHarness wires one backend's artifact surfaces into the shared suite.
// Every subtest gets a fresh harness.
type ArtifactHarness struct {
	Workspace string
	Artifacts store.ArtifactStore
}

// RunArtifactCreateRetryConformance pins the create/retry/content semantics
// shared by in-process and fleet-db artifact stores.
func RunArtifactCreateRetryConformance(t *testing.T, newHarness func(t testing.TB) *ArtifactHarness) {
	t.Helper()
	t.Run("CreateRejectsDuplicateID", func(t *testing.T) {
		h := newHarness(t)
		in := artifactCreate(h.Workspace, "artifact-fixed")
		if _, err := h.Artifacts.Create(context.Background(), in); err != nil {
			t.Fatalf("first Create: %v", err)
		}
		if _, err := h.Artifacts.Create(context.Background(), in); !errors.Is(err, domain.ErrAlreadyExists) {
			t.Fatalf("second Create err = %v, want ErrAlreadyExists", err)
		}
	})

	t.Run("FinalizedUploadRetryPreservesContent", func(t *testing.T) {
		h := newHarness(t)
		ctx := context.Background()
		in := artifactCreate(h.Workspace, "artifact-upload-retry")
		first, err := store.UploadContentArtifact(ctx, h.Artifacts, in, []byte("first content"))
		if err != nil {
			t.Fatalf("first UploadContentArtifact: %v", err)
		}
		second, err := store.UploadContentArtifact(ctx, h.Artifacts, in, []byte("replacement content"))
		if err != nil {
			t.Fatalf("second UploadContentArtifact: %v", err)
		}
		if first.ArtifactID != second.ArtifactID || first.ContentHash != second.ContentHash || first.UpdatedAt != second.UpdatedAt {
			t.Fatalf("retry changed artifact: first=%+v second=%+v", first, second)
		}
		content := readArtifactContent(t, ctx, h.Artifacts, h.Workspace, in.ArtifactID)
		if string(content) != "first content" {
			t.Fatalf("content = %q, want first content", content)
		}
	})

	t.Run("ContentRoundTrip", func(t *testing.T) {
		h := newHarness(t)
		ctx := context.Background()
		in := artifactCreate(h.Workspace, "artifact-content-roundtrip")
		if _, err := store.UploadContentArtifact(ctx, h.Artifacts, in, []byte("round trip bytes\n")); err != nil {
			t.Fatalf("UploadContentArtifact: %v", err)
		}
		content := readArtifactContent(t, ctx, h.Artifacts, h.Workspace, in.ArtifactID)
		if string(content) != "round trip bytes\n" {
			t.Fatalf("content = %q", content)
		}
	})
}

func artifactCreate(workspace, artifactID string) store.ArtifactCreate {
	return store.ArtifactCreate{
		WorkspaceKey: workspace,
		ArtifactID:   artifactID,
		OwnerType:    "task_run",
		OwnerID:      "task-run-1",
		Type:         "log",
		MIMEType:     "text/plain; charset=utf-8",
	}
}

func readArtifactContent(t testing.TB, ctx context.Context, artifacts store.ArtifactStore, workspace, artifactID string) []byte {
	t.Helper()
	reader, ok := artifacts.(store.ArtifactContentReader)
	if !ok {
		t.Fatalf("artifact store %T does not implement ArtifactContentReader", artifacts)
	}
	content, err := reader.ReadContent(ctx, workspace, artifactID)
	if err != nil {
		t.Fatalf("ReadContent: %v", err)
	}
	return content
}
