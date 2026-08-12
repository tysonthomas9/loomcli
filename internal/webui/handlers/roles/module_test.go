package roles

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/bootstrap"
	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/infra/memstore"
	"github.com/tysonthomas9/loomcli/internal/modules/agents"
	"github.com/tysonthomas9/loomcli/internal/platform/authority"
	"github.com/tysonthomas9/loomcli/internal/store"
	"github.com/tysonthomas9/loomcli/internal/webui/server/middleware"
	"github.com/tysonthomas9/loomcli/internal/webui/storeadapter"
)

type testRoleAPI struct {
	store store.Store
}

func (api *testRoleAPI) GetRole(ctx context.Context, workspace, roleName string) (*agents.Role, error) {
	role, err := api.store.Roles().Get(ctx, workspace, roleName)
	return agentsRoleFromDomain(role), err
}

func (api *testRoleAPI) ListRoles(ctx context.Context, workspace string) ([]*agents.Role, error) {
	roles, err := api.store.Roles().List(ctx, workspace)
	if err != nil {
		return nil, err
	}
	out := make([]*agents.Role, 0, len(roles))
	for _, role := range roles {
		out = append(out, agentsRoleFromDomain(role))
	}
	return out, nil
}

func (api *testRoleAPI) CreateRole(
	ctx context.Context,
	_ authority.OperatorAuthority,
	command agents.CreateRoleCommand,
) (*agents.Role, error) {
	definition := command.Role
	role, err := api.store.Roles().Create(ctx, store.RoleCreate{
		WorkspaceKey: command.WorkspaceKey, Name: definition.Name, Kind: definition.Kind,
		Description: definition.Description, Prompt: definition.Prompt, PromptFile: definition.PromptFile,
		Model: definition.Model, TaskFilter: definition.TaskFilter, Backend: definition.Backend,
		Effort: definition.Effort, PathPatterns: definition.PathPatterns, Skills: definition.Skills,
		MaxPriority: definition.MaxPriority, MaxConcurrency: definition.MaxConcurrency,
		ReadOnly: definition.ReadOnly, AllowedTools: definition.AllowedTools,
		DeniedTools: definition.DeniedTools, MaxBudgetUSD: definition.MaxBudgetUSD,
	})
	return agentsRoleFromDomain(role), err
}

func (api *testRoleAPI) UpdateRole(
	ctx context.Context,
	_ authority.OperatorAuthority,
	command agents.UpdateRoleCommand,
) (*agents.Role, error) {
	current, err := api.store.Roles().Get(ctx, command.WorkspaceKey, command.RoleName)
	if err != nil {
		return nil, err
	}
	if current == nil || !current.UpdatedAt.Equal(command.ExpectedUpdatedAt) {
		return nil, agents.ErrConflict
	}
	patch := command.Patch
	role, err := api.store.Roles().Update(ctx, command.WorkspaceKey, command.RoleName, store.RoleUpdate{
		Kind: patch.Kind, Description: patch.Description, Prompt: patch.Prompt,
		PromptFile: patch.PromptFile, Model: patch.Model, TaskFilter: patch.TaskFilter,
		Backend: patch.Backend, Effort: patch.Effort, PathPatterns: patch.PathPatterns,
		Skills: patch.Skills, MaxPriority: patch.MaxPriority, MaxConcurrency: patch.MaxConcurrency,
		ReadOnly: patch.ReadOnly, AllowedTools: patch.AllowedTools, DeniedTools: patch.DeniedTools,
		MaxBudgetUSD: patch.MaxBudgetUSD,
	})
	return agentsRoleFromDomain(role), err
}

func (api *testRoleAPI) DeleteRole(
	ctx context.Context,
	_ authority.OperatorAuthority,
	command agents.DeleteRoleCommand,
) error {
	current, err := api.store.Roles().Get(ctx, command.WorkspaceKey, command.RoleName)
	if err != nil {
		return err
	}
	if current == nil || !current.UpdatedAt.Equal(command.ExpectedUpdatedAt) {
		return agents.ErrConflict
	}
	return api.store.Roles().Delete(ctx, command.WorkspaceKey, command.RoleName)
}

