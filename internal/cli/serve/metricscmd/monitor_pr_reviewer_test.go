package metricscmd

import (
	"testing"

	"github.com/tysonthomas9/loomcli/internal/domain"
)

func TestPRReviewerDisplayFields(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name            string
		agent           *domain.Agent
		wantDisplayName string
		wantRoleLabel   string
	}{
		{
			name: "nil agent",
		},
		{
			name:  "non reviewer role",
			agent: &domain.Agent{Name: "codex-coder", RoleName: "task"},
		},
		{
			name: "repos preferred over name parse",
			agent: &domain.Agent{
				Name:     "review-tysonthomas9-loomcli-3a8e1ebe-pr-222",
				RoleName: prReviewerRoleName,
				Repos:    []string{"loomcli"},
			},
			wantDisplayName: "loomcli#222",
			wantRoleLabel:   prReviewerRoleLabel,
		},
		{
			name: "hashed name fallback without repos",
			agent: &domain.Agent{
				Name:     "review-tysonthomas9-loomcli-3a8e1ebe-pr-222",
				RoleName: prReviewerRoleName,
			},
			wantDisplayName: "loomcli#222",
			wantRoleLabel:   prReviewerRoleLabel,
		},
		{
			name: "legacy name without hash",
			agent: &domain.Agent{
				Name:     "review-hello-pr-7",
				RoleName: prReviewerRoleName,
			},
			wantDisplayName: "hello#7",
			wantRoleLabel:   prReviewerRoleLabel,
		},
		{
			name: "role label without parseable number",
			agent: &domain.Agent{
				Name:     "review-broken",
				RoleName: prReviewerRoleName,
			},
			wantRoleLabel: prReviewerRoleLabel,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			gotDisplay, gotRole := prReviewerDisplayFields(tt.agent)
			if gotDisplay != tt.wantDisplayName {
				t.Fatalf("display_name = %q, want %q", gotDisplay, tt.wantDisplayName)
			}
			if gotRole != tt.wantRoleLabel {
				t.Fatalf("role_label = %q, want %q", gotRole, tt.wantRoleLabel)
			}
		})
	}
}
