package teamtemplate_test

import (
	"testing"

	"github.com/tysonthomas9/loomcli/internal/backend"
	cli "github.com/tysonthomas9/loomcli/internal/cli"
	"github.com/tysonthomas9/loomcli/internal/teamtemplate"
)

func templateRole(t *testing.T, name string) teamtemplate.TemplateRole {
	t.Helper()
	tpl, ok := teamtemplate.ByID("fullstack-app")
	if !ok {
		t.Fatal("fullstack-app template missing")
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
	issue := backend.IssueData{ID: "T-1", IssueType: "task", Status: "open", HasDesign: true}
	if got := matchTemplateRole(issue, templateRole(t, "app-architect")); got.Score > 0 {
		t.Fatalf("architect still matches approved task: %+v", got)
	}
	frontend := matchTemplateRole(issue, templateRole(t, "frontend-dev"))
	backendDev := matchTemplateRole(issue, templateRole(t, "backend-dev"))
	if frontend.Score == 0 && backendDev.Score == 0 {
		t.Fatalf("approved task matches no implementer: frontend=%+v backend=%+v", frontend, backendDev)
	}
}

func TestQARoutesOnlyAfterImplementationHandoff(t *testing.T) {
	fresh := backend.IssueData{ID: "T-2", IssueType: "task", Status: "open", HasDesign: true}
	qaRole := templateRole(t, "qa-engineer")
	if got := matchTemplateRole(fresh, qaRole); got.Score > 0 {
		t.Fatalf("QA matched fresh unimplemented task: %+v", got)
	}
	ready := fresh
	ready.Labels = []string{"ready-for-qa"}
	if got := matchTemplateRole(ready, qaRole); got.Score == 0 {
		t.Fatalf("QA did not match implementation handoff: %+v", got)
	}
	for _, name := range []string{"frontend-dev", "backend-dev"} {
		if got := matchTemplateRole(ready, templateRole(t, name)); got.Score > 0 {
			t.Fatalf("%s can reclaim QA handoff: %+v", name, got)
		}
	}
}

func TestQAFiledDefectRoutesToArchitect(t *testing.T) {
	defect := backend.IssueData{ID: "BUG-1", IssueType: "task", Status: "open", Labels: []string{"architect"}}
	if got := matchTemplateRole(defect, templateRole(t, "app-architect")); got.Score == 0 {
		t.Fatalf("architect did not match QA defect: %+v", got)
	}
}
