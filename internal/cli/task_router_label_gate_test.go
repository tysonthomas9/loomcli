package cli

import (
	"testing"

	"github.com/tysonthomas9/loomcli/internal/backend"
	"github.com/tysonthomas9/loomcli/internal/cli/config"
)

// --- MatchTask label gate tests ---

// labelGateIssue is a plain open task that passes every other filter, so the
// only thing a rejection below can be attributed to is the label gate.
func labelGateIssue(labels ...string) backend.IssueData {
	return backend.IssueData{
		ID: "T-1", Status: "open", IssueType: "task",
		Priority: 0, Design: "plan", Labels: labels,
	}
}

func TestMatchTask_SeededPlanRoleRejectsArchitectLabel(t *testing.T) {
	constraints := MergeRoleConstraints(config.RoleConfig{
		TaskFilter:    "needs_plan",
		ExcludeLabels: []string{"architect"},
	}, config.AgentEntry{Role: "plan"})
	issue := backend.IssueData{
		ID: "T-architect", Status: "open", IssueType: "feature",
		Labels: []string{"architect"},
	}

	got := MatchTask(issue, constraints)
	if got.Score != 0 || got.Reason != "excluded label: architect" {
		t.Fatalf("architect-labeled issue match = score %d, reason %q; want hard exclusion", got.Score, got.Reason)
	}
}

func TestMatchTask_LabelGate(t *testing.T) {
	tests := []struct {
		name        string
		labels      []string
		exclude     []string
		issueLabels []string
		wantScore   int
		wantReason  string
	}{
		{
			name:        "no gate configured accepts",
			issueLabels: []string{"needs-design"},
			wantScore:   120,
		},
		{
			name:        "empty non-nil lists gate nothing",
			labels:      []string{},
			exclude:     []string{},
			issueLabels: []string{"needs-design"},
			wantScore:   120,
		},
		{
			name:        "empty lists accept an unlabelled issue",
			labels:      []string{},
			exclude:     []string{},
			issueLabels: nil,
			wantScore:   120,
		},
		{
			name:        "required label present",
			labels:      []string{"needs-design"},
			issueLabels: []string{"needs-design"},
			wantScore:   120,
		},
		{
			name:        "required label missing",
			labels:      []string{"needs-design"},
			issueLabels: []string{"area:api"},
			wantScore:   0,
			wantReason:  "missing required label: needs-design",
		},
		{
			name:        "required label missing from an unlabelled issue",
			labels:      []string{"needs-design"},
			issueLabels: nil,
			wantScore:   0,
			wantReason:  "missing required label: needs-design",
		},
		{
			name:        "AND: every required label present",
			labels:      []string{"needs-design", "area:api"},
			issueLabels: []string{"area:api", "needs-design", "unrelated"},
			wantScore:   120,
		},
		{
			name:        "AND: one of several required labels missing",
			labels:      []string{"needs-design", "area:api"},
			issueLabels: []string{"needs-design"},
			wantScore:   0,
			wantReason:  "missing required label: area:api",
		},
		{
			name:        "AND is not OR: a single overlap is not enough",
			labels:      []string{"a", "b", "c"},
			issueLabels: []string{"b"},
			wantScore:   0,
			wantReason:  "missing required label: a",
		},
		{
			name:        "excluded label rejects",
			exclude:     []string{"wip"},
			issueLabels: []string{"needs-design", "wip"},
			wantScore:   0,
			wantReason:  "excluded label: wip",
		},
		{
			name:        "OR: any excluded label rejects",
			exclude:     []string{"wip", "on-hold"},
			issueLabels: []string{"on-hold"},
			wantScore:   0,
			wantReason:  "excluded label: on-hold",
		},
		{
			name:        "excluded label absent accepts",
			exclude:     []string{"wip"},
			issueLabels: []string{"needs-design"},
			wantScore:   120,
		},
		{
			name:        "exclusion beats inclusion when a label is in both lists",
			labels:      []string{"needs-design"},
			exclude:     []string{"needs-design"},
			issueLabels: []string{"needs-design"},
			wantScore:   0,
			wantReason:  "excluded label: needs-design",
		},
		{
			name:        "exclusion is evaluated before a satisfied inclusion",
			labels:      []string{"area:api"},
			exclude:     []string{"wip"},
			issueLabels: []string{"area:api", "wip"},
			wantScore:   0,
			wantReason:  "excluded label: wip",
		},
		{
			name:        "exclusion is evaluated before an unsatisfied inclusion",
			labels:      []string{"area:api"},
			exclude:     []string{"wip"},
			issueLabels: []string{"wip"},
			wantScore:   0,
			wantReason:  "excluded label: wip",
		},
		{
			name:        "required label comparison is case-sensitive",
			labels:      []string{"needs-design"},
			issueLabels: []string{"Needs-Design"},
			wantScore:   0,
			wantReason:  "missing required label: needs-design",
		},
		{
			name:        "excluded label comparison is case-sensitive",
			exclude:     []string{"WIP"},
			issueLabels: []string{"wip"},
			wantScore:   120,
		},
		{
			name:        "no trimming: whitespace is part of the label",
			labels:      []string{"needs-design"},
			issueLabels: []string{" needs-design "},
			wantScore:   0,
			wantReason:  "missing required label: needs-design",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := RoleConstraints{
				TaskFilter:    "has_design",
				Labels:        tt.labels,
				ExcludeLabels: tt.exclude,
			}

			got := MatchTask(labelGateIssue(tt.issueLabels...), c)

			if got.Score != tt.wantScore {
				t.Errorf("Score = %d (%s), want %d", got.Score, got.Reason, tt.wantScore)
			}
			if tt.wantReason != "" && got.Reason != tt.wantReason {
				t.Errorf("Reason = %q, want %q", got.Reason, tt.wantReason)
			}
		})
	}
}

