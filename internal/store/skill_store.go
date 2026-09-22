package store

import (
	"context"

	"github.com/tysonthomas9/loomcli/internal/domain"
)

// SkillCreate is the input for SkillStore.Create and the body half of
// SkillUpsert. Ref carries the scope, the role (when role-scoped) and the
// name; there is no CreatedBy field, because provenance identity is the
// authenticated actor of the write and never a value a caller supplies.
type SkillCreate struct {
	WorkspaceKey string
	Ref          domain.SkillRef
	Description  string
	Content      string
	Files        []domain.SkillFile

	// Source names the mechanism doing the writing — "manual",
	// "import:<dir>", "pack:<name>". Recorded and displayed; it authorizes
	// nothing (see domain.Skill.Source).
	Source string
	// SourceRef pins the source's version where one exists.
	SourceRef string
}

// SkillUpsert is the provenance-guarded create-or-update.
type SkillUpsert struct {
	Skill SkillCreate

	// Force overwrites a skill created by a different actor. It is a distinct
	// route on the server, gated by skill.force_overwrite, because
	// authorization binds a permission to a route and cannot read a request
	// body or a query string. Without it, an overwrite of someone else's
	// skill fails with domain.ErrSkillProvenanceConflict.
	Force bool
}

// SkillUpdate is the partial-update payload for a skill. Omitted fields are
// left unchanged.
type SkillUpdate struct {
	Description *string
	Content     *string

	// Files replaces the WHOLE bundled set; there is no per-path merge. A
	// pointer to an empty slice drops every bundled file, nil leaves them
	// alone. To write one document without holding its siblings, use PutFile
	// instead — that is what it exists for.
	Files *[]domain.SkillFile

	SourceRef *string

	// Source describes this write's mechanism. Recorded, never authorizing.
	Source string
}

// SkillDelete carries the descriptive source of a whole-skill delete and the
// force flag.
type SkillDelete struct {
	Source string

	// Force removes a skill created by a different actor, on its own
	// permission. Delete is guarded exactly like an overwrite because
	// delete-then-recreate IS an overwrite, in two steps.
	Force bool
}

// SkillFilter narrows a listing. The zero value returns both scopes, which is
// what resolving an agent's whole scope chain wants — see
// domain.ResolveSkillChain.
type SkillFilter struct {
	Scope    domain.SkillScope
	RoleName string
}

// SkillFileWrite is one atomic per-document write inside a skill.
//
// It exists because a whole-record replacement is the wrong write for an
// editor: a client with two of a skill's documents open saves them
// independently, and "replace the skill" would carry a stale copy of the
// sibling and silently revert it. The server does the read-modify-write.
//
// KNOWN GAP (fleet-db amendment A2): the server-side read-modify-write is not
// atomic against a simultaneous write to a *different* document of the same
// skill. Both writes see their own document unchanged, both are accepted, and
// the one that lands second carries the other's file at its pre-edit content.
// IfMatch cannot detect this — it is per document by design. Safe for two
// editors on one document; lossy for two documents of one skill saved within a
// round trip. Closing it needs a conditional append in the event store.
type SkillFileWrite struct {
	// Path names the document. domain.SkillFileNameSKILLMD addresses the
	// skill's own body rather than a bundled file.
	Path       string
	Content    string
	Executable bool

	// IfMatch is the revision the document was read at.
	//
	// IT HOLDS A BARE REVISION, NOT AN ETAG. The value to put here is
	// domain.SkillFile.Revision or domain.Skill.ContentRevision — the
	// unquoted token every read path returns. The fleet-db adapter is what
	// adds the quotes RFC 9110 requires of an entity-tag on the wire; no
	// caller has to, and no caller should.
	//
	// A quoted or `W/`-prefixed value is accepted anyway and normalized,
	// because the realistic caller is a web handler holding `If-Match: "abc"`
	// straight off a browser request: quoting that again would produce
	// `""abc""`, a permanent 412 indistinguishable from a genuine conflict —
	// the same failure the server had before it learned to parse entity-tags.
	// A comma-separated multi-tag header is rejected with domain.ErrInvalid
	// rather than silently reduced to one of its tags.
	//
	// Empty means an unconditional last-write-wins save, which is the only
	// option open to a caller with no revision to offer. A stale revision
	// fails with domain.ErrSkillPreconditionFailed — re-read, merge, write
	// again.
	IfMatch string

	// IfNoneMatchAny requires that the document not already exist, which is
	// how a create is expressed without racing an existing file.
	IfNoneMatchAny bool

	// Source describes this write's mechanism. Recorded, never authorizing.
	Source string
}

