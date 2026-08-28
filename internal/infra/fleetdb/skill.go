package fleetdb

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/store"
)

// fleet-db error codes this file classifies. The two are the whole reason
// skill writes do not collapse into a generic conflict: one is recoverable by
// re-reading and merging, the other is not recoverable at all.
const (
	skillProvenanceConflictCode = "skill_provenance_conflict"
	preconditionFailedCode      = "precondition_failed"
)

const (
	ifMatchHeader = "If-Match"
	etagHeader    = "ETag"
)

type skillStore struct{ client *Client }

type skillPackStore struct{ client *Client }

var (
	_ store.SkillStore     = (*skillStore)(nil)
	_ store.SkillPackStore = (*skillPackStore)(nil)
)

// skillWire mirrors fleet-db's file-tree-only models.Skill JSON shape.
type skillWire struct {
	WorkspaceKey     string    `json:"workspace_key"`
	Name             string    `json:"name"`
	Scope            string    `json:"scope"`
	RoleName         string    `json:"role_name,omitempty"`
	Description      string    `json:"description"`
	FileTreeRevision string    `json:"file_tree_revision"`
	CreatedBy        string    `json:"created_by,omitempty"`
	UpdatedBy        string    `json:"updated_by,omitempty"`
	Source           string    `json:"source,omitempty"`
	SourceRef        string    `json:"source_ref,omitempty"`
	CreatedAt        time.Time `json:"created_at"`
	UpdatedAt        time.Time `json:"updated_at"`
}

func (s skillWire) toDomain() *domain.Skill {
	return &domain.Skill{
		WorkspaceKey: s.WorkspaceKey, Name: s.Name, Scope: domain.SkillScope(s.Scope), RoleName: s.RoleName,
		Description: s.Description, FileTreeRevision: s.FileTreeRevision,
		CreatedBy: s.CreatedBy, UpdatedBy: s.UpdatedBy, Source: s.Source, SourceRef: s.SourceRef,
		CreatedAt: s.CreatedAt, UpdatedAt: s.UpdatedAt,
	}
}

type createSkillBody struct {
	Name             string `json:"name,omitempty"`
	Description      string `json:"description"`
	FileTreeRevision string `json:"file_tree_revision"`
	Source           string `json:"source,omitempty"`
	SourceRef        string `json:"source_ref,omitempty"`
}

// newCreateSkillBody builds the create/upsert body. withName is false on the
// upsert routes, where the path already names the skill and a body that also
// names one must agree with it — omitting it removes the only way the two can
// disagree.
func newCreateSkillBody(in store.SkillCreate, withName bool) createSkillBody {
	body := createSkillBody{
		Description: in.Description, FileTreeRevision: in.FileTreeRevision,
		Source: in.Source, SourceRef: in.SourceRef,
	}
	if withName {
		body.Name = in.Ref.Name
	}
	return body
}

type updateSkillBody struct {
	Description      *string `json:"description,omitempty"`
	FileTreeRevision *string `json:"file_tree_revision,omitempty"`
	Source           string  `json:"source,omitempty"`
	SourceRef        *string `json:"source_ref,omitempty"`
}

// ifMatchHeaderValue renders a caller's revision as the entity-tag the wire
// carries.
//
// It normalizes first, and that is the whole point of the function. The store
// contract says IfMatch holds a bare revision, but the realistic caller is a
// web handler holding `If-Match: "abc"` off a browser request; quoting that
// again yields `""abc""`, which no revision can ever equal, so every write
// 412s forever with an error that reads like a genuine conflict. That is the
// exact bug fleet-db shipped and fixed on its own side of this header, and
// re-creating it one layer up would be no better. Normalizing makes the two
// forms one form; wildcard, weak and multi-tag conditions are rejected because
// Fleet accepts exactly one quoted strong current tree revision.
func ifMatchHeaderValue(revision string) (string, error) {
	normalized, err := domain.NormalizeSkillTreeRevision(revision)
	if err != nil {
		return "", err
	}
	if normalized == "" {
		return "", fmt.Errorf("expected skill file tree revision is required: %w", domain.ErrInvalid)
	}
	return strconv.Quote(normalized), nil
}

