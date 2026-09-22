package domain

import (
	"errors"
	"fmt"
	"path"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"golang.org/x/text/cases"
	"golang.org/x/text/unicode/norm"
)

// SkillScope names the audience a Skill loads into.
//
// There are exactly two, and the empty string is not one of them: a skill with
// no scope has no defined audience, and defaulting it to workspace would
// silently publish a role's private document to every agent in the fleet.
// Mirrors fleet-db's models.SkillScope* constants.
type SkillScope string

const (
	// SkillScopeWorkspace loads the skill into every agent in the workspace.
	SkillScopeWorkspace SkillScope = "workspace"
	// SkillScopeRole loads the skill only into agents running in RoleName,
	// where it shadows a workspace-scoped skill of the same name.
	SkillScopeRole SkillScope = "role"
)

// SkillFileNameSKILLMD is the document that addresses a Skill's own Content
// rather than a bundled file. It is reserved: a bundled file may not claim the
// same path, because the writer would have to choose between the two silently.
// The per-document API routes use it as a path, which is deliberately the same
// shape the runtime materializes on disk, so an editor browsing a skill
// directory saves back through the path it read.
const SkillFileNameSKILLMD = "SKILL.md"

// MaxSkillNameLength is the Agent Skills limit on a skill name, mirrored from
// fleet-db's models.MaxSkillNameLength.
const MaxSkillNameLength = 64

const (
	// MaxRoleNameLength mirrors fleet-db's role-name path-segment limit.
	MaxRoleNameLength = 100
	// MaxSkillDescriptionCharacters is the Agent Skills description limit.
	MaxSkillDescriptionCharacters = 1024
	// MaxSkillContentBytes bounds the complete SKILL.md object.
	MaxSkillContentBytes = 100_000
	// MaxSkillFilePathLength bounds one bundled file destination.
	MaxSkillFilePathLength = 256
	// MaxSkillFilePathSegmentLength mirrors filesystem NAME_MAX.
	MaxSkillFilePathSegmentLength = 255
	// MaxSkillProvenanceLength bounds source and source_ref metadata.
	MaxSkillProvenanceLength = 256
)

