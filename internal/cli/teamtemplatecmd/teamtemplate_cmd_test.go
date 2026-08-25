package teamtemplatecmd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/tysonthomas9/loomcli/internal/bootstrap"
	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/infra/memstore"
	"github.com/tysonthomas9/loomcli/internal/store"
	"github.com/tysonthomas9/loomcli/internal/teamtemplate"
)

const testWorkspace = "TEST"

func TestTemplateCommandWiring(t *testing.T) {
	cmd := newTemplateCommand(commandDeps{})
	if cmd.Use != "template" {
		t.Fatalf("Use = %q, want template", cmd.Use)
	}
	if cmd.GroupID != "workspace" {
		t.Fatalf("GroupID = %q, want workspace", cmd.GroupID)
	}
	if cmd.RunE == nil {
		t.Fatal("template RunE = nil")
	}
	for _, name := range []string{"list", "show", "apply"} {
		sub, _, err := cmd.Find([]string{name})
		if err != nil {
			t.Fatalf("find %s: %v", name, err)
		}
		if sub.RunE == nil {
			t.Errorf("%s RunE = nil", name)
		}
	}
	apply, _, err := cmd.Find([]string{"apply"})
	if err != nil {
		t.Fatalf("find apply: %v", err)
	}
	if apply.Flags().Lookup("workspace") != nil {
		t.Fatal("apply defines a local --workspace flag")
	}
	for _, name := range []string{"dry-run", "json", "strict"} {
		if apply.Flags().Lookup(name) == nil {
			t.Errorf("apply missing --%s", name)
		}
	}
}