type testRoleAuthorityResolver struct{}

func (testRoleAuthorityResolver) ResolveOperatorAuthority(
	*http.Request,
	string,
	authority.Action,
) (authority.OperatorAuthority, error) {
	return authority.OperatorAuthority{}, nil
}

type capturingRoleAuthorityResolver struct {
	workspace string
	action    authority.Action
}

func (resolver *capturingRoleAuthorityResolver) ResolveOperatorAuthority(
	_ *http.Request,
	workspace string,
	action authority.Action,
) (authority.OperatorAuthority, error) {
	resolver.workspace = workspace
	resolver.action = action
	return authority.OperatorAuthority{}, nil
}

func newTestRoleModule(st store.Store) *Module {
	return NewModule(Config{
		WorkspacePath: testRoleWorkspacePath(st), Roles: &testRoleAPI{store: st}, Authority: testRoleAuthorityResolver{},
	})
}

func testRoleWorkspacePath(st store.Store) WorkspacePathResolver {
	return func(ctx context.Context, workspace string) string {
		return storeadapter.ResolveOrHealWorkspacePath(ctx, st, workspace)
	}
}

func ensureRoleForTest(
	ctx context.Context,
	st store.Store,
	ws string,
	req EnsureRoleRequest,
) (*domain.Role, bool, error) {
	return EnsureRole(ctx, testRoleWorkspacePath(st), &testRoleAPI{store: st}, authority.OperatorAuthority{}, ws, req)
}

func ensureRoleWithReceiptForTest(
	ctx context.Context,
	st store.Store,
	ws string,
	req EnsureRoleRequest,
) (*EnsureRoleResult, error) {
	return EnsureRoleWithReceipt(ctx, testRoleWorkspacePath(st), &testRoleAPI{store: st}, authority.OperatorAuthority{}, ws, req)
}

func agentsRoleFromDomain(role *domain.Role) *agents.Role {
	if role == nil {
		return nil
	}
	return &agents.Role{
		WorkspaceKey: role.WorkspaceKey, Name: role.Name, Kind: string(role.Kind),
		Description: role.Description, Prompt: role.Prompt, PromptFile: role.PromptFile,
		Model: role.Model, TaskFilter: role.TaskFilter, Backend: role.Backend, Effort: role.Effort,
		PathPatterns: append([]string(nil), role.PathPatterns...), Skills: append([]string(nil), role.Skills...),
		MaxPriority: cloneInt(role.MaxPriority), MaxConcurrency: cloneInt(role.MaxConcurrency),
		ReadOnly: role.ReadOnly, AllowedTools: append([]string(nil), role.AllowedTools...),
		DeniedTools: append([]string(nil), role.DeniedTools...), MaxBudgetUSD: cloneFloat64(role.MaxBudgetUSD),
		CreatedAt: role.CreatedAt, UpdatedAt: role.UpdatedAt,
	}
}

func newRolesMux() *http.ServeMux {
	mux := http.NewServeMux()
	newTestRoleModule(memstore.New()).Register(mux)
	return mux
}

func postRole(t *testing.T, mux *http.ServeMux, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/workspaces/WS/roles", strings.NewReader(body))
	req = withCanonicalWorkspace(req, "WS", "WS")
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}