// skillNamePattern is the Agent Skills name rule: lowercase letters, digits
// and internal hyphens. Stricter than a role name, because a skill name
// becomes a directory name verbatim when the runtime materializes it.
var skillNamePattern = regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)*$`)

// roleNamePattern mirrors fleet-db's models.roleNamePattern. A role name is
// also an HTTP path segment, so this is both domain validation and a routing
// security boundary.
var roleNamePattern = regexp.MustCompile(`^[a-z0-9]([a-z0-9._-]{0,98}[a-z0-9])?$`)

// skillReservedNames are the names the Agent Skills spec or Loom's synthetic
// skill catalog reserves; the materialized directory owns these names.
var skillReservedNames = map[string]bool{
	"anthropic":          true,
	"claude":             true,
	"loom-skill-catalog": true,
}

// skillDeviceNames are the DOS device names Windows resolves in every
// directory, so a skill named "con" is one that materializes everywhere except
// Windows. Mirrors fleet-db's models.windowsDeviceNames.
var skillDeviceNames = map[string]bool{
	"con": true, "prn": true, "aux": true, "nul": true,
	"com1": true, "com2": true, "com3": true, "com4": true, "com5": true,
	"com6": true, "com7": true, "com8": true, "com9": true,
	"lpt1": true, "lpt2": true, "lpt3": true, "lpt4": true, "lpt5": true,
	"lpt6": true, "lpt7": true, "lpt8": true, "lpt9": true,
}

// Skill write failures that a caller must be able to tell apart.
//
// These two are the whole reason skill writes do not collapse into
// ErrConflict. One of them a caller fixes by re-reading and merging; the other
// it cannot fix at all without a privileged force. A CLI reports them
// differently and a UI offers different buttons, so they are different
// sentinels rather than one conflict with different prose.
var (
	// ErrSkillProvenanceConflict is fleet-db's 409 skill_provenance_conflict:
	// the stored skill was created by a different actor, and this write would
	// overwrite (or delete) someone else's document. No retry helps — the
	// caller needs the force route, which costs skill.force_overwrite.
	// Unwrap the error with errors.As into *SkillProvenanceConflictError for
	// both owners, which is what a bulk import needs to report what it skipped.
	ErrSkillProvenanceConflict = errors.New("domain: skill is owned by a different actor")

	// ErrSkillPreconditionFailed is fleet-db's 412 precondition_failed: the
	// If-Match revision on a per-document write did not hold, so the document
	// changed since the caller read it. Recoverable — re-read, merge, write
	// again. errors.As into *SkillPreconditionError for the revision the
	// caller held and the one on record, enough to offer a diff without a
	// second round trip.
	ErrSkillPreconditionFailed = errors.New("domain: skill document changed since it was read")

	// ErrSkillForbidden preserves fleet-db's authoritative 403 across the
	// Store boundary. It is skills-specific because the legacy generic client
	// mapping classifies other 403 responses as ErrConflict.
	ErrSkillForbidden = errors.New("domain: skill operation forbidden")

	// ErrSkillMaterializationLeaseConflict reports that another writer holds
	// the lease for the same host-local materialization target.
	ErrSkillMaterializationLeaseConflict = errors.New("domain: skill materialization target is leased")

	// ErrSkillMaterializationLeaseTokenMismatch reports a renew or release
	// attempted with a token from a different lease generation.
	ErrSkillMaterializationLeaseTokenMismatch = errors.New("domain: skill materialization lease token mismatch")

	// ErrSkillMaterializationLeaseStoreUnavailable preserves fleet-db's
	// dedicated 503 classification for the ephemeral lease store.
	ErrSkillMaterializationLeaseStoreUnavailable = errors.New("domain: skill materialization lease store unavailable")
)

// SkillRef is the scope-qualified identity of a Skill within a workspace.
//
// A plain name cannot identify a skill: scope shadowing requires a
// workspace-scoped and a role-scoped skill to be able to share a name and both
// exist, so the identity has to carry the scope. Renders as fleet-db's wire
// form — "workspace:<name>" or "role:<role>:<name>".
type SkillRef struct {
	Scope    SkillScope
	RoleName string
	Name     string
}

// WorkspaceSkillRef builds a workspace-scoped ref.
func WorkspaceSkillRef(name string) SkillRef {
	return SkillRef{Scope: SkillScopeWorkspace, Name: name}
}

// RoleSkillRef builds a role-scoped ref.
func RoleSkillRef(roleName, name string) SkillRef {
	return SkillRef{Scope: SkillScopeRole, RoleName: roleName, Name: name}
}

// String renders the ref in fleet-db's wire form. A ref with an unknown scope
// renders empty rather than guessing a scope for it.
func (r SkillRef) String() string {
	switch r.Scope {
	case SkillScopeRole:
		return string(SkillScopeRole) + ":" + r.RoleName + ":" + r.Name
	case SkillScopeWorkspace:
		return string(SkillScopeWorkspace) + ":" + r.Name
	default:
		return ""
	}
}

// Validate checks the ref's shape: a known scope, a name, and a role name
// present exactly when the scope is role.
func (r SkillRef) Validate() error {
	switch r.Scope {
	case SkillScopeWorkspace:
		if strings.TrimSpace(r.RoleName) != "" {
			return fmt.Errorf("skill role_name must be empty when scope is workspace: %w", ErrInvalid)
		}
	case SkillScopeRole:
		if err := ValidateRoleName(r.RoleName); err != nil {
			return fmt.Errorf("skill role_name %q must be a valid role name: %w", r.RoleName, err)
		}
	default:
		return fmt.Errorf("skill scope %q must be one of workspace, role: %w", r.Scope, ErrInvalid)
	}
	return ValidateSkillName(r.Name)
}

// ParseSkillRef splits fleet-db's scope-qualified skill identifier back into
// its parts. Safe to call on a value straight off the wire: the shape is
// validated, and a malformed ref wraps ErrInvalid.
func ParseSkillRef(ref string) (SkillRef, error) {
	parts := strings.Split(ref, ":")
	var out SkillRef
	switch {
	case len(parts) == 2 && parts[0] == string(SkillScopeWorkspace):
		out = SkillRef{Scope: SkillScopeWorkspace, Name: parts[1]}
	case len(parts) == 3 && parts[0] == string(SkillScopeRole):
		out = SkillRef{Scope: SkillScopeRole, RoleName: parts[1], Name: parts[2]}
	default:
		return SkillRef{}, fmt.Errorf("skill ref %q must be %q or %q: %w",
			ref, "workspace:<name>", "role:<role>:<name>", ErrInvalid)
	}
	if err := out.Validate(); err != nil {
		return SkillRef{}, err
	}
	return out, nil
}

// NormalizeSkillRevision returns the bare revision from either form a caller
// may be holding: the unquoted token this package's types carry, or the quoted
// (optionally weak) entity-tag an HTTP layer read off an ETag or If-Match
// header.
//
// It exists because the two forms meet in exactly one place and the meeting is
// where the bug lives. fleet-db sends a revision twice — unquoted in the JSON
// `revision` field, quoted in the ETag header, because RFC 9110 requires an
// entity-tag to be quoted — and it once compared an incoming If-Match against
// the unquoted stored value, so every spec-compliant client that echoed the
// ETag back got a permanent 412 that looked exactly like a real conflict. The
// same trap is one layer up: a web handler holds `If-Match: "abc"` from a
// browser, hands that string to a client that quotes what it is given, and the
// server sees `""abc""`. Normalizing at the boundary is what makes both forms
// mean the same thing.
//
// The wildcard "*" passes through: it is a legal If-Match meaning "any
// revision, but the document must exist".
//
// A comma-separated LIST is rejected rather than collapsed. A revision is a
// hex hash and never contains a comma, so a comma is unambiguously a
// multi-tag header, and silently picking one of its tags would change what the
// caller asked for.
func NormalizeSkillRevision(value string) (string, error) {
	tag := strings.TrimSpace(value)
	if tag == "" || tag == "*" {
		return tag, nil
	}
	if strings.Contains(tag, ",") {
		return "", fmt.Errorf("skill revision %q carries more than one entity-tag; pass a single revision: %w", value, ErrInvalid)
	}
	tag = strings.TrimPrefix(tag, "W/")
	if len(tag) >= 2 && strings.HasPrefix(tag, `"`) && strings.HasSuffix(tag, `"`) {
		tag = tag[1 : len(tag)-1]
	}
	if strings.Contains(tag, `"`) {
		return "", fmt.Errorf("skill revision %q is not a well-formed entity-tag: %w", value, ErrInvalid)
	}
	return tag, nil
}