// The gate sits after the open/type checks and before applyTaskFilter, so an
// issue that would fail both reports the label rejection — that ordering is what
// makes the reason readable when a role is label-gated.
func TestMatchTask_LabelGateRunsBeforeTaskFilter(t *testing.T) {
	// No design, so has_design would reject it on its own.
	issue := backend.IssueData{ID: "T-1", Status: "open", IssueType: "task", Labels: []string{"wip"}}

	got := MatchTask(issue, RoleConstraints{
		TaskFilter:    "has_design",
		Labels:        []string{"needs-design"},
		ExcludeLabels: []string{"wip"},
	})

	if got.Score != 0 {
		t.Fatalf("Score = %d, want 0", got.Score)
	}
	if got.Reason != "excluded label: wip" {
		t.Errorf("Reason = %q, want %q", got.Reason, "excluded label: wip")
	}
}

// The gate must reject outright, not deprioritize: unlike the skills fallback
// (Score=10), a label miss is a hard filter and SelectBestTask must drop it.
func TestMatchTask_LabelGateIsAHardRejectNotAFallback(t *testing.T) {
	issues := []backend.IssueData{
		{ID: "T-1", Status: "open", IssueType: "task", Design: "plan", Labels: []string{"area:api"}},
		{ID: "T-2", Status: "open", IssueType: "task", Design: "plan", Labels: []string{"needs-design"}},
	}
	c := RoleConstraints{TaskFilter: "has_design", Labels: []string{"needs-design"}}

	got := SelectBestTask(issues, c)

	if got == nil {
		t.Fatal("got nil, want T-2")
	}
	if got.Issue.ID != "T-2" {
		t.Errorf("ID = %q, want T-2 (T-1 must be rejected, not merely deprioritized)", got.Issue.ID)
	}

	// And a queue where nothing carries the required label yields no work at all.
	if m := SelectBestTask(issues[:1], c); m != nil {
		t.Errorf("SelectBestTask = %+v, want nil", m)
	}
}

