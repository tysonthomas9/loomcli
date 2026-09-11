package taskdelivery

import (
	"errors"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/domain"
)

func TestResolve(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		input   ResolveInput
		want    Resolution
		wantErr error
	}{
		{name: "migration default", want: Resolution{Requirement: domain.TaskDeliveryWorkingCopy, Source: SourceMigrationDefault}},
		{name: "workspace pull request", input: ResolveInput{WorkspaceRequirement: domain.TaskDeliveryPullRequest}, want: Resolution{Requirement: domain.TaskDeliveryPullRequest, Source: SourceWorkspace}},
		{name: "repository strengthens", input: ResolveInput{RepositoryRequirement: domain.TaskDeliveryPullRequest}, want: Resolution{Requirement: domain.TaskDeliveryPullRequest, Source: SourceRepository}},
		{name: "repository cannot weaken", input: ResolveInput{WorkspaceRequirement: domain.TaskDeliveryPullRequest, RepositoryRequirement: domain.TaskDeliveryWorkingCopy}, want: Resolution{Requirement: domain.TaskDeliveryPullRequest, Source: SourceWorkspace}},
		{name: "run strengthens", input: ResolveInput{RunOverride: domain.TaskDeliveryPullRequest}, want: Resolution{Requirement: domain.TaskDeliveryPullRequest, Source: SourceRunOverride}},
		{name: "run cannot weaken", input: ResolveInput{WorkspaceRequirement: domain.TaskDeliveryPullRequest, RunOverride: domain.TaskDeliveryWorkingCopy}, wantErr: ErrRunOverrideWeakens},
		{name: "invalid", input: ResolveInput{WorkspaceRequirement: "stacked_pull_request"}, wantErr: ErrInvalidRequirement},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := Resolve(tt.input)
			if tt.wantErr != nil {
				if !errors.Is(err, tt.wantErr) {
					t.Fatalf("Resolve() error = %v, want %v", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("Resolve() error: %v", err)
			}
			if got != tt.want {
				t.Fatalf("Resolve() = %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestFreeze(t *testing.T) {
	t.Parallel()

	input := FreezeInput{
		RunID:        "task-run-1",
		WorkspaceKey: "FLEET",
		Repository:   "fleet-db",
		Resolution: Resolution{
			Requirement: domain.TaskDeliveryPullRequest,
			Source:      SourceRepository,
		},
	}
	one, err := Freeze(input)
	if err != nil {
		t.Fatalf("Freeze() error: %v", err)
	}
	two, err := Freeze(input)
	if err != nil {
		t.Fatalf("Freeze() second error: %v", err)
	}
	if one != two || one.PlanID == "" || one.SchemaVersion != PlanSchemaVersion {
		t.Fatalf("Freeze() plans not stable: one=%+v two=%+v", one, two)
	}

	input.Repository = ""
	if _, err := Freeze(input); !errors.Is(err, ErrMissingDeliveryRepo) {
		t.Fatalf("Freeze() without PR repo error = %v, want %v", err, ErrMissingDeliveryRepo)
	}
}
