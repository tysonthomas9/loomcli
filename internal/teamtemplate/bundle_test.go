package teamtemplate

import (
	"reflect"
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/domain"
)

// wantBundleIDs is the picker order the registry promises.
var wantBundleIDs = []string{"fullstack-app", "website", "ai-agent", "backend"}

func TestAllReturnsBundlesInPickerOrder(t *testing.T) {
	all := All()
	if len(all) != len(wantBundleIDs) {
		t.Fatalf("All() returned %d templates, want %d", len(all), len(wantBundleIDs))
	}
	for i, want := range wantBundleIDs {
		if all[i].ID != want {
			t.Errorf("All()[%d].ID = %q, want %q", i, all[i].ID, want)
		}
		if all[i].SchemaVersion != SchemaVersion {
			t.Errorf("%s: schema_version %d, want %d", all[i].ID, all[i].SchemaVersion, SchemaVersion)
		}
		if all[i].Revision < 1 {
			t.Errorf("%s: revision %d, want >= 1", all[i].ID, all[i].Revision)
		}
		if strings.TrimSpace(all[i].Label) == "" || strings.TrimSpace(all[i].Description) == "" {
			t.Errorf("%s: label and description are required on a picker card", all[i].ID)
		}
	}
}

func TestByID(t *testing.T) {
	for _, id := range wantBundleIDs {
		if _, ok := ByID(id); !ok {
			t.Errorf("ByID(%q) not found", id)
		}
	}
	if _, ok := ByID("nope"); ok {
		t.Error("ByID(\"nope\") reported found")
	}
}

// The registry is package state shared by every caller; handing out an aliased
// slice would let one caller re-route another's bundle.
func TestAllReturnsDefensiveCopies(t *testing.T) {
	first := All()[0]
	first.Roles[0].Labels[0] = "mutated"
	first.Roles[0].Skills = append(first.Roles[0].Skills, "mutated")
	if got := All()[0].Roles[0].Labels[0]; got == "mutated" {
		t.Fatalf("mutating a returned template changed the registry (labels[0] = %q)", got)
	}
	if got := All()[0].Roles[0].Skills; len(got) != 2 {
		t.Fatalf("mutating a returned template changed the registry (skills = %v)", got)
	}
}

// Every embedded bundle satisfies every rule, asserted field by field rather
// than by re-calling validate, so a rule that silently stops being enforced is
// still caught here.
func TestBundlesSatisfyEveryRule(t *testing.T) {
	for _, tpl := range All() {
		t.Run(tpl.ID, func(t *testing.T) {
			roleKinds := map[string]string{}
			for _, role := range tpl.Roles {
				roleKinds[role.Name] = role.Kind
				assertRole(t, role)
			}
			for _, agent := range tpl.Agents {
				assertAgent(t, agent, roleKinds)
			}
		})
	}
}

func assertRole(t *testing.T, role TemplateRole) {
	t.Helper()
	if role.Kind != string(domain.RoleKindWorker) && role.Kind != string(domain.RoleKindInteractive) {
		t.Errorf("agent role %q: kind %q is not explicit", role.Name, role.Kind)
	}
	if _, reserved := reservedRoleNames[role.Name]; reserved {
		t.Errorf("agent role %q: reserved name", role.Name)
	}
	if role.TaskFilter == "needs_plan" || role.TaskFilter == "needs_design" {
		t.Errorf("agent role %q: task_filter %q dies at spawn or mis-routes", role.Name, role.TaskFilter)
	}
	if !validTaskFilters[role.TaskFilter] {
		t.Errorf("agent role %q: task_filter %q outside the vocabulary", role.Name, role.TaskFilter)
	}
	if len(role.DeniedTools) != 0 || len(role.AllowedTools) != 0 {
		t.Errorf("agent role %q: tool lists must ship empty, got denied=%v allowed=%v", role.Name, role.DeniedTools, role.AllowedTools)
	}
	if role.ReadOnly {
		t.Errorf("agent role %q: read_only must be false", role.Name)
	}
	if role.Model != "" {
		t.Errorf("agent role %q: model must be empty, got %q", role.Name, role.Model)
	}
	if !validDisplayLabels[role.DisplayLabel] {
		t.Errorf("agent role %q: display_label %q outside the vocabulary", role.Name, role.DisplayLabel)
	}
	if strings.TrimSpace(role.Description) == "" {
		t.Errorf("agent role %q: description is the delegation criterion and is required", role.Name)
	}
	assertRolePrompt(t, role)
	assertRoleRouting(t, role)
}

