package taskrunlogs_test

import (
	"errors"
	"strconv"
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/infra/memstore"
	"github.com/tysonthomas9/loomcli/internal/store"
	"github.com/tysonthomas9/loomcli/internal/taskrunlogs"
)

func TestPutTaskGetRoundTrip(t *testing.T) {
	st := memstore.New()
	ref, err := taskrunlogs.PutTask(t.Context(), st, "WS", "task-run-1", "task output\n")
	if err != nil {
		t.Fatalf("PutTask: %v", err)
	}
	if !strings.HasPrefix(ref, "artifact://log-task-task-run-1-") {
		t.Fatalf("ref = %q", ref)
	}
	got, err := taskrunlogs.Get(t.Context(), st, "WS", ref)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Content != "task output\n" || got.ModifiedAt.IsZero() || got.Truncated {
		t.Fatalf("log = %+v", got)
	}
}

func TestPutRunEmptyContentCreatesNoArtifact(t *testing.T) {
	st := memstore.New()
	for _, content := range []string{"", " \n\t"} {
		ref, err := taskrunlogs.PutRun(t.Context(), st, "WS", "run-1", content)
		if err != nil || ref != "" {
			t.Fatalf("PutRun(%q) = %q, %v", content, ref, err)
		}
	}
	artifacts, err := st.Artifacts().List(t.Context(), "WS", store.ArtifactFilter{})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(artifacts) != 0 {
		t.Fatalf("artifacts = %d, want 0", len(artifacts))
	}
}

func TestPutTaskTailCapsAndRecordsTruncation(t *testing.T) {
	st := memstore.New()
	tail := strings.Repeat("z", 1<<20)
	content := "discarded-prefix" + tail
	ref, err := taskrunlogs.PutTask(t.Context(), st, "WS", "task-run-large", content)
	if err != nil {
		t.Fatalf("PutTask: %v", err)
	}
	got, err := taskrunlogs.Get(t.Context(), st, "WS", ref)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Content != tail || !got.Truncated {
		t.Fatalf("content bytes = %d truncated = %v", len(got.Content), got.Truncated)
	}
	artifactID := strings.TrimPrefix(ref, "artifact://")
	artifact, err := st.Artifacts().Get(t.Context(), "WS", artifactID)
	if err != nil {
		t.Fatalf("Get artifact: %v", err)
	}
	if artifact.Metadata["log.original_bytes"] != strconv.Itoa(len(content)) || artifact.Metadata["log.truncated"] != "true" {
		t.Fatalf("metadata = %#v", artifact.Metadata)
	}
}

func TestGetRejectsUnknownAndMissingRefs(t *testing.T) {
	st := memstore.New()
	for _, ref := range []string{"", "file://log", "artifact://missing"} {
		if _, err := taskrunlogs.Get(t.Context(), st, "WS", ref); !errors.Is(err, domain.ErrNotFound) {
			t.Fatalf("Get(%q) err = %v, want ErrNotFound", ref, err)
		}
	}
}

func TestPutTaskUsesUniqueAttemptScopedRefs(t *testing.T) {
	st := memstore.New()
	first, err := taskrunlogs.PutTask(t.Context(), st, "WS", "task-run-retry", "first")
	if err != nil {
		t.Fatalf("first PutTask: %v", err)
	}
	second, err := taskrunlogs.PutTask(t.Context(), st, "WS", "task-run-retry", "second")
	if err != nil {
		t.Fatalf("second PutTask: %v", err)
	}
	if first == second {
		t.Fatalf("refs are equal: %q", first)
	}
}
