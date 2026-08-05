// Package roles exposes a webui HTTP surface for creating custom agent Roles
// (and seeding their prompt files on disk). Until now role creation was
// CLI-only (`loom role add` + a hand-placed prompt file); this module is the
// single backend keystone that lets the web UI self-serve custom supervised
// agent templates (e.g. bug triage).
package roles

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/modules/agents"
	workflowcataloghttp "github.com/tysonthomas9/loomcli/internal/modules/workflowcatalog/httpapi"
	"github.com/tysonthomas9/loomcli/internal/platform/authority"
	"github.com/tysonthomas9/loomcli/internal/roleprompts"
	"github.com/tysonthomas9/loomcli/internal/store"
	"github.com/tysonthomas9/loomcli/internal/webui/server/handler"
	"github.com/tysonthomas9/loomcli/internal/webui/server/middleware"
	"github.com/tysonthomas9/loomcli/internal/webui/storeadapter"
)

const maxRoleBodyBytes = 1 << 20

// Module registers role read/create routes. It writes prompt files to the
// machine-local workspace directory, so it holds the store directly (the same
// shape as the workflows/webhooks modules).
type Module struct {
	store     store.Store
	roles     RoleAPI
	authority workflowcataloghttp.OperatorAuthorityResolver
}

type RoleAPI interface {
	agents.RoleQueries
	agents.RoleCommands
}

type Config struct {
	Store     store.Store
	Roles     RoleAPI
	Authority workflowcataloghttp.OperatorAuthorityResolver
}

func NewModule(config Config) *Module {
	return &Module{store: config.Store, roles: config.Roles, authority: config.Authority}
}

func (m *Module) Register(mux *http.ServeMux) {
	if m.store == nil || m.roles == nil {
		return
	}
	mux.HandleFunc("GET /api/workspaces/{ws}/roles", m.listRoles)
	mux.HandleFunc("POST /api/workspaces/{ws}/roles", m.createRole)
	// Phase B: read/edit/clone/delete a single role so the UI can manage an
	// agent's prompt + config in place (store Update/Delete already exist).
	mux.HandleFunc("GET /api/workspaces/{ws}/roles/{name}", m.getRole)
	mux.HandleFunc("PATCH /api/workspaces/{ws}/roles/{name}", m.updateRole)
	mux.HandleFunc("DELETE /api/workspaces/{ws}/roles/{name}", m.deleteRole)
	mux.HandleFunc("POST /api/workspaces/{ws}/roles/{name}/clone", m.cloneRole)
}

type createRoleRequest struct {
	Name                string   `json:"name"`
	Kind                string   `json:"kind,omitempty"`
	Description         string   `json:"description,omitempty"`
	Prompt              string   `json:"prompt,omitempty"`          // prompt body written to disk
	PromptFile          string   `json:"prompt_file,omitempty"`     // existing workspace-relative prompt reference
	PromptFilename      string   `json:"prompt_filename,omitempty"` // defaults to <name>.md
	Model               string   `json:"model,omitempty"`
	TaskFilter          string   `json:"task_filter,omitempty"`
	Backend             string   `json:"backend,omitempty"`
	Effort              string   `json:"effort,omitempty"`
	PathPatterns        []string `json:"path_patterns,omitempty"`
	Skills              []string `json:"skills,omitempty"`
	MaxPriority         *int     `json:"max_priority,omitempty"`
	MaxConcurrency      *int     `json:"max_concurrency,omitempty"`
	ReadOnly            bool     `json:"read_only,omitempty"`
	AllowedTools        []string `json:"allowed_tools,omitempty"`
	DeniedTools         []string `json:"denied_tools,omitempty"`
	MaxBudgetUSD        *float64 `json:"max_budget_usd,omitempty"`
	PersistInlinePrompt bool     `json:"persist_inline_prompt,omitempty"`
}

type EnsureRoleRequest struct {
	Name                string
	Kind                string
	Description         string
	Prompt              string
	PromptFile          string
	PromptFilename      string
	Model               string
	TaskFilter          string
	Backend             string
	Effort              string
	PathPatterns        []string
	MaxPriority         *int
	MaxConcurrency      *int
	ReadOnly            bool
	AllowedTools        []string
	DeniedTools         []string
	Skills              []string
	MaxBudgetUSD        *float64
	PersistInlinePrompt bool
}

// EnsureRoleResult reports whether an ensure call created the durable role.
type EnsureRoleResult struct {
	Role    *domain.Role
	Created bool
}