func assertRolePrompt(t *testing.T, role TemplateRole) {
	t.Helper()
	id, ok := role.promptID()
	if !ok {
		t.Fatalf("agent role %q: prompt_file %q is not a builtin: reference", role.Name, role.PromptFile)
	}
	registered := domain.IsBuiltinWorkerPrompt(id)
	if role.Kind == string(domain.RoleKindInteractive) {
		registered = domain.IsBuiltinInteractivePrompt(id)
	}
	if !registered {
		t.Errorf("agent role %q: builtin prompt %q is not registered for kind %q", role.Name, id, role.Kind)
	}
}

func assertRoleRouting(t *testing.T, role TemplateRole) {
	t.Helper()
	if role.Kind == string(domain.RoleKindWorker) && role.TaskFilter == "any" && len(role.Labels) == 0 {
		t.Errorf("agent role %q: task_filter any without a label gate is an unscoped generalist", role.Name)
	}
	excluded := map[string]bool{}
	for _, label := range role.ExcludeLabels {
		excluded[label] = true
	}
	for _, label := range role.Labels {
		if excluded[label] {
			t.Errorf("agent role %q: label %q is both required and excluded", role.Name, label)
		}
	}
}

func assertAgent(t *testing.T, agent TemplateAgent, roleKinds map[string]string) {
	t.Helper()
	if want := agent.RoleName + "-1"; agent.Name != want {
		t.Errorf("agent %q: must be named %q", agent.Name, want)
	}
	if !agent.CrossRepo {
		t.Errorf("agent %q: cross_repo must be true", agent.Name)
	}
	if !agent.Auto {
		t.Errorf("agent %q: auto must be true", agent.Name)
	}
	if agent.DesiredState != string(domain.AgentDesiredRunning) {
		t.Errorf("agent %q: desired_state %q, want running", agent.Name, agent.DesiredState)
	}
	kind, declared := roleKinds[agent.RoleName]
	if !declared && !seededRoleNames[agent.RoleName] {
		t.Fatalf("agent %q: agent role %q is neither declared nor seeded", agent.Name, agent.RoleName)
	}
	if kind == string(domain.RoleKindInteractive) {
		t.Errorf("agent %q: interactive agent roles are provisioned without agents", agent.Name)
	}
}

// Role names repeat across bundles on purpose so a second template converges
// instead of duplicating. That only works if the shared definitions are
// identical — anything else turns a re-apply into a divergence report. Any
// deliberate exception must be listed here so a reviewer sees it; there are
// currently none.
func TestSharedRoleNamesConvergeAcrossBundles(t *testing.T) {
	deliberatelyDifferent := map[string]string{}
	seen := map[string]TemplateRole{}
	from := map[string]string{}
	for _, tpl := range All() {
		for _, role := range tpl.Roles {
			prior, ok := seen[role.Name]
			if !ok {
				seen[role.Name] = role
				from[role.Name] = tpl.ID
				continue
			}
			same := reflect.DeepEqual(prior, role)
			if why, expected := deliberatelyDifferent[role.Name]; expected {
				if same {
					t.Errorf("agent role %q is identical in %s and %s but listed as deliberately different (%s)", role.Name, from[role.Name], tpl.ID, why)
				}
				continue
			}
			if !same {
				t.Errorf("agent role %q differs between %s and %s; a shared name must converge on re-apply", role.Name, from[role.Name], tpl.ID)
			}
		}
	}
}

// Every registered team worker prompt is used by at least one bundle: an
// unreferenced prompt means a roster row was dropped.
func TestEveryTeamWorkerPromptIsUsed(t *testing.T) {
	used := map[string]bool{}
	for _, tpl := range All() {
		for _, role := range tpl.Roles {
			if id, ok := role.promptID(); ok {
				used[id] = true
			}
		}
	}
	for _, prompt := range domain.BuiltinWorkerPrompts() {
		if !used[prompt.ID] {
			t.Errorf("worker prompt %q is registered but no bundle references it", prompt.ID)
		}
	}
}

