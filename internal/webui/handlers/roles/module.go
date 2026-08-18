// Package roles exposes role prompt metadata, safe prompt-body reads, and
// prompt-only conditional updates for the WebUI.
package roles

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/tysonthomas9/loomcli/internal/agentprompt"
	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/roleprompts"
	"github.com/tysonthomas9/loomcli/internal/store"
	"github.com/tysonthomas9/loomcli/internal/webui/server/dto"
	"github.com/tysonthomas9/loomcli/internal/webui/server/handler"
	"github.com/tysonthomas9/loomcli/internal/webui/server/middleware"
	"github.com/tysonthomas9/loomcli/internal/webui/storeadapter"
)

const (
	sourceBuiltinTemplate = "builtinTemplate"
	sourceManaged         = "managed"
	sourceFile            = "file"
	sourceInline          = "inline"
	sourceBuiltinSelector = "builtinSelector"

	reasonBuiltin    = "builtin"
	reasonManaged    = "managed"
	reasonUnreadable = "unreadable"
	reasonExternal   = "external"

	managedReviewerRole = "pr-reviewer"
)

const (
	workerActivationNote      = "Takes effect when the daemon reconciles (~30s) or on next spawn; running agents keep the prompt they launched with."
	interactiveActivationNote = "Existing terminals keep their prompt until a new session."
)

type Module struct {
	store        store.Store
	patchAccess  middleware.Middleware
	workspaceDir func(ctx context.Context, st store.Store, workspace string) string
}

// NewModule constructs the v5 direct-store roles module. PATCH uses the same
// file-access policy as workspace file writes.
func NewModule(st store.Store, accessCfg ...middleware.FileAccessConfig) *Module {
	cfg := middleware.FileAccessConfig{}
	if len(accessCfg) > 0 {
		cfg = accessCfg[0]
	}
	return &Module{
		store:       st,
		patchAccess: middleware.FileAccess(cfg),
		workspaceDir: func(ctx context.Context, st store.Store, workspace string) string {
			return storeadapter.ResolveOrHealWorkspacePath(ctx, st, workspace)
		},
	}
}

func (m *Module) Register(mux *http.ServeMux) {
	if m == nil || m.store == nil {
		return
	}
	mux.HandleFunc("GET /api/workspaces/{ws}/roles/{name}", m.getRole)
	mux.Handle("PATCH /api/workspaces/{ws}/roles/{name}", m.patchAccess(http.HandlerFunc(m.updateRolePrompt)))
}

type roleDetailDTO struct {
	SourceKind     string `json:"sourceKind"`
	SourceBody     string `json:"sourceBody"`
	SourceError    string `json:"sourceError,omitempty"`
	Editable       bool   `json:"editable"`
	EditableReason string `json:"editableReason"`
	Revision       string `json:"revision"`
	ActivationNote string `json:"activationNote"`
}

type itemResponse[T any] struct {
	Success bool `json:"success"`
	Data    T    `json:"data"`
}

type updateRolePromptRequest struct {
	Prompt           *string `json:"prompt"`
	ExpectedRevision *string `json:"expectedRevision"`
}

type roleProjection struct {
	sourceKind     string
	sourceBody     string
	sourceError    string
	editable       bool
	editableReason string
	activationNote string
}

func (m *Module) getRole(w http.ResponseWriter, r *http.Request) {
	ws, ok := canonicalWorkspace(w, r)
	if !ok {
		return
	}
	role, err := m.store.Roles().Get(r.Context(), ws, strings.TrimSpace(r.PathValue("name")))
	if err != nil {
		writeStoreError(w, err, "role not found")
		return
	}
	if role == nil {
		writeStoreError(w, domain.ErrNotFound, "role not found")
		return
	}
	m.writeRoleDetail(w, r, ws, role)
}