// roleWithPrompt is the GET/PATCH single-role response: the stored role plus its
// current prompt body (read back from PromptFile; empty for builtin roles that
// carry no prompt file).
type roleWithPrompt struct {
	Role   *domain.Role `json:"role"`
	Prompt string       `json:"prompt"`
}

// updateRoleRequest is a partial update: only non-nil fields are applied, so the
// UI can PATCH just the prompt without resending the whole role.
type updateRoleRequest struct {
	Description         *string   `json:"description,omitempty"`
	Kind                *string   `json:"kind,omitempty"`
	Prompt              *string   `json:"prompt,omitempty"` // new prompt body (publishes a new immutable file)
	PromptFile          *string   `json:"prompt_file,omitempty"`
	PromptFilename      *string   `json:"prompt_filename,omitempty"` // optional new filename
	Model               *string   `json:"model,omitempty"`
	TaskFilter          *string   `json:"task_filter,omitempty"`
	Backend             *string   `json:"backend,omitempty"`
	Effort              *string   `json:"effort,omitempty"`
	PathPatterns        *[]string `json:"path_patterns,omitempty"`
	MaxPriority         *int      `json:"max_priority,omitempty"`
	ClearPriority       bool      `json:"clear_max_priority,omitempty"`
	MaxConcurrency      *int      `json:"max_concurrency,omitempty"`
	ClearConcurrent     bool      `json:"clear_max_concurrency,omitempty"`
	ReadOnly            *bool     `json:"read_only,omitempty"`
	AllowedTools        *[]string `json:"allowed_tools,omitempty"`
	DeniedTools         *[]string `json:"denied_tools,omitempty"`
	Skills              *[]string `json:"skills,omitempty"`
	MaxBudgetUSD        *float64  `json:"max_budget_usd,omitempty"`
	ClearBudget         bool      `json:"clear_max_budget_usd,omitempty"`
	PersistInlinePrompt bool      `json:"persist_inline_prompt,omitempty"`
}

// cloneRoleRequest duplicates an existing role (and its prompt) under a new name.
type cloneRoleRequest struct {
	TargetName  string `json:"target_name"`
	Description string `json:"description,omitempty"`
}

func (m *Module) listRoles(w http.ResponseWriter, r *http.Request) {
	ws, ok := canonicalWorkspace(w, r)
	if !ok {
		return
	}
	values, err := m.roles.ListRoles(r.Context(), ws)
	if err != nil {
		writeRoleError(w, err, "list roles failed")
		return
	}
	roles := make([]*domain.Role, 0, len(values))
	for _, role := range values {
		roles = append(roles, domainRole(role))
	}
	handler.WriteJSON(w, http.StatusOK, roles)
}

func EnsureRole(
	ctx context.Context,
	st store.Store,
	api RoleAPI,
	auth authority.OperatorAuthority,
	ws string,
	req EnsureRoleRequest,
) (*domain.Role, bool, error) {
	result, err := EnsureRoleWithReceipt(ctx, st, api, auth, ws, req)
	if err != nil {
		return nil, false, err
	}
	return result.Role, result.Created, nil
}

// EnsureRoleWithReceipt is EnsureRole with a compensating ownership receipt.
func EnsureRoleWithReceipt(
	ctx context.Context,
	st store.Store,
	api RoleAPI,
	auth authority.OperatorAuthority,
	ws string,
	req EnsureRoleRequest,
) (*EnsureRoleResult, error) {
	if st == nil || api == nil {
		return nil, fmt.Errorf("store and Agents role API are required: %w", domain.ErrInvalid)
	}
	ws = strings.TrimSpace(ws)
	name := strings.TrimSpace(req.Name)
	if ws == "" || name == "" {
		return nil, fmt.Errorf("workspace and role name are required: %w", domain.ErrInvalid)
	}

	existing, found, err := findEnsuredRole(ctx, api, ws, name, req)
	if err != nil {
		return nil, err
	}
	if found {
		return &EnsureRoleResult{Role: existing}, nil
	}

	in, _, err := buildEnsuredRoleCreate(ctx, st, ws, name, req)
	if err != nil {
		return nil, err
	}
	created, err := api.CreateRole(ctx, auth, agents.CreateRoleCommand{
		WorkspaceKey: ws,
		Role:         roleDefinitionFromCreate(in),
	})
	if err == nil {
		return &EnsureRoleResult{Role: domainRole(created), Created: true}, nil
	}
	if !errors.Is(err, agents.ErrAlreadyExists) && !errors.Is(err, domain.ErrAlreadyExists) {
		// The immutable prompt is deliberately retained. Another concurrent
		// ensure may already have adopted it, and there is no transaction that
		// spans the role store and filesystem. Retention is retry-safe.
		return nil, err
	}

	existingRole, getErr := api.GetRole(ctx, ws, name)
	existing = domainRole(existingRole)
	if getErr == nil && existing != nil {
		if matchErr := validateEnsureRoleMatch(existing, req); matchErr != nil {
			return nil, matchErr
		}
		return &EnsureRoleResult{Role: existing}, nil
	}
	// The create outcome is ambiguous when the winner cannot be read. Keep the
	// prompt rather than risk deleting a file the committed role references.
	return nil, err
}