func TestTemplateListRegistryPure(t *testing.T) {
	calledStore := false
	deps := commandDeps{
		withActiveWorkspace: func(func(context.Context, *bootstrap.StoreHandle, string) error) error {
			calledStore = true
			return errors.New("store must not be opened")
		},
		writeJSON: func(any) error { return nil },
	}
	cmd := newTemplateCommand(deps)
	var output bytes.Buffer
	cmd.SetOut(&output)
	cmd.SetErr(&output)
	cmd.SetArgs([]string{"list"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("list: %v", err)
	}
	if calledStore {
		t.Fatal("list accessed the store")
	}
	for _, want := range []string{
		"ID", "LABEL", "REV", "AGENT ROLES", "AGENTS",
		"fullstack-app", "Full-Stack App Development",
		"website", "ai-agent", "backend",
	} {
		if !strings.Contains(output.String(), want) {
			t.Errorf("list output missing %q:\n%s", want, output.String())
		}
	}
}

func TestTemplateShowRegistryPure(t *testing.T) {
	calledStore := false
	deps := commandDeps{
		withActiveWorkspace: func(func(context.Context, *bootstrap.StoreHandle, string) error) error {
			calledStore = true
			return errors.New("store must not be opened")
		},
		writeJSON: func(any) error { return nil },
	}
	cmd := newTemplateCommand(deps)
	var output bytes.Buffer
	cmd.SetOut(&output)
	cmd.SetErr(&output)
	cmd.SetArgs([]string{"show", "fullstack-app"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("show: %v", err)
	}
	if calledStore {
		t.Fatal("show accessed the store")
	}
	for _, want := range []string{
		"Template:     fullstack-app",
		"Revision:     3 (schema 1)",
		"Agent roles (5):",
		"KIND", "LABEL", "PROMPT", "TASK FILTER", "LABELS/EXCLUDE",
		"app-architect", "builtin:team-architect", "architect",
		"Agents (4):", "AGENT ROLE", "DESIRED", "running",
	} {
		if !strings.Contains(output.String(), want) {
			t.Errorf("show output missing %q:\n%s", want, output.String())
		}
	}
}

func TestTemplateShowUnknownID(t *testing.T) {
	cmd := newTemplateCommand(commandDeps{})
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	cmd.SetArgs([]string{"show", "missing"})
	err := cmd.Execute()
	if err == nil || err.Error() != `template "missing" not found (run 'loom template list')` {
		t.Fatalf("error = %v", err)
	}
}

func TestTemplateApplyCreated(t *testing.T) {
	st := newTemplateStore(t, true)
	cmd, output, materialized := newApplyHarness(st, localWorkspaceState())
	cmd.SetArgs([]string{"apply", "backend"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("apply: %v\n%s", err, output.String())
	}
	for _, want := range []string{
		`Applying Team Template "Backend Development" (backend rev 3) to workspace TEST`,
		"Created agent role TEST/api-architect",
		"Created agent TEST/api-architect-1 (agent role=api-architect)",
		"Applied backend to TEST: 9 created, 0 skipped, 0 failed",
		"4 agents configured to run (4 worktrees). A running daemon adopts them on its next poll.",
	} {
		if !strings.Contains(output.String(), want) {
			t.Errorf("apply output missing %q:\n%s", want, output.String())
		}
	}
	if *materialized != 4 {
		t.Errorf("materialized calls = %d, want 4", *materialized)
	}
	roles, err := st.Roles().List(context.Background(), testWorkspace)
	if err != nil {
		t.Fatalf("list agent roles: %v", err)
	}
	agents, err := st.Agents().List(context.Background(), testWorkspace)
	if err != nil {
		t.Fatalf("list agents: %v", err)
	}
	if len(roles) != 5 || len(agents) != 4 {
		t.Fatalf("created agent roles/agents = %d/%d, want 5/4", len(roles), len(agents))
	}
}

func TestTemplateApplySkippedMatch(t *testing.T) {
	st := newTemplateStore(t, true)
	seedTemplate(t, st, "backend")
	cmd, output, materialized := newApplyHarness(st, localWorkspaceState())
	cmd.SetArgs([]string{"apply", "backend"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("re-apply: %v\n%s", err, output.String())
	}
	for _, want := range []string{
		"Skipped agent role TEST/api-architect (already applied)",
		"Skipped agent TEST/api-architect-1 (already applied; worktrees re-checked)",
		"Applied backend to TEST: 0 created, 9 skipped (9 match), 0 failed",
		"Workspace already matches this template.",
	} {
		if !strings.Contains(output.String(), want) {
			t.Errorf("re-apply output missing %q:\n%s", want, output.String())
		}
	}
	if *materialized != 4 {
		t.Errorf("materialized calls = %d, want 4", *materialized)
	}
}

func TestTemplateApplySkippedDiverged(t *testing.T) {
	st := newTemplateStore(t, true)
	seedTemplate(t, st, "backend")
	promptFile := "prompts/custom.md"
	if _, err := st.Roles().Update(context.Background(), testWorkspace, "backend-dev", store.RoleUpdate{PromptFile: &promptFile}); err != nil {
		t.Fatalf("diverge agent role: %v", err)
	}

	cmd, output, _ := newApplyHarness(st, localWorkspaceState())
	cmd.SetArgs([]string{"apply", "backend"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("default divergence must succeed: %v\n%s", err, output.String())
	}
	for _, want := range []string{
		"Skipped agent role TEST/backend-dev (diverged: prompt_file)",
		"9 skipped (8 match, 1 diverged)",
		"Note: diverged entries were left untouched (apply never overwrites).",
	} {
		if !strings.Contains(output.String(), want) {
			t.Errorf("diverged output missing %q:\n%s", want, output.String())
		}
	}
}

func TestTemplateApplyFailed(t *testing.T) {
	base := newTemplateStore(t, true)
	failing := storeWithRoleFailure{
		Store: base,
		roles: roleCreateFailureStore{
			RoleStore: base.Roles(),
			name:      "backend-dev",
		},
	}
	cmd, output, _ := newApplyHarness(failing, localWorkspaceState())
	cmd.SetArgs([]string{"apply", "backend"})
	err := cmd.Execute()
	if err == nil || err.Error() != "template apply: 2 of 9 steps failed" {
		t.Fatalf("error = %v, want partial-failure summary\n%s", err, output.String())
	}
	for _, want := range []string{
		"Failed agent role TEST/backend-dev: injected create failure",
		`Failed agent TEST/backend-dev-1: agent role "backend-dev" was not created: injected create failure`,
		"Applied backend to TEST: 7 created, 0 skipped, 2 failed",
		"Created entries were kept.",
	} {
		if !strings.Contains(output.String(), want) {
			t.Errorf("failure output missing %q:\n%s", want, output.String())
		}
	}
}

func TestTemplateApplyFailureWinsStrict(t *testing.T) {
	base := newTemplateStore(t, true)
	failing := storeWithRoleFailure{
		Store: base,
		roles: roleCreateFailureStore{
			RoleStore: base.Roles(),
			name:      "backend-dev",
		},
	}
	cmd, output, _ := newApplyHarness(failing, localWorkspaceState())
	cmd.SetArgs([]string{"apply", "backend", "--strict"})
	err := cmd.Execute()
	if err == nil || err.Error() != "template apply: 2 of 9 steps failed" {
		t.Fatalf("strict failure error = %v, want failure summary to win\n%s", err, output.String())
	}
}

func TestTemplateApplyDryRun(t *testing.T) {
	st := newTemplateStore(t, true)
	cmd, output, materialized := newApplyHarness(st, localWorkspaceState())
	cmd.SetArgs([]string{"apply", "backend", "--dry-run"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("dry-run: %v\n%s", err, output.String())
	}
	for _, want := range []string{
		`Plan for Team Template "Backend Development" (backend rev 3) on workspace TEST`,
		"Would create agent role TEST/api-architect",
		"Would create agent TEST/api-architect-1 (agent role=api-architect)",
		"Plan: 9 to create, 0 skipped. No changes made.",
	} {
		if !strings.Contains(output.String(), want) {
			t.Errorf("dry-run output missing %q:\n%s", want, output.String())
		}
	}
	if *materialized != 0 {
		t.Errorf("dry-run materialized calls = %d, want 0", *materialized)
	}
	roles, _ := st.Roles().List(context.Background(), testWorkspace)
	agents, _ := st.Agents().List(context.Background(), testWorkspace)
	if len(roles) != 0 || len(agents) != 0 {
		t.Fatalf("dry-run created agent roles/agents = %d/%d", len(roles), len(agents))
	}
}

func TestTemplateApplyStrict(t *testing.T) {
	st := newTemplateStore(t, true)
	seedTemplate(t, st, "backend")
	promptFile := "prompts/custom.md"
	if _, err := st.Roles().Update(context.Background(), testWorkspace, "backend-dev", store.RoleUpdate{PromptFile: &promptFile}); err != nil {
		t.Fatalf("diverge agent role: %v", err)
	}

	for _, args := range [][]string{
		{"apply", "backend", "--strict"},
		{"apply", "backend", "--dry-run", "--strict"},
		{"apply", "backend", "--json", "--strict"},
	} {
		cmd, output, _ := newApplyHarness(st, localWorkspaceState())
		cmd.SetArgs(args)
		err := cmd.Execute()
		if err == nil || err.Error() != "template apply: 1 of 9 entries differ from the template (--strict)" {
			t.Errorf("%v error = %v\n%s", args, err, output.String())
		}
	}
}

func TestTemplateApplyJSON(t *testing.T) {
	st := newTemplateStore(t, true)
	cmd, output, _ := newApplyHarness(st, localWorkspaceState())
	cmd.SetArgs([]string{"apply", "backend", "--json"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("JSON apply: %v\n%s", err, output.String())
	}
	if strings.Contains(output.String(), "Applying Team Template") || strings.Contains(output.String(), "Created agent") {
		t.Fatalf("JSON output contains human step lines:\n%s", output.String())
	}
	var report teamtemplate.ApplyReport
	decoder := json.NewDecoder(strings.NewReader(output.String()))
	if err := decoder.Decode(&report); err != nil {
		t.Fatalf("decode report: %v\n%s", err, output.String())
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		t.Fatalf("JSON output has more than one document: %v\n%s", err, output.String())
	}
	if report.TemplateID != "backend" || report.WorkspaceKey != testWorkspace || report.Created != 9 || len(report.Steps) != 9 {
		t.Fatalf("report = %+v", report)
	}
	for _, key := range []string{`"template_id"`, `"schema_version"`, `"workspace_key"`, `"dry_run"`, `"steps"`, `"materialized"`} {
		if !strings.Contains(output.String(), key) {
			t.Errorf("JSON output missing %s:\n%s", key, output.String())
		}
	}
}

func TestTemplateApplyPreflightRefusal(t *testing.T) {
	st := newTemplateStore(t, false)
	cmd, output, _ := newApplyHarness(st, localWorkspaceState())
	cmd.SetArgs([]string{"apply", "backend"})
	err := cmd.Execute()
	if err == nil || !strings.Contains(err.Error(), `workspace "TEST" has no repositories`) {
		t.Fatalf("error = %v", err)
	}
	if strings.Contains(output.String(), "Applying Team Template") || strings.Contains(output.String(), "Created agent") {
		t.Fatalf("preflight refusal printed apply progress:\n%s", output.String())
	}
	roles, _ := st.Roles().List(context.Background(), testWorkspace)
	if len(roles) != 0 {
		t.Fatalf("preflight refusal created %d agent roles", len(roles))
	}
}

func TestTemplateApplyWarningsDoNotFail(t *testing.T) {
	st := newTemplateStore(t, false)
	maxAgents := 1
	if _, err := st.Daemon().Upsert(context.Background(), &domain.DaemonProfile{
		WorkspaceKey: testWorkspace,
		MaxAgents:    &maxAgents,
	}); err != nil {
		t.Fatalf("set daemon max agents: %v", err)
	}
	state := &bootstrap.StateCache{Workspaces: map[string]bootstrap.WorkspaceLocalState{
		testWorkspace: {},
	}}
	cmd, output, materialized := newApplyHarness(st, state)
	cmd.SetArgs([]string{"apply", "backend", "--strict"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("warning-only apply: %v\n%s", err, output.String())
	}
	for _, want := range []string{
		"Warning: workspace \"TEST\" has no local checkout",
		"max_agents limit of 1",
		"4 agents configured to run (0 worktrees; no local checkout). See warnings above.",
	} {
		if !strings.Contains(output.String(), want) {
			t.Errorf("warning output missing %q:\n%s", want, output.String())
		}
	}
	if *materialized != 0 {
		t.Errorf("path-less apply materialized calls = %d, want 0", *materialized)
	}
}

func newTemplateStore(t *testing.T, withRepo bool) *memstore.Store {
	t.Helper()
	ctx := context.Background()
	st := memstore.New()
	t.Cleanup(func() { _ = st.Close() })
	if _, err := st.Workspaces().Create(ctx, store.WorkspaceCreate{Key: testWorkspace, Name: "Test"}); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	if withRepo {
		if _, err := st.Repos().Create(ctx, store.RepoCreate{
			WorkspaceKey: testWorkspace,
			Name:         "repo",
			SourceRepoID: "repo",
		}); err != nil {
			t.Fatalf("create repo: %v", err)
		}
	}
	return st
}

func localWorkspaceState() *bootstrap.StateCache {
	return &bootstrap.StateCache{Workspaces: map[string]bootstrap.WorkspaceLocalState{
		testWorkspace: {
			Path:  "/workspace",
			Repos: map[string]string{"repo": "/workspace/repo"},
		},
	}}
}

func newApplyHarness(st store.Store, state *bootstrap.StateCache) (*cobra.Command, *bytes.Buffer, *int) {
	output := &bytes.Buffer{}
	materialized := 0
	deps := commandDeps{
		withActiveWorkspace: func(fn func(context.Context, *bootstrap.StoreHandle, string) error) error {
			return fn(context.Background(), &bootstrap.StoreHandle{Store: st}, testWorkspace)
		},
		loadStateCache: func() (*bootstrap.StateCache, error) {
			return state, nil
		},
		localMaterializer: func(store.Store) teamtemplate.LocalMaterializer {
			return func(context.Context, domain.Agent) error {
				materialized++
				return nil
			}
		},
		writeJSON: func(value any) error {
			encoder := json.NewEncoder(output)
			encoder.SetIndent("", "  ")
			return encoder.Encode(value)
		},
	}
	cmd := newTemplateCommand(deps)
	cmd.SetOut(output)
	cmd.SetErr(output)
	return cmd, output, &materialized
}

func seedTemplate(t *testing.T, st store.Store, id string) {
	t.Helper()
	tpl, ok := teamtemplate.ByID(id)
	if !ok {
		t.Fatalf("template %q not found", id)
	}
	report, err := teamtemplate.Apply(context.Background(), teamtemplate.ApplyDeps{
		Store:     st,
		LocalPath: "/workspace",
	}, testWorkspace, tpl)
	if err != nil {
		t.Fatalf("seed template: %v", err)
	}
	if report.Failed != 0 {
		t.Fatalf("seed report has %d failures: %+v", report.Failed, report.Steps)
	}
}

type storeWithRoleFailure struct {
	store.Store
	roles store.RoleStore
}

func (s storeWithRoleFailure) Roles() store.RoleStore { return s.roles }

type roleCreateFailureStore struct {
	store.RoleStore
	name string
}

func (s roleCreateFailureStore) Create(ctx context.Context, in store.RoleCreate) (*domain.Role, error) {
	if in.Name == s.name {
		return nil, fmt.Errorf("injected create failure")
	}
	return s.RoleStore.Create(ctx, in)
}
