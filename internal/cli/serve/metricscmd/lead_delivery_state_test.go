package metricscmd

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/ops"
)

func TestMonitorBranchIgnoresUnrecordedAgentCheckout(t *testing.T) {
	t.Setenv("LOOM_CONFIG_DIR", t.TempDir())
	root := t.TempDir()
	oldPath := filepath.Join(root, "worktrees", "repo", "agent")
	if err := os.MkdirAll(filepath.Join(oldPath, ".git"), 0755); err != nil {
		t.Fatal(err)
	}
	workspace := &ops.WorkspaceData{ID: "ws", Path: root, Repos: []ops.WorkspaceRepo{{Name: "repo"}}}
	if branch := monitorBranchFromAgent(workspace, &domain.Agent{Name: "agent", Repos: []string{"repo"}}); branch != "unknown" {
		t.Fatalf("branch = %q, want unknown without a recorded checkout", branch)
	}
}

func TestMonitorLeadDeliveryState(t *testing.T) {
	updatedAt := time.Date(2026, 5, 17, 8, 0, 0, 123, time.UTC)
	version := updatedAt.Format(time.RFC3339Nano)
	lead := &domain.Agent{
		Name:      "nova",
		RoleName:  "lead",
		Parent:    "EPIC-1",
		UpdatedAt: updatedAt,
	}

	for _, tt := range []struct {
		name    string
		agent   *domain.Agent
		session *domain.AgentSession
		want    string
	}{
		{
			name:  "unassigned lead",
			agent: &domain.Agent{Name: "nova", RoleName: "lead"},
			want:  "",
		},
		{
			name:  "assigned non-lead",
			agent: &domain.Agent{Name: "worker", RoleName: "task", Parent: "EPIC-1"},
			want:  "",
		},
		{
			name:  "assigned lead without delivery metadata",
			agent: lead,
			want:  "pending",
		},
		{
			name:  "delivered assignment version",
			agent: lead,
			session: &domain.AgentSession{Metadata: map[string]string{
				"lead_assignment_delivered_version": version,
			}},
			want: "delivered",
		},
		{
			name:  "acknowledged assignment version wins over delivered",
			agent: lead,
			session: &domain.AgentSession{Metadata: map[string]string{
				"lead_assignment_delivered_version":    version,
				"lead_assignment_acknowledged_version": version,
			}},
			want: "acknowledged",
		},
		{
			name:  "stale metadata stays pending",
			agent: lead,
			session: &domain.AgentSession{Metadata: map[string]string{
				"lead_assignment_delivered_version": "old-version",
			}},
			want: "pending",
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := monitorLeadDeliveryState(tt.agent, tt.session); got != tt.want {
				t.Fatalf("monitorLeadDeliveryState() = %q, want %q", got, tt.want)
			}
		})
	}
}