func buildEnsuredRoleCreate(
	ctx context.Context,
	st store.Store,
	ws string,
	name string,
	req EnsureRoleRequest,
) (store.RoleCreate, roleprompts.PromptFileReceipt, error) {
	in := store.RoleCreate{
		WorkspaceKey:   ws,
		Name:           name,
		Kind:           strings.TrimSpace(req.Kind),
		Description:    strings.TrimSpace(req.Description),
		PromptFile:     strings.TrimSpace(req.PromptFile),
		Model:          strings.TrimSpace(req.Model),
		TaskFilter:     strings.TrimSpace(req.TaskFilter),
		Backend:        strings.TrimSpace(req.Backend),
		Effort:         strings.TrimSpace(req.Effort),
		PathPatterns:   slices.Clone(req.PathPatterns),
		MaxPriority:    cloneInt(req.MaxPriority),
		MaxConcurrency: cloneInt(req.MaxConcurrency),
		ReadOnly:       req.ReadOnly,
		AllowedTools:   slices.Clone(req.AllowedTools),
		DeniedTools:    slices.Clone(req.DeniedTools),
		Skills:         slices.Clone(req.Skills),
		MaxBudgetUSD:   cloneFloat64(req.MaxBudgetUSD),
	}
	if req.PersistInlinePrompt {
		in.Prompt = req.Prompt
	}
	var receipt roleprompts.PromptFileReceipt
	if !req.PersistInlinePrompt && strings.TrimSpace(req.Prompt) != "" {
		var err error
		receipt, err = ensureRolePromptWithReceipt(ctx, st, ws, name, req.PromptFilename, req.Prompt)
		if err != nil {
			return store.RoleCreate{}, roleprompts.PromptFileReceipt{}, err
		}
		in.PromptFile = receipt.Path
	}
	return in, receipt, nil
}

// Compensate intentionally retains a committed role and immutable prompt.
// RoleStore exposes only name-based Delete, so check-then-delete could remove a
// concurrently edited, recreated, or newly adopted role. An exact retry safely
// reuses the retained definition.
func (*EnsureRoleResult) Compensate(context.Context, store.Store, string) error {
	return nil
}

func deleteRoleRecord(
	ctx context.Context,
	api RoleAPI,
	auth authority.OperatorAuthority,
	ws, name string,
	expectedUpdatedAt time.Time,
) error {
	return api.DeleteRole(ctx, auth, agents.DeleteRoleCommand{
		WorkspaceKey: ws, RoleName: name, ExpectedUpdatedAt: expectedUpdatedAt,
	})
}

func findEnsuredRole(
	ctx context.Context,
	api RoleAPI,
	ws string,
	name string,
	req EnsureRoleRequest,
) (*domain.Role, bool, error) {
	value, err := api.GetRole(ctx, ws, name)
	if err != nil {
		if errors.Is(err, agents.ErrNotFound) || errors.Is(err, domain.ErrNotFound) {
			return nil, false, nil
		}
		return nil, false, err
	}
	existing := domainRole(value)
	if existing == nil {
		return nil, false, nil
	}
	if err := validateEnsureRoleMatch(existing, req); err != nil {
		return nil, false, err
	}
	return existing, true, nil
}

