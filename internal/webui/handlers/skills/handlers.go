package skills

import (
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/store"
	"github.com/tysonthomas9/loomcli/internal/webui/server/handler"
	"github.com/tysonthomas9/loomcli/internal/webui/server/middleware"
	"github.com/tysonthomas9/loomcli/internal/webui/service"
)

const webuiSkillSource = "webui"

// Handler fronts the canonical Store skill seam. It never constructs or calls
// a fleet-db client directly.
type Handler struct {
	Store store.Store
}

type catalogFile struct {
	Path       string `json:"path"`
	Revision   string `json:"revision"`
	Executable bool   `json:"executable"`
}

type catalogSkill struct {
	Name            string            `json:"name"`
	Scope           domain.SkillScope `json:"scope"`
	Role            string            `json:"role,omitempty"`
	Description     string            `json:"description"`
	ContentRevision string            `json:"content_revision"`
	Files           []catalogFile     `json:"files"`
	CreatedBy       string            `json:"created_by,omitempty"`
	UpdatedBy       string            `json:"updated_by,omitempty"`
	Source          string            `json:"source,omitempty"`
	SourceRef       string            `json:"source_ref,omitempty"`
	CreatedAt       time.Time         `json:"created_at"`
	UpdatedAt       time.Time         `json:"updated_at"`
}

type catalogGroup struct {
	Scope  domain.SkillScope `json:"scope"`
	Role   string            `json:"role,omitempty"`
	Skills []catalogSkill    `json:"skills"`
}

type catalogResponse struct {
	Groups []catalogGroup `json:"groups"`
}

type skillDetailResponse struct {
	Name            string            `json:"name"`
	Scope           domain.SkillScope `json:"scope"`
	Role            string            `json:"role,omitempty"`
	Description     string            `json:"description"`
	Content         string            `json:"content"`
	ContentRevision string            `json:"content_revision"`
	Files           []catalogFile     `json:"files"`
	CreatedBy       string            `json:"created_by,omitempty"`
	UpdatedBy       string            `json:"updated_by,omitempty"`
	Source          string            `json:"source,omitempty"`
	SourceRef       string            `json:"source_ref,omitempty"`
	CreatedAt       time.Time         `json:"created_at"`
	UpdatedAt       time.Time         `json:"updated_at"`
}

type skillFileResponse struct {
	Path       string `json:"path"`
	Content    string `json:"content"`
	Executable bool   `json:"executable"`
	Revision   string `json:"revision"`
	SkillRef   string `json:"skill_ref"`
}

type putSkillFileRequest struct {
	Content    string `json:"content"`
	Executable bool   `json:"executable"`
}

type createSkillRequest struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Content     string `json:"content,omitempty"`
	SourceRef   string `json:"source_ref,omitempty"`
}

type patchSkillRequest struct {
	Description *string `json:"description,omitempty"`
	SourceRef   *string `json:"source_ref,omitempty"`
}

type fileWritePrecondition struct {
	IfMatch        string
	IfNoneMatchAny bool
}

type capabilitiesResponse struct {
	CanEditRoleScope bool   `json:"can_edit_role_scope"`
	WorkspaceScope   string `json:"workspace_scope"`
}

func (h *Handler) getCatalog(w http.ResponseWriter, r *http.Request) {
	skills, err := h.Store.Skills().List(r.Context(), requestWorkspaceID(r), store.SkillFilter{})
	if err != nil {
		writeSkillError(w, err)
		return
	}
	handler.WriteJSON(w, http.StatusOK, projectCatalog(skills))
}

func (h *Handler) getWorkspaceSkill(w http.ResponseWriter, r *http.Request) {
	h.getSkill(w, r, domain.SkillScopeWorkspace)
}

func (h *Handler) getRoleSkill(w http.ResponseWriter, r *http.Request) {
	h.getSkill(w, r, domain.SkillScopeRole)
}

func (h *Handler) getSkill(w http.ResponseWriter, r *http.Request, scope domain.SkillScope) {
	ref, err := requestSkillRef(r, scope)
	if err != nil {
		writeSkillError(w, err)
		return
	}
	skill, err := h.Store.Skills().Get(r.Context(), requestWorkspaceID(r), ref)
	if err != nil {
		writeSkillError(w, err)
		return
	}
	writeSkillDetail(w, http.StatusOK, skill)
}

