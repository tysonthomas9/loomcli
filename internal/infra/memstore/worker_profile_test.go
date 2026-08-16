package memstore

import (
	"errors"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/modules/execution"

	"github.com/tysonthomas9/loomcli/internal/platform/persistence"
)

func TestWorkerProfileMemstoreLifecycle(t *testing.T) {
	ctx := t.Context()
	s := New()

	empty, err := s.WorkerProfiles().List(ctx, "WS", execution.WorkerProfileFilter{})
	if err != nil {
		t.Fatalf("List empty worker profiles: %v", err)
	}
	if empty == nil || len(empty) != 0 {
		t.Fatalf("empty list = %+v, want non-nil empty slice", empty)
	}

	maxPriority := 2
	repos := []string{"api"}
	labels := []string{"gpu"}
	capabilities := []string{"tests"}
	metadata := map[string]string{"tier": "gold"}
	created, err := s.WorkerProfiles().Create(ctx, execution.WorkerProfileCreate{
		WorkspaceKey: "WS",
		ProfileID:    " falcon ",
		Role:         "task",
		Backend:      "codex",
		Repos:        repos,
		MaxPriority:  &maxPriority,
		ParentEpic:   "EPIC-1",
		Labels:       labels,
		Capabilities: capabilities,
		Metadata:     metadata,
	})
	if err != nil {
		t.Fatalf("Create worker profile: %v", err)
	}
	if created.ProfileID != "falcon" || created.Name != "falcon" || !created.Enabled || created.MaxPriority == nil || *created.MaxPriority != 2 {
		t.Fatalf("created = %+v, want trimmed default-enabled profile", created)
	}
	repos[0] = "mutated"
	labels[0] = "mutated"
	capabilities[0] = "mutated"
	metadata["tier"] = "mutated"
	maxPriority = 4
	created.Repos[0] = "return-mutated"
	created.Labels[0] = "return-mutated"
	created.Capabilities[0] = "return-mutated"
	created.Metadata["tier"] = "return-mutated"
	*created.MaxPriority = 4

	got, err := s.WorkerProfiles().Get(ctx, "WS", "falcon")
	if err != nil {
		t.Fatalf("Get worker profile: %v", err)
	}
	if got.Repos[0] != "api" || got.Labels[0] != "gpu" || got.Capabilities[0] != "tests" || got.Metadata["tier"] != "gold" || *got.MaxPriority != 2 {
		t.Fatalf("stored clone = %+v, want original values", got)
	}

	if _, err := s.WorkerProfiles().Create(ctx, execution.WorkerProfileCreate{WorkspaceKey: "WS", ProfileID: "falcon", Role: "task"}); !errors.Is(err, persistence.ErrAlreadyExists) {
		t.Fatalf("duplicate Create err = %v, want ErrAlreadyExists", err)
	}
	invalidPriority := 5
	if _, err := s.WorkerProfiles().Create(ctx, execution.WorkerProfileCreate{WorkspaceKey: "WS", ProfileID: "bad-priority", Role: "task", MaxPriority: &invalidPriority}); !errors.Is(err, persistence.ErrInvalid) {
		t.Fatalf("invalid priority Create err = %v, want ErrInvalid", err)
	}

	disabled := false
	if _, err := s.WorkerProfiles().Create(ctx, execution.WorkerProfileCreate{WorkspaceKey: "WS", ProfileID: "raven", Name: "Raven", Role: "service", Backend: "claude", Enabled: &disabled}); err != nil {
		t.Fatalf("Create disabled worker profile: %v", err)
	}
	enabledProfiles, err := s.WorkerProfiles().List(ctx, "WS", execution.WorkerProfileFilter{Role: "task", Enabled: boolPtr(true)})
	if err != nil {
		t.Fatalf("List enabled task profiles: %v", err)
	}
	if len(enabledProfiles) != 1 || enabledProfiles[0].ProfileID != "falcon" {
		t.Fatalf("enabled task profiles = %+v, want falcon", enabledProfiles)
	}
	claudeProfiles, err := s.WorkerProfiles().List(ctx, "WS", execution.WorkerProfileFilter{Backend: "claude", Limit: 1})
	if err != nil {
		t.Fatalf("List claude profiles: %v", err)
	}
	if len(claudeProfiles) != 1 || claudeProfiles[0].ProfileID != "raven" {
		t.Fatalf("claude profiles = %+v, want raven", claudeProfiles)
	}

	name := "Falcon v2"
	role := "service"
	backend := "claude"
	updateRepos := []string{}
	parentEpic := "EPIC-2"
	updateLabels := []string{"edge"}
	updateCapabilities := []string{"reviews"}
	updateMetadata := map[string]string{"tier": "platinum"}
	maxPriority = 1
	disabled = false
	updated, err := s.WorkerProfiles().Update(ctx, "WS", "falcon", execution.WorkerProfileUpdate{
		Name:             &name,
		Role:             &role,
		Backend:          &backend,
		Repos:            &updateRepos,
		MaxPriority:      &maxPriority,
		ClearMaxPriority: true,
		ParentEpic:       &parentEpic,
		Labels:           &updateLabels,
		Capabilities:     &updateCapabilities,
		Enabled:          &disabled,
		Metadata:         &updateMetadata,
	})
	if err != nil {
		t.Fatalf("Update worker profile: %v", err)
	}
	if updated.Name != name || updated.Role != role || updated.Backend != backend || updated.Enabled || updated.ParentEpic != parentEpic || updated.MaxPriority != nil || len(updated.Repos) != 0 {
		t.Fatalf("updated = %+v, want patched profile with nil max priority", updated)
	}
	updateLabels[0] = "mutated"
	updateCapabilities[0] = "mutated"
	updateMetadata["tier"] = "mutated"
	updated.Labels[0] = "return-mutated"
	updated.Capabilities[0] = "return-mutated"
	updated.Metadata["tier"] = "return-mutated"

	got, err = s.WorkerProfiles().Get(ctx, "WS", "falcon")
	if err != nil {
		t.Fatalf("Get updated worker profile: %v", err)
	}
	if got.Labels[0] != "edge" || got.Capabilities[0] != "reviews" || got.Metadata["tier"] != "platinum" {
		t.Fatalf("updated clone = %+v, want original patch values", got)
	}

	blankRole := " "
	if _, err := s.WorkerProfiles().Update(ctx, "WS", "falcon", execution.WorkerProfileUpdate{Role: &blankRole}); !errors.Is(err, persistence.ErrInvalid) {
		t.Fatalf("invalid role Update err = %v, want ErrInvalid", err)
	}
	got, err = s.WorkerProfiles().Get(ctx, "WS", "falcon")
	if err != nil {
		t.Fatalf("Get after failed update: %v", err)
	}
	if got.Role != "service" {
		t.Fatalf("failed update mutated role = %q, want service", got.Role)
	}
	if err := s.WorkerProfiles().Delete(ctx, "WS", "falcon"); err != nil {
		t.Fatalf("Delete worker profile: %v", err)
	}
	if _, err := s.WorkerProfiles().Get(ctx, "WS", "falcon"); !errors.Is(err, persistence.ErrNotFound) {
		t.Fatalf("deleted Get err = %v, want ErrNotFound", err)
	}
	if err := s.WorkerProfiles().Delete(ctx, "WS", "falcon"); !errors.Is(err, persistence.ErrNotFound) {
		t.Fatalf("duplicate Delete err = %v, want ErrNotFound", err)
	}
	if _, err := s.WorkerProfiles().Get(ctx, "WS", "missing"); !errors.Is(err, persistence.ErrNotFound) {
		t.Fatalf("missing Get err = %v, want ErrNotFound", err)
	}
}

func boolPtr(v bool) *bool {
	return &v
}