// validateEnsureRoleMatch makes "ensure" exact rather than name-only. A role
// name is shared authority: silently reusing a role whose prompt or policy
// differs from the requested definition could turn a read-only UI template
// into a mutating agent. The error names only mismatched fields; prompt bodies
// are deliberately never copied into logs or API errors.
func validateEnsureRoleMatch(existing *domain.Role, req EnsureRoleRequest) error {
	if existing == nil {
		return fmt.Errorf("existing role is required: %w", domain.ErrInvalid)
	}

	var mismatches []string
	addMismatch := func(field string, matches bool) {
		if !matches {
			mismatches = append(mismatches, field)
		}
	}
	addMismatch("description", existing.Description == strings.TrimSpace(req.Description))
	addMismatch("kind", string(existing.Kind) == strings.TrimSpace(req.Kind))
	addMismatch(
		"prompt_file",
		existing.PromptFile == strings.TrimSpace(req.PromptFile) ||
			(!req.PersistInlinePrompt && strings.TrimSpace(req.Prompt) != ""),
	)
	addMismatch("model", existing.Model == strings.TrimSpace(req.Model))
	addMismatch("task_filter", existing.TaskFilter == strings.TrimSpace(req.TaskFilter))
	addMismatch("backend", existing.Backend == strings.TrimSpace(req.Backend))
	addMismatch("effort", existing.Effort == strings.TrimSpace(req.Effort))
	addMismatch("path_patterns", slices.Equal(existing.PathPatterns, req.PathPatterns))
	addMismatch("max_priority", equalInt(existing.MaxPriority, req.MaxPriority))
	addMismatch("max_concurrency", equalInt(existing.MaxConcurrency, req.MaxConcurrency))
	addMismatch("read_only", existing.ReadOnly == req.ReadOnly)
	addMismatch("allowed_tools", slices.Equal(existing.AllowedTools, req.AllowedTools))
	addMismatch("denied_tools", slices.Equal(existing.DeniedTools, req.DeniedTools))
	addMismatch("skills", slices.Equal(existing.Skills, req.Skills))
	addMismatch("max_budget_usd", equalFloat64(existing.MaxBudgetUSD, req.MaxBudgetUSD))

	requestedPrompt := normalizePromptBody(req.Prompt)
	existingPrompt := normalizePromptBody(ReadPromptBody(existing))
	addMismatch("prompt", existingPrompt == requestedPrompt)

	if len(mismatches) > 0 {
		return fmt.Errorf(
			"role %q already exists with incompatible configuration (%s): %w",
			existing.Name, strings.Join(mismatches, ", "), domain.ErrConflict,
		)
	}
	return nil
}

func normalizePromptBody(prompt string) string {
	if strings.TrimSpace(prompt) == "" {
		return ""
	}
	return prompt
}

// ValidatePromptAgentRole rejects role definitions that the prompt-agent
// workflow cannot execute. Keep this set aligned with
// builtin/prompt-agent.ts resolveRolePhase: legacy empty/"any" roles map to
// has_design, plan/coder roles use the two explicit phase filters, Review
// event roles use the target-only review filter, and the issue-type "bug"
// filter is restricted to read-only roles.
func ValidatePromptAgentRole(role *domain.Role) error {
	if role == nil {
		return fmt.Errorf("prompt-agent role is required: %w", domain.ErrInvalid)
	}
	if strings.TrimSpace(ReadPromptBody(role)) == "" {
		return fmt.Errorf("role %q requires a non-empty prompt or readable prompt_file: %w", role.Name, domain.ErrInvalid)
	}
	switch taskFilter := strings.TrimSpace(role.TaskFilter); taskFilter {
	case "", "any", "needs_plan", "has_design":
		return nil
	case "review":
		if !role.ReadOnly {
			return nil
		}
		return fmt.Errorf(
			"role %q task_filter %q requires read_only=false in prompt-agent because Review delivery must publish a local branch: %w",
			role.Name, taskFilter, domain.ErrInvalid,
		)
	case "bug":
		if role.ReadOnly {
			return nil
		}
		return fmt.Errorf(
			"role %q task_filter %q requires read_only=true in prompt-agent: %w",
			role.Name, taskFilter, domain.ErrInvalid,
		)
	default:
		return fmt.Errorf(
			"role %q task_filter %q is unsupported by prompt-agent; expected any, needs_plan, has_design, review, or bug: %w",
			role.Name, taskFilter, domain.ErrInvalid,
		)
	}
}

// createRole is an exact idempotent "ensure": an identical named role is
// returned (200), a new role is created (201), and an incompatible collision
// is rejected (409).
func (m *Module) createRole(w http.ResponseWriter, r *http.Request) {
	ws, ok := canonicalWorkspace(w, r)
	if !ok {
		return
	}

	var req createRoleRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxRoleBodyBytes)).Decode(&req); err != nil {
		handler.RespondError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	name := strings.TrimSpace(req.Name)
	if name == "" {
		handler.RespondError(w, http.StatusBadRequest, "name is required")
		return
	}
	auth, ok := m.resolveOperator(w, r, ws, agents.ActionCreateRole)
	if !ok {
		return
	}
	role, created, err := EnsureRole(r.Context(), m.store, m.roles, auth, ws, EnsureRoleRequest{
		Name:                name,
		Kind:                req.Kind,
		Description:         req.Description,
		Prompt:              req.Prompt,
		PromptFile:          req.PromptFile,
		PromptFilename:      req.PromptFilename,
		Model:               req.Model,
		TaskFilter:          req.TaskFilter,
		Backend:             req.Backend,
		Effort:              req.Effort,
		PathPatterns:        req.PathPatterns,
		MaxPriority:         req.MaxPriority,
		MaxConcurrency:      req.MaxConcurrency,
		ReadOnly:            req.ReadOnly,
		AllowedTools:        req.AllowedTools,
		DeniedTools:         req.DeniedTools,
		Skills:              req.Skills,
		MaxBudgetUSD:        req.MaxBudgetUSD,
		PersistInlinePrompt: req.PersistInlinePrompt,
	})
	if err != nil {
		writeRoleError(w, err, "create role failed")
		return
	}
	status := http.StatusOK
	if created {
		status = http.StatusCreated
	}
	handler.WriteJSON(w, status, role)
}

