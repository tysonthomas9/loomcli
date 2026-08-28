package skills

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/store"
	"github.com/tysonthomas9/loomcli/internal/webui/server/handler"
	"github.com/tysonthomas9/loomcli/internal/webui/server/middleware"
	"github.com/tysonthomas9/loomcli/internal/webui/service"
)

const webuiSkillSource = "webui"

// Handler fronts the canonical Store seams. Skill records contain only a
// pointer; immutable file manifests and bytes belong to WorkspaceFiles.
type Handler struct{ Store store.Store }

type catalogFile struct {
	Path         string `json:"path"`
	Revision     string `json:"revision"`
	SizeBytes    int64  `json:"size_bytes"`
	MediaType    string `json:"media_type,omitempty"`
	Executable   bool   `json:"executable"`
	TextEditable bool   `json:"text_editable"`
}

type catalogSkill struct {
	Name             string            `json:"name"`
	Scope            domain.SkillScope `json:"scope"`
	Role             string            `json:"role,omitempty"`
	Description      string            `json:"description"`
	FileTreeRevision string            `json:"file_tree_revision"`
	Files            []catalogFile     `json:"files"`
	CreatedBy        string            `json:"created_by,omitempty"`
	UpdatedBy        string            `json:"updated_by,omitempty"`
	Source           string            `json:"source,omitempty"`
	SourceRef        string            `json:"source_ref,omitempty"`
	CreatedAt        time.Time         `json:"created_at"`
	UpdatedAt        time.Time         `json:"updated_at"`
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
	catalogSkill
	Content string `json:"content"`
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
	workspace := requestWorkspaceID(r)
	skills, err := h.Store.Skills().List(r.Context(), workspace, store.SkillFilter{})
	if err != nil {
		writeSkillError(w, err)
		return
	}
	projected, err := h.projectCatalog(r.Context(), workspace, skills)
	if err != nil {
		writeSkillError(w, err)
		return
	}
	handler.WriteJSON(w, http.StatusOK, projected)
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
	workspace := requestWorkspaceID(r)
	skill, err := h.Store.Skills().Get(r.Context(), workspace, ref)
	if err != nil {
		writeSkillError(w, err)
		return
	}
	loaded, err := h.loadTree(r.Context(), workspace, skill.FileTreeRevision)
	if err != nil {
		writeSkillError(w, err)
		return
	}
	writeSkillDetail(w, http.StatusOK, skill, loaded.tree, string(loaded.snapshot.Body))
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
	snapshot, err := domain.BuildSkillFileTree(req.Name, req.Description, []byte(req.Content), nil)
	if err != nil {
		writeSkillError(w, err)
		return
	}
	workspace := requestWorkspaceID(r)
	tree, err := h.publishTree(r.Context(), workspace, snapshot.Files)
	if err != nil {
		writeSkillError(w, err)
		return
	}
	skill, err := h.Store.Skills().Create(r.Context(), store.SkillCreate{
		WorkspaceKey: workspace, Ref: ref, Description: req.Description,
		FileTreeRevision: tree.Revision, Source: webuiSkillSource, SourceRef: req.SourceRef,
	})
	if err != nil {
		writeSkillError(w, err)
		return
	}
	writeSkillDetail(w, http.StatusCreated, skill, tree, req.Content)
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
	req, ok := readPatchSkillRequest(w, r)
	if !ok {
		return
	}
	workspace := requestWorkspaceID(r)
	skill, err := h.Store.Skills().Get(r.Context(), workspace, ref)
	if err != nil {
		writeSkillError(w, err)
		return
	}
	if !requireCurrentTree(w, ref, ifMatch, skill.FileTreeRevision) {
		return
	}
	patch := store.SkillUpdate{
		Description: req.Description, SourceRef: req.SourceRef, Source: webuiSkillSource,
		ExpectedFileTreeRevision: skill.FileTreeRevision,
	}
	var tree *domain.WorkspaceFileTree
	content := ""
	if req.Description != nil {
		tree, content, err = h.rebuildDescriptionTree(r.Context(), workspace, skill, *req.Description)
		if err != nil {
			writeSkillError(w, err)
			return
		}
		patch.FileTreeRevision = &tree.Revision
	}
	updated, err := h.Store.Skills().Update(r.Context(), workspace, ref, patch)
	if err != nil {
		writeSkillError(w, err)
		return
	}
	if tree == nil {
		loaded, loadErr := h.loadTree(r.Context(), workspace, updated.FileTreeRevision)
		if loadErr != nil {
			writeSkillError(w, loadErr)
			return
		}
		tree, content = loaded.tree, string(loaded.snapshot.Body)
	}
	writeSkillDetail(w, http.StatusOK, updated, tree, content)
}

