package fleetdb

import (
	"context"
	"encoding/json"
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
	ifMatchHeader     = "If-Match"
	ifNoneMatchHeader = "If-None-Match"
	etagHeader        = "ETag"
)

type skillStore struct{ client *Client }

type skillPackStore struct{ client *Client }

var (
	_ store.SkillStore     = (*skillStore)(nil)
	_ store.SkillPackStore = (*skillPackStore)(nil)
)

// skillWire mirrors fleet-db's models.Skill JSON shape.
//
// files carries domain.SkillFile directly rather than a local mirror, on the
// same reasoning roleWire carries domain.RoleInputPolicy directly: the JSON
// tags already match field for field, and a second copy of the type is a
// second place to get the revision field's quoting rule wrong.
type skillWire struct {
	WorkspaceKey    string             `json:"workspace_key"`
	Name            string             `json:"name"`
	Scope           string             `json:"scope"`
	RoleName        string             `json:"role_name,omitempty"`
	Description     string             `json:"description"`
	Content         string             `json:"content"`
	Files           []domain.SkillFile `json:"files,omitempty"`
	ContentRevision string             `json:"content_revision,omitempty"`
	CreatedBy       string             `json:"created_by,omitempty"`
	UpdatedBy       string             `json:"updated_by,omitempty"`
	Source          string             `json:"source,omitempty"`
	SourceRef       string             `json:"source_ref,omitempty"`
	CreatedAt       time.Time          `json:"created_at"`
	UpdatedAt       time.Time          `json:"updated_at"`
}

func (s skillWire) toDomain() *domain.Skill {
	return &domain.Skill{
		WorkspaceKey:    s.WorkspaceKey,
		Name:            s.Name,
		Scope:           domain.SkillScope(s.Scope),
		RoleName:        s.RoleName,
		Description:     s.Description,
		Content:         s.Content,
		Files:           append([]domain.SkillFile(nil), s.Files...),
		ContentRevision: s.ContentRevision,
		CreatedBy:       s.CreatedBy,
		UpdatedBy:       s.UpdatedBy,
		Source:          s.Source,
		SourceRef:       s.SourceRef,
		CreatedAt:       s.CreatedAt,
		UpdatedAt:       s.UpdatedAt,
	}
}

// skillFileBody is a bundled file on the way OUT. It drops the revision:
// revisions are derived server-side and a value sent on a write is ignored, so
// echoing one back would only suggest it meant something.
type skillFileBody struct {
	Path       string `json:"path"`
	Content    string `json:"content"`
	Executable bool   `json:"executable,omitempty"`
}

func skillFileBodies(files []domain.SkillFile) []skillFileBody {
	out := make([]skillFileBody, 0, len(files))
	for _, f := range files {
		out = append(out, skillFileBody{Path: f.Path, Content: f.Content, Executable: f.Executable})
	}
	return out
}

type createSkillBody struct {
	Name        string          `json:"name,omitempty"`
	Description string          `json:"description"`
	Content     string          `json:"content,omitempty"`
	Files       []skillFileBody `json:"files,omitempty"`
	Source      string          `json:"source,omitempty"`
	SourceRef   string          `json:"source_ref,omitempty"`
}

// newCreateSkillBody builds the create/upsert body. withName is false on the
// upsert routes, where the path already names the skill and a body that also
// names one must agree with it — omitting it removes the only way the two can
// disagree.
func newCreateSkillBody(in store.SkillCreate, withName bool) createSkillBody {
	body := createSkillBody{
		Description: in.Description,
		Content:     in.Content,
		Files:       skillFileBodies(in.Files),
		Source:      in.Source,
		SourceRef:   in.SourceRef,
	}
	if withName {
		body.Name = in.Ref.Name
	}
	return body
}

type updateSkillBody struct {
	Description *string          `json:"description,omitempty"`
	Content     *string          `json:"content,omitempty"`
	Files       *[]skillFileBody `json:"files,omitempty"`
	Source      string           `json:"source,omitempty"`
	SourceRef   *string          `json:"source_ref,omitempty"`
}

type putSkillFileBody struct {
	Content    string `json:"content"`
	Executable bool   `json:"executable,omitempty"`
	Source     string `json:"source,omitempty"`
}

// skillDocumentWire mirrors fleet-db's SkillFileResponse.
type skillDocumentWire struct {
	Path       string `json:"path"`
	Content    string `json:"content"`
	Executable bool   `json:"executable,omitempty"`
	Revision   string `json:"revision"`
	SkillRef   string `json:"skill_ref"`
}