// parseETag turns a strong ETag response header into the bare opaque revision.
func parseETag(header string) string {
	header = strings.TrimSpace(header)
	if len(header) < 2 || !strings.HasPrefix(header, `"`) || !strings.HasSuffix(header, `"`) {
		return ""
	}
	revision, err := domain.NormalizeSkillTreeRevision(header)
	if err != nil {
		return ""
	}
	return revision
}

// --- paths ---
//
// The scope decides the route family, and that is not cosmetic: the
// workspace lane carries skill.workspace_write while the role lane carries
// skill.create/update, so which lane a call takes is what the server
// authorizes against.

func skillCollectionPath(ws string, ref domain.SkillRef) string {
	if ref.Scope == domain.SkillScopeRole {
		return "/api/v1/" + pathEscape(ws) + "/roles/" + pathEscape(ref.RoleName) + "/skills"
	}
	return "/api/v1/" + pathEscape(ws) + "/skills"
}

func skillItemPath(ws string, ref domain.SkillRef) string {
	return skillCollectionPath(ws, ref) + "/" + pathEscape(ref.Name)
}

func sourceQuery(source string) url.Values {
	q := url.Values{}
	if source != "" {
		q.Set("source", source)
	}
	return q
}

// --- SkillStore ---

func (s *skillStore) Create(ctx context.Context, in store.SkillCreate) (*domain.Skill, error) {
	if err := validateSkillCreateInput(in); err != nil {
		return nil, err
	}
	var resp skillWire
	path := skillCollectionPath(in.WorkspaceKey, in.Ref)
	_, headers, err := s.client.doWithResponseNoRedirect(ctx, http.MethodPost, path, newCreateSkillBody(in, true), &resp, nil)
	if err != nil {
		return nil, err
	}
	return validateSkillResponse(resp, headers.Get(etagHeader), in.WorkspaceKey, in.Ref, true)
}

func (s *skillStore) Get(ctx context.Context, ws string, ref domain.SkillRef) (*domain.Skill, error) {
	if err := validateSkillRoute(ws, ref); err != nil {
		return nil, err
	}
	var resp skillWire
	_, headers, err := s.client.doWithResponse(ctx, http.MethodGet, skillItemPath(ws, ref), nil, &resp, nil)
	if err != nil {
		return nil, err
	}
	return validateSkillResponse(resp, headers.Get(etagHeader), ws, ref, true)
}

