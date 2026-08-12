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
	"path/filepath"
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
	Prompt         *string   `json:"prompt,omitempty"`          // new prompt body (rewrites the file)
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
	if st == nil {
		return nil, false, fmt.Errorf("store is required: %w", domain.ErrInvalid)
	}
	name := strings.TrimSpace(req.Name)
	if ws == "" || name == "" {
		return nil, false, fmt.Errorf("workspace and role name are required: %w", domain.ErrInvalid)
	}
	if existing, err := st.Roles().Get(ctx, ws, name); err == nil && existing != nil {
		return existing, false, nil
	} else if err != nil && !errors.Is(err, domain.ErrNotFound) {
		return nil, false, err
	}

	in := store.RoleCreate{
		WorkspaceKey: ws,
		Name:         name,
		Description:  strings.TrimSpace(req.Description),
		Model:        strings.TrimSpace(req.Model),
		TaskFilter:   strings.TrimSpace(req.TaskFilter),
		Backend:      strings.TrimSpace(req.Backend),
		Effort:       strings.TrimSpace(req.Effort),
		ReadOnly:     req.ReadOnly,
		AllowedTools: req.AllowedTools,
		DeniedTools:  req.DeniedTools,
		Skills:       req.Skills,
	}
	if strings.TrimSpace(req.Prompt) != "" {
		promptPath, err := writeRolePrompt(ctx, st, ws, name, req.PromptFilename, req.Prompt)
		if err != nil {
			return nil, false, err
		}
		in.PromptFile = promptPath
	}
	role, err := st.Roles().Create(ctx, in)
	if err != nil {
		if errors.Is(err, domain.ErrAlreadyExists) {
			existing, getErr := st.Roles().Get(ctx, ws, name)
			if getErr == nil && existing != nil {
				return existing, false, nil
			}
		}
		return nil, false, err
	}
	return role, true, nil
}

// createRole is an idempotent "ensure": if the named role already exists it is
// returned unchanged (200), otherwise it is created (201). This lets the
// template gallery call it on every custom-role agent creation without 409s.
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

	// Fast idempotent path: an already-provisioned role is returned untouched.
	fetch := func() (*domain.Role, bool) {
		role, err := m.store.Roles().Get(r.Context(), ws, name)
		return role, err == nil && role != nil
	}
	if handler.WriteExistingIfFound(w, fetch) {
		return
	}

	in := store.RoleCreate{
		WorkspaceKey: ws,
		Name:         name,
		Description:  strings.TrimSpace(req.Description),
		Model:        strings.TrimSpace(req.Model),
		TaskFilter:   strings.TrimSpace(req.TaskFilter),
		Backend:      strings.TrimSpace(req.Backend),
		Effort:       strings.TrimSpace(req.Effort),
		ReadOnly:     req.ReadOnly,
		AllowedTools: req.AllowedTools,
		DeniedTools:  req.DeniedTools,
		Skills:       req.Skills,
	}

	// A custom (non-builtin) role MUST have a prompt file on disk — the daemon
	// supervisor refuses to spawn a custom role without prompt_file. Persist it
	// to a CWD-independent absolute path under the workspace so the agent
	// process can read it at launch regardless of its working directory.
	if strings.TrimSpace(req.Prompt) != "" {
		promptPath, err := m.writeRolePrompt(r.Context(), ws, name, req.PromptFilename, req.Prompt)
		if err != nil {
			handler.RespondError(w, http.StatusInternalServerError, err.Error())
			return
		}
		in.PromptFile = promptPath
	}

	role, err := m.store.Roles().Create(r.Context(), in)
	handler.WriteCreatedOrExisting(w, role, err, fetch, "create role failed")
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

// updateRole applies a partial edit to a role. A new prompt body rewrites the
// role's prompt file (reusing its existing filename so the stored path stays
// stable). The change takes effect on the agent's next spawn — a running agent
// keeps the prompt it read at launch.
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
	// The role must exist to edit it — Get also gives us the current prompt file.
	existing, err := m.store.Roles().Get(r.Context(), ws, name)
	if err != nil {
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
		if filename == "" && strings.TrimSpace(existing.PromptFile) != "" {
			filename = filepath.Base(existing.PromptFile)
		}
		promptPath, werr := m.writeRolePrompt(r.Context(), ws, name, filename, *req.Prompt)
		if werr != nil {
			handler.RespondError(w, http.StatusInternalServerError, werr.Error())
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
	if err := m.store.Roles().Delete(r.Context(), ws, name); err != nil {
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

// ReadPromptBody reads a role's prompt body from its PromptFile, returning ""
// when the role has no prompt file (builtin) or the file is unreadable. It is
// the single loader for role prompt bodies, shared by this module's read/clone
// paths and the driver-op roles.get surface (internal/webui/handlers/driverapi)
// so the on-disk prompt is read one way. PromptFile is an absolute path (the
// roles API persists it that way via writeRolePrompt).
func ReadPromptBody(role *domain.Role) string {
	if role == nil || strings.TrimSpace(role.PromptFile) == "" {
		return ""
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