func (m *Module) updateRolePrompt(w http.ResponseWriter, r *http.Request) {
	ws, ok := canonicalWorkspace(w, r)
	if !ok {
		return
	}
	name := strings.TrimSpace(r.PathValue("name"))
	if name == "" {
		handler.RespondError(w, http.StatusBadRequest, "role name is required")
		return
	}
	current, err := m.store.Roles().Get(r.Context(), ws, name)
	if err != nil {
		writeStoreError(w, err, "role not found")
		return
	}
	if current == nil {
		writeStoreError(w, domain.ErrNotFound, "role not found")
		return
	}
	if isDaemonBuiltin(name) {
		w.Header().Set("Allow", http.MethodGet)
		writeCodedError(w, http.StatusMethodNotAllowed, "builtin_role", "built-in role prompts are read-only")
		return
	}
	if isManagedRole(name) {
		writeCodedError(w, http.StatusConflict, "managed_role", "managed role prompts cannot be edited")
		return
	}

	var req updateRolePromptRequest
	if err := readStrictJSON(w, r, &req); err != nil {
		handler.RespondError(w, http.StatusBadRequest, err.Error())
		return
	}
	if req.Prompt == nil || req.ExpectedRevision == nil {
		handler.RespondError(w, http.StatusBadRequest, "prompt and expectedRevision are required")
		return
	}
	revision, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(*req.ExpectedRevision))
	if err != nil {
		handler.RespondError(w, http.StatusBadRequest, "expectedRevision is invalid")
		return
	}

	empty := ""
	patch := store.RoleUpdate{ExpectedUpdatedAt: &revision}
	kind := domain.ResolveRoleKind(current, current.Name)
	if kind == domain.RoleKindInteractive {
		patch.Prompt = req.Prompt
		patch.PromptFile = &empty
	} else {
		workspaceDir := strings.TrimSpace(m.workspaceDir(r.Context(), m.store, ws))
		if workspaceDir == "" {
			handler.RespondError(w, http.StatusInternalServerError, "workspace path unavailable; cannot persist role prompt")
			return
		}
		path, err := roleprompts.Publish(workspaceDir, current.Name, *req.Prompt)
		if err != nil {
			handler.RespondError(w, http.StatusInternalServerError, "publish role prompt failed")
			return
		}
		patch.PromptFile = &path
		patch.Prompt = &empty
	}
	updated, err := m.store.Roles().Update(r.Context(), ws, name, patch)
	if err != nil {
		if errors.Is(err, domain.ErrConflict) {
			writeCodedError(w, http.StatusConflict, "stale_revision", "role prompt changed elsewhere")
			return
		}
		writeStoreError(w, err, "update role prompt failed")
		return
	}
	if updated == nil {
		writeStoreError(w, domain.ErrNotFound, "role not found")
		return
	}
	m.writeRoleDetail(w, r, ws, updated)
}

func (m *Module) writeRoleDetail(w http.ResponseWriter, r *http.Request, ws string, role *domain.Role) {
	workspaceDir := strings.TrimSpace(m.workspaceDir(r.Context(), m.store, ws))
	projection, err := projectRole(workspaceDir, role)
	if err != nil {
		handler.RespondError(w, http.StatusInternalServerError, "resolve role prompt failed")
		return
	}
	detail := roleDetailDTO{
		SourceKind: projection.sourceKind, SourceBody: projection.sourceBody,
		SourceError: projection.sourceError, Editable: projection.editable,
		EditableReason: projection.editableReason, Revision: revisionString(role.UpdatedAt),
		ActivationNote: projection.activationNote,
	}
	handler.WriteJSON(w, http.StatusOK, itemResponse[roleDetailDTO]{Success: true, Data: detail})
}