// getRole returns a single role plus its current prompt body so the UI can
// populate an editor. A missing role is a 404.
func (m *Module) getRole(w http.ResponseWriter, r *http.Request) {
	ws, ok := canonicalWorkspace(w, r)
	if !ok {
		return
	}
	name := strings.TrimSpace(r.PathValue("name"))
	if name == "" {
		handler.RespondError(w, http.StatusBadRequest, "role name is required")
		return
	}
	value, err := m.roles.GetRole(r.Context(), ws, name)
	if err != nil {
		writeRoleError(w, err, "get role failed")
		return
	}
	role := domainRole(value)
	handler.WriteJSON(w, http.StatusOK, roleWithPrompt{Role: role, Prompt: m.readRolePrompt(role)})
}

// updateRole applies a partial edit to a role. A new prompt body is published
// under a fresh immutable filename, then the edited role is repointed to it.
// The old file remains untouched in case another role still references it. The
// change takes effect on the agent's next spawn — a running agent keeps the
// prompt it read at launch.
func (m *Module) updateRole(w http.ResponseWriter, r *http.Request) { //nolint:funlen // Partial role updates are intentionally explicit.
	ws, ok := canonicalWorkspace(w, r)
	if !ok {
		return
	}
	name := strings.TrimSpace(r.PathValue("name"))
	if name == "" {
		handler.RespondError(w, http.StatusBadRequest, "role name is required")
		return
	}
	var req updateRoleRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxRoleBodyBytes)).Decode(&req); err != nil {
		handler.RespondError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	currentValue, err := m.roles.GetRole(r.Context(), ws, name)
	if err != nil {
		writeRoleError(w, err, "get role failed")
		return
	}
	current := domainRole(currentValue)
	auth, ok := m.resolveOperator(w, r, ws, agents.ActionUpdateRole)
	if !ok {
		return
	}

	// A nil field means "leave unchanged"; RoleUpdate's fields are all pointers,
	// so trimPtr threads that through for the string fields.
	patch := store.RoleUpdate{
		Kind:           trimPtr(req.Kind),
		Description:    trimPtr(req.Description),
		PromptFile:     trimPtr(req.PromptFile),
		Model:          trimPtr(req.Model),
		TaskFilter:     trimPtr(req.TaskFilter),
		Backend:        trimPtr(req.Backend),
		Effort:         trimPtr(req.Effort),
		PathPatterns:   req.PathPatterns,
		MaxPriority:    optionalIntPatch(req.MaxPriority, req.ClearPriority),
		MaxConcurrency: optionalIntPatch(req.MaxConcurrency, req.ClearConcurrent),
		ReadOnly:       req.ReadOnly,
		AllowedTools:   req.AllowedTools,
		DeniedTools:    req.DeniedTools,
		Skills:         req.Skills,
		MaxBudgetUSD:   optionalFloatPatch(req.MaxBudgetUSD, req.ClearBudget),
	}

	if req.Prompt != nil {
		if req.PersistInlinePrompt {
			patch.Prompt = req.Prompt
		} else {
			filename := ""
			if req.PromptFilename != nil {
				filename = strings.TrimSpace(*req.PromptFilename)
			}
			promptPath, werr := ensureRolePrompt(r.Context(), m.store, ws, name, filename, *req.Prompt)
			if werr != nil {
				handler.WriteDomainError(w, werr, "update role prompt failed")
				return
			}
			patch.PromptFile = &promptPath
		}
	}

	updated, err := m.roles.UpdateRole(r.Context(), auth, agents.UpdateRoleCommand{
		WorkspaceKey: ws, RoleName: name, ExpectedUpdatedAt: current.UpdatedAt,
		Patch: rolePatchFromStore(patch),
	})
	if err != nil {
		writeRoleError(w, err, "update role failed")
		return
	}
	role := domainRole(updated)
	handler.WriteJSON(w, http.StatusOK, roleWithPrompt{Role: role, Prompt: m.readRolePrompt(role)})
}