func (h *Handler) createWorkspaceSkill(w http.ResponseWriter, _ *http.Request) {
	writeWorkspaceScopeReadonly(w)
}

func (h *Handler) createRoleSkill(w http.ResponseWriter, r *http.Request) {
	roleName := r.PathValue("role")
	if err := domain.ValidateRoleName(roleName); err != nil {
		writeSkillError(w, invalidSkillRequestPath(err))
		return
	}
	var req createSkillRequest
	if err := handler.ReadJSON(w, r, &req); err != nil {
		writeSkillError(w, err)
		return
	}
	ref := domain.RoleSkillRef(roleName, req.Name)
	if err := validateSkillCreate(ref, req); err != nil {
		writeSkillError(w, err)
		return
	}
	skill, err := h.Store.Skills().Create(r.Context(), store.SkillCreate{
		WorkspaceKey: requestWorkspaceID(r),
		Ref:          ref,
		Description:  req.Description,
		Content:      req.Content,
		Source:       webuiSkillSource,
		SourceRef:    req.SourceRef,
	})
	if err != nil {
		writeSkillError(w, err)
		return
	}
	writeSkillDetail(w, http.StatusCreated, skill)
}

func (h *Handler) patchWorkspaceSkill(w http.ResponseWriter, _ *http.Request) {
	writeWorkspaceScopeReadonly(w)
}

func (h *Handler) patchRoleSkill(w http.ResponseWriter, r *http.Request) {
	ref, err := requestSkillRef(r, domain.SkillScopeRole)
	if err != nil {
		writeSkillError(w, err)
		return
	}
	ifMatch, ok := requiredIfMatch(w, r)
	if !ok {
		return
	}
	if !h.requireSkillRevision(w, r, ref, ifMatch) {
		return
	}

	var req patchSkillRequest
	if err := handler.ReadJSON(w, r, &req); err != nil {
		writeSkillError(w, err)
		return
	}
	if req.Description != nil {
		if err := domain.ValidateSkillDescription(*req.Description); err != nil {
			writeSkillError(w, err)
			return
		}
	}
	if req.SourceRef != nil {
		if err := domain.ValidateSkillProvenanceField("source_ref", *req.SourceRef); err != nil {
			writeSkillError(w, err)
			return
		}
	}
	skill, err := h.Store.Skills().Update(r.Context(), requestWorkspaceID(r), ref, store.SkillUpdate{
		Description: req.Description,
		SourceRef:   req.SourceRef,
		Source:      webuiSkillSource,
	})
	if err != nil {
		writeSkillError(w, err)
		return
	}
	writeSkillDetail(w, http.StatusOK, skill)
}

func (h *Handler) deleteWorkspaceSkill(w http.ResponseWriter, _ *http.Request) {
	writeWorkspaceScopeReadonly(w)
}