func projectRole(workspaceDir string, role *domain.Role) (roleProjection, error) {
	if role == nil {
		return roleProjection{}, domain.ErrNotFound
	}
	name := strings.ToLower(strings.TrimSpace(role.Name))
	kind := domain.ResolveRoleKind(role, role.Name)
	activationNote := workerActivationNote
	if kind == domain.RoleKindInteractive {
		activationNote = interactiveActivationNote
	}
	if isDaemonBuiltin(name) {
		templateName := name
		if name == "plan" {
			templateName = "planning"
		}
		body, err := agentprompt.TemplateSource(templateName)
		if err != nil {
			return roleProjection{}, err
		}
		return roleProjection{sourceKind: sourceBuiltinTemplate, sourceBody: body, editableReason: reasonBuiltin, activationNote: activationNote}, nil
	}
	if isManagedRole(name) {
		body, err := agentprompt.TemplateSource("pr-review-checkout")
		if err != nil {
			return roleProjection{}, err
		}
		return roleProjection{sourceKind: sourceManaged, sourceBody: body, editableReason: reasonManaged, activationNote: activationNote}, nil
	}
	if kind == domain.RoleKindInteractive && strings.TrimSpace(role.Prompt) != "" {
		return roleProjection{sourceKind: sourceInline, sourceBody: role.Prompt, editable: true, activationNote: activationNote}, nil
	}
	promptFile := strings.TrimSpace(role.PromptFile)
	if kind == domain.RoleKindInteractive && strings.HasPrefix(promptFile, "builtin:") {
		return roleProjection{sourceKind: sourceBuiltinSelector, sourceBody: promptFile, editable: true, activationNote: activationNote}, nil
	}
	if promptFile != "" {
		body, err := roleprompts.ReadValidated(workspaceDir, promptFile)
		if err == nil {
			return roleProjection{sourceKind: sourceFile, sourceBody: body, editable: true, activationNote: activationNote}, nil
		}
		if errors.Is(err, roleprompts.ErrExternal) {
			return roleProjection{sourceKind: sourceFile, sourceError: "Prompt file is outside this workspace and was not read.", editableReason: reasonExternal, activationNote: activationNote}, nil
		}
		return roleProjection{sourceKind: sourceFile, sourceError: "Prompt file could not be read.", editableReason: reasonUnreadable, activationNote: activationNote}, nil
	}
	if kind == domain.RoleKindInteractive {
		return roleProjection{sourceKind: sourceInline, sourceBody: role.Prompt, editable: true, activationNote: activationNote}, nil
	}
	return roleProjection{sourceKind: sourceFile, sourceError: "Prompt file is not configured.", editableReason: reasonUnreadable, activationNote: activationNote}, nil
}

func revisionString(value time.Time) string { return value.UTC().Format(time.RFC3339Nano) }

func isDaemonBuiltin(name string) bool {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "plan", "task":
		return true
	default:
		return false
	}
}

func isManagedRole(name string) bool {
	return strings.EqualFold(strings.TrimSpace(name), managedReviewerRole)
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

func readStrictJSON(w http.ResponseWriter, r *http.Request, dst any) error {
	r.Body = http.MaxBytesReader(w, r.Body, handler.MaxRequestBody)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(dst); err != nil {
		return fmt.Errorf("invalid request body")
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return fmt.Errorf("request body contains trailing content")
	}
	return nil
}

func writeCodedError(w http.ResponseWriter, status int, code, message string) {
	handler.WriteJSON(w, status, dto.NewErrorResponse(message, code))
}

func writeStoreError(w http.ResponseWriter, err error, fallback string) {
	switch {
	case errors.Is(err, domain.ErrNotFound):
		handler.RespondError(w, http.StatusNotFound, fallback)
	case errors.Is(err, domain.ErrInvalid):
		handler.RespondError(w, http.StatusBadRequest, err.Error())
	case errors.Is(err, domain.ErrConflict), errors.Is(err, domain.ErrAlreadyExists):
		handler.RespondError(w, http.StatusConflict, fallback)
	default:
		handler.RespondError(w, http.StatusInternalServerError, fallback)
	}
}