// toDomain converts a per-document response, reconciling the two forms the
// revision arrives in.
//
// The server sends it twice: unquoted in the JSON `revision` field, and quoted
// in the ETag header, because RFC 9110 requires an entity-tag to be quoted.
// The body's value is the canonical one and wins; the header is only a
// fallback. What must never happen is the quoted form leaking upward, because
// a caller would then compare a quoted token against the unquoted one the list
// endpoint gives it, never match, and see a permanent conflict that looks
// exactly like a real one. (That is the client-side twin of the server bug
// that made conditional writes fail 100% of the time before parseETagList
// landed.)
func (d skillDocumentWire) toDomain(etag string) (*domain.SkillDocument, error) {
	ref, err := domain.ParseSkillRef(d.SkillRef)
	if err != nil {
		return nil, err
	}
	revision := d.Revision
	if revision == "" {
		revision = parseETag(etag)
	}
	return &domain.SkillDocument{
		Ref:        ref,
		Path:       d.Path,
		Content:    d.Content,
		Executable: d.Executable,
		Revision:   revision,
	}, nil
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
// forms one form; a multi-tag header is rejected rather than guessed at.
//
// "*" travels unquoted — it is a wildcard, not an entity-tag.
func ifMatchHeaderValue(revision string) (string, error) {
	normalized, err := domain.NormalizeSkillRevision(revision)
	if err != nil {
		return "", err
	}
	if normalized == "" || normalized == "*" {
		return normalized, nil
	}
	return strconv.Quote(normalized), nil
}

// conditionalWriteHeaders builds the precondition headers for one document
// write. An empty ifMatch sends no If-Match at all — the caller asserting no
// precondition — which fleet-db answers 428 for a mutation, deliberately.
func conditionalWriteHeaders(ifMatch string, ifNoneMatchAny bool) (map[string]string, error) {
	headers := map[string]string{}
	if ifMatch != "" {
		value, err := ifMatchHeaderValue(ifMatch)
		if err != nil {
			return nil, err
		}
		headers[ifMatchHeader] = value
	}
	if ifNoneMatchAny {
		headers[ifNoneMatchHeader] = "*"
	}
	return headers, nil
}

// parseETag turns an ETag response header into the bare revision. Weak tags
// are accepted per RFC 9110 §8.8.3: these revisions hash exact bytes, so weak
// and strong comparison give the same answer.
func parseETag(header string) string {
	revision, err := domain.NormalizeSkillRevision(header)
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

// skillDocumentPath addresses one document. The server's route ends in a
// `{path...}` wildcard, so the separators stay literal and only the segments
// are escaped — escaping the whole path would turn "references/api.md" into a
// single segment that matches nothing.
func skillDocumentPath(ws string, ref domain.SkillRef, filePath string) string {
	segments := strings.Split(filePath, "/")
	for i, segment := range segments {
		segments[i] = pathEscape(segment)
	}
	return skillItemPath(ws, ref) + "/files/" + strings.Join(segments, "/")
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
	if err := validateSkillRoute(in.WorkspaceKey, in.Ref); err != nil {
		return nil, err
	}
	var resp skillWire
	path := skillCollectionPath(in.WorkspaceKey, in.Ref)
	if _, _, err := s.client.doWithResponseNoRedirect(ctx, http.MethodPost, path, newCreateSkillBody(in, true), &resp, nil); err != nil {
		return nil, err
	}
	return resp.toDomain(), nil
}

func (s *skillStore) Get(ctx context.Context, ws string, ref domain.SkillRef) (*domain.Skill, error) {
	if err := validateSkillRoute(ws, ref); err != nil {
		return nil, err
	}
	var resp skillWire
	if err := s.client.do(ctx, http.MethodGet, skillItemPath(ws, ref), nil, &resp); err != nil {
		return nil, err
	}
	return resp.toDomain(), nil
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
	if resp.Skills == nil {
		return nil, fmt.Errorf("fleetdb: skill list requires an explicit skills array")
	}
	out := make([]*domain.Skill, 0, len(resp.Skills))
	for _, sk := range resp.Skills {
		skill := sk.toDomain()
		if skill.WorkspaceKey != ws {
			return nil, fmt.Errorf("fleetdb: skill list workspace mismatch")
		}
		if err := skill.Ref().Validate(); err != nil {
			return nil, fmt.Errorf("fleetdb: invalid listed skill: %w", err)
		}
		out = append(out, skill)
	}
	return out, nil
}

func (s *skillStore) Upsert(ctx context.Context, in store.SkillUpsert) (*domain.Skill, bool, error) {
	if err := validateSkillRoute(in.Skill.WorkspaceKey, in.Skill.Ref); err != nil {
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
	status, _, err := s.client.doWithResponseNoRedirect(ctx, method, path, newCreateSkillBody(in.Skill, false), &resp, nil)
	if err != nil {
		return nil, false, err
	}
	return resp.toDomain(), status == http.StatusCreated, nil
}

func (s *skillStore) Update(ctx context.Context, ws string, ref domain.SkillRef, patch store.SkillUpdate) (*domain.Skill, error) {
	if err := validateSkillRoute(ws, ref); err != nil {
		return nil, err
	}
	body := updateSkillBody{
		Description: patch.Description,
		Content:     patch.Content,
		Source:      patch.Source,
		SourceRef:   patch.SourceRef,
	}
	if patch.Files != nil {
		// A non-nil pointer to an empty slice must serialize as `[]`, not be
		// omitted: "drop every bundled file" and "leave them alone" are
		// different instructions and the pointer is what distinguishes them.
		files := skillFileBodies(*patch.Files)
		body.Files = &files
	}
	var resp skillWire
	if _, _, err := s.client.doWithResponseNoRedirect(ctx, http.MethodPatch, skillItemPath(ws, ref), body, &resp, nil); err != nil {
		return nil, err
	}
	return resp.toDomain(), nil
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

func (s *skillStore) GetFile(ctx context.Context, ws string, ref domain.SkillRef, filePath string) (*domain.SkillDocument, error) {
	if err := validateSkillDocumentRoute(ws, ref, filePath); err != nil {
		return nil, err
	}
	var raw json.RawMessage
	path := skillDocumentPath(ws, ref, filePath)
	_, headers, err := s.client.doWithResponse(ctx, http.MethodGet, path, nil, &raw, nil)
	if err != nil {
		return nil, err
	}
	return decodeSkillDocumentRead(raw, headers.Get(etagHeader), ref, filePath)
}

// decodeSkillDocumentRead validates the source before zero-value decoding can
// turn an absent document body into an apparently valid empty document.
func decodeSkillDocumentRead(raw json.RawMessage, etag string, ref domain.SkillRef, filePath string) (*domain.SkillDocument, error) {
	var required struct {
		Content    *string         `json:"content"`
		Path       *string         `json:"path"`
		Revision   *string         `json:"revision"`
		SkillRef   *string         `json:"skill_ref"`
		Executable json.RawMessage `json:"executable"`
	}
	if err := json.Unmarshal(raw, &required); err != nil {
		return nil, fmt.Errorf("decode skill document: %w", err)
	}
	if required.Content == nil || required.Path == nil || required.Revision == nil || strings.TrimSpace(*required.Revision) == "" || required.SkillRef == nil {
		return nil, fmt.Errorf("skill document response lacks required content or identity")
	}
	if string(required.Executable) == "null" {
		return nil, fmt.Errorf("skill document executable flag is null")
	}
	var wire skillDocumentWire
	if err := json.Unmarshal(raw, &wire); err != nil {
		return nil, fmt.Errorf("decode skill document: %w", err)
	}
	doc, err := wire.toDomain(etag)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(doc.Revision) == "" {
		return nil, fmt.Errorf("skill document response lacks revision")
	}
	if doc.Ref != ref || doc.Path != filePath {
		return nil, fmt.Errorf("skill document response identity does not match requested file")
	}
	if etag != "" && parseETag(etag) != doc.Revision {
		return nil, fmt.Errorf("skill document response revision disagrees with ETag")
	}
	return doc, nil
}

func (s *skillStore) PutFile(ctx context.Context, ws string, ref domain.SkillRef, write store.SkillFileWrite) (*domain.SkillDocument, error) {
	if err := validateSkillDocumentRoute(ws, ref, write.Path); err != nil {
		return nil, err
	}
	headers, err := conditionalWriteHeaders(write.IfMatch, write.IfNoneMatchAny)
	if err != nil {
		return nil, err
	}
	body := putSkillFileBody{Content: write.Content, Executable: write.Executable, Source: write.Source}
	var resp skillDocumentWire
	path := skillDocumentPath(ws, ref, write.Path)
	_, respHeaders, err := s.client.doWithResponseNoRedirect(ctx, http.MethodPut, path, body, &resp, headers)
	if err != nil {
		return nil, err
	}
	return resp.toDomain(respHeaders.Get(etagHeader))
}

func (s *skillStore) DeleteFile(ctx context.Context, ws string, ref domain.SkillRef, del store.SkillFileDelete) error {
	if err := validateSkillDocumentRoute(ws, ref, del.Path); err != nil {
		return err
	}
	headers, err := conditionalWriteHeaders(del.IfMatch, false)
	if err != nil {
		return err
	}
	// Source travels as a query parameter because DELETE carries no body.
	path := withQuery(skillDocumentPath(ws, ref, del.Path), sourceQuery(del.Source))
	_, _, err = s.client.doWithResponseNoRedirect(ctx, http.MethodDelete, path, nil, nil, headers)
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

func validateSkillDocumentRoute(ws string, ref domain.SkillRef, filePath string) error {
	if err := validateSkillRoute(ws, ref); err != nil {
		return err
	}
	if filePath == domain.SkillFileNameSKILLMD {
		return nil
	}
	return domain.ValidateSkillFilePath(filePath)
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
		Path:     meta["path"],
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
