package teamtemplate_test

import (
	"reflect"
	"sort"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/backend"
	"github.com/tysonthomas9/loomcli/internal/cli"
	"github.com/tysonthomas9/loomcli/internal/teamtemplate"
)

func TestBundleWorkerRoutingContract(t *testing.T) {
	for _, bundle := range teamtemplate.All() {
		t.Run(bundle.ID, func(t *testing.T) {
			architects := roleNamesMatching(bundle.Roles, func(role teamtemplate.TemplateRole) bool {
				return role.Kind == "worker" && contains(role.Labels, "architect")
			})
			implementers := roleNamesMatching(bundle.Roles, func(role teamtemplate.TemplateRole) bool {
				return role.Kind == "worker" && role.TaskFilter == "has_design"
			})
			qa := roleNamesMatching(bundle.Roles, func(role teamtemplate.TemplateRole) bool {
				return role.Kind == "worker" && contains(role.Labels, "qa")
			})

			assertMatchedRoles(t, bundle.Roles, backend.IssueData{
				ID: "architecture", Status: "open", IssueType: "task", Labels: []string{"architect"},
			}, architects)
			assertMatchedRoles(t, bundle.Roles, backend.IssueData{
				ID: "implementation", Status: "open", IssueType: "task", Design: "approved design",
			}, implementers)
			assertMatchedRoles(t, bundle.Roles, backend.IssueData{
				ID: "qa-without-design", Status: "open", IssueType: "task", Labels: []string{"qa"},
			}, qa)
			assertMatchedRoles(t, bundle.Roles, backend.IssueData{
				ID: "qa-with-design", Status: "open", IssueType: "task", Design: "approved design", Labels: []string{"qa"},
			}, qa)
			assertMatchedRoles(t, bundle.Roles, backend.IssueData{
				ID: "design-revision", Status: "open", IssueType: "task", Design: "rejected design", Labels: []string{"needs-revision", "architect"},
			}, architects)

			// This state is a deadlock: callers must add architect alongside
			// needs-revision so a design role can reclaim the task.
			assertMatchedRoles(t, bundle.Roles, backend.IssueData{
				ID: "deadlock", Status: "open", IssueType: "task", Design: "rejected design", Labels: []string{"needs-revision"},
			}, nil)
		})
	}
}

func assertMatchedRoles(t *testing.T, roles []teamtemplate.TemplateRole, issue backend.IssueData, want []string) {
	t.Helper()
	got := roleNamesMatching(roles, func(role teamtemplate.TemplateRole) bool {
		if role.Kind != "worker" {
			return false
		}
		match := cli.MatchTask(issue, cli.RoleConstraints{
			TaskFilter:    role.TaskFilter,
			Labels:        role.Labels,
			ExcludeLabels: role.ExcludeLabels,
			Skills:        role.Skills,
		})
		return match.Score > 0
	})
	if !reflect.DeepEqual(got, want) {
		t.Errorf("issue %q matched roles %v, want %v", issue.ID, got, want)
	}
}

func roleNamesMatching(roles []teamtemplate.TemplateRole, match func(teamtemplate.TemplateRole) bool) []string {
	var names []string
	for _, role := range roles {
		if match(role) {
			names = append(names, role.Name)
		}
	}
	sort.Strings(names)
	return names
}

func contains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