// List returns skills in the workspace. An empty filter returns BOTH scopes in
// one call, which is what domain.ResolveSkillChain wants: an agent's scope
// chain is the workspace skills plus its own role's, and asking for them
// separately would be two round trips and a window for them to disagree.
func (s *skillStore) List(ctx context.Context, ws string, filter store.SkillFilter) ([]*domain.Skill, error) {
	if err := validateSkillPathSegment("workspace", ws); err != nil {
		return nil, err
	}
	if filter.RoleName != "" {
		if err := domain.ValidateRoleName(filter.RoleName); err != nil {
			return nil, err
		}
	}
	q := url.Values{}
	if filter.Scope != "" {
		q.Set("scope", string(filter.Scope))
	}
	if filter.RoleName != "" {
		q.Set("role", filter.RoleName)
	}
	var resp struct {
		Skills []skillWire `json:"skills"`
	}
	path := withQuery("/api/v1/"+pathEscape(ws)+"/skills", q)
	if err := s.client.do(ctx, http.MethodGet, path, nil, &resp); err != nil {
		return nil, err
	}
	out := make([]*domain.Skill, 0, len(resp.Skills))
	for _, sk := range resp.Skills {
		item, err := validateSkillResponse(sk, "", ws, sk.toDomain().Ref(), false)
		if err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, nil
}

func (s *skillStore) Upsert(ctx context.Context, in store.SkillUpsert) (*domain.Skill, bool, error) {
	if err := validateSkillCreateInput(in.Skill); err != nil {
		return nil, false, err
	}
	method := http.MethodPut
	path := skillItemPath(in.Skill.WorkspaceKey, in.Skill.Ref)
	if in.Force {
		// Force is a separate route rather than a flag because the server's
		// authorizer binds one permission to one route and cannot read a body
		// or a query string.
		method, path = http.MethodPost, path+"/force-upsert"
	}
	var resp skillWire
	status, headers, err := s.client.doWithResponseNoRedirect(ctx, method, path, newCreateSkillBody(in.Skill, false), &resp, nil)
	if err != nil {
		return nil, false, err
	}
	item, err := validateSkillResponse(resp, headers.Get(etagHeader), in.Skill.WorkspaceKey, in.Skill.Ref, true)
	return item, status == http.StatusCreated, err
}

func (s *skillStore) Update(ctx context.Context, ws string, ref domain.SkillRef, patch store.SkillUpdate) (*domain.Skill, error) {
	if err := validateSkillRoute(ws, ref); err != nil {
		return nil, err
	}
	if patch.Description != nil && patch.FileTreeRevision == nil {
		return nil, fmt.Errorf("skill description change requires a file_tree_revision CAS: %w", domain.ErrInvalid)
	}
	body := updateSkillBody{
		Description: patch.Description, FileTreeRevision: patch.FileTreeRevision,
		Source: patch.Source, SourceRef: patch.SourceRef,
	}
	var headers map[string]string
	if patch.FileTreeRevision != nil {
		if *patch.FileTreeRevision == "" {
			return nil, fmt.Errorf("skill file_tree_revision is required: %w", domain.ErrInvalid)
		}
		value, err := ifMatchHeaderValue(patch.ExpectedFileTreeRevision)
		if err != nil {
			return nil, err
		}
		headers = map[string]string{ifMatchHeader: value}
	}
	var resp skillWire
	_, responseHeaders, err := s.client.doWithResponseNoRedirect(ctx, http.MethodPatch, skillItemPath(ws, ref), body, &resp, headers)
	if err != nil {
		return nil, err
	}
	return validateSkillResponse(resp, responseHeaders.Get(etagHeader), ws, ref, true)
}

func (s *skillStore) Delete(ctx context.Context, ws string, ref domain.SkillRef, del store.SkillDelete) error {
	if err := validateSkillRoute(ws, ref); err != nil {
		return err
	}
	// Delete is guarded exactly like an overwrite, and force-delete is its own
	// route for the same reason force-upsert is: delete-then-recreate IS an
	// ownership transfer, so it has to cost what an overwrite costs.
	method, path := http.MethodDelete, skillItemPath(ws, ref)
	if del.Force {
		method, path = http.MethodPost, path+"/force-delete"
	}
	_, _, err := s.client.doWithResponseNoRedirect(ctx, method, withQuery(path, sourceQuery(del.Source)), nil, nil, nil)
	return err
}

// validateSkillRoute is the adapter's final defense before constructing a URL.
// Domain validation protects role/name; the segment check also protects the
// workspace key supplied separately to the Store method.
func validateSkillRoute(ws string, ref domain.SkillRef) error {
	if err := validateSkillPathSegment("workspace", ws); err != nil {
		return err
	}
	if err := ref.Validate(); err != nil {
		return err
	}
	return nil
}

func validateSkillCreateInput(in store.SkillCreate) error {
	if err := validateSkillRoute(in.WorkspaceKey, in.Ref); err != nil {
		return err
	}
	if err := domain.ValidateSkillDescription(in.Description); err != nil {
		return err
	}
	if in.FileTreeRevision == "" {
		return fmt.Errorf("skill file_tree_revision is required: %w", domain.ErrInvalid)
	}
	return nil
}

func validateSkillPathSegment(field, value string) error {
	if strings.TrimSpace(value) == "" || value == "." || value == ".." || strings.ContainsAny(value, `/\`) {
		return fmt.Errorf("skill %s %q must be a safe path segment: %w", field, value, domain.ErrInvalid)
	}
	for _, r := range value {
		if unicode.IsControl(r) {
			return fmt.Errorf("skill %s %q contains a control character: %w", field, value, domain.ErrInvalid)
		}
	}
	escaped := pathEscape(value)
	if escaped == "." || escaped == ".." || strings.Contains(escaped, "/") {
		return fmt.Errorf("skill %s %q escapes to an unsafe path segment: %w", field, value, domain.ErrInvalid)
	}
	return nil
}

func validateSkillResponse(wire skillWire, etag, workspace string, ref domain.SkillRef, requireETag bool) (*domain.Skill, error) {
	skill := wire.toDomain()
	if err := skill.Ref().Validate(); err != nil {
		return nil, fmt.Errorf("skill response ref: %w", err)
	}
	if skill.WorkspaceKey != workspace || skill.Ref() != ref {
		return nil, fmt.Errorf("skill response identity %q/%s does not match %q/%s: %w", skill.WorkspaceKey, skill.Ref(), workspace, ref, domain.ErrIntegrity)
	}
	if err := domain.ValidateSkillDescription(skill.Description); err != nil {
		return nil, fmt.Errorf("skill response description: %w", err)
	}
	if skill.FileTreeRevision == "" {
		return nil, fmt.Errorf("skill response file_tree_revision is required: %w", domain.ErrIntegrity)
	}
	if requireETag && etag == "" {
		return nil, fmt.Errorf("skill response ETag is required: %w", domain.ErrIntegrity)
	}
	if etag != "" && parseETag(etag) != skill.FileTreeRevision {
		return nil, fmt.Errorf("skill response ETag %q does not match file_tree_revision %q: %w", etag, skill.FileTreeRevision, domain.ErrIntegrity)
	}
	return skill, nil
}

// --- error classification ---

// skillProvenanceConflictError renders fleet-db's 409 skill_provenance_conflict
// as a typed error carrying both owners. A bulk import cannot assemble its
// "skipped these, owned by those" report out of a message string, so the meta
// is unpacked here rather than left for a caller to parse.
func skillProvenanceConflictError(prefix string, body []byte) error {
	meta := extractErrorMeta(body)
	out := &domain.SkillProvenanceConflictError{
		Ref:               skillRefFromMeta(meta),
		Message:           prefix,
		ExistingCreatedBy: meta["existing_created_by"],
		ExistingSource:    meta["existing_source"],
		ExistingSourceRef: meta["existing_source_ref"],
		IncomingCreatedBy: meta["incoming_created_by"],
		IncomingSource:    meta["incoming_source"],
	}
	if ts, err := time.Parse(time.RFC3339Nano, meta["existing_updated_at"]); err == nil {
		out.ExistingUpdatedAt = ts
	}
	return out
}

// skillPreconditionError renders fleet-db's 412 precondition_failed, carrying
// the revision the caller held and the one on record so an editor can offer a
// diff without a second round trip.
func skillPreconditionError(prefix string, body []byte) error {
	meta := extractErrorMeta(body)
	ref, _ := domain.ParseSkillRef(meta["ref"])
	return &domain.SkillPreconditionError{
		Ref:      ref,
		Message:  prefix,
		Expected: meta["expected_revision"],
		Stored:   meta["stored_revision"],
	}
}

// skillRefFromMeta prefers the split scope/role/name fields and falls back to
// the composed ref, so the error still identifies its skill if the server ever
// sends only one of the two.
func skillRefFromMeta(meta map[string]string) domain.SkillRef {
	ref := domain.SkillRef{
		Scope:    domain.SkillScope(meta["scope"]),
		RoleName: meta["role_name"],
		Name:     meta["name"],
	}
	if ref.Validate() == nil {
		return ref
	}
	parsed, err := domain.ParseSkillRef(meta["ref"])
	if err != nil {
		return domain.SkillRef{}
	}
	return parsed
}

// --- SkillPackStore ---

// skillPackWire mirrors fleet-db's models.SkillPack JSON shape.
type skillPackWire struct {
	WorkspaceKey     string    `json:"workspace_key"`
	Name             string    `json:"name"`
	RepoURL          string    `json:"repo_url"`
	Ref              string    `json:"ref,omitempty"`
	Path             string    `json:"path,omitempty"`
	Description      string    `json:"description,omitempty"`
	CreatedBy        string    `json:"created_by,omitempty"`
	LastSyncedAt     time.Time `json:"last_synced_at,omitempty"`
	LastSyncedCommit string    `json:"last_synced_commit,omitempty"`
	LastSyncStatus   string    `json:"last_sync_status,omitempty"`
	LastSyncError    string    `json:"last_sync_error,omitempty"`
	LastSyncedSkills []string  `json:"last_synced_skills,omitempty"`
	CreatedAt        time.Time `json:"created_at"`
	UpdatedAt        time.Time `json:"updated_at"`
}

func (p skillPackWire) toDomain() *domain.SkillPack {
	return &domain.SkillPack{
		WorkspaceKey:     p.WorkspaceKey,
		Name:             p.Name,
		RepoURL:          p.RepoURL,
		Ref:              p.Ref,
		Path:             p.Path,
		Description:      p.Description,
		CreatedBy:        p.CreatedBy,
		LastSyncedAt:     p.LastSyncedAt,
		LastSyncedCommit: p.LastSyncedCommit,
		LastSyncStatus:   p.LastSyncStatus,
		LastSyncError:    p.LastSyncError,
		LastSyncedSkills: append([]string(nil), p.LastSyncedSkills...),
		CreatedAt:        p.CreatedAt,
		UpdatedAt:        p.UpdatedAt,
	}
}

func skillPacksPath(ws string) string { return "/api/v1/" + pathEscape(ws) + "/skill-packs" }

func (s *skillPackStore) Create(ctx context.Context, in store.SkillPackCreate) (*domain.SkillPack, error) {
	body := struct {
		Name        string `json:"name"`
		RepoURL     string `json:"repo_url"`
		Ref         string `json:"ref,omitempty"`
		Path        string `json:"path,omitempty"`
		Description string `json:"description,omitempty"`
	}{Name: in.Name, RepoURL: in.RepoURL, Ref: in.Ref, Path: in.Path, Description: in.Description}
	var resp skillPackWire
	if err := s.client.do(ctx, http.MethodPost, skillPacksPath(in.WorkspaceKey), body, &resp); err != nil {
		return nil, err
	}
	return resp.toDomain(), nil
}

func (s *skillPackStore) Get(ctx context.Context, ws, name string) (*domain.SkillPack, error) {
	var resp skillPackWire
	path := skillPacksPath(ws) + "/" + pathEscape(name)
	if err := s.client.do(ctx, http.MethodGet, path, nil, &resp); err != nil {
		return nil, err
	}
	return resp.toDomain(), nil
}

func (s *skillPackStore) List(ctx context.Context, ws string) ([]*domain.SkillPack, error) {
	var resp struct {
		SkillPacks []skillPackWire `json:"skill_packs"`
	}
	if err := s.client.do(ctx, http.MethodGet, skillPacksPath(ws), nil, &resp); err != nil {
		return nil, err
	}
	out := make([]*domain.SkillPack, 0, len(resp.SkillPacks))
	for _, p := range resp.SkillPacks {
		out = append(out, p.toDomain())
	}
	return out, nil
}

func (s *skillPackStore) Update(ctx context.Context, ws, name string, patch store.SkillPackUpdate) (*domain.SkillPack, error) {
	body := struct {
		RepoURL     *string               `json:"repo_url,omitempty"`
		Ref         *string               `json:"ref,omitempty"`
		Path        *string               `json:"path,omitempty"`
		Description *string               `json:"description,omitempty"`
		RecordSync  *domain.SkillPackSync `json:"record_sync,omitempty"`
	}{
		RepoURL: patch.RepoURL, Ref: patch.Ref, Path: patch.Path,
		Description: patch.Description, RecordSync: patch.RecordSync,
	}
	var resp skillPackWire
	path := skillPacksPath(ws) + "/" + pathEscape(name)
	if err := s.client.do(ctx, http.MethodPatch, path, body, &resp); err != nil {
		return nil, err
	}
	return resp.toDomain(), nil
}

func (s *skillPackStore) Delete(ctx context.Context, ws, name string) error {
	return s.client.do(ctx, http.MethodDelete, skillPacksPath(ws)+"/"+pathEscape(name), nil, nil)
}