func TestParseBundleRejectsUnknownFields(t *testing.T) {
	raw := []byte(`
schema_version: 1
id: typo
label: Typo
description: A bundle with a typo.
revision: 1
roles:
  - name: some-dev
    kind: worker
    display_label: Developer
    description: Implements things.
    prompt_file: builtin:team-backend-dev
    task_filter: has_design
    exclude_label: [architect]
`)
	if _, err := parseBundle(raw); err == nil {
		t.Fatal("parseBundle accepted an unknown field")
	} else if !strings.Contains(err.Error(), "exclude_label") {
		t.Fatalf("error %q does not name the unknown field", err)
	}
}

// The invalid-data table. Each case starts from a real bundle and breaks
// exactly one rule, so a rule that stops being enforced fails loudly here
// rather than at a user's first apply.
func TestValidateRejectsInvalidBundles(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*TeamTemplate)
		want   string
	}{
		{"schema version", func(t *TeamTemplate) { t.SchemaVersion = 2 }, "schema_version"},
		{"revision zero", func(t *TeamTemplate) { t.Revision = 0 }, "revision"},
		{"bad id", func(t *TeamTemplate) { t.ID = "Fullstack App" }, "id"},
		{"empty label", func(t *TeamTemplate) { t.Label = "" }, "label is required"},
		{"empty description", func(t *TeamTemplate) { t.Description = "" }, "description is required"},
		{"no roles", func(t *TeamTemplate) { t.Roles = nil; t.Agents = nil }, "at least one agent role"},
		{"bad role name", func(t *TeamTemplate) { t.Roles[0].Name = "App_Architect!" }, "name must be"},
		{"reserved role name", func(t *TeamTemplate) { t.Roles[0].Name = "lead" }, "reserved"},
		{"auth tier role name", func(t *TeamTemplate) { t.Roles[0].Name = "developer" }, "reserved"},
		{"orchestrator role name", func(t *TeamTemplate) { t.Roles[0].Name = "orchestrator" }, "reserved"},
		{"duplicate role", func(t *TeamTemplate) { t.Roles[1].Name = t.Roles[0].Name }, "declared twice"},
		{"blank kind", func(t *TeamTemplate) { t.Roles[0].Kind = "" }, "explicitly worker or interactive"},
		{"empty role description", func(t *TeamTemplate) { t.Roles[0].Description = "" }, "delegation criterion"},
		{"bad display label", func(t *TeamTemplate) { t.Roles[0].DisplayLabel = "Design" }, "display_label"},
		{"path prompt", func(t *TeamTemplate) { t.Roles[0].PromptFile = "prompts/architect.md" }, "builtin: reference"},
		{"unregistered worker prompt", func(t *TeamTemplate) { t.Roles[0].PromptFile = "builtin:team-nope" }, "not a registered worker prompt"},
		{"worker prompt on interactive role", func(t *TeamTemplate) { t.Roles[4].PromptFile = "builtin:team-qa" }, "not a registered interactive prompt"},
		{"needs_plan", func(t *TeamTemplate) { t.Roles[0].TaskFilter = "needs_plan" }, "task_filter"},
		{"needs_design", func(t *TeamTemplate) { t.Roles[1].TaskFilter = "needs_design" }, "task_filter"},
		{"bad effort", func(t *TeamTemplate) { t.Roles[0].Effort = "extreme" }, "effort"},
		{"pinned model", func(t *TeamTemplate) { t.Roles[0].Model = "claude-opus" }, "model must be empty"},
		{"any without labels", func(t *TeamTemplate) { t.Roles[0].Labels = nil }, "needs at least one entry in labels"},
		{"upper-case label", func(t *TeamTemplate) { t.Roles[0].Labels = []string{"Architect"} }, "lowercase issue label"},
		{"label overlap", func(t *TeamTemplate) { t.Roles[0].ExcludeLabels = []string{"architect"} }, "both required and excluded"},
		{"read only", func(t *TeamTemplate) { t.Roles[0].ReadOnly = true }, "read_only must be false"},
		{"denied tools", func(t *TeamTemplate) { t.Roles[3].DeniedTools = []string{"Bash"} }, "must be empty"},
		{"allowed tools", func(t *TeamTemplate) { t.Roles[3].AllowedTools = []string{"Read"} }, "must be empty"},
		{"bad agent name", func(t *TeamTemplate) { t.Agents[0].Name = "architect-1" }, "must be named"},
		{"agent suffix slip", func(t *TeamTemplate) { t.Agents[3].Name = "qa-1" }, "must be named"},
		{"duplicate agent", func(t *TeamTemplate) { t.Agents[1].Name = t.Agents[0].Name }, "declared twice"},
		{"unknown agent role", func(t *TeamTemplate) { t.Agents[0].RoleName = "ghost"; t.Agents[0].Name = "ghost-1" }, "neither declared"},
		{"agent on interactive role", func(t *TeamTemplate) {
			t.Agents[0].RoleName = "code-reviewer"
			t.Agents[0].Name = "code-reviewer-1"
		}, "interactive"},
		{"bad desired state", func(t *TeamTemplate) { t.Agents[0].DesiredState = "draining" }, "desired_state"},
		{"single repo", func(t *TeamTemplate) { t.Agents[0].CrossRepo = false }, "cross_repo"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			tpl, ok := ByID("fullstack-app")
			if !ok {
				t.Fatal("fullstack-app missing")
			}
			tc.mutate(&tpl)
			err := validate(tpl)
			if err == nil {
				t.Fatalf("validate accepted a bundle that breaks %q", tc.name)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error %q does not mention %q", err, tc.want)
			}
		})
	}
}

