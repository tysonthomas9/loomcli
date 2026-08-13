package artifacts

import (
	"context"
	"errors"
	"reflect"
	"testing"
)

type queryStoreFake struct {
	artifact *Artifact
	values   []*Artifact
	content  []byte
	gets     int
	lists    int
	reads    int
}

func (fake *queryStoreFake) GetArtifactRecord(context.Context, string, string) (*Artifact, error) {
	fake.gets++
	return fake.artifact, nil
}

func (fake *queryStoreFake) ListArtifactRecords(context.Context, string, SearchFilter) ([]*Artifact, error) {
	fake.lists++
	return fake.values, nil
}

func (fake *queryStoreFake) ReadArtifactContent(context.Context, string, string) ([]byte, error) {
	fake.reads++
	return fake.content, nil
}

func validQueryArtifact() *Artifact {
	content := []byte("content")
	return &Artifact{
		WorkspaceKey: "WS", ArtifactID: "artifact-1", TaskID: "TASK-1",
		OwnerType: OwnerTaskRun, OwnerID: "run-1", Type: "transcript",
		DurableStatus: StatusFinalized, SizeBytes: int64(len(content)), ContentHash: artifactContentHash(content),
		Metadata: map[string]string{"format": "canonical"},
	}
}

func TestQueryServiceRejectsCorruptDurableContent(t *testing.T) {
	artifact := validQueryArtifact()
	store := &queryStoreFake{artifact: artifact, content: []byte("tampered")}
	service, err := NewQuery(store)
	if err != nil {
		t.Fatal(err)
	}
	_, err = service.ReadArtifactContent(t.Context(), Query{WorkspaceKey: "WS", ArtifactID: artifact.ArtifactID})
	if !errors.Is(err, ErrEvidenceCorrupt) {
		t.Fatalf("ReadArtifactContent error = %v, want ErrEvidenceCorrupt", err)
	}

	artifact.DurableStatus = StatusUploading
	_, err = service.ReadArtifactContent(t.Context(), Query{WorkspaceKey: "WS", ArtifactID: artifact.ArtifactID})
	if !errors.Is(err, ErrContentUnavailable) || store.reads != 1 {
		t.Fatalf("non-finalized read error = %v reads=%d", err, store.reads)
	}
}

func TestQueryServiceGetsListsAndReadsDefensiveCopies(t *testing.T) {
	artifact := validQueryArtifact()
	store := &queryStoreFake{artifact: artifact, values: []*Artifact{artifact}, content: []byte("content")}
	service, err := NewQuery(store)
	if err != nil {
		t.Fatal(err)
	}

	got, err := service.GetArtifact(t.Context(), Query{WorkspaceKey: "WS", ArtifactID: "artifact-1"})
	if err != nil {
		t.Fatal(err)
	}
	got.Metadata["format"] = "mutated"
	if artifact.Metadata["format"] != "canonical" {
		t.Fatal("GetArtifact leaked persisted metadata map")
	}

	values, err := service.ListArtifacts(t.Context(), SearchQuery{
		WorkspaceKey: "WS",
		Filter:       SearchFilter{TaskID: "TASK-1", OwnerType: OwnerTaskRun, Type: "transcript", DurableStatus: StatusFinalized},
	})
	if err != nil || len(values) != 1 {
		t.Fatalf("ListArtifacts = %+v, %v", values, err)
	}
	content, err := service.ReadArtifactContent(t.Context(), Query{WorkspaceKey: "WS", ArtifactID: "artifact-1"})
	if err != nil {
		t.Fatal(err)
	}
	content[0] = 'X'
	if !reflect.DeepEqual(store.content, []byte("content")) {
		t.Fatal("ReadArtifactContent leaked store byte slice")
	}
}

func TestQueryServiceRejectsEscapedRowsAndDuplicates(t *testing.T) {
	store := &queryStoreFake{values: []*Artifact{validQueryArtifact()}}
	service, err := NewQuery(store)
	if err != nil {
		t.Fatal(err)
	}
	store.values[0].TaskID = "TASK-OTHER"
	_, err = service.ListArtifacts(t.Context(), SearchQuery{
		WorkspaceKey: "WS", Filter: SearchFilter{TaskID: "TASK-1"},
	})
	if !errors.Is(err, ErrInvalidPersistedState) {
		t.Fatalf("escaped row error = %v", err)
	}

	first := validQueryArtifact()
	second := validQueryArtifact()
	store.values = []*Artifact{first, second}
	_, err = service.ListArtifacts(t.Context(), SearchQuery{WorkspaceKey: "WS"})
	if !errors.Is(err, ErrInvalidPersistedState) {
		t.Fatalf("duplicate row error = %v", err)
	}
}

func TestQueryServiceValidatesBeforeStoreAndFailsClosed(t *testing.T) {
	store := &queryStoreFake{}
	service, err := NewQuery(store)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.GetArtifact(t.Context(), Query{WorkspaceKey: " padded ", ArtifactID: "artifact-1"}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("noncanonical GetArtifact = %v", err)
	}
	if _, err := service.ListArtifacts(t.Context(), SearchQuery{
		WorkspaceKey: "WS", Filter: SearchFilter{OwnerType: "unknown"},
	}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("invalid owner filter = %v", err)
	}
	if store.gets != 0 || store.lists != 0 {
		t.Fatalf("invalid query reached store: gets=%d lists=%d", store.gets, store.lists)
	}
	if _, err := NewQuery(nil); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("NewQuery(nil) = %v", err)
	}
	var missing *QueryService
	if _, err := missing.GetArtifact(t.Context(), Query{WorkspaceKey: "WS", ArtifactID: "artifact-1"}); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("nil service GetArtifact = %v", err)
	}
}