// Every role that exists today has empty Labels/ExcludeLabels. The gate has to
// be bit-for-bit invisible to them.
func TestMatchTask_LabelGateNoOpForUngatedRoles(t *testing.T) {
	maxP := 3
	issues := []backend.IssueData{
		{ID: "T-1", Status: "open", IssueType: "task", Priority: 1, Design: "plan", Labels: []string{"go", "daemon"}},
		{ID: "T-2", Status: "open", IssueType: "task", Priority: 0, Design: "plan", Labels: nil},
		{ID: "T-3", Status: "open", IssueType: "task", Priority: 2, Labels: []string{"go"}},
		{ID: "T-4", Status: "closed", IssueType: "task", Priority: 0, Design: "plan"},
	}
	base := RoleConstraints{TaskFilter: "has_design", Skills: []string{"go"}, MaxPriority: &maxP}

	for _, gate := range []struct {
		name    string
		labels  []string
		exclude []string
	}{
		{name: "nil lists", labels: nil, exclude: nil},
		{name: "empty lists", labels: []string{}, exclude: []string{}},
	} {
		t.Run(gate.name, func(t *testing.T) {
			gated := base
			gated.Labels = gate.labels
			gated.ExcludeLabels = gate.exclude

			for _, issue := range issues {
				want := MatchTask(issue, base)
				got := MatchTask(issue, gated)
				if got.Score != want.Score || got.Reason != want.Reason {
					t.Errorf("%s: got (%d, %q), want (%d, %q)", issue.ID, got.Score, got.Reason, want.Score, want.Reason)
				}
			}
		})
	}
}

// --- constraint plumbing tests ---

func TestMergeRoleConstraints_LabelGateCopied(t *testing.T) {
	rc := RoleConfig{
		Labels:        []string{"needs-design", "area:api"},
		ExcludeLabels: []string{"wip"},
	}

	got := MergeRoleConstraints(rc, AgentEntry{})

	if len(got.Labels) != 2 || got.Labels[0] != "needs-design" || got.Labels[1] != "area:api" {
		t.Errorf("Labels = %v, want [needs-design area:api]", got.Labels)
	}
	if len(got.ExcludeLabels) != 1 || got.ExcludeLabels[0] != "wip" {
		t.Errorf("ExcludeLabels = %v, want [wip]", got.ExcludeLabels)
	}
}

// The worker rebuilds its constraints from the environment, so the gate is only
// enforced inside the worker if these two variables survive the hop.
func TestRoleConfigFromEnv_LabelGate(t *testing.T) {
	t.Setenv("LOOM_ROLE_LABELS", "needs-design,area:api")
	t.Setenv("LOOM_ROLE_EXCLUDE_LABELS", "wip")

	rc := RoleConfigFromEnv()

	if len(rc.Labels) != 2 || rc.Labels[0] != "needs-design" || rc.Labels[1] != "area:api" {
		t.Errorf("Labels = %v, want [needs-design area:api]", rc.Labels)
	}
	if len(rc.ExcludeLabels) != 1 || rc.ExcludeLabels[0] != "wip" {
		t.Errorf("ExcludeLabels = %v, want [wip]", rc.ExcludeLabels)
	}
}

func TestRoleConfigFromEnv_LabelGateEmpty(t *testing.T) {
	t.Setenv("LOOM_ROLE_LABELS", "")
	t.Setenv("LOOM_ROLE_EXCLUDE_LABELS", "")

	rc := RoleConfigFromEnv()

	if len(rc.Labels) != 0 {
		t.Errorf("Labels = %v, want empty", rc.Labels)
	}
	if len(rc.ExcludeLabels) != 0 {
		t.Errorf("ExcludeLabels = %v, want empty", rc.ExcludeLabels)
	}
}

// A role whose only constraint is the label gate still needs the router's
// availability check — otherwise it falls back to the unfiltered one and the
// gate is skipped on that path.
func TestBuildRouterTaskCheck_LabelOnlyRoleStillFilters(t *testing.T) {
	if check := BuildRouterTaskCheck(RoleConfig{Labels: []string{"needs-design"}}, AgentEntry{}, ""); check == nil {
		t.Error("BuildRouterTaskCheck = nil for a labels-only role, want a router check")
	}
	if check := BuildRouterTaskCheck(RoleConfig{ExcludeLabels: []string{"wip"}}, AgentEntry{}, ""); check == nil {
		t.Error("BuildRouterTaskCheck = nil for an exclude-labels-only role, want a router check")
	}
	if check := BuildRouterTaskCheck(RoleConfig{}, AgentEntry{}, ""); check != nil {
		t.Error("BuildRouterTaskCheck != nil for an unconstrained role, want nil (unchanged behavior)")
	}
}