// The one thing bundle data must never contain: a path on this machine, a
// credential, or a setup command.
func TestBundlesCarryNoLocalPathsOrSecrets(t *testing.T) {
	banned := []string{"/Users/", "/home/", "C:\\", "http://", "https://", "token", "secret", "password", "api_key"}
	for _, tpl := range All() {
		for _, role := range tpl.Roles {
			haystack := strings.ToLower(role.Description + " " + role.PromptFile + " " + strings.Join(role.Skills, " "))
			for _, needle := range banned {
				if strings.Contains(haystack, strings.ToLower(needle)) {
					t.Errorf("%s/%s: bundle data contains %q", tpl.ID, role.Name, needle)
				}
			}
		}
	}
}

// TestCheckSharedRoles_RejectsDivergentDuplicate is the guard that replaces the
// prose comments: two bundles declaring the same role name with different
// content must fail to load rather than make apply order significant.
func TestCheckSharedRoles_RejectsDivergentDuplicate(t *testing.T) {
	base := TemplateRole{Name: "code-reviewer", Kind: "worker", Effort: "medium"}
	divergent := base
	divergent.Effort = "high"

	err := checkSharedRoles([]TeamTemplate{
		{ID: "alpha", Roles: []TemplateRole{base}},
		{ID: "beta", Roles: []TemplateRole{divergent}},
	})
	if err == nil {
		t.Fatal("checkSharedRoles = nil for a divergent duplicate role, want an error")
	}
	for _, want := range []string{"code-reviewer", "alpha", "beta"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not name %q", err, want)
		}
	}
}

// TestCheckSharedRoles_AllowsIdenticalDuplicate keeps the check from rejecting
// the shared roles the shipped bundles actually rely on.
func TestCheckSharedRoles_AllowsIdenticalDuplicate(t *testing.T) {
	role := TemplateRole{Name: "code-reviewer", Kind: "worker", Skills: []string{"review"}}
	if err := checkSharedRoles([]TeamTemplate{
		{ID: "alpha", Roles: []TemplateRole{role}},
		{ID: "beta", Roles: []TemplateRole{role}},
	}); err != nil {
		t.Fatalf("checkSharedRoles = %v for an identical duplicate role, want nil", err)
	}
}

// TestShippedBundlesShareRolesVerbatim proves the invariant holds across the
// bundles as shipped, and that at least one role really is shared (so the check
// is not vacuously passing).
func TestShippedBundlesShareRolesVerbatim(t *testing.T) {
	counts := map[string]int{}
	for _, tpl := range All() {
		for _, role := range tpl.Roles {
			counts[role.Name]++
		}
	}
	shared := 0
	for _, n := range counts {
		if n > 1 {
			shared++
		}
	}
	if shared == 0 {
		t.Fatal("no role name is shared across bundles; checkSharedRoles guards nothing")
	}
	if err := checkSharedRoles(All()); err != nil {
		t.Fatalf("shipped bundles violate the shared-role invariant: %v", err)
	}
}
