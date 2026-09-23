package backends

import (
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/domain"
)

func TestIsPlannerWorker(t *testing.T) {
	cases := []struct {
		role, filter string
		want         bool
	}{
		{"plan", "", true},
		{"plan", "needs_plan", true},
		{"task", "has_design", false},
		{"custom-planner", "needs_plan", true},
		{"lead", "", false},
	}
	for _, c := range cases {
		if got := IsPlannerWorker(c.role, c.filter); got != c.want {
			t.Errorf("IsPlannerWorker(%q,%q)=%v, want %v", c.role, c.filter, got, c.want)
		}
	}
}

func TestEffectiveEnforcement(t *testing.T) {
	cases := []struct {
		backend  string
		readOnly bool
		want     EnforcementLevel
	}{
		{"claude", true, EnforcementHardTools},
		{"codex", true, EnforcementOSSandbox},
		{"gemini", true, EnforcementHardTools},
		{"cursor", true, EnforcementPromptOnly},
		{"opencode", true, EnforcementPromptOnly},
		{"claude", false, EnforcementNone},
	}
	for _, c := range cases {
		if got := EffectiveEnforcement(c.backend, c.readOnly); got != c.want {
			t.Errorf("EffectiveEnforcement(%q,%v)=%q, want %q", c.backend, c.readOnly, got, c.want)
		}
	}
}

func TestResolvePlannerCaps_Matrix(t *testing.T) {
	hooksOK := DefaultPlanHostHooks()
	cases := []struct {
		name      string
		in        PlannerCapInput
		wantAdmit bool
		wantVia   map[string]string // cap → via
	}{
		{
			name: "claude+read_only+hooks admits via host",
			in: PlannerCapInput{
				Backend: "claude", ReadOnly: true, RoleName: "plan",
				Hooks: hooksOK, AssignedTaskIDSet: true,
			},
			wantAdmit: true,
			wantVia: map[string]string{
				CapTaskDesignSubmit: "host",
				CapTaskStatusReview: "host",
				CapTaskReadAssigned: "host",
			},
		},
		{
			name: "claude+read_only+no hooks fails closed",
			in: PlannerCapInput{
				Backend: "claude", ReadOnly: true, RoleName: "plan",
				AssignedTaskIDSet: true,
			},
			wantAdmit: false,
		},
		{
			name: "codex+read_only+no hooks+no server fails closed",
			in: PlannerCapInput{
				Backend: "codex", ReadOnly: true, RoleName: "plan",
				AssignedTaskIDSet: true,
			},
			wantAdmit: false,
		},
		{
			name: "codex+read_only+server URL admits via agent_shell",
			in: PlannerCapInput{
				Backend: "codex", ReadOnly: true, RoleName: "plan",
				AssignedTaskIDSet: true, ServerURL: "http://127.0.0.1:8080",
			},
			wantAdmit: true,
			wantVia: map[string]string{
				CapTaskDesignSubmit: "agent_shell",
			},
		},
		{
			name: "cursor+read_only+no hooks admits via soft shell",
			in: PlannerCapInput{
				Backend: "cursor", ReadOnly: true, RoleName: "plan",
				AssignedTaskIDSet: true,
			},
			wantAdmit: true,
			wantVia: map[string]string{
				CapTaskDesignSubmit: "agent_shell",
			},
		},
		{
			name: "non-planner ignored",
			in: PlannerCapInput{
				Backend: "claude", ReadOnly: true, RoleName: "task",
				TaskFilter: "has_design",
			},
			wantAdmit: true,
		},
		{
			name: "gemini+hooks admits",
			in: PlannerCapInput{
				Backend: "gemini", ReadOnly: true, RoleName: "plan",
				Hooks: hooksOK, AssignedTaskIDSet: true,
			},
			wantAdmit: true,
		},
		{
			name: "needs_plan filter without plan role still gated",
			in: PlannerCapInput{
				Backend: "claude", ReadOnly: true, RoleName: "custom",
				TaskFilter: "needs_plan", AssignedTaskIDSet: true,
			},
			wantAdmit: false,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := ResolvePlannerCaps(c.in)
			admitted := got.AdmitError == nil
			if admitted != c.wantAdmit {
				t.Fatalf("admit=%v (err=%v), want admit=%v", admitted, got.AdmitError, c.wantAdmit)
			}
			if !c.wantAdmit {
				msg := got.AdmitError.Error()
				for _, needle := range []string{"fail-closed", "missing", CapTaskDesignSubmit} {
					if !strings.Contains(msg, needle) {
						t.Errorf("admit error missing %q: %s", needle, msg)
					}
				}
				if !strings.Contains(msg, "on-complete-write-design") {
					t.Errorf("admit error must name remediation: %s", msg)
				}
			}
			for cap, via := range c.wantVia {
				found := false
				for _, sat := range got.Caps {
					if sat.Cap == cap {
						found = true
						if sat.Via != via {
							t.Errorf("cap %s via=%q, want %q", cap, sat.Via, via)
						}
					}
				}
				if !found && got.IsPlanner {
					t.Errorf("cap %s missing from result", cap)
				}
			}
			// repo.write must stay denied for planners under read_only.
			if got.IsPlanner && c.in.ReadOnly {
				for _, sat := range got.Caps {
					if sat.Cap == CapRepoWrite && sat.Satisfied {
						t.Error("repo.write must remain denied under plan read_only")
					}
				}
			}
		})
	}
}

func TestDefaultPlanHostHooks_Valid(t *testing.T) {
	h := DefaultPlanHostHooks()
	if err := h.Validate(); err != nil {
		t.Fatalf("DefaultPlanHostHooks invalid: %v", err)
	}
	if !HasHostDesignSubmit(h) || !HasHostStatusReview(h) {
		t.Fatal("default hooks must satisfy host design+review")
	}
}

func TestHasHostDesignSubmit_RequiresFinalReply(t *testing.T) {
	bad := &domain.AgentHooks{OnComplete: []domain.AgentHookAction{
		{Type: domain.AgentHookActionWriteDesign, Source: "first_reply"},
	}}
	if HasHostDesignSubmit(bad) {
		t.Fatal("non-final_reply write_design must not count")
	}
}