// SkillFile is one bundled file inside a skill directory — a reference
// document or a script the harness reads or runs on demand.
//
// Content is text, not bytes: v1 stores no binary assets.
type SkillFile struct {
	// Path is the file's location relative to the skill directory, e.g.
	// "references/api.md". Relative and normalized; fleet-db is the authority
	// on what a legal path is and rejects traversal, absolute and
	// case/normalization-colliding paths at the write.
	Path string `json:"path"`

	// Content is the file's text.
	Content string `json:"content"`

	// Executable asks the writer to set the executable bit. Scripts only.
	Executable bool `json:"executable,omitempty"`

	// Revision is the opaque per-file concurrency token, derived server-side
	// from the file's content and returned on every read path including the
	// listing. Hand it back as SkillFileWrite.IfMatch on the next write to
	// this file. Read-only: a value sent on a write is ignored.
	//
	// It is stored here UNQUOTED — the bare hash, as the JSON `revision`
	// field carries it. The HTTP layer is what wraps it in the quotes RFC 9110
	// requires of an entity-tag and unwraps them again on the way in; keeping
	// exactly one representation above that layer is what stops a caller from
	// comparing a quoted token against an unquoted one and getting a
	// permanent, genuine-looking conflict.
	Revision string `json:"revision,omitempty"`
}