func TestCreateRole_CreatesThenIsIdempotent(t *testing.T) {
	mux := newRolesMux()

	// No prompt body, so no filesystem dependency — exercises the store path.
	rec := postRole(t, mux, `{"name":"bug-triage","task_filter":"any","read_only":true,"description":"triage"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create status = %d, want 201; body=%s", rec.Code, rec.Body.String())
	}
	var role domain.Role
	if err := json.Unmarshal(rec.Body.Bytes(), &role); err != nil {
		t.Fatalf("decode role: %v", err)
	}
	if role.Name != "bug-triage" || role.TaskFilter != "any" || !role.ReadOnly {
		t.Fatalf("unexpected role: %+v", role)
	}

	// Second create of the same role is an idempotent 200, not a 409.
	rec2 := postRole(t, mux, `{"name":"bug-triage","task_filter":"any","read_only":true,"description":"triage"}`)
	if rec2.Code != http.StatusOK {
		t.Fatalf("idempotent create status = %d, want 200; body=%s", rec2.Code, rec2.Body.String())
	}
}

func TestRoleRoutesUseCanonicalWorkspaceAndFailClosedWithoutResolution(t *testing.T) {
	body := `{"name":"docs","task_filter":"any"}`

	t.Run("alias resolves to canonical workspace", func(t *testing.T) {
		st := memstore.New()
		resolver := &capturingRoleAuthorityResolver{}
		mux := http.NewServeMux()
		NewModule(Config{
			WorkspacePath: testRoleWorkspacePath(st), Roles: &testRoleAPI{store: st}, Authority: resolver,
		}).Register(mux)
		req := httptest.NewRequest(
			http.MethodPost,
			"/api/workspaces/ALIAS/roles",
			strings.NewReader(body),
		)
		req = withCanonicalWorkspace(req, "ALIAS", "CANONICAL")
		rec := httptest.NewRecorder()

		mux.ServeHTTP(rec, req)

		if rec.Code != http.StatusCreated {
			t.Fatalf("status/body = %d/%s", rec.Code, rec.Body.String())
		}
		if resolver.workspace != "CANONICAL" || resolver.action != agents.ActionCreateRole {
			t.Fatalf("authority scope = %q/%q", resolver.workspace, resolver.action)
		}
		if _, err := st.Roles().Get(t.Context(), "CANONICAL", "docs"); err != nil {
			t.Fatalf("canonical role lookup: %v", err)
		}
		if _, err := st.Roles().Get(t.Context(), "ALIAS", "docs"); !errors.Is(err, domain.ErrNotFound) {
			t.Fatalf("alias role lookup err = %v, want not found", err)
		}
	})

	t.Run("missing canonical workspace fails closed", func(t *testing.T) {
		st := memstore.New()
		resolver := &capturingRoleAuthorityResolver{}
		mux := http.NewServeMux()
		NewModule(Config{
			WorkspacePath: testRoleWorkspacePath(st), Roles: &testRoleAPI{store: st}, Authority: resolver,
		}).Register(mux)
		req := httptest.NewRequest(
			http.MethodPost,
			"/api/workspaces/WS/roles",
			strings.NewReader(body),
		)
		rec := httptest.NewRecorder()

		mux.ServeHTTP(rec, req)

		if rec.Code != http.StatusBadRequest {
			t.Fatalf("status/body = %d/%s", rec.Code, rec.Body.String())
		}
		if resolver.workspace != "" {
			t.Fatalf("authority invoked with workspace %q", resolver.workspace)
		}
	})
}

func TestCreateRole_ExistingPromptAndPolicyMustMatch(t *testing.T) {
	st := memstore.New()
	if _, err := st.Roles().Create(context.Background(), store.RoleCreate{
		WorkspaceKey: "WS",
		Name:         "bug-triage",
		Description:  "triage",
		Prompt:       "existing prompt",
		TaskFilter:   "any",
		ReadOnly:     false,
	}); err != nil {
		t.Fatalf("seed role: %v", err)
	}
	mux := http.NewServeMux()
	newTestRoleModule(st).Register(mux)

	rec := postRole(t, mux, `{
		"name":"bug-triage",
		"description":"triage",
		"prompt":"different prompt",
		"task_filter":"any",
		"read_only":true
	}`)
	if rec.Code != http.StatusConflict {
		t.Fatalf("collision status = %d, want 409; body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "prompt") || !strings.Contains(rec.Body.String(), "read_only") {
		t.Fatalf("collision body does not identify safe fields: %s", rec.Body.String())
	}
	role, err := st.Roles().Get(context.Background(), "WS", "bug-triage")
	if err != nil {
		t.Fatalf("get role: %v", err)
	}
	if role.Prompt != "existing prompt" || role.ReadOnly {
		t.Fatalf("colliding ensure mutated role: %+v", role)
	}
}

func TestCreateRole_ExactInlinePromptIsIdempotent(t *testing.T) {
	st := memstore.New()
	if _, err := st.Roles().Create(context.Background(), store.RoleCreate{
		WorkspaceKey: "WS",
		Name:         "bug-triage",
		Description:  "triage",
		Prompt:       "same prompt",
		TaskFilter:   "any",
		ReadOnly:     true,
	}); err != nil {
		t.Fatalf("seed role: %v", err)
	}
	mux := http.NewServeMux()
	newTestRoleModule(st).Register(mux)

	rec := postRole(t, mux, `{
		"name":"bug-triage",
		"description":"triage",
		"prompt":"same prompt",
		"prompt_filename":"bug-triage.md",
		"task_filter":"any",
		"read_only":true
	}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("exact ensure status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
}

func TestEnsureRole_RecreateAfterDeleteUsesNewImmutablePrompt(t *testing.T) {
	st := newPromptRoleStore(t)
	ctx := context.Background()
	first, created, err := ensureRoleForTest(ctx, st, "WS", EnsureRoleRequest{
		Name: "docs", Prompt: "first prompt", PromptFilename: "docs.md", TaskFilter: "has_design",
	})
	if err != nil || !created {
		t.Fatalf("first EnsureRole = %+v created=%v err=%v", first, created, err)
	}
	if err := st.Roles().Delete(ctx, "WS", "docs"); err != nil {
		t.Fatalf("delete first role: %v", err)
	}
	second, recreated, err := ensureRoleForTest(ctx, st, "WS", EnsureRoleRequest{
		Name: "docs", Prompt: "second prompt", PromptFilename: "docs.md", TaskFilter: "has_design",
	})
	if err != nil || !recreated {
		t.Fatalf("recreated EnsureRole = %+v created=%v err=%v", second, recreated, err)
	}
	if second.PromptFile == first.PromptFile {
		t.Fatalf("recreated role reused prior immutable prompt path %q", second.PromptFile)
	}
	if got := ReadPromptBody(second); got != "second prompt" {
		t.Fatalf("recreated role prompt = %q, want second prompt", got)
	}
}

func TestEnsureRole_ExplicitPromptFilenameIsRoleNamespaced(t *testing.T) {
	st := newPromptRoleStore(t)
	ctx := context.Background()
	first, firstCreated, err := ensureRoleForTest(ctx, st, "WS", EnsureRoleRequest{
		Name: "reviewer", Prompt: "shared prompt", PromptFilename: "shared.md", TaskFilter: "has_design",
	})
	if err != nil || !firstCreated {
		t.Fatalf("first EnsureRole = %+v created=%v err=%v", first, firstCreated, err)
	}
	second, secondCreated, err := ensureRoleForTest(ctx, st, "WS", EnsureRoleRequest{
		Name: "auditor", Prompt: "shared prompt", PromptFilename: "shared.md", TaskFilter: "has_design",
	})
	if err != nil || !secondCreated {
		t.Fatalf("second EnsureRole = %+v created=%v err=%v", second, secondCreated, err)
	}
	if first.PromptFile == second.PromptFile {
		t.Fatalf("explicit prompt filename shared across roles: %q", first.PromptFile)
	}
	if got := ReadPromptBody(first); got != "shared prompt" {
		t.Fatalf("first prompt = %q, want shared prompt", got)
	}
	if got := ReadPromptBody(second); got != "shared prompt" {
		t.Fatalf("second prompt = %q, want shared prompt", got)
	}
}

func TestEnsureRoleCompensationRetainsRetryableRoleAndPrompt(t *testing.T) {
	st := newPromptRoleStore(t)
	ctx := context.Background()
	created, err := ensureRoleWithReceiptForTest(ctx, st, "WS", EnsureRoleRequest{
		Name: "reviewer", Prompt: "owned prompt", PromptFilename: "reviewer.md", TaskFilter: "has_design",
	})
	if err != nil || created == nil || !created.Created {
		t.Fatalf("EnsureRoleWithReceipt = %+v err=%v, want created receipt", created, err)
	}
	promptPath := created.Role.PromptFile
	if err := created.Compensate(ctx, testRoleWorkspacePath(st), "WS"); err != nil {
		t.Fatalf("Compensate created role: %v", err)
	}
	if _, err := st.Roles().Get(ctx, "WS", "reviewer"); err != nil {
		t.Fatalf("retryable role was removed: %v", err)
	}
	if _, err := os.Stat(promptPath); err != nil {
		t.Fatalf("retryable prompt was removed: %v", err)
	}

	reused, err := ensureRoleWithReceiptForTest(ctx, st, "WS", EnsureRoleRequest{
		Name: "reviewer", Prompt: "pre-existing prompt", PromptFilename: "reviewer.md", TaskFilter: "has_design",
	})
	if err == nil || reused != nil {
		t.Fatalf("conflicting retry = %+v err=%v, want conflict", reused, err)
	}
	role, err := st.Roles().Get(ctx, "WS", "reviewer")
	if err != nil {
		t.Fatalf("retained role disappeared after conflict: %v", err)
	}
	if got := ReadPromptBody(role); got != "owned prompt" {
		t.Fatalf("retained prompt changed = %q", got)
	}
}

func TestEnsureRoleCompensationNeverDeletesEditedGeneration(t *testing.T) {
	st := newPromptRoleStore(t)
	ctx := context.Background()
	receipt, err := ensureRoleWithReceiptForTest(ctx, st, "WS", EnsureRoleRequest{
		Name: "reviewer", Prompt: "owned prompt", TaskFilter: "has_design",
	})
	if err != nil {
		t.Fatalf("EnsureRoleWithReceipt: %v", err)
	}
	description := "operator edit"
	if _, err := st.Roles().Update(ctx, "WS", "reviewer", store.RoleUpdate{Description: &description}); err != nil {
		t.Fatalf("edit role: %v", err)
	}
	if err := receipt.Compensate(ctx, testRoleWorkspacePath(st), "WS"); err != nil {
		t.Fatalf("Compensate edited role: %v", err)
	}
	if _, err := st.Roles().Get(ctx, "WS", "reviewer"); err != nil {
		t.Fatalf("edited role was deleted: %v", err)
	}
	if _, err := os.Stat(receipt.Role.PromptFile); err != nil {
		t.Fatalf("edited role prompt was deleted: %v", err)
	}
}

func TestEnsureRole_ConcurrentDifferentPromptsCannotOverwriteWinner(t *testing.T) {
	st := newPromptRoleStore(t)
	ctx := context.Background()
	start := make(chan struct{})
	results := make(chan error, 2)
	var wg sync.WaitGroup
	for _, prompt := range []string{"read-only prompt", "mutating prompt"} {
		prompt := prompt
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			_, _, err := ensureRoleForTest(ctx, st, "WS", EnsureRoleRequest{
				Name: "reviewer", Prompt: prompt, PromptFilename: "reviewer.md",
				TaskFilter: "has_design", ReadOnly: prompt == "read-only prompt",
			})
			results <- err
		}()
	}
	close(start)
	wg.Wait()
	close(results)

	successes, conflicts := 0, 0
	for err := range results {
		switch {
		case err == nil:
			successes++
		case errors.Is(err, domain.ErrConflict):
			conflicts++
		default:
			t.Fatalf("concurrent EnsureRole error = %v", err)
		}
	}
	if successes != 1 || conflicts != 1 {
		t.Fatalf("concurrent results: successes=%d conflicts=%d, want 1/1", successes, conflicts)
	}
	winner, err := st.Roles().Get(ctx, "WS", "reviewer")
	if err != nil {
		t.Fatalf("get winning role: %v", err)
	}
	prompt := ReadPromptBody(winner)
	if prompt != "read-only prompt" && prompt != "mutating prompt" {
		t.Fatalf("winning prompt was overwritten or corrupted: %q", prompt)
	}
	if winner.ReadOnly != (prompt == "read-only prompt") {
		t.Fatalf("winning role policy and prompt diverged: %+v prompt=%q", winner, prompt)
	}
}

func newPromptRoleStore(t *testing.T) *memstore.Store {
	t.Helper()
	t.Setenv("LOOM_CONFIG_DIR", t.TempDir())
	workspacePath := t.TempDir()
	if err := bootstrap.MutateWorkspaceLocalState("WS", func(local *bootstrap.WorkspaceLocalState) error {
		local.Path = workspacePath
		return nil
	}); err != nil {
		t.Fatalf("seed workspace local path: %v", err)
	}
	st := memstore.New()
	if _, err := st.Workspaces().Create(context.Background(), store.WorkspaceCreate{
		Key: "WS", Name: "Test Workspace",
	}); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	return st
}

func TestCreateRole_RequiresName(t *testing.T) {
	rec := postRole(t, newRolesMux(), `{"name":"   "}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", rec.Code, rec.Body.String())
	}
}

func TestValidatePromptAgentRole(t *testing.T) {
	tests := []struct {
		name    string
		role    *domain.Role
		wantErr string
	}{
		{name: "missing prompt", role: &domain.Role{Name: "empty", TaskFilter: "has_design"}, wantErr: "non-empty prompt"},
		{name: "unsupported filter", role: &domain.Role{Name: "docs", Prompt: "work", TaskFilter: "docs"}, wantErr: "unsupported"},
		{name: "inline legacy any", role: &domain.Role{Name: "legacy", Prompt: "work", TaskFilter: "any"}},
		{name: "inline planner", role: &domain.Role{Name: "planner", Prompt: "work", TaskFilter: "needs_plan"}},
		{name: "review event role", role: &domain.Role{Name: "documentation", Prompt: "update docs", TaskFilter: "review"}},
		{name: "read-only review role", role: &domain.Role{Name: "documentation-audit", Prompt: "audit docs", TaskFilter: "review", ReadOnly: true}, wantErr: "read_only=false"},
		{name: "read-only bug triage", role: &domain.Role{Name: "triage", Prompt: "work", TaskFilter: "bug", ReadOnly: true}},
		{name: "mutating bug filter", role: &domain.Role{Name: "unsafe-triage", Prompt: "work", TaskFilter: "bug"}, wantErr: "read_only=true"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidatePromptAgentRole(tt.role)
			if tt.wantErr == "" && err != nil {
				t.Fatalf("ValidatePromptAgentRole: %v", err)
			}
			if tt.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tt.wantErr)) {
				t.Fatalf("ValidatePromptAgentRole error = %v, want substring %q", err, tt.wantErr)
			}
		})
	}
}

// doRole issues an arbitrary method/path against the roles mux.
func doRole(t *testing.T, mux *http.ServeMux, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req = withCanonicalWorkspace(req, "WS", "WS")
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}

func withCanonicalWorkspace(request *http.Request, requested, canonical string) *http.Request {
	ref := middleware.WorkspaceRef{RequestedID: requested, CanonicalID: canonical}
	return request.WithContext(middleware.WithWorkspaceRef(request.Context(), ref))
}

// The Phase-B single-role endpoints below deliberately avoid a prompt body, so
// they never touch the filesystem (writeRolePrompt needs a machine-local
// workspace path) and exercise only the store-interaction logic. The
// prompt-file round-trip is covered by the live local-mode verification.

func TestGetRole_ReturnsRoleWithEmptyPrompt(t *testing.T) {
	mux := newRolesMux()
	if rec := postRole(t, mux, `{"name":"bug-triage","task_filter":"any"}`); rec.Code != http.StatusCreated {
		t.Fatalf("seed create status = %d; body=%s", rec.Code, rec.Body.String())
	}

	rec := doRole(t, mux, http.MethodGet, "/api/workspaces/WS/roles/bug-triage", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("get status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var got roleWithPrompt
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode roleWithPrompt: %v", err)
	}
	if got.Role == nil || got.Role.Name != "bug-triage" || got.Prompt != "" {
		t.Fatalf("unexpected get response: %+v (prompt=%q)", got.Role, got.Prompt)
	}
}

func TestGetRole_MissingIs404(t *testing.T) {
	rec := doRole(t, newRolesMux(), http.MethodGet, "/api/workspaces/WS/roles/nope", "")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("get missing status = %d, want 404; body=%s", rec.Code, rec.Body.String())
	}
}

func TestUpdateRole_PatchesFields(t *testing.T) {
	mux := newRolesMux()
	if rec := postRole(t, mux, `{"name":"bug-triage","description":"old","read_only":true}`); rec.Code != http.StatusCreated {
		t.Fatalf("seed create status = %d; body=%s", rec.Code, rec.Body.String())
	}

	rec := doRole(t, mux, http.MethodPatch, "/api/workspaces/WS/roles/bug-triage",
		`{"description":"new","read_only":false}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("patch status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var got roleWithPrompt
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Role.Description != "new" || got.Role.ReadOnly {
		t.Fatalf("patch did not apply: %+v", got.Role)
	}
}

func TestUpdateRole_PublishesImmutablePromptAndLeavesSharedFileUntouched(t *testing.T) {
	st := newPromptRoleStore(t)
	ctx := context.Background()
	first, created, err := ensureRoleForTest(ctx, st, "WS", EnsureRoleRequest{
		Name: "reviewer", Prompt: "shared prompt", PromptFilename: "shared.md", TaskFilter: "has_design",
	})
	if err != nil || !created {
		t.Fatalf("seed first role = %+v created=%v err=%v", first, created, err)
	}
	if _, err := st.Roles().Create(ctx, store.RoleCreate{
		WorkspaceKey: "WS",
		Name:         "auditor",
		PromptFile:   first.PromptFile,
		TaskFilter:   "has_design",
	}); err != nil {
		t.Fatalf("seed second role sharing legacy prompt path: %v", err)
	}
	mux := http.NewServeMux()
	newTestRoleModule(st).Register(mux)

	rec := doRole(t, mux, http.MethodPatch, "/api/workspaces/WS/roles/reviewer", `{"prompt":"updated prompt"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("patch status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}

	reviewer, err := st.Roles().Get(ctx, "WS", "reviewer")
	if err != nil {
		t.Fatalf("get updated reviewer: %v", err)
	}
	auditor, err := st.Roles().Get(ctx, "WS", "auditor")
	if err != nil {
		t.Fatalf("get untouched auditor: %v", err)
	}
	if reviewer.PromptFile == first.PromptFile {
		t.Fatalf("prompt edit reused mutable path %q", reviewer.PromptFile)
	}
	if auditor.PromptFile != first.PromptFile {
		t.Fatalf("unrelated role was repointed from %q to %q", first.PromptFile, auditor.PromptFile)
	}
	if got := ReadPromptBody(reviewer); got != "updated prompt" {
		t.Fatalf("updated reviewer prompt = %q, want updated prompt", got)
	}
	if got := ReadPromptBody(auditor); got != "shared prompt" {
		t.Fatalf("shared legacy prompt was overwritten: %q", got)
	}
}

func TestUpdateRole_MissingIs404(t *testing.T) {
	rec := doRole(t, newRolesMux(), http.MethodPatch, "/api/workspaces/WS/roles/nope", `{"description":"x"}`)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("patch missing status = %d, want 404; body=%s", rec.Code, rec.Body.String())
	}
}

func TestCloneRole_DuplicatesConfig(t *testing.T) {
	mux := newRolesMux()
	if rec := postRole(t, mux, `{"name":"bug-triage","task_filter":"any","read_only":true}`); rec.Code != http.StatusCreated {
		t.Fatalf("seed create status = %d; body=%s", rec.Code, rec.Body.String())
	}

	rec := doRole(t, mux, http.MethodPost, "/api/workspaces/WS/roles/bug-triage/clone",
		`{"target_name":"bug-triage-2"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("clone status = %d, want 201; body=%s", rec.Code, rec.Body.String())
	}
	var clone domain.Role
	if err := json.Unmarshal(rec.Body.Bytes(), &clone); err != nil {
		t.Fatalf("decode clone: %v", err)
	}
	if clone.Name != "bug-triage-2" || clone.TaskFilter != "any" || !clone.ReadOnly {
		t.Fatalf("clone did not copy config: %+v", clone)
	}

	// Cloning onto an existing name is a real conflict (409), not a silent ensure.
	if rec := doRole(t, mux, http.MethodPost, "/api/workspaces/WS/roles/bug-triage/clone",
		`{"target_name":"bug-triage-2"}`); rec.Code != http.StatusConflict {
		t.Fatalf("clone-collision status = %d, want 409; body=%s", rec.Code, rec.Body.String())
	}
	// Same-name and missing-source are 400 / 404 respectively.
	if rec := doRole(t, mux, http.MethodPost, "/api/workspaces/WS/roles/bug-triage/clone",
		`{"target_name":"bug-triage"}`); rec.Code != http.StatusBadRequest {
		t.Fatalf("clone-to-self status = %d, want 400; body=%s", rec.Code, rec.Body.String())
	}
	if rec := doRole(t, mux, http.MethodPost, "/api/workspaces/WS/roles/nope/clone",
		`{"target_name":"x"}`); rec.Code != http.StatusNotFound {
		t.Fatalf("clone-missing-source status = %d, want 404; body=%s", rec.Code, rec.Body.String())
	}
}

func TestDeleteRole_RemovesAndGuardsBuiltin(t *testing.T) {
	mux := newRolesMux()
	if rec := postRole(t, mux, `{"name":"bug-triage"}`); rec.Code != http.StatusCreated {
		t.Fatalf("seed create status = %d; body=%s", rec.Code, rec.Body.String())
	}

	if rec := doRole(t, mux, http.MethodDelete, "/api/workspaces/WS/roles/bug-triage", ""); rec.Code != http.StatusNoContent {
		t.Fatalf("delete status = %d, want 204; body=%s", rec.Code, rec.Body.String())
	}
	if rec := doRole(t, mux, http.MethodGet, "/api/workspaces/WS/roles/bug-triage", ""); rec.Code != http.StatusNotFound {
		t.Fatalf("get after delete status = %d, want 404", rec.Code)
	}
	// Built-in roles are refused.
	if rec := doRole(t, mux, http.MethodDelete, "/api/workspaces/WS/roles/plan", ""); rec.Code != http.StatusBadRequest {
		t.Fatalf("delete builtin status = %d, want 400; body=%s", rec.Code, rec.Body.String())
	}
	// Deleting a missing role is a 404.
	if rec := doRole(t, mux, http.MethodDelete, "/api/workspaces/WS/roles/nope", ""); rec.Code != http.StatusNotFound {
		t.Fatalf("delete missing status = %d, want 404; body=%s", rec.Code, rec.Body.String())
	}
}