// cloneRole duplicates a role (config + prompt) under a new name so the UI's
// "clone" action can seed a new agent from an existing one. The new prompt is
// written to its own file so edits to one do not affect the other. A name
// collision is a 409 (the caller picked a taken name) — unlike createRole, clone
// is not a silent ensure.
func (m *Module) cloneRole(w http.ResponseWriter, r *http.Request) { //nolint:funlen // Clone validation and persistence stay in one handler.
	ws, ok := canonicalWorkspace(w, r)
	if !ok {
		return
	}
	name := strings.TrimSpace(r.PathValue("name"))
	if name == "" {
		handler.RespondError(w, http.StatusBadRequest, "source role name is required")
		return
	}
	var req cloneRoleRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxRoleBodyBytes)).Decode(&req); err != nil {
		handler.RespondError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	target := strings.TrimSpace(req.TargetName)
	if target == "" {
		handler.RespondError(w, http.StatusBadRequest, "target_name is required")
		return
	}
	if target == name {
		handler.RespondError(w, http.StatusBadRequest, "target_name must differ from the source role")
		return
	}

	value, err := m.roles.GetRole(r.Context(), ws, name)
	if err != nil {
		writeRoleError(w, err, "get source role failed")
		return
	}
	src := domainRole(value)
	auth, ok := m.resolveOperator(w, r, ws, agents.ActionCreateRole)
	if !ok {
		return
	}

	description := strings.TrimSpace(req.Description)
	if description == "" {
		description = src.Description
	}
	in := store.RoleCreate{
		WorkspaceKey:   ws,
		Name:           target,
		Description:    description,
		Model:          src.Model,
		TaskFilter:     src.TaskFilter,
		Backend:        src.Backend,
		Effort:         src.Effort,
		PathPatterns:   src.PathPatterns,
		Skills:         src.Skills,
		MaxPriority:    src.MaxPriority,
		MaxConcurrency: src.MaxConcurrency,
		ReadOnly:       src.ReadOnly,
		AllowedTools:   src.AllowedTools,
		DeniedTools:    src.DeniedTools,
		MaxBudgetUSD:   src.MaxBudgetUSD,
	}
	if prompt := m.readRolePrompt(src); prompt != "" {
		promptPath, werr := m.writeRolePrompt(r.Context(), ws, target, "", prompt)
		if werr != nil {
			handler.RespondError(w, http.StatusInternalServerError, werr.Error())
			return
		}
		in.PromptFile = promptPath
	}

	created, err := m.roles.CreateRole(r.Context(), auth, agents.CreateRoleCommand{
		WorkspaceKey: ws,
		Role:         roleDefinitionFromCreate(in),
	})
	if err != nil {
		writeRoleError(w, err, "clone role failed")
		return
	}
	role := domainRole(created)
	handler.WriteJSON(w, http.StatusCreated, role)
}