// Skill is a workspace-scoped instruction document in the Agent Skills
// (SKILL.md) format, stored centrally and materialized into agent working
// directories at spawn.
//
// Distinct from Role.Skills, which is a list of routing tags matched against
// issue labels and has nothing to do with this type — the two senses of the
// word coexist by decision (fleet-db ADR-005 §1).
//
// A Skill is mutable and unversioned: one record, edited in place. Spawns get
// whatever is current; history and rollback come from the event log.
type Skill struct {
	WorkspaceKey string `json:"workspace_key"`

	// Name is the skill's identifier within its scope, and the directory name
	// it materializes into. Immutable, and unique per (workspace, scope,
	// role) rather than per workspace — shadowing needs a workspace skill and
	// a role skill to be able to share one.
	Name string `json:"name"`

	// Scope is workspace or role. Immutable: moving scopes changes which
	// agents load the skill, which is a create plus a delete, not an edit.
	Scope SkillScope `json:"scope"`

	// RoleName is the role this skill is scoped to, referenced by name
	// because a role has no ID. Set exactly when Scope is role.
	RoleName string `json:"role_name,omitempty"`

	// Description says what the skill does and when to use it. Required: it
	// is the only part of a skill always present in the harness's context, so
	// a skill without one can never be selected.
	Description string `json:"description"`

	// Content is the SKILL.md body. The frontmatter is generated from Name
	// and Description at materialization, so Content must not repeat it.
	Content string `json:"content"`

	// Files are the optional bundled files written alongside SKILL.md.
	Files []SkillFile `json:"files,omitempty"`

	// ContentRevision is the concurrency token for Content — SkillFile's
	// Revision for the one document that is not in Files. Unquoted, read-only,
	// derived server-side.
	ContentRevision string `json:"content_revision,omitempty"`

	// CreatedBy is the actor that first wrote the skill, taken from the
	// authenticated credential and never from a request body. It is the sole
	// input to the ownership guard: only this actor, or a caller holding
	// skill.force_overwrite, may overwrite or delete the skill.
	CreatedBy string `json:"created_by,omitempty"`

	// UpdatedBy is the actor of the most recent write.
	UpdatedBy string `json:"updated_by,omitempty"`

	// Source is the *mechanism* that wrote the skill — "manual",
	// "agent:<name>", "import:<dir>", "pack:<name>". Descriptive only: every
	// caller can send it and every read discloses it, so it is a claim rather
	// than a credential and nothing is authorized on it. Ownership is
	// CreatedBy.
	Source string `json:"source,omitempty"`

	// SourceRef pins the source's version where one exists — the commit a
	// pack sync resolved, for instance.
	SourceRef string `json:"source_ref,omitempty"`

	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// Ref returns the skill's scope-qualified identity.
func (s *Skill) Ref() SkillRef {
	if s == nil {
		return SkillRef{}
	}
	return SkillRef{Scope: s.Scope, RoleName: s.RoleName, Name: s.Name}
}

// FindFile returns the bundled file at path. The lookup is byte-exact, as
// fleet-db's is; the server is what guarantees no two stored paths collide
// once case-folded and normalized.
func (s *Skill) FindFile(path string) (SkillFile, bool) {
	if s == nil {
		return SkillFile{}, false
	}
	for _, f := range s.Files {
		if f.Path == path {
			return f, true
		}
	}
	return SkillFile{}, false
}

// Clone deep-copies a skill so a stored value can be handed out without the
// caller being able to mutate the Files slice behind the store's back.
func (s *Skill) Clone() *Skill {
	if s == nil {
		return nil
	}
	out := *s
	out.Files = append([]SkillFile(nil), s.Files...)
	return &out
}

// SkillDocument is one addressable document inside a skill: a bundled file,
// or SKILL.md, which addresses the skill's own Content.
//
// It is what the per-document API returns, and it is deliberately the unit an
// editor works in — a client with several of a skill's documents open needs
// each to carry its own precondition, which a record-level version could not
// give it.
type SkillDocument struct {
	// Ref is the skill this document belongs to, so a document-oriented
	// caller can find its way back to the record.
	Ref SkillRef

	Path       string
	Content    string
	Executable bool

	// Revision is the unquoted concurrency token for this document — see
	// SkillFile.Revision.
	Revision string
}

// SkillPack sync outcomes. Empty means never synced, which is a third state
// and not a failure.
const (
	SkillPackSyncOK     = "ok"
	SkillPackSyncFailed = "failed"
)

// SkillPackSync is the outcome of one client-side sync run, recorded as a
// unit. The fields describe one event, so they are written together: a record
// asserting a status with no time and no commit behind it is not a record of
// anything.
// The fields carry fleet-db's JSON tags and travel to the server as this
// struct, the way SkillFile does. They describe one event and are only ever
// written together — a record asserting a status with no time and no commit
// behind it is not a record of anything — so a separate wire mirror would only
// be this type spelled twice.
type SkillPackSync struct {
	// Status is SkillPackSyncOK or SkillPackSyncFailed.
	Status string `json:"status"`
	// Commit is the commit the run resolved Ref to, on success.
	Commit string `json:"commit,omitempty"`
	// Error is the failure message, on failure.
	Error string `json:"error,omitempty"`
	// Skills are the skill names a successful run wrote.
	Skills []string `json:"skills,omitempty"`
}

// SkillPack is a git repository a workspace pulls Skills from.
//
// The pack record is the source of truth for *where* skills come from; the
// skills themselves are ordinary Skill records the sync writes through the
// provenance-guarded upsert with source "pack:<name>". Sync is on demand —
// the server never fetches anything itself, so every last-sync field here was
// reported by a client.
type SkillPack struct {
	WorkspaceKey string `json:"workspace_key"`
	Name         string `json:"name"`
	RepoURL      string `json:"repo_url"`

	// Ref is the branch, tag or commit to read. Empty means the remote's
	// default branch, resolved by the client at sync time.
	Ref string `json:"ref,omitempty"`

	// Path is the subdirectory holding the skill directories. Empty is the
	// repository root.
	Path string `json:"path,omitempty"`

	Description string `json:"description,omitempty"`
	CreatedBy   string `json:"created_by,omitempty"`

	// LastSyncedAt is when a sync last finished, successfully or not. Zero
	// means never synced.
	LastSyncedAt time.Time `json:"last_synced_at,omitempty"`

	// LastSyncedCommit is what makes an installed version auditable: Ref may
	// be a moving branch, so only the resolved commit answers "which content
	// is actually installed".
	LastSyncedCommit string `json:"last_synced_commit,omitempty"`

	LastSyncStatus string `json:"last_sync_status,omitempty"`
	LastSyncError  string `json:"last_sync_error,omitempty"`

	// LastSyncedSkills lists what the last successful sync wrote, so a later
	// sync can notice skills the pack has since dropped.
	LastSyncedSkills []string `json:"last_synced_skills,omitempty"`

	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// Source returns the Skill.Source label that skills from this pack carry.
func (p *SkillPack) Source() string { return SkillPackSource(p.Name) }

// SkillPackSource builds the Skill.Source label for a pack name.
func SkillPackSource(name string) string { return "pack:" + name }

// Clone deep-copies a pack.
func (p *SkillPack) Clone() *SkillPack {
	if p == nil {
		return nil
	}
	out := *p
	out.LastSyncedSkills = append([]string(nil), p.LastSyncedSkills...)
	return &out
}

// SkillProvenanceConflictError reports a write refused because the stored
// skill was created by a different actor. It carries both sides of the
// collision rather than only a message, because the caller that hits this is
// usually importing or syncing many skills at once and has to tell its user
// which ones it skipped and who owns them.
//
// Matches ErrSkillProvenanceConflict via errors.Is, so a caller that only
// wants to classify the failure need not unwrap the struct.
type SkillProvenanceConflictError struct {
	Ref     SkillRef
	Message string

	// The owner on record.
	ExistingCreatedBy string
	ExistingSource    string
	ExistingSourceRef string
	ExistingUpdatedAt time.Time

	// The writer that was refused.
	IncomingCreatedBy string
	IncomingSource    string
}

func (e *SkillProvenanceConflictError) Error() string {
	if e.Message != "" {
		return e.Message
	}
	return fmt.Sprintf("skill %s is owned by %s; refusing to overwrite it", e.Ref, orUnknownActor(e.ExistingCreatedBy))
}

// Unwrap makes errors.Is(err, ErrSkillProvenanceConflict) hold.
func (e *SkillProvenanceConflictError) Unwrap() error { return ErrSkillProvenanceConflict }

// SkillPreconditionError reports a per-document write whose If-Match did not
// hold — the caller was working from an older copy. Unlike a provenance
// conflict, a re-read and a merge fixes it, which is why the two are
// different errors.
type SkillPreconditionError struct {
	Ref     SkillRef
	Path    string
	Message string

	// Expected is the revision the caller said it was editing; empty when the
	// condition was existence rather than a revision.
	Expected string
	// Stored is the revision on record now; empty when the document is absent.
	Stored string
}

func (e *SkillPreconditionError) Error() string {
	if e.Message != "" {
		return e.Message
	}
	return fmt.Sprintf("skill %s document %q changed: expected revision %s, stored revision is %s",
		e.Ref, e.Path, orUnknownActor(e.Expected), orUnknownActor(e.Stored))
}

// Unwrap makes errors.Is(err, ErrSkillPreconditionFailed) hold.
func (e *SkillPreconditionError) Unwrap() error { return ErrSkillPreconditionFailed }

func orUnknownActor(s string) string {
	if s == "" {
		return "(unknown)"
	}
	return s
}

// ResolvedSkill is one entry of an agent's resolved skill set.
type ResolvedSkill struct {
	// Skill is what the agent loads.
	Skill *Skill
	// Shadowed is the workspace-scoped skill this role-scoped one displaces,
	// when it displaces one. Nil otherwise. Kept so a CLI or a UI can say
	// "this role overrides the workspace copy" instead of silently dropping
	// a document the operator can still see in the listing.
	Shadowed *Skill
}

// ResolveSkillChain returns the skills an agent running in roleName loads,
// sorted by name.
//
// The rule, in one sentence: an agent gets every workspace-scoped skill plus
// every skill scoped to its own role, and where both scopes carry a skill of
// the same name the role-scoped one wins. There is no attach list and no
// exclude list — scope IS the loading decision, so a skill an agent can see is
// a skill an agent loads.
//
// Pass the unfiltered workspace listing: fleet-db's GET .../skills returns
// both scopes in one call precisely so this can be resolved without a second
// round trip. Skills scoped to some OTHER role are dropped here, which is the
// half of the filtering the server cannot do for a caller that asked for
// everything.
func ResolveSkillChain(skills []*Skill, roleName string) []*Skill {
	resolved := ResolveSkillChainDetail(skills, roleName)
	out := make([]*Skill, 0, len(resolved))
	for _, r := range resolved {
		out = append(out, r.Skill)
	}
	return out
}

// ResolveSkillChainDetail is ResolveSkillChain with the shadowing made
// visible: each entry names the skill that wins and, when the winner is a
// role-scoped override, the workspace-scoped skill it displaced.
//
// Defensive by construction, because it runs on whatever a listing returned:
//
//   - nil entries and skills with no name are dropped;
//   - a skill whose scope is neither workspace nor role is dropped rather
//     than defaulted — an unrecognized scope must never widen an audience;
//   - a role-scoped skill belonging to a different role is dropped, and an
//     empty roleName therefore resolves to the workspace set alone;
//   - among two entries of the same scope and name the first wins, which
//     cannot arise from fleet-db (the pair is unique per scope) and is
//     defined here only so the outcome does not depend on map iteration.
//
// Role names are compared exactly, after trimming surrounding whitespace:
// fleet-db keys a role by its name and does not case-fold it, so neither does
// this. The trim is there because the role name typically arrives through an
// env hop, where a stray newline would otherwise silently drop every
// role-scoped skill the agent was supposed to load.
func ResolveSkillChainDetail(skills []*Skill, roleName string) []ResolvedSkill {
	role := strings.TrimSpace(roleName)
	workspace := make(map[string]*Skill, len(skills))
	roleScoped := make(map[string]*Skill, len(skills))
	for _, s := range skills {
		if s == nil || s.Name == "" {
			continue
		}
		switch s.Scope {
		case SkillScopeWorkspace:
			if _, seen := workspace[s.Name]; !seen {
				workspace[s.Name] = s
			}
		case SkillScopeRole:
			if role == "" || strings.TrimSpace(s.RoleName) != role {
				continue
			}
			if _, seen := roleScoped[s.Name]; !seen {
				roleScoped[s.Name] = s
			}
		}
	}
	out := make([]ResolvedSkill, 0, len(workspace)+len(roleScoped))
	for name, s := range roleScoped {
		out = append(out, ResolvedSkill{Skill: s, Shadowed: workspace[name]})
	}
	for name, s := range workspace {
		if _, shadowed := roleScoped[name]; !shadowed {
			out = append(out, ResolvedSkill{Skill: s})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Skill.Name < out[j].Skill.Name })
	return out
}

// ValidateSkillName checks a skill name against the Agent Skills rules, with
// the same messages fleet-db returns so a bad name fails locally rather than
// round-tripping to find out. Source of truth: fleet-db
// internal/models/skill.go ValidateSkillName.
func ValidateSkillName(name string) error {
	if err := validateAgentSkillsName("skill", name); err != nil {
		return err
	}
	if skillReservedNames[name] {
		return fmt.Errorf("skill name %q is reserved: %w", name, ErrInvalid)
	}
	// The name becomes a directory name verbatim, so a DOS device name is a
	// skill nobody can materialize on Windows. No folding or extension
	// stripping is needed here — unlike a bundled file path, the pattern above
	// has already forced the name to lowercase and forbidden the dot.
	if skillDeviceNames[name] {
		return fmt.Errorf("skill name %q is a reserved device name: %w", name, ErrInvalid)
	}
	return nil
}

// ValidateRoleName checks a role name against the exact fleet-db grammar. In
// addition to matching the persisted model, this guarantees a safe, single
// URL path segment: traversal tokens, slashes, backslashes, controls and
// percent-encoded variants decoded by net/http cannot pass.
func ValidateRoleName(name string) error {
	if strings.TrimSpace(name) == "" {
		return fmt.Errorf("role name is required: %w", ErrInvalid)
	}
	if len(name) > MaxRoleNameLength || !roleNamePattern.MatchString(name) {
		return fmt.Errorf("role name %q must be 1-100 lowercase alphanumeric characters with internal dots/underscores/hyphens: %w", name, ErrInvalid)
	}
	return nil
}

// ValidateSkillDescription checks the required, bounded Agent Skills
// description before a write crosses the Store seam.
func ValidateSkillDescription(description string) error {
	if strings.TrimSpace(description) == "" {
		return fmt.Errorf("skill description is required: %w", ErrInvalid)
	}
	if utf8.RuneCountInString(description) > MaxSkillDescriptionCharacters {
		return fmt.Errorf("skill description must be at most %d characters: %w", MaxSkillDescriptionCharacters, ErrInvalid)
	}
	for _, r := range description {
		if unicode.IsControl(r) {
			return fmt.Errorf("skill description must not contain control characters: %w", ErrInvalid)
		}
	}
	return nil
}

// ValidateSkillContent bounds the SKILL.md body.
func ValidateSkillContent(content string) error {
	if len(content) > MaxSkillContentBytes {
		return fmt.Errorf("skill content must be at most %d bytes: %w", MaxSkillContentBytes, ErrInvalid)
	}
	return nil
}

// ValidateSkillProvenanceField bounds and validates source metadata.
func ValidateSkillProvenanceField(field, value string) error {
	if len(value) > MaxSkillProvenanceLength {
		return fmt.Errorf("skill %s must be at most %d characters: %w", field, MaxSkillProvenanceLength, ErrInvalid)
	}
	if !utf8.ValidString(value) {
		return fmt.Errorf("skill %s must be valid UTF-8: %w", field, ErrInvalid)
	}
	for _, r := range value {
		if unicode.IsControl(r) {
			return fmt.Errorf("skill %s must not contain control characters: %w", field, ErrInvalid)
		}
	}
	return nil
}

// ValidateSkillFilePath is the canonical bundled-file destination validator
// shared by the CLI, webui, and fleet-db adapter. SKILL.md must be routed
// around it because that name addresses the skill body rather than a bundled
// file.
//
//nolint:funlen // Each rejected path class keeps its own labeled check.
func ValidateSkillFilePath(filePath string) error {
	if filePath == "" {
		return fmt.Errorf("skill file path is required: %w", ErrInvalid)
	}
	if len(filePath) > MaxSkillFilePathLength {
		return fmt.Errorf("skill file path %q must be at most %d characters: %w", filePath, MaxSkillFilePathLength, ErrInvalid)
	}
	if !utf8.ValidString(filePath) {
		return fmt.Errorf("skill file path must be valid UTF-8: %w", ErrInvalid)
	}
	for _, r := range filePath {
		if unicode.IsControl(r) {
			return fmt.Errorf("skill file path %q must not contain control characters (U+%04X): %w", filePath, r, ErrInvalid)
		}
	}
	if strings.Contains(filePath, `\`) {
		return fmt.Errorf("skill file path %q must not contain backslashes: %w", filePath, ErrInvalid)
	}
	if strings.HasPrefix(filePath, "/") {
		return fmt.Errorf("skill file path %q must be relative, not absolute: %w", filePath, ErrInvalid)
	}
	if strings.HasPrefix(filePath, "~") {
		return fmt.Errorf("skill file path %q must not start with ~: %w", filePath, ErrInvalid)
	}
	if strings.Contains(filePath, ":") {
		return fmt.Errorf("skill file path %q must not contain a colon: %w", filePath, ErrInvalid)
	}
	for i, segment := range strings.Split(filePath, "/") {
		switch segment {
		case "":
			return fmt.Errorf("skill file path %q must not contain empty segments: %w", filePath, ErrInvalid)
		case ".", "..":
			return fmt.Errorf("skill file path %q must not contain %q segments: %w", filePath, segment, ErrInvalid)
		}
		if len(segment) > MaxSkillFilePathSegmentLength {
			return fmt.Errorf("skill file path %q has a component longer than %d bytes: %w", filePath, MaxSkillFilePathSegmentLength, ErrInvalid)
		}
		folded := cases.Fold().String(norm.NFC.String(segment))
		device := folded
		if dot := strings.IndexByte(device, '.'); dot >= 0 {
			device = device[:dot]
		}
		if skillDeviceNames[device] {
			return fmt.Errorf("skill file path %q uses the reserved device name %q: %w", filePath, segment, ErrInvalid)
		}
		if i == 0 && folded == strings.ToLower(SkillFileNameSKILLMD) {
			return fmt.Errorf("skill file path %q is reserved for the skill content (spelled %q here): %w", SkillFileNameSKILLMD, segment, ErrInvalid)
		}
	}
	if path.Clean(filePath) != filePath {
		return fmt.Errorf("skill file path %q must be normalized (got %q): %w", filePath, path.Clean(filePath), ErrInvalid)
	}
	return nil
}

// ValidateSkillPackName checks a pack name: the same character rules as a
// skill name, since the name is also the `pack:<name>` source label every
// skill the pack syncs carries.
// A pack name deliberately stops at the character rules. It is a source label,
// never a directory name, so the reserved-name and DOS-device checks that
// ValidateSkillName adds do not apply: a pack called "claude" or "con" is
// legal. Do not "finish" this by delegating to ValidateSkillName.
func ValidateSkillPackName(name string) error {
	return validateAgentSkillsName("skill_pack", name)
}

// validateAgentSkillsName checks the character rules shared by skill and pack
// names: required, bounded, and lowercase letters, digits and internal hyphens
// only. label names the field in the error so each caller keeps the message
// fleet-db returns for it.
func validateAgentSkillsName(label, name string) error {
	if strings.TrimSpace(name) == "" {
		return fmt.Errorf("%s name is required: %w", label, ErrInvalid)
	}
	if len(name) > MaxSkillNameLength {
		return fmt.Errorf("%s name %q must be at most %d characters: %w", label, name, MaxSkillNameLength, ErrInvalid)
	}
	if !skillNamePattern.MatchString(name) {
		return fmt.Errorf("%s name %q must be lowercase letters, digits and internal hyphens only: %w", label, name, ErrInvalid)
	}
	return nil
}
