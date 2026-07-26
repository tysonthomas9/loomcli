package cli

import (
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/backend"
	"github.com/tysonthomas9/loomcli/internal/cli/config"
)

func labelIssue(id string, labels ...string) backend.IssueData {
	return backend.IssueData{
		ID: id, Status: "open", IssueType: "task",
		Design: "plan", Labels: labels,
	}
}

func TestMatchTask_LabelGating(t *testing.T) {
	cases := []struct {
		name        string
		issueLabels []string
		include     []string
		exclude     []string
		wantMatch   bool
		wantReason  string
	}{
		{"no constraints matches", []string{"anything"}, nil, nil, true, ""},
		{"include satisfied", []string{"plan", "extra"}, []string{"plan"}, nil, true, ""},
		{"include missing rejects", []string{"extra"}, []string{"plan"}, nil, false, "missing required label plan"},
		// AND semantics: every listed label must be present.
		{"include is AND", []string{"plan"}, []string{"plan", "urgent"}, nil, false, "missing required label urgent"},
		{"include all present", []string{"plan", "urgent"}, []string{"plan", "urgent"}, nil, true, ""},
		{"exclude absent matches", []string{"plan"}, nil, []string{"criticized"}, true, ""},
		{"exclude present rejects", []string{"plan", "criticized"}, nil, []string{"criticized"}, false, "excluded by label criticized"},
		// OR semantics: any one forbidden label is enough.
		{"exclude is OR", []string{"plan", "reviewed"}, nil, []string{"criticized", "reviewed"}, false, "excluded by label reviewed"},
		// The pipeline-stage shape: claim X that is not yet Y.
		{"stage armed", []string{"plan"}, []string{"plan"}, []string{"criticized"}, true, ""},
		{"stage disarmed after stamping", []string{"plan", "criticized"}, []string{"plan"}, []string{"criticized"}, false, "excluded by label criticized"},
		// Exclusion is evaluated first, so it wins a conflict.
		{"exclusion wins a conflict", []string{"plan"}, []string{"plan"}, []string{"plan"}, false, "excluded by label plan"},
		// Exact, case-sensitive matching.
		{"case sensitive", []string{"Plan"}, []string{"plan"}, nil, false, "missing required label plan"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := MatchTask(labelIssue("T-1", tc.issueLabels...), RoleConstraints{
				TaskFilter:    "any",
				Labels:        tc.include,
				ExcludeLabels: tc.exclude,
			})
			if tc.wantMatch {
				if got.Score <= 0 {
					t.Fatalf("Score = %d (%s), want a match", got.Score, got.Reason)
				}
				return
			}
			// A hard reject, not the score-10 fallback Skills uses — that is the
			// whole point: a demoted candidate is still claimed when the queue is
			// otherwise empty, which is the steady state for a pipeline stage.
			if got.Score != 0 {
				t.Fatalf("Score = %d (%s), want 0 (hard reject)", got.Score, got.Reason)
			}
			if !strings.Contains(got.Reason, tc.wantReason) {
				t.Errorf("Reason = %q, want it to contain %q", got.Reason, tc.wantReason)
			}
		})
	}
}

// A stage must stop selecting an issue once it has stamped its output label.
func TestSelectBestTask_StageTerminates(t *testing.T) {
	constraints := RoleConstraints{
		TaskFilter:    "any",
		Labels:        []string{"plan"},
		ExcludeLabels: []string{"criticized"},
	}
	pending := []backend.IssueData{labelIssue("T-1", "plan")}
	if got := SelectBestTask(pending, constraints); got == nil {
		t.Fatal("want the un-stamped issue selected")
	}
	stamped := []backend.IssueData{labelIssue("T-1", "plan", "criticized")}
	if got := SelectBestTask(stamped, constraints); got != nil {
		t.Fatalf("selected %s after stamping — the stage would re-claim forever", got.Issue.ID)
	}
}

// A role configured with ONLY label constraints must still activate the router;
// otherwise the constraint is silently ignored — the exact bug this fixes.
func TestHasRoutingConstraints(t *testing.T) {
	cases := []struct {
		name      string
		c         RoleConstraints
		repoLabel string
		want      bool
	}{
		{"nothing set", RoleConstraints{}, "", false},
		{"only include labels", RoleConstraints{Labels: []string{"plan"}}, "", true},
		{"only exclude labels", RoleConstraints{ExcludeLabels: []string{"criticized"}}, "", true},
		{"only skills", RoleConstraints{Skills: []string{"go"}}, "", true},
		{"only task filter", RoleConstraints{TaskFilter: "any"}, "", true},
		{"only repo", RoleConstraints{}, "backend", true},
		{"only source repos", RoleConstraints{SourceRepos: []string{"r"}}, "", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.c.HasRoutingConstraints(tc.repoLabel); got != tc.want {
				t.Errorf("HasRoutingConstraints = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestMergeRoleConstraints_CarriesLabels(t *testing.T) {
	got := MergeRoleConstraints(config.RoleConfig{
		Labels:        []string{"plan"},
		ExcludeLabels: []string{"criticized"},
	}, config.AgentEntry{})
	if len(got.Labels) != 1 || got.Labels[0] != "plan" {
		t.Errorf("Labels = %v, want [plan]", got.Labels)
	}
	if len(got.ExcludeLabels) != 1 || got.ExcludeLabels[0] != "criticized" {
		t.Errorf("ExcludeLabels = %v, want [criticized]", got.ExcludeLabels)
	}
}

func TestRoleConfigFromEnv_Labels(t *testing.T) {
	t.Setenv("LOOM_ROLE_LABELS", "plan,urgent")
	t.Setenv("LOOM_ROLE_EXCLUDE_LABELS", "criticized")
	rc := RoleConfigFromEnv()
	if len(rc.Labels) != 2 || rc.Labels[0] != "plan" || rc.Labels[1] != "urgent" {
		t.Errorf("Labels = %v, want [plan urgent]", rc.Labels)
	}
	if len(rc.ExcludeLabels) != 1 || rc.ExcludeLabels[0] != "criticized" {
		t.Errorf("ExcludeLabels = %v, want [criticized]", rc.ExcludeLabels)
	}
}

func TestRoleConfigFromEnv_LabelsUnset(t *testing.T) {
	t.Setenv("LOOM_ROLE_LABELS", "")
	t.Setenv("LOOM_ROLE_EXCLUDE_LABELS", "")
	rc := RoleConfigFromEnv()
	if rc.Labels != nil || rc.ExcludeLabels != nil {
		t.Errorf("labels = %v / %v, want both nil", rc.Labels, rc.ExcludeLabels)
	}
}