func readPatchSkillRequest(w http.ResponseWriter, r *http.Request) (patchSkillRequest, bool) {
	var req patchSkillRequest
	if err := handler.ReadJSON(w, r, &req); err != nil {
		writeSkillError(w, err)
		return patchSkillRequest{}, false
	}
	if req.Description != nil {
		if err := domain.ValidateSkillDescription(*req.Description); err != nil {
			writeSkillError(w, err)
			return patchSkillRequest{}, false
		}
	}
	if req.SourceRef != nil {
		if err := domain.ValidateSkillProvenanceField("source_ref", *req.SourceRef); err != nil {
			writeSkillError(w, err)
			return patchSkillRequest{}, false
		}
	}
	return req, true
}

func (h *Handler) rebuildDescriptionTree(ctx context.Context, workspace string, skill *domain.Skill, description string) (*domain.WorkspaceFileTree, string, error) {
	loaded, err := h.loadTree(ctx, workspace, skill.FileTreeRevision)
	if err != nil {
		return nil, "", err
	}
	snapshot, err := domain.BuildSkillFileTree(skill.Name, description, loaded.snapshot.Body, bundledFiles(loaded.snapshot.Files))
	if err != nil {
		return nil, "", err
	}
	tree, err := h.publishTree(ctx, workspace, snapshot.Files)
	return tree, string(snapshot.Body), err
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
	skill, err := h.Store.Skills().Get(r.Context(), requestWorkspaceID(r), ref)
	if err != nil {
		writeSkillError(w, err)
		return
	}
	if !requireCurrentTree(w, ref, ifMatch, skill.FileTreeRevision) {
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
	workspace := requestWorkspaceID(r)
	skill, err := h.Store.Skills().Get(r.Context(), workspace, ref)
	if err != nil {
		writeSkillError(w, err)
		return
	}
	meta, err := h.Store.WorkspaceFiles().Stat(r.Context(), workspace, skill.FileTreeRevision, filePath)
	if err != nil {
		writeSkillError(w, err)
		return
	}
	body, err := h.Store.WorkspaceFiles().Download(r.Context(), workspace, skill.FileTreeRevision, filePath)
	if err != nil {
		writeSkillError(w, err)
		return
	}
	if !utf8.Valid(body) || bytes.IndexByte(body, 0) >= 0 {
		writeBinarySkillFileUnsupported(w, meta)
		return
	}
	if filePath == domain.SkillFileNameSKILLMD {
		loaded, loadErr := h.loadTree(r.Context(), workspace, skill.FileTreeRevision)
		if loadErr != nil {
			writeSkillError(w, loadErr)
			return
		}
		body = loaded.snapshot.Body
	}
	writeSkillFile(w, http.StatusOK, ref, filePath, body, meta.Executable, skill.FileTreeRevision)
}

func (h *Handler) putRoleSkillFile(w http.ResponseWriter, r *http.Request) {
	ref, filePath, err := requestSkillFile(r, domain.SkillScopeRole)
	if err != nil {
		writeSkillError(w, err)
		return
	}
	precondition, ok := requiredFileWritePrecondition(w, r)
	if !ok {
		return
	}
	req, ok := readPutSkillFileRequest(w, r)
	if !ok {
		return
	}
	workspace := requestWorkspaceID(r)
	skill, err := h.Store.Skills().Get(r.Context(), workspace, ref)
	if err != nil {
		writeSkillError(w, err)
		return
	}
	if precondition.IfMatch != "" && !requireCurrentTree(w, ref, precondition.IfMatch, skill.FileTreeRevision) {
		return
	}
	loaded, err := h.loadTree(r.Context(), workspace, skill.FileTreeRevision)
	if err != nil {
		writeSkillError(w, err)
		return
	}
	if precondition.IfNoneMatchAny && hasTreeFile(loaded.snapshot.Files, filePath) {
		writeSkillError(w, &domain.SkillPreconditionError{Ref: ref, Expected: "file must not exist", Stored: skill.FileTreeRevision})
		return
	}
	files, err := updatedTreeFiles(skill, loaded.snapshot.Files, filePath, req)
	if err != nil {
		writeSkillError(w, err)
		return
	}
	tree, err := h.publishTree(r.Context(), workspace, files)
	if err != nil {
		writeSkillError(w, err)
		return
	}
	updated, err := h.Store.Skills().Update(r.Context(), workspace, ref, store.SkillUpdate{
		FileTreeRevision: &tree.Revision, ExpectedFileTreeRevision: skill.FileTreeRevision, Source: webuiSkillSource,
	})
	if err != nil {
		writeSkillError(w, err)
		return
	}
	writeSkillFile(w, skillFilePutStatus(precondition), ref, filePath, []byte(req.Content), filePath != domain.SkillFileNameSKILLMD && req.Executable, updated.FileTreeRevision)
}

func skillFilePutStatus(precondition fileWritePrecondition) int {
	if precondition.IfNoneMatchAny {
		return http.StatusCreated
	}
	return http.StatusOK
}

func readPutSkillFileRequest(w http.ResponseWriter, r *http.Request) (putSkillFileRequest, bool) {
	var req putSkillFileRequest
	if err := handler.ReadJSON(w, r, &req); err != nil {
		writeSkillError(w, err)
		return putSkillFileRequest{}, false
	}
	return req, true
}

func updatedTreeFiles(skill *domain.Skill, current []domain.SkillFileTreeFile, path string, req putSkillFileRequest) ([]domain.SkillFileTreeFile, error) {
	if path == domain.SkillFileNameSKILLMD {
		snapshot, err := domain.ValidateSkillFileTree(current)
		if err != nil {
			return nil, err
		}
		for _, file := range snapshot.Files {
			if file.Path != domain.SkillFileNameSKILLMD {
				continue
			}
			if !bytes.HasSuffix(file.Bytes, snapshot.Body) {
				return nil, fmt.Errorf("SKILL.md parsed body is not an exact byte suffix: %w", domain.ErrIntegrity)
			}
			prefix := file.Bytes[:len(file.Bytes)-len(snapshot.Body)]
			replacement := file
			replacement.Bytes = append(append([]byte(nil), prefix...), []byte(req.Content)...)
			files := replaceTreeFile(current, replacement)
			validated, err := domain.ValidateSkillFileTree(files)
			if err != nil {
				return nil, err
			}
			if validated.Name != skill.Name || validated.Description != skill.Description {
				return nil, fmt.Errorf("updated SKILL.md metadata mismatch: %w", domain.ErrIntegrity)
			}
			return validated.Files, nil
		}
		return nil, fmt.Errorf("SKILL.md is required: %w", domain.ErrIntegrity)
	}
	files := replaceTreeFile(current, domain.SkillFileTreeFile{
		Path: path, Bytes: []byte(req.Content), Executable: req.Executable, MediaType: mediaTypeForPath(path),
	})
	if _, err := domain.ValidateSkillFileTree(files); err != nil {
		return nil, err
	}
	return files, nil
}

func (h *Handler) deleteRoleSkillFile(w http.ResponseWriter, r *http.Request) {
	ref, filePath, err := requestSkillFile(r, domain.SkillScopeRole)
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
	workspace := requestWorkspaceID(r)
	skill, err := h.Store.Skills().Get(r.Context(), workspace, ref)
	if err != nil {
		writeSkillError(w, err)
		return
	}
	if !requireCurrentTree(w, ref, ifMatch, skill.FileTreeRevision) {
		return
	}
	loaded, err := h.loadTree(r.Context(), workspace, skill.FileTreeRevision)
	if err != nil {
		writeSkillError(w, err)
		return
	}
	if !hasTreeFile(loaded.snapshot.Files, filePath) {
		writeSkillError(w, domain.ErrNotFound)
		return
	}
	files := deleteTreeFile(loaded.snapshot.Files, filePath)
	if _, err := domain.ValidateSkillFileTree(files); err != nil {
		writeSkillError(w, err)
		return
	}
	tree, err := h.publishTree(r.Context(), workspace, files)
	if err != nil {
		writeSkillError(w, err)
		return
	}
	if _, err := h.Store.Skills().Update(r.Context(), workspace, ref, store.SkillUpdate{
		FileTreeRevision: &tree.Revision, ExpectedFileTreeRevision: skill.FileTreeRevision, Source: webuiSkillSource,
	}); err != nil {
		writeSkillError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) getCapabilities(w http.ResponseWriter, r *http.Request) {
	capabilities, ok := service.FileCapabilitiesFromContext(r.Context())
	handler.WriteJSON(w, http.StatusOK, capabilitiesResponse{CanEditRoleScope: ok && capabilities.Write, WorkspaceScope: "read_only"})
}

type loadedSkillTree struct {
	tree     *domain.WorkspaceFileTree
	snapshot *domain.SkillFileTreeSnapshot
}

func (h *Handler) loadTree(ctx context.Context, workspace, revision string) (*loadedSkillTree, error) {
	tree, err := h.Store.WorkspaceFiles().GetTree(ctx, workspace, revision)
	if err != nil {
		return nil, err
	}
	files := make([]domain.SkillFileTreeFile, 0, len(tree.Files))
	for _, meta := range tree.Files {
		body, downloadErr := h.Store.WorkspaceFiles().Download(ctx, workspace, revision, meta.Path)
		if downloadErr != nil {
			return nil, downloadErr
		}
		files = append(files, domain.SkillFileTreeFile{Path: meta.Path, Bytes: body, MediaType: meta.MediaType, Executable: meta.Executable})
	}
	snapshot, err := domain.ValidateSkillFileTree(files)
	if err != nil {
		return nil, err
	}
	return &loadedSkillTree{tree: tree, snapshot: snapshot}, nil
}

func (h *Handler) publishTree(ctx context.Context, workspace string, files []domain.SkillFileTreeFile) (*domain.WorkspaceFileTree, error) {
	inputs := make([]domain.WorkspaceFileInput, 0, len(files))
	for _, file := range files {
		inputs = append(inputs, domain.WorkspaceFileInput(file))
	}
	result, err := h.Store.WorkspaceFiles().Publish(ctx, workspace, inputs)
	if err != nil {
		return nil, err
	}
	if result == nil || result.Tree == nil {
		return nil, service.ErrInternal("workspace file store returned an empty tree", nil)
	}
	return result.Tree, nil
}

func bundledFiles(files []domain.SkillFileTreeFile) []domain.SkillFileTreeFile {
	out := make([]domain.SkillFileTreeFile, 0, len(files))
	for _, file := range files {
		if file.Path != domain.SkillFileNameSKILLMD {
			out = append(out, file)
		}
	}
	return out
}
func hasTreeFile(files []domain.SkillFileTreeFile, path string) bool {
	for _, file := range files {
		if file.Path == path {
			return true
		}
	}
	return false
}
func replaceTreeFile(files []domain.SkillFileTreeFile, replacement domain.SkillFileTreeFile) []domain.SkillFileTreeFile {
	out := make([]domain.SkillFileTreeFile, 0, len(files)+1)
	replaced := false
	for _, file := range files {
		if file.Path == replacement.Path {
			out = append(out, replacement)
			replaced = true
		} else {
			out = append(out, file)
		}
	}
	if !replaced {
		out = append(out, replacement)
	}
	return out
}
func deleteTreeFile(files []domain.SkillFileTreeFile, path string) []domain.SkillFileTreeFile {
	out := make([]domain.SkillFileTreeFile, 0, len(files)-1)
	for _, file := range files {
		if file.Path != path {
			out = append(out, file)
		}
	}
	return out
}
func mediaTypeForPath(path string) string {
	if path == domain.SkillFileNameSKILLMD || strings.HasSuffix(strings.ToLower(path), ".md") {
		return "text/markdown"
	}
	return "text/plain"
}

func isTextEditableMediaType(mediaType string) bool {
	mediaType = strings.ToLower(strings.TrimSpace(strings.SplitN(mediaType, ";", 2)[0]))
	if strings.HasPrefix(mediaType, "text/") {
		return true
	}
	switch mediaType {
	case "application/json", "application/javascript", "application/xml", "application/yaml", "application/x-yaml":
		return true
	default:
		return false
	}
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
		handler.WriteJSON(w, http.StatusPreconditionRequired, map[string]string{"code": "precondition_required", "error": "If-Match is required for this skill mutation"})
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
	return fileWritePrecondition{IfMatch: revision}, ok
}
func normalizeSingleIfMatch(w http.ResponseWriter, raw string) (string, bool) {
	if strings.Contains(raw, ",") {
		writeBadPrecondition(w, "If-Match must contain a single ETag; ETag lists are not supported")
		return "", false
	}
	revision, err := domain.NormalizeSkillTreeRevision(raw)
	if err != nil {
		writeBadPrecondition(w, "If-Match must contain a single valid tree-revision ETag")
		return "", false
	}
	return revision, true
}
func writePreconditionRequired(w http.ResponseWriter) {
	handler.WriteJSON(w, http.StatusPreconditionRequired, map[string]string{"code": "precondition_required", "error": "send If-Match for an existing skill tree or If-None-Match: * to create a bundled file"})
}
func writeBadPrecondition(w http.ResponseWriter, detail string) {
	handler.WriteJSON(w, http.StatusBadRequest, map[string]string{"code": "invalid_precondition", "error": detail})
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
func requireCurrentTree(w http.ResponseWriter, ref domain.SkillRef, expected, stored string) bool {
	if expected == stored {
		return true
	}
	writeSkillError(w, &domain.SkillPreconditionError{Ref: ref, Expected: expected, Stored: stored})
	return false
}

func workspaceScopeReadonly(w http.ResponseWriter, _ *http.Request) { writeWorkspaceScopeReadonly(w) }
func writeWorkspaceScopeReadonly(w http.ResponseWriter) {
	handler.WriteJSON(w, http.StatusForbidden, map[string]string{"code": "workspace_scope_readonly", "error": "workspace-scoped skills are read-only in the web UI; use `loom skill update` to edit them"})
}
func writeBinarySkillFileUnsupported(w http.ResponseWriter, file *domain.WorkspaceFile) {
	handler.WriteJSON(w, http.StatusUnsupportedMediaType, map[string]any{
		"code": "binary_skill_file", "error": "binary skill files cannot be opened in the text editor",
		"path": file.Path, "size_bytes": file.SizeBytes, "media_type": file.MediaType,
	})
}
func writeSkillFile(w http.ResponseWriter, status int, ref domain.SkillRef, path string, body []byte, executable bool, treeRevision string) {
	if treeRevision != "" {
		w.Header().Set("ETag", strconv.Quote(treeRevision))
	}
	handler.WriteJSON(w, status, skillFileResponse{Path: path, Content: string(body), Executable: executable, Revision: treeRevision, SkillRef: ref.String()})
}
func writeSkillDetail(w http.ResponseWriter, status int, skill *domain.Skill, tree *domain.WorkspaceFileTree, content string) {
	if skill == nil || tree == nil {
		writeSkillError(w, service.ErrInternal("skill store returned an incomplete skill tree", nil))
		return
	}
	if skill.FileTreeRevision != "" {
		w.Header().Set("ETag", strconv.Quote(skill.FileTreeRevision))
	}
	handler.WriteJSON(w, status, skillDetailResponse{catalogSkill: projectCatalogSkill(skill, tree), Content: content})
}

func (h *Handler) projectCatalog(ctx context.Context, workspace string, skills []*domain.Skill) (catalogResponse, error) {
	groups := make(map[string]*catalogGroup)
	for _, skill := range skills {
		if skill == nil {
			continue
		}
		tree, err := h.Store.WorkspaceFiles().GetTree(ctx, workspace, skill.FileTreeRevision)
		if err != nil {
			return catalogResponse{}, fmt.Errorf("load file tree for %s: %w", skill.Ref(), err)
		}
		key := string(skill.Scope) + "\x00" + skill.RoleName
		group := groups[key]
		if group == nil {
			group = &catalogGroup{Scope: skill.Scope, Role: skill.RoleName, Skills: []catalogSkill{}}
			groups[key] = group
		}
		group.Skills = append(group.Skills, projectCatalogSkill(skill, tree))
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
	return out, nil
}
func projectCatalogSkill(skill *domain.Skill, tree *domain.WorkspaceFileTree) catalogSkill {
	return catalogSkill{
		Name: skill.Name, Scope: skill.Scope, Role: skill.RoleName, Description: skill.Description,
		FileTreeRevision: skill.FileTreeRevision, Files: projectCatalogFiles(tree.Files),
		CreatedBy: skill.CreatedBy, UpdatedBy: skill.UpdatedBy, Source: skill.Source, SourceRef: skill.SourceRef,
		CreatedAt: skill.CreatedAt, UpdatedAt: skill.UpdatedAt,
	}
}
func projectCatalogFiles(files []domain.WorkspaceFile) []catalogFile {
	out := make([]catalogFile, 0, len(files))
	for _, file := range files {
		if file.Path == domain.SkillFileNameSKILLMD {
			continue
		}
		out = append(out, catalogFile{
			Path: file.Path, Revision: file.Revision, SizeBytes: file.SizeBytes,
			MediaType: file.MediaType, Executable: file.Executable,
			TextEditable: isTextEditableMediaType(file.MediaType),
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out
}
