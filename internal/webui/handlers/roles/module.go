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

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/roleprompts"
	"github.com/tysonthomas9/loomcli/internal/store"
	"github.com/tysonthomas9/loomcli/internal/webui/server/handler"
	"github.com/tysonthomas9/loomcli/internal/webui/storeadapter"
)

const maxRoleBodyBytes = 1 << 20

// Module registers role read/create routes. It writes prompt files to the
// machine-local workspace directory, so it holds the store directly (the same
// shape as the workflows/webhooks modules).
type Module struct {
	store store.Store
}

func NewModule(st store.Store) *Module {
	return &Module{store: st}
}

func (m *Module) Register(mux *http.ServeMux) {
	if m.store == nil {
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
	Name           string   `json:"name"`
	Description    string   `json:"description,omitempty"`
	Prompt         string   `json:"prompt,omitempty"`          // prompt body written to disk
	PromptFilename string   `json:"prompt_filename,omitempty"` // defaults to <name>.md
	Model          string   `json:"model,omitempty"`
	TaskFilter     string   `json:"task_filter,omitempty"`
	Backend        string   `json:"backend,omitempty"`
	Effort         string   `json:"effort,omitempty"`
	ReadOnly       bool     `json:"read_only,omitempty"`
	AllowedTools   []string `json:"allowed_tools,omitempty"`
	DeniedTools    []string `json:"denied_tools,omitempty"`
	Skills         []string `json:"skills,omitempty"`
}

type EnsureRoleRequest struct {
	Name           string
	Description    string
	Prompt         string
	PromptFilename string
	Model          string
	TaskFilter     string
	Backend        string
	Effort         string
	ReadOnly       bool
	AllowedTools   []string
	DeniedTools    []string
	Skills         []string
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
	Description    *string   `json:"description,omitempty"`
	Prompt         *string   `json:"prompt,omitempty"`          // new prompt body (publishes a new immutable file)
	PromptFilename *string   `json:"prompt_filename,omitempty"` // optional new filename
	Model          *string   `json:"model,omitempty"`
	TaskFilter     *string   `json:"task_filter,omitempty"`
	Backend        *string   `json:"backend,omitempty"`
	Effort         *string   `json:"effort,omitempty"`
	ReadOnly       *bool     `json:"read_only,omitempty"`
	AllowedTools   *[]string `json:"allowed_tools,omitempty"`
	DeniedTools    *[]string `json:"denied_tools,omitempty"`
	Skills         *[]string `json:"skills,omitempty"`
}

// cloneRoleRequest duplicates an existing role (and its prompt) under a new name.
type cloneRoleRequest struct {
	TargetName  string `json:"target_name"`
	Description string `json:"description,omitempty"`
}

func (m *Module) listRoles(w http.ResponseWriter, r *http.Request) {
	ws := strings.TrimSpace(r.PathValue("ws"))
	roles, err := m.store.Roles().List(r.Context(), ws)
	if err != nil {
		handler.WriteDomainError(w, err, "list roles failed")
		return
	}
	handler.WriteJSON(w, http.StatusOK, roles)
}

func EnsureRole(ctx context.Context, st store.Store, ws string, req EnsureRoleRequest) (*domain.Role, bool, error) {
	result, err := EnsureRoleWithReceipt(ctx, st, ws, req)
	if err != nil {
		return nil, false, err
	}
	return result.Role, result.Created, nil
}

// EnsureRoleWithReceipt is EnsureRole with a compensating ownership receipt.
func EnsureRoleWithReceipt(ctx context.Context, st store.Store, ws string, req EnsureRoleRequest) (*EnsureRoleResult, error) {
	if st == nil {
		return nil, fmt.Errorf("store is required: %w", domain.ErrInvalid)
	}
	ws = strings.TrimSpace(ws)
	name := strings.TrimSpace(req.Name)
	if ws == "" || name == "" {
		return nil, fmt.Errorf("workspace and role name are required: %w", domain.ErrInvalid)
	}

	existing, found, err := findEnsuredRole(ctx, st, ws, name, req)
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
	role, err := st.Roles().Create(ctx, in)
	if err == nil {
		return &EnsureRoleResult{Role: role, Created: true}, nil
	}
	if !errors.Is(err, domain.ErrAlreadyExists) {
		// The immutable prompt is deliberately retained. Another concurrent
		// ensure may already have adopted it, and there is no transaction that
		// spans the role store and filesystem. Retention is retry-safe.
		return nil, err
	}

	existing, getErr := st.Roles().Get(ctx, ws, name)
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
		WorkspaceKey: ws,
		Name:         name,
		Description:  strings.TrimSpace(req.Description),
		Model:        strings.TrimSpace(req.Model),
		TaskFilter:   strings.TrimSpace(req.TaskFilter),
		Backend:      strings.TrimSpace(req.Backend),
		Effort:       strings.TrimSpace(req.Effort),
		ReadOnly:     req.ReadOnly,
		AllowedTools: slices.Clone(req.AllowedTools),
		DeniedTools:  slices.Clone(req.DeniedTools),
		Skills:       slices.Clone(req.Skills),
	}
	var receipt roleprompts.PromptFileReceipt
	if strings.TrimSpace(req.Prompt) != "" {
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

func deleteRoleRecord(ctx context.Context, st store.Store, ws, name string) error {
	return st.Roles().Delete(ctx, ws, name)
}

func findEnsuredRole(
	ctx context.Context,
	st store.Store,
	ws string,
	name string,
	req EnsureRoleRequest,
) (*domain.Role, bool, error) {
	existing, err := st.Roles().Get(ctx, ws, name)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			return nil, false, nil
		}
		return nil, false, err
	}
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
	addMismatch("model", existing.Model == strings.TrimSpace(req.Model))
	addMismatch("task_filter", existing.TaskFilter == strings.TrimSpace(req.TaskFilter))
	addMismatch("backend", existing.Backend == strings.TrimSpace(req.Backend))
	addMismatch("effort", existing.Effort == strings.TrimSpace(req.Effort))
	addMismatch("read_only", existing.ReadOnly == req.ReadOnly)
	addMismatch("allowed_tools", slices.Equal(existing.AllowedTools, req.AllowedTools))
	addMismatch("denied_tools", slices.Equal(existing.DeniedTools, req.DeniedTools))
	addMismatch("skills", slices.Equal(existing.Skills, req.Skills))

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
	ws := strings.TrimSpace(r.PathValue("ws"))
	if ws == "" {
		handler.RespondError(w, http.StatusBadRequest, "workspace is required")
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
	role, created, err := EnsureRole(r.Context(), m.store, ws, EnsureRoleRequest{
		Name:           name,
		Description:    req.Description,
		Prompt:         req.Prompt,
		PromptFilename: req.PromptFilename,
		Model:          req.Model,
		TaskFilter:     req.TaskFilter,
		Backend:        req.Backend,
		Effort:         req.Effort,
		ReadOnly:       req.ReadOnly,
		AllowedTools:   req.AllowedTools,
		DeniedTools:    req.DeniedTools,
		Skills:         req.Skills,
	})
	if err != nil {
		handler.WriteDomainError(w, err, "create role failed")
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
	ws := strings.TrimSpace(r.PathValue("ws"))
	name := strings.TrimSpace(r.PathValue("name"))
	if ws == "" || name == "" {
		handler.RespondError(w, http.StatusBadRequest, "workspace and role name are required")
		return
	}
	role, err := m.store.Roles().Get(r.Context(), ws, name)
	if err != nil {
		handler.WriteDomainError(w, err, "get role failed")
		return
	}
	handler.WriteJSON(w, http.StatusOK, roleWithPrompt{Role: role, Prompt: m.readRolePrompt(role)})
}

// updateRole applies a partial edit to a role. A new prompt body is published
// under a fresh immutable filename, then the edited role is repointed to it.
// The old file remains untouched in case another role still references it. The
// change takes effect on the agent's next spawn — a running agent keeps the
// prompt it read at launch.
func (m *Module) updateRole(w http.ResponseWriter, r *http.Request) { //nolint:funlen // Partial role updates are intentionally explicit.
	ws := strings.TrimSpace(r.PathValue("ws"))
	name := strings.TrimSpace(r.PathValue("name"))
	if ws == "" || name == "" {
		handler.RespondError(w, http.StatusBadRequest, "workspace and role name are required")
		return
	}
	var req updateRoleRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxRoleBodyBytes)).Decode(&req); err != nil {
		handler.RespondError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	// The role must exist to edit it.
	if _, err := m.store.Roles().Get(r.Context(), ws, name); err != nil {
		handler.WriteDomainError(w, err, "get role failed")
		return
	}

	// A nil field means "leave unchanged"; RoleUpdate's fields are all pointers,
	// so trimPtr threads that through for the string fields.
	patch := store.RoleUpdate{
		Description:  trimPtr(req.Description),
		Model:        trimPtr(req.Model),
		TaskFilter:   trimPtr(req.TaskFilter),
		Backend:      trimPtr(req.Backend),
		Effort:       trimPtr(req.Effort),
		ReadOnly:     req.ReadOnly,
		AllowedTools: req.AllowedTools,
		DeniedTools:  req.DeniedTools,
		Skills:       req.Skills,
	}

	if req.Prompt != nil {
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

	role, err := m.store.Roles().Update(r.Context(), ws, name, patch)
	if err != nil {
		handler.WriteDomainError(w, err, "update role failed")
		return
	}
	handler.WriteJSON(w, http.StatusOK, roleWithPrompt{Role: role, Prompt: m.readRolePrompt(role)})
}

// cloneRole duplicates a role (config + prompt) under a new name so the UI's
// "clone" action can seed a new agent from an existing one. The new prompt is
// written to its own file so edits to one do not affect the other. A name
// collision is a 409 (the caller picked a taken name) — unlike createRole, clone
// is not a silent ensure.
func (m *Module) cloneRole(w http.ResponseWriter, r *http.Request) { //nolint:funlen // Clone validation and persistence stay in one handler.
	ws := strings.TrimSpace(r.PathValue("ws"))
	name := strings.TrimSpace(r.PathValue("name"))
	if ws == "" || name == "" {
		handler.RespondError(w, http.StatusBadRequest, "workspace and source role name are required")
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

	src, err := m.store.Roles().Get(r.Context(), ws, name)
	if err != nil {
		handler.WriteDomainError(w, err, "get source role failed")
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

	role, err := m.store.Roles().Create(r.Context(), in)
	if err != nil {
		handler.WriteDomainError(w, err, "clone role failed")
		return
	}
	handler.WriteJSON(w, http.StatusCreated, role)
}

// deleteRole removes a custom role. Built-in roles (domain.BuiltinRoleNames) are
// refused — they are auto-seeded and assumed to exist. The store also refuses a
// role still in use by an agent service. The role's prompt file is left on disk
// (harmless).
func (m *Module) deleteRole(w http.ResponseWriter, r *http.Request) {
	ws := strings.TrimSpace(r.PathValue("ws"))
	name := strings.TrimSpace(r.PathValue("name"))
	if ws == "" || name == "" {
		handler.RespondError(w, http.StatusBadRequest, "workspace and role name are required")
		return
	}
	if domain.IsBuiltinRole(name) {
		handler.RespondError(w, http.StatusBadRequest, "cannot delete the built-in "+name+" role")
		return
	}
	if err := deleteRoleRecord(r.Context(), m.store, ws, name); err != nil {
		handler.WriteDomainError(w, err, "delete role failed")
		return
	}
	w.WriteHeader(http.StatusNoContent)
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
