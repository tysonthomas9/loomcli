package driver

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/infra/memstore"
	"github.com/tysonthomas9/loomcli/internal/store"
)

func TestCreateDriverRunUsesActivePassedVersion(t *testing.T) {
	ctx := context.Background()
	st := memstore.New()
	if _, err := st.Drivers().Create(ctx, store.DriverCreate{
		WorkspaceKey: "TEST",
		DriverID:     "epic-runner",
		Name:         "epic-runner",
		Status:       domain.DriverStatusDraft,
	}); err != nil {
		t.Fatalf("Create driver: %v", err)
	}
	if _, err := st.DriverVersions().Create(ctx, store.DriverVersionCreate{
		WorkspaceKey:     "TEST",
		VersionID:        "version-1",
		DriverID:         "epic-runner",
		Version:          1,
		SourceDigest:     "sha256:source1",
		BundleDigest:     "sha256:bundle1",
		ValidationStatus: domain.DriverVersionValidationPassed,
	}); err != nil {
		t.Fatalf("Create version 1: %v", err)
	}
	if _, err := st.DriverVersions().Create(ctx, store.DriverVersionCreate{
		WorkspaceKey:     "TEST",
		VersionID:        "version-2",
		DriverID:         "epic-runner",
		Version:          2,
		SourceDigest:     "sha256:source2",
		BundleDigest:     "sha256:bundle2",
		ValidationStatus: domain.DriverVersionValidationPassed,
	}); err != nil {
		t.Fatalf("Create version 2: %v", err)
	}
	activeVersion := "version-1"
	activeStatus := domain.DriverStatusActive
	if _, err := st.Drivers().Update(ctx, "TEST", "epic-runner", store.DriverUpdate{ActiveVersionID: &activeVersion, Status: &activeStatus}); err != nil {
		t.Fatalf("Activate driver: %v", err)
	}

	run, err := CreateDriverRun(ctx, st, RunOptions{
		WorkspaceKey:   "TEST",
		DriverID:       "epic-runner",
		EpicID:         "TEST-1",
		RunID:          "run-1",
		IdempotencyKey: "idem-1",
		Payload:        json.RawMessage(`{"epicId":"wrong","requestedBy":"cli"}`),
	})
	if err != nil {
		t.Fatalf("CreateDriverRun: %v", err)
	}
	if run.Status != domain.DriverRunQueued || run.DriverVersionID != "version-1" || string(run.Payload) != `{"epicId":"wrong","requestedBy":"cli"}` {
		t.Fatalf("run = %+v, want queued pinned version-1 with raw payload", run)
	}

	replay, err := CreateDriverRun(ctx, st, RunOptions{
		WorkspaceKey:   "TEST",
		DriverID:       "epic-runner",
		EpicID:         "TEST-1",
		RunID:          "run-replay",
		IdempotencyKey: "idem-1",
	})
	if err != nil {
		t.Fatalf("CreateDriverRun replay: %v", err)
	}
	if replay.RunID != "run-1" {
		t.Fatalf("replay run_id = %q, want run-1", replay.RunID)
	}

	active, err := CreateDriverRun(ctx, st, RunOptions{
		WorkspaceKey:   "TEST",
		DriverID:       "epic-runner",
		EpicID:         "TEST-1",
		RunID:          "run-active",
		IdempotencyKey: "idem-2",
	})
	if err != nil {
		t.Fatalf("CreateDriverRun active replay: %v", err)
	}
	if active.RunID != "run-1" {
		t.Fatalf("active run_id = %q, want run-1", active.RunID)
	}
}

func TestCreateDriverRunRejectsDriverWithoutActiveVersion(t *testing.T) {
	ctx := context.Background()
	st := memstore.New()
	if _, err := st.Drivers().Create(ctx, store.DriverCreate{
		WorkspaceKey: "TEST",
		DriverID:     "draft-driver",
		Name:         "draft-driver",
		Status:       domain.DriverStatusDraft,
	}); err != nil {
		t.Fatalf("Create driver: %v", err)
	}
	if _, err := CreateDriverRun(ctx, st, RunOptions{WorkspaceKey: "TEST", DriverID: "draft-driver", RunID: "run-1"}); !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("CreateDriverRun err = %v, want ErrInvalid", err)
	}
}
