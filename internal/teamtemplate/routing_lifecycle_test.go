package teamtemplate_test

import (
	"testing"

	"github.com/tysonthomas9/loomcli/internal/backend"
	cli "github.com/tysonthomas9/loomcli/internal/cli"
	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/teamtemplate"
)

func templateRole(t *testing.T, templateID, name string) teamtemplate.TemplateRole {
	t.Helper()
	tpl, ok := teamtemplate.ByID(templateID)
	if !ok {
		t.Fatalf("%s template missing", templateID)
	}
	for _, role := range tpl.Roles {
		if role.Name == name {
			return role
		}
	}
	t.Fatalf("role %q missing", name)
	return teamtemplate.TemplateRole{}
}

func matchTemplateRole(issue backend.IssueData, role teamtemplate.TemplateRole) cli.TaskMatch {
	return cli.MatchTask(issue, cli.RoleConstraints{
		TaskFilter:    role.TaskFilter,
		Skills:        role.Skills,
		Labels:        role.Labels,
		ExcludeLabels: role.ExcludeLabels,
	})
}

func TestApprovedArchitectTaskRoutesToImplementation(t *testing.T) {
	issue := backend.IssueData{ID: "T-1", IssueType: "task", Status: "open", HasDesign: true, Labels: []string{"backend"}}
	if got := matchTemplateRole(issue, templateRole(t, "fullstack-app", "app-architect")); got.Score > 0 {
		t.Fatalf("architect still matches approved task: %+v", got)
	}
	frontend := matchTemplateRole(issue, templateRole(t, "fullstack-app", "frontend-dev"))
	backendDev := matchTemplateRole(issue, templateRole(t, "fullstack-app", "backend-dev"))
	if frontend.Score == 0 && backendDev.Score == 0 {
		t.Fatalf("approved task matches no implementer: frontend=%+v backend=%+v", frontend, backendDev)
	}
}

func TestQARoutesOnlyAfterImplementationHandoff(t *testing.T) {
	fresh := backend.IssueData{ID: "T-2", IssueType: "task", Status: "open", HasDesign: true}
	qaRole := templateRole(t, "fullstack-app", "qa-engineer")
	if got := matchTemplateRole(fresh, qaRole); got.Score > 0 {
		t.Fatalf("QA matched fresh unimplemented task: %+v", got)
	}
	ready := fresh
	ready.Labels = []string{"ready-for-qa"}
	if got := matchTemplateRole(ready, qaRole); got.Score == 0 {
		t.Fatalf("QA did not match implementation handoff: %+v", got)
	}
	for _, name := range []string{"frontend-dev", "backend-dev"} {
		if got := matchTemplateRole(ready, templateRole(t, "fullstack-app", name)); got.Score > 0 {
			t.Fatalf("%s can reclaim QA handoff: %+v", name, got)
		}
	}
}

func TestFinalVerificationAgentsCloseOnlyAfterSupervisorPublishes(t *testing.T) {
	tests := []struct {
		templateID string
		agentName  string
	}{
		{templateID: "fullstack-app", agentName: "qa-engineer-1"},
		{templateID: "backend", agentName: "qa-engineer-1"},
		{templateID: "website", agentName: "site-qa-1"},
		{templateID: "ai-agent", agentName: "eval-engineer-1"},
	}

	for _, tt := range tests {
		t.Run(tt.templateID, func(t *testing.T) {
			tpl, ok := teamtemplate.ByID(tt.templateID)
			if !ok {
				t.Fatalf("template %q missing", tt.templateID)
			}
			for _, agent := range tpl.Agents {
				if agent.Name != tt.agentName {
					continue
				}
				if agent.Hooks == nil || len(agent.Hooks.OnComplete) != 1 || agent.Hooks.OnComplete[0].Type != domain.AgentHookActionClose {
					t.Fatalf("%s hooks=%+v, want one supervisor close action", tt.agentName, agent.Hooks)
				}
				return
			}
			t.Fatalf("agent %q missing", tt.agentName)
		})
	}
}

func TestQAFiledDefectRoutesToArchitect(t *testing.T) {
	defect := backend.IssueData{ID: "BUG-1", IssueType: "task", Status: "open", Labels: []string{"architect"}}
	if got := matchTemplateRole(defect, templateRole(t, "fullstack-app", "app-architect")); got.Score == 0 {
		t.Fatalf("architect did not match QA defect: %+v", got)
	}
}

func TestBackendTemplateDomainLabelsAreHardGates(t *testing.T) {
	tests := []struct {
		name        string
		labels      []string
		wantBackend bool
		wantData    bool
	}{
		{name: "unclassified", wantBackend: false, wantData: false},
		{name: "backend", labels: []string{"backend"}, wantBackend: true, wantData: false},
		{name: "data", labels: []string{"data"}, wantBackend: false, wantData: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			issue := backend.IssueData{ID: "T-domain", IssueType: "task", Status: "open", HasDesign: true, Labels: tt.labels}
			backendMatch := matchTemplateRole(issue, templateRole(t, "backend", "backend-dev")).Score > 0
			dataMatch := matchTemplateRole(issue, templateRole(t, "backend", "data-engineer")).Score > 0
			if backendMatch != tt.wantBackend || dataMatch != tt.wantData {
				t.Fatalf("labels=%v: backend=%v data=%v, want backend=%v data=%v", tt.labels, backendMatch, dataMatch, tt.wantBackend, tt.wantData)
			}
		})
	}
}

func TestSharedImplementationRolesUseCanonicalHardLabels(t *testing.T) {
	tests := []struct {
		templateID string
		role       string
		label      string
	}{
		{templateID: "fullstack-app", role: "frontend-dev", label: "frontend"},
		{templateID: "fullstack-app", role: "backend-dev", label: "backend"},
		{templateID: "website", role: "frontend-dev", label: "frontend"},
		{templateID: "website", role: "content-writer", label: "content"},
	}

	for _, tt := range tests {
		t.Run(tt.templateID+"/"+tt.role, func(t *testing.T) {
			role := templateRole(t, tt.templateID, tt.role)
			if len(role.Labels) != 1 || role.Labels[0] != tt.label {
				t.Fatalf("labels=%v, want [%s]", role.Labels, tt.label)
			}
		})
	}
}
