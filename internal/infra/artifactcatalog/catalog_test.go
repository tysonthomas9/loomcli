package artifactcatalog

import (
	"bytes"
	"context"
	"errors"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/infra/memstore"
	"github.com/tysonthomas9/loomcli/internal/modules/artifacts"
	"github.com/tysonthomas9/loomcli/internal/store"
)

type metadataOnlyStore struct{ store.ArtifactStore }

func TestCatalogProjectsFiltersAndReadsManagedContent(t *testing.T) {
	composite := memstore.New()
	legacy := composite.Artifacts()
	created, err := legacy.Create(t.Context(), store.ArtifactCreate{
		WorkspaceKey: "WS", ArtifactID: "artifact-1", TaskID: "TASK-1",
		OwnerType: "task_run", OwnerID: "run-1", Type: "transcript", Metadata: map[string]string{"format": "canonical"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := legacy.UploadContent(t.Context(), "WS", created.ArtifactID, store.ArtifactContentUpload{
		Body: bytes.NewBufferString("content"), MIMEType: "application/json",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := legacy.Finalize(t.Context(), "WS", created.ArtifactID, store.ArtifactFinalize{}); err != nil {
		t.Fatal(err)
	}

	catalog, err := FromProvider(composite)
	if err != nil {
		t.Fatal(err)
	}
	artifact, err := catalog.GetArtifactRecord(t.Context(), "WS", "artifact-1")
	if err != nil || artifact.OwnerType != artifacts.OwnerTaskRun || artifact.DurableStatus != artifacts.StatusFinalized {
		t.Fatalf("GetArtifactRecord = %+v, %v", artifact, err)
	}
	values, err := catalog.ListArtifactRecords(t.Context(), "WS", artifacts.SearchFilter{
		TaskID: "TASK-1", OwnerType: artifacts.OwnerTaskRun, DurableStatus: artifacts.StatusFinalized,
	})
	if err != nil || len(values) != 1 {
		t.Fatalf("ListArtifactRecords = %+v, %v", values, err)
	}
	content, err := catalog.ReadArtifactContent(t.Context(), "WS", "artifact-1")
	if err != nil || string(content) != "content" {
		t.Fatalf("ReadArtifactContent = %q, %v", content, err)
	}
}

func TestCatalogMapsMissingAndUnavailableContent(t *testing.T) {
	legacy := memstore.New().Artifacts()
	catalog, err := New(legacy)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := catalog.GetArtifactRecord(t.Context(), "WS", "missing"); !errors.Is(err, artifacts.ErrNotFound) {
		t.Fatalf("missing artifact = %v", err)
	}

	metadataOnly, err := New(&metadataOnlyStore{ArtifactStore: legacy})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := metadataOnly.ReadArtifactContent(context.Background(), "WS", "missing"); !errors.Is(err, artifacts.ErrContentUnavailable) {
		t.Fatalf("metadata-only content = %v", err)
	}
	if _, err := FromProvider(nil); !errors.Is(err, artifacts.ErrUnavailable) {
		t.Fatalf("FromProvider(nil) = %v", err)
	}
}