// deleteRole removes a custom role. Built-in roles (domain.BuiltinRoleNames) are
// refused — they are auto-seeded and assumed to exist. The store also refuses a
// role still in use by an agent service. The role's prompt file is left on disk
// (harmless).
func (m *Module) deleteRole(w http.ResponseWriter, r *http.Request) {
	ws, ok := canonicalWorkspace(w, r)
	if !ok {
		return
	}
	name := strings.TrimSpace(r.PathValue("name"))
	if name == "" {
		handler.RespondError(w, http.StatusBadRequest, "role name is required")
		return
	}
	if domain.IsBuiltinRole(name) {
		handler.RespondError(w, http.StatusBadRequest, "cannot delete the built-in "+name+" role")
		return
	}
	current, err := m.roles.GetRole(r.Context(), ws, name)
	if err != nil {
		writeRoleError(w, err, "get role failed")
		return
	}
	auth, ok := m.resolveOperator(w, r, ws, agents.ActionDeleteRole)
	if !ok {
		return
	}
	if err := deleteRoleRecord(r.Context(), m.roles, auth, ws, name, current.UpdatedAt); err != nil {
		writeRoleError(w, err, "delete role failed")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func canonicalWorkspace(w http.ResponseWriter, r *http.Request) (string, bool) {
	if r == nil {
		handler.RespondError(w, http.StatusBadRequest, "canonical workspace is required")
		return "", false
	}
	workspace := strings.TrimSpace(middleware.WorkspaceFromContext(r.Context()))
	if workspace == "" {
		handler.RespondError(w, http.StatusBadRequest, "canonical workspace is required")
		return "", false
	}
	return workspace, true
}

// readRolePrompt reads a role's prompt body from its PromptFile. It returns ""
// when the role has no prompt file (builtin) or the file is unreadable — the
// editor simply shows an empty prompt rather than failing the request.
func (m *Module) readRolePrompt(role *domain.Role) string {
	return ReadPromptBody(role)
}

// ReadPromptBody reads a role's prompt body from its PromptFile, falling back
// to the persisted inline Prompt when no file is configured. It returns ""
// when a configured file is unreadable. It is
// the single loader for role prompt bodies, shared by this module's read/clone
// paths and the driver-op roles.get surface (internal/webui/handlers/driverapi)
// so the on-disk prompt is read one way. PromptFile is an absolute path (the
// roles API persists it that way via writeRolePrompt).
func ReadPromptBody(role *domain.Role) string {
	if role == nil {
		return ""
	}
	if strings.TrimSpace(role.PromptFile) == "" {
		return role.Prompt
	}
	data, err := os.ReadFile(role.PromptFile)
	if err != nil {
		return ""
	}
	return string(data)
}

// trimPtr returns nil for a nil pointer, otherwise a pointer to the trimmed
// value — so a partial-update patch carries "unchanged" (nil) vs a trimmed new
// value without a per-field if block.
func trimPtr(p *string) *string {
	if p == nil {
		return nil
	}
	v := strings.TrimSpace(*p)
	return &v
}

func optionalIntPatch(value *int, clear bool) **int {
	if clear {
		var cleared *int
		return &cleared
	}
	if value == nil {
		return nil
	}
	cloned := *value
	pointer := &cloned
	return &pointer
}

func optionalFloatPatch(value *float64, clear bool) **float64 {
	if clear {
		var cleared *float64
		return &cleared
	}
	if value == nil {
		return nil
	}
	cloned := *value
	pointer := &cloned
	return &pointer
}

func (m *Module) resolveOperator(
	w http.ResponseWriter,
	r *http.Request,
	workspace string,
	action authority.Action,
) (authority.OperatorAuthority, bool) {
	if m == nil || m.authority == nil {
		writeRoleError(w, agents.ErrUnavailable, "role management unavailable")
		return authority.OperatorAuthority{}, false
	}
	auth, err := m.authority.ResolveOperatorAuthority(r, workspace, action)
	if err != nil {
		writeRoleError(w, err, "role authorization failed")
		return authority.OperatorAuthority{}, false
	}
	return auth, true
}

func writeRoleError(w http.ResponseWriter, err error, fallback string) {
	switch {
	case errors.Is(err, workflowcataloghttp.ErrUnauthenticated):
		handler.RespondError(w, http.StatusUnauthorized, "operator authentication required")
	case errors.Is(err, authority.ErrWorkspaceMismatch),
		errors.Is(err, authority.ErrAdmissionDenied),
		errors.Is(err, authority.ErrActionNotAllowed):
		handler.RespondError(w, http.StatusForbidden, "operator is not allowed to manage this workspace")
	case errors.Is(err, agents.ErrInvalid), errors.Is(err, domain.ErrInvalid):
		handler.RespondError(w, http.StatusBadRequest, err.Error())
	case errors.Is(err, agents.ErrNotFound), errors.Is(err, domain.ErrNotFound):
		handler.RespondError(w, http.StatusNotFound, fallback)
	case errors.Is(err, agents.ErrAlreadyExists), errors.Is(err, agents.ErrConflict),
		errors.Is(err, domain.ErrAlreadyExists), errors.Is(err, domain.ErrConflict):
		handler.RespondError(w, http.StatusConflict, err.Error())
	case errors.Is(err, agents.ErrUnavailable):
		handler.RespondError(w, http.StatusServiceUnavailable, fallback)
	default:
		handler.RespondError(w, http.StatusInternalServerError, fallback)
	}
}

func roleDefinitionFromCreate(input store.RoleCreate) agents.RoleDefinition {
	return agents.RoleDefinition{
		Name: input.Name, Kind: input.Kind, Description: input.Description,
		Prompt: input.Prompt, PromptFile: input.PromptFile, Model: input.Model,
		TaskFilter: input.TaskFilter, Backend: input.Backend, Effort: input.Effort,
		PathPatterns: slices.Clone(input.PathPatterns), Skills: slices.Clone(input.Skills),
		MaxPriority: cloneInt(input.MaxPriority), MaxConcurrency: cloneInt(input.MaxConcurrency),
		ReadOnly: input.ReadOnly, AllowedTools: slices.Clone(input.AllowedTools),
		DeniedTools: slices.Clone(input.DeniedTools), MaxBudgetUSD: cloneFloat64(input.MaxBudgetUSD),
	}
}

func rolePatchFromStore(patch store.RoleUpdate) agents.RolePatch {
	return agents.RolePatch{
		Kind: patch.Kind, Description: patch.Description, Prompt: patch.Prompt,
		PromptFile: patch.PromptFile, Model: patch.Model, TaskFilter: patch.TaskFilter,
		Backend: patch.Backend, Effort: patch.Effort, PathPatterns: patch.PathPatterns,
		Skills: patch.Skills, MaxPriority: patch.MaxPriority, MaxConcurrency: patch.MaxConcurrency,
		ReadOnly: patch.ReadOnly, AllowedTools: patch.AllowedTools, DeniedTools: patch.DeniedTools,
		MaxBudgetUSD: patch.MaxBudgetUSD,
	}
}

func domainRole(role *agents.Role) *domain.Role {
	if role == nil {
		return nil
	}
	return &domain.Role{
		WorkspaceKey: role.WorkspaceKey, Name: role.Name, Kind: domain.RoleKind(role.Kind),
		Description: role.Description, Prompt: role.Prompt, PromptFile: role.PromptFile,
		Model: role.Model, TaskFilter: role.TaskFilter, Backend: role.Backend, Effort: role.Effort,
		PathPatterns: slices.Clone(role.PathPatterns), Skills: slices.Clone(role.Skills),
		MaxPriority: cloneInt(role.MaxPriority), MaxConcurrency: cloneInt(role.MaxConcurrency),
		ReadOnly: role.ReadOnly, AllowedTools: slices.Clone(role.AllowedTools),
		DeniedTools: slices.Clone(role.DeniedTools), MaxBudgetUSD: cloneFloat64(role.MaxBudgetUSD),
		CreatedAt: role.CreatedAt, UpdatedAt: role.UpdatedAt,
	}
}

func cloneInt(value *int) *int {
	if value == nil {
		return nil
	}
	out := *value
	return &out
}

func cloneFloat64(value *float64) *float64 {
	if value == nil {
		return nil
	}
	out := *value
	return &out
}

func equalInt(left, right *int) bool {
	return left == nil && right == nil || left != nil && right != nil && *left == *right
}

func equalFloat64(left, right *float64) bool {
	return left == nil && right == nil || left != nil && right != nil && *left == *right
}

// writeRolePrompt writes the prompt body to <workspace>/.loom/prompts/<file>
// and returns the absolute path. The path/write logic is shared with the serve
// workspacemgr seed/backfill via roleprompts.WritePromptFile so both writers
// agree on layout (this handler must not be imported by workspacemgr, hence the
// neutral shared package).
func (m *Module) writeRolePrompt(ctx context.Context, ws, roleName, filename, content string) (string, error) {
	return writeRolePrompt(ctx, m.store, ws, roleName, filename, content)
}

func writeRolePrompt(ctx context.Context, st store.Store, ws, roleName, filename, content string) (string, error) {
	wsPath := strings.TrimSpace(storeadapter.ResolveOrHealWorkspacePath(ctx, st, ws))
	if wsPath == "" {
		return "", fmt.Errorf("workspace path unavailable; cannot persist role prompt")
	}
	return roleprompts.WritePromptFile(wsPath, roleName, filename, content)
}

func ensureRolePrompt(ctx context.Context, st store.Store, ws, roleName, filename, content string) (string, error) {
	receipt, err := ensureRolePromptWithReceipt(ctx, st, ws, roleName, filename, content)
	return receipt.Path, err
}

func ensureRolePromptWithReceipt(
	ctx context.Context,
	st store.Store,
	ws, roleName, filename, content string,
) (roleprompts.PromptFileReceipt, error) {
	wsPath := strings.TrimSpace(storeadapter.ResolveOrHealWorkspacePath(ctx, st, ws))
	if wsPath == "" {
		return roleprompts.PromptFileReceipt{}, fmt.Errorf("workspace path unavailable; cannot persist role prompt")
	}
	immutableFilename := roleprompts.ImmutablePromptFilename(roleName, filename, content)
	receipt, err := roleprompts.EnsurePromptFileWithReceipt(wsPath, roleName, immutableFilename, content)
	if errors.Is(err, roleprompts.ErrPromptFileConflict) {
		return roleprompts.PromptFileReceipt{},
			fmt.Errorf("role %q prompt file conflicts with an existing definition: %w", roleName, domain.ErrConflict)
	}
	return receipt, err
}