// SkillFileDelete removes one bundled file. SKILL.md is not removable this way
// — it is the skill itself, so delete the skill instead.
type SkillFileDelete struct {
	Path string
	// IfMatch is the bare revision, with the same normalization rules as
	// SkillFileWrite.IfMatch.
	IfMatch string
	// Source travels as a query parameter server-side because DELETE carries
	// no body. Recorded, never authorizing.
	Source string
}

// SkillStore is the persistence interface for Skill entities.
//
// Skills are workspace-scoped records addressed by a scope-qualified ref, and
// the scope split is load-bearing on the server: role-scoped writes and
// workspace-scoped writes are different routes carrying different permissions,
// so which lane a call takes is decided by the Ref it is given, not by a flag.
//
// v1 EXPOSES NO AGENT-AUTHENTICATED WRITE PATH. Agent self-save is deferred
// (grilling amendment A1): fleet-db cannot verify that a role-scoped write
// targets the writer's OWN role — an API key carries an actor ID and a
// workspace tier, no loom role — and client-side enforcement is not a security
// boundary, so an unrestricted agent credential could create a skill under
// another role's scope and have that role's agents load it at their next
// spawn. Every writer of this interface must therefore be human- or
// CLI-initiated. Do not wire these methods to a surface an agent reaches.
type SkillStore interface {
	Create(ctx context.Context, in SkillCreate) (*domain.Skill, error)
	Get(ctx context.Context, workspaceKey string, ref domain.SkillRef) (*domain.Skill, error)
	List(ctx context.Context, workspaceKey string, filter SkillFilter) ([]*domain.Skill, error)

	// Upsert creates the skill or updates it in place, subject to the
	// ownership guard. The bool reports whether the record was created (as
	// opposed to updated), which is what an import or a sync summarizes.
	Upsert(ctx context.Context, in SkillUpsert) (*domain.Skill, bool, error)

	Update(ctx context.Context, workspaceKey string, ref domain.SkillRef, patch SkillUpdate) (*domain.Skill, error)
	Delete(ctx context.Context, workspaceKey string, ref domain.SkillRef, del SkillDelete) error

	// GetFile reads one document, with the revision to hand back as IfMatch.
	GetFile(ctx context.Context, workspaceKey string, ref domain.SkillRef, path string) (*domain.SkillDocument, error)
	// PutFile writes one document and returns it at its new revision.
	PutFile(ctx context.Context, workspaceKey string, ref domain.SkillRef, write SkillFileWrite) (*domain.SkillDocument, error)
	// DeleteFile removes one bundled file.
	DeleteFile(ctx context.Context, workspaceKey string, ref domain.SkillRef, del SkillFileDelete) error
}

// SkillPackCreate is the input for SkillPackStore.Create.
type SkillPackCreate struct {
	WorkspaceKey string
	Name         string
	RepoURL      string
	// Ref is a branch, tag or commit. Empty means the remote's default
	// branch, resolved client-side at sync time.
	Ref string
	// Path is the subdirectory holding the skill directories. Empty is the
	// repository root.
	Path        string
	Description string
}

// SkillPackUpdate is the partial-update payload for a pack.
type SkillPackUpdate struct {
	RepoURL     *string
	Ref         *string
	Path        *string
	Description *string

	// RecordSync writes the outcome of one client-side sync run as a unit.
	// The server never fetches anything itself, so every last-sync field on
	// the record got there through here.
	RecordSync *domain.SkillPackSync
}

// SkillPackStore is the persistence interface for SkillPack entities — the
// record of WHERE skills come from. Deleting a pack leaves the skills it
// synced in place; only the record of their origin goes away.
type SkillPackStore interface {
	Create(ctx context.Context, in SkillPackCreate) (*domain.SkillPack, error)
	Get(ctx context.Context, workspaceKey, name string) (*domain.SkillPack, error)
	List(ctx context.Context, workspaceKey string) ([]*domain.SkillPack, error)
	Update(ctx context.Context, workspaceKey, name string, patch SkillPackUpdate) (*domain.SkillPack, error)
	Delete(ctx context.Context, workspaceKey, name string) error
}
