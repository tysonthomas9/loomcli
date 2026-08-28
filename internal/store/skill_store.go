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
	WorkspaceKey     string
	Ref              domain.SkillRef
	Description      string
	FileTreeRevision string

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
	// Description and FileTreeRevision move together because Description is
	// duplicated in SKILL.md frontmatter. A description change is therefore a
	// whole-tree CAS, never a metadata-only patch.
	Description              *string
	FileTreeRevision         *string
	ExpectedFileTreeRevision string

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