func (h *Handler) deleteRoleSkill(w http.ResponseWriter, r *http.Request) {
	ref, err := requestSkillRef(r, domain.SkillScopeRole)
	if err != nil {
		writeSkillError(w, err)
		return
	}
	ifMatch, ok := requiredIfMatch(w, r)
	if !ok {
		return
	}
	if !h.requireSkillRevision(w, r, ref, ifMatch) {
		return
	}
	if err := h.Store.Skills().Delete(r.Context(), requestWorkspaceID(r), ref, store.SkillDelete{Source: webuiSkillSource}); err != nil {
		writeSkillError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) getWorkspaceSkillFile(w http.ResponseWriter, r *http.Request) {
	h.getSkillFile(w, r, domain.SkillScopeWorkspace)
}

func (h *Handler) getRoleSkillFile(w http.ResponseWriter, r *http.Request) {
	h.getSkillFile(w, r, domain.SkillScopeRole)
}

func (h *Handler) getSkillFile(w http.ResponseWriter, r *http.Request, scope domain.SkillScope) {
	ref, filePath, err := requestSkillFile(r, scope)
	if err != nil {
		writeSkillError(w, err)
		return
	}
	doc, err := h.Store.Skills().GetFile(r.Context(), requestWorkspaceID(r), ref, filePath)
	if err != nil {
		writeSkillError(w, err)
		return
	}
	writeSkillFile(w, http.StatusOK, doc)
}

func (h *Handler) putWorkspaceSkillFile(w http.ResponseWriter, r *http.Request) {
	h.putSkillFile(w, r, domain.SkillScopeWorkspace)
}

func (h *Handler) putRoleSkillFile(w http.ResponseWriter, r *http.Request) {
	h.putSkillFile(w, r, domain.SkillScopeRole)
}

func (h *Handler) putSkillFile(w http.ResponseWriter, r *http.Request, scope domain.SkillScope) {
	if scope == domain.SkillScopeWorkspace {
		writeWorkspaceScopeReadonly(w)
		return
	}
	ref, filePath, err := requestSkillFile(r, scope)
	if err != nil {
		writeSkillError(w, err)
		return
	}
	precondition, ok := requiredFileWritePrecondition(w, r)
	if !ok {
		return
	}

	var req putSkillFileRequest
	if err := handler.ReadJSON(w, r, &req); err != nil {
		writeSkillError(w, err)
		return
	}
	if filePath == domain.SkillFileNameSKILLMD {
		req.Executable = false
	}
	doc, err := h.Store.Skills().PutFile(r.Context(), requestWorkspaceID(r), ref, store.SkillFileWrite{
		Path:           filePath,
		Content:        req.Content,
		Executable:     req.Executable,
		IfMatch:        precondition.IfMatch,
		IfNoneMatchAny: precondition.IfNoneMatchAny,
		Source:         webuiSkillSource,
	})
	if err != nil {
		writeSkillError(w, err)
		return
	}
	status := http.StatusOK
	if precondition.IfNoneMatchAny {
		status = http.StatusCreated
	}
	writeSkillFile(w, status, doc)
}

func (h *Handler) deleteWorkspaceSkillFile(w http.ResponseWriter, r *http.Request) {
	h.deleteSkillFile(w, r, domain.SkillScopeWorkspace)
}

func (h *Handler) deleteRoleSkillFile(w http.ResponseWriter, r *http.Request) {
	h.deleteSkillFile(w, r, domain.SkillScopeRole)
}

func (h *Handler) deleteSkillFile(w http.ResponseWriter, r *http.Request, scope domain.SkillScope) {
	if scope == domain.SkillScopeWorkspace {
		writeWorkspaceScopeReadonly(w)
		return
	}
	ref, filePath, err := requestSkillFile(r, scope)
	if err != nil {
		writeSkillError(w, err)
		return
	}
	if filePath == domain.SkillFileNameSKILLMD {
		writeSkillError(w, invalidSkillf("%s is the skill body and cannot be deleted through the file route", domain.SkillFileNameSKILLMD))
		return
	}
	ifMatch, ok := requiredIfMatch(w, r)
	if !ok {
		return
	}
	if err := h.Store.Skills().DeleteFile(r.Context(), requestWorkspaceID(r), ref, store.SkillFileDelete{
		Path: filePath, IfMatch: ifMatch, Source: webuiSkillSource,
	}); err != nil {
		writeSkillError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) getCapabilities(w http.ResponseWriter, r *http.Request) {
	capabilities, ok := service.FileCapabilitiesFromContext(r.Context())
	handler.WriteJSON(w, http.StatusOK, capabilitiesResponse{
		CanEditRoleScope: ok && capabilities.Write,
		WorkspaceScope:   "read_only",
	})
}

func requestWorkspaceID(r *http.Request) string {
	if ws := middleware.WorkspaceFromContext(r.Context()); ws != "" {
		return ws
	}
	return r.PathValue("ws")
}

func requestSkillRef(r *http.Request, scope domain.SkillScope) (domain.SkillRef, error) {
	name := r.PathValue("name")
	if err := domain.ValidateSkillName(name); err != nil {
		return domain.SkillRef{}, invalidSkillRequestPath(err)
	}
	if scope == domain.SkillScopeRole {
		ref := domain.RoleSkillRef(r.PathValue("role"), name)
		if err := ref.Validate(); err != nil {
			return domain.SkillRef{}, invalidSkillRequestPath(err)
		}
		return ref, nil
	}
	return domain.WorkspaceSkillRef(name), nil
}

func requestSkillFile(r *http.Request, scope domain.SkillScope) (domain.SkillRef, string, error) {
	ref, err := requestSkillRef(r, scope)
	if err != nil {
		return domain.SkillRef{}, "", err
	}
	filePath := r.PathValue("path")
	if filePath != domain.SkillFileNameSKILLMD {
		if err := domain.ValidateSkillFilePath(filePath); err != nil {
			return domain.SkillRef{}, "", invalidSkillRequestPath(err)
		}
	}
	return ref, filePath, nil
}

func requiredIfMatch(w http.ResponseWriter, r *http.Request) (string, bool) {
	raw := r.Header.Get("If-Match")
	if strings.TrimSpace(raw) == "" {
		handler.WriteJSON(w, http.StatusPreconditionRequired, map[string]string{
			"code":  "precondition_required",
			"error": "If-Match is required for this skill mutation",
		})
		return "", false
	}
	revision, ok := normalizeSingleIfMatch(w, raw)
	if !ok {
		return "", false
	}
	if revision == "" {
		writePreconditionRequired(w)
		return "", false
	}
	return revision, true
}

func requiredFileWritePrecondition(w http.ResponseWriter, r *http.Request) (fileWritePrecondition, bool) {
	ifMatch := strings.TrimSpace(r.Header.Get("If-Match"))
	ifNoneMatch := strings.TrimSpace(r.Header.Get("If-None-Match"))
	if ifMatch != "" && ifNoneMatch != "" {
		writeBadPrecondition(w, "send exactly one of If-Match or If-None-Match: *")
		return fileWritePrecondition{}, false
	}
	if ifNoneMatch != "" {
		if ifNoneMatch != "*" {
			writeBadPrecondition(w, "If-None-Match on skill file creation must be exactly *")
			return fileWritePrecondition{}, false
		}
		return fileWritePrecondition{IfNoneMatchAny: true}, true
	}
	if ifMatch == "" {
		writePreconditionRequired(w)
		return fileWritePrecondition{}, false
	}
	revision, ok := normalizeSingleIfMatch(w, ifMatch)
	if !ok {
		return fileWritePrecondition{}, false
	}
	if revision == "" {
		writePreconditionRequired(w)
		return fileWritePrecondition{}, false
	}
	return fileWritePrecondition{IfMatch: revision}, true
}

func normalizeSingleIfMatch(w http.ResponseWriter, raw string) (string, bool) {
	if strings.Contains(raw, ",") {
		writeBadPrecondition(w, "If-Match must contain a single ETag or *; ETag lists are not supported")
		return "", false
	}
	revision, err := domain.NormalizeSkillRevision(raw)
	if err != nil {
		writeBadPrecondition(w, "If-Match must contain a single valid ETag or *")
		return "", false
	}
	return revision, true
}

func writePreconditionRequired(w http.ResponseWriter) {
	handler.WriteJSON(w, http.StatusPreconditionRequired, map[string]string{
		"code":  "precondition_required",
		"error": "send If-Match for an existing skill document or If-None-Match: * to create a bundled file",
	})
}

func writeBadPrecondition(w http.ResponseWriter, detail string) {
	handler.WriteJSON(w, http.StatusBadRequest, map[string]string{
		"code": "invalid_precondition", "error": detail,
	})
}

func validateSkillCreate(ref domain.SkillRef, req createSkillRequest) error {
	if err := ref.Validate(); err != nil {
		return err
	}
	if err := domain.ValidateSkillDescription(req.Description); err != nil {
		return err
	}
	if err := domain.ValidateSkillContent(req.Content); err != nil {
		return err
	}
	return domain.ValidateSkillProvenanceField("source_ref", req.SourceRef)
}

func (h *Handler) requireSkillRevision(w http.ResponseWriter, r *http.Request, ref domain.SkillRef, ifMatch string) bool {
	// SkillUpdate and SkillDelete do not yet carry a record-level conditional
	// token through the Store seam. This check rejects a stale body revision
	// before either operation; making the check atomic with the mutation needs a
	// future Store/fleet-db contract rather than an HTTP-only shim here.
	skill, err := h.Store.Skills().Get(r.Context(), requestWorkspaceID(r), ref)
	if err != nil {
		writeSkillError(w, err)
		return false
	}
	if ifMatch != "*" && ifMatch != skill.ContentRevision {
		writeSkillError(w, &domain.SkillPreconditionError{
			Ref: ref, Path: domain.SkillFileNameSKILLMD, Expected: ifMatch, Stored: skill.ContentRevision,
		})
		return false
	}
	return true
}

func writeWorkspaceScopeReadonly(w http.ResponseWriter) {
	handler.WriteJSON(w, http.StatusForbidden, map[string]string{
		"code":  "workspace_scope_readonly",
		"error": "workspace-scoped skills are read-only in the web UI; use `loom skill update` to edit them",
	})
}

func writeSkillFile(w http.ResponseWriter, status int, doc *domain.SkillDocument) {
	if doc == nil {
		writeSkillError(w, service.ErrInternal("skill store returned an empty document", nil))
		return
	}
	if doc.Revision != "" {
		w.Header().Set("ETag", strconv.Quote(doc.Revision))
	}
	handler.WriteJSON(w, status, skillFileResponse{
		Path:       doc.Path,
		Content:    doc.Content,
		Executable: doc.Executable,
		Revision:   doc.Revision,
		SkillRef:   doc.Ref.String(),
	})
}

func writeSkillDetail(w http.ResponseWriter, status int, skill *domain.Skill) {
	if skill == nil {
		writeSkillError(w, service.ErrInternal("skill store returned an empty skill", nil))
		return
	}
	if skill.ContentRevision != "" {
		w.Header().Set("ETag", strconv.Quote(skill.ContentRevision))
	}
	handler.WriteJSON(w, status, projectSkillDetail(skill))
}

func projectCatalog(skills []*domain.Skill) catalogResponse {
	groups := make(map[string]*catalogGroup)
	for _, skill := range skills {
		if skill == nil {
			continue
		}
		key := string(skill.Scope) + "\x00" + skill.RoleName
		group := groups[key]
		if group == nil {
			group = &catalogGroup{Scope: skill.Scope, Role: skill.RoleName, Skills: []catalogSkill{}}
			groups[key] = group
		}
		group.Skills = append(group.Skills, projectCatalogSkill(skill))
	}
	out := catalogResponse{Groups: make([]catalogGroup, 0, len(groups))}
	for _, group := range groups {
		sort.Slice(group.Skills, func(i, j int) bool { return group.Skills[i].Name < group.Skills[j].Name })
		out.Groups = append(out.Groups, *group)
	}
	sort.Slice(out.Groups, func(i, j int) bool {
		if out.Groups[i].Scope != out.Groups[j].Scope {
			return out.Groups[i].Scope == domain.SkillScopeWorkspace
		}
		return out.Groups[i].Role < out.Groups[j].Role
	})
	return out
}

func projectCatalogSkill(skill *domain.Skill) catalogSkill {
	return catalogSkill{
		Name:            skill.Name,
		Scope:           skill.Scope,
		Role:            skill.RoleName,
		Description:     skill.Description,
		ContentRevision: skill.ContentRevision,
		Files:           projectCatalogFiles(skill.Files),
		CreatedBy:       skill.CreatedBy,
		UpdatedBy:       skill.UpdatedBy,
		Source:          skill.Source,
		SourceRef:       skill.SourceRef,
		CreatedAt:       skill.CreatedAt,
		UpdatedAt:       skill.UpdatedAt,
	}
}

func projectSkillDetail(skill *domain.Skill) skillDetailResponse {
	entry := projectCatalogSkill(skill)
	return skillDetailResponse{
		Name:            entry.Name,
		Scope:           entry.Scope,
		Role:            entry.Role,
		Description:     entry.Description,
		Content:         skill.Content,
		ContentRevision: entry.ContentRevision,
		Files:           entry.Files,
		CreatedBy:       entry.CreatedBy,
		UpdatedBy:       entry.UpdatedBy,
		Source:          entry.Source,
		SourceRef:       entry.SourceRef,
		CreatedAt:       entry.CreatedAt,
		UpdatedAt:       entry.UpdatedAt,
	}
}

func projectCatalogFiles(files []domain.SkillFile) []catalogFile {
	out := make([]catalogFile, 0, len(files))
	for _, file := range files {
		out = append(out, catalogFile{Path: file.Path, Revision: file.Revision, Executable: file.Executable})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out
}
