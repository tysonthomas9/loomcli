package memstore

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/store"
)

// defaultSkillActor stands in for the authenticated identity fleet-db takes
// from the credential (or X-Actor). memstore has no credentials, so it has one
// caller unless a test says otherwise — see Store.SetSkillActor.
const defaultSkillActor = "memstore"

type skillStore struct {
	mu    sync.RWMutex
	items map[string]map[string]*domain.Skill // wsKey → ref → Skill
	roles *roleStore
	files *workspaceFileStore
	actor string
}

func newSkillStore(roles *roleStore, files *workspaceFileStore) *skillStore {
	return &skillStore{
		items: make(map[string]map[string]*domain.Skill),
		roles: roles,
		files: files,
		actor: defaultSkillActor,
	}
}

type skillPackStore struct {
	mu    sync.RWMutex
	items map[string]map[string]*domain.SkillPack // wsKey → name → SkillPack
	actor string
}

func newSkillPackStore() *skillPackStore {
	return &skillPackStore{items: make(map[string]map[string]*domain.SkillPack), actor: defaultSkillActor}
}

var (
	_ store.SkillStore     = (*skillStore)(nil)
	_ store.SkillPackStore = (*skillPackStore)(nil)
)

// setActor swaps the identity subsequent writes are recorded under, so a test
// can play a second writer and exercise the ownership guard.
func (s *skillStore) setActor(actor string) {
	s.mu.Lock()
	s.actor = actor
	s.mu.Unlock()
}

// --- ownership guard ---

// checkSkillProvenance mirrors fleet-db's rule: a skill is owned by its
// CreatedBy, only that actor (or a forcing caller) may overwrite or delete it,
// and Source authorizes nothing. A record with no CreatedBy is unowned and
// updatable, because the alternative is a record nobody can legitimately edit.
func (s *skillStore) checkProvenance(existing *domain.Skill, source string, force bool) error {
	if existing == nil || force || existing.CreatedBy == "" {
		return nil
	}
	if s.actor != "" && existing.CreatedBy == s.actor {
		return nil
	}
	return &domain.SkillProvenanceConflictError{
		Ref:               existing.Ref(),
		ExistingCreatedBy: existing.CreatedBy,
		ExistingSource:    existing.Source,
		ExistingSourceRef: existing.SourceRef,
		ExistingUpdatedAt: existing.UpdatedAt,
		IncomingCreatedBy: s.actor,
		IncomingSource:    source,
	}
}

// --- SkillStore ---

func (s *skillStore) Create(ctx context.Context, in store.SkillCreate) (*domain.Skill, error) {
	if err := s.validateWrite(ctx, in.WorkspaceKey, in.Ref, in.Description, in.FileTreeRevision); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.items[in.WorkspaceKey] == nil {
		s.items[in.WorkspaceKey] = make(map[string]*domain.Skill)
	}
	key := in.Ref.String()
	if _, ok := s.items[in.WorkspaceKey][key]; ok {
		return nil, fmt.Errorf("skill %q in workspace %q: %w", key, in.WorkspaceKey, domain.ErrAlreadyExists)
	}
	sk := s.newSkill(in)
	s.items[in.WorkspaceKey][key] = sk
	return sk.Clone(), nil
}

func (s *skillStore) newSkill(in store.SkillCreate) *domain.Skill {
	now := time.Now().UTC()
	return &domain.Skill{
		WorkspaceKey:     in.WorkspaceKey,
		Name:             in.Ref.Name,
		Scope:            in.Ref.Scope,
		RoleName:         in.Ref.RoleName,
		Description:      in.Description,
		FileTreeRevision: in.FileTreeRevision,
		CreatedBy:        s.actor,
		UpdatedBy:        s.actor,
		Source:           in.Source,
		SourceRef:        in.SourceRef,
		CreatedAt:        now,
		UpdatedAt:        now,
	}
}

// validateWrite rejects the record shapes fleet-db rejects at write time.
//
// This double stands in for the server in tests, so anything it accepts that
// the server would refuse is a test that passes on data production cannot
// produce. The bundled-file rules are the ones worth mirroring: a path fleet-db
// refuses is a path the materializer would then have to defend against.
func (s *skillStore) validateWrite(ctx context.Context, ws string, ref domain.SkillRef, description, revision string) error {
	if err := s.validateTarget(ws, ref); err != nil {
		return err
	}
	if err := domain.ValidateSkillDescription(description); err != nil {
		return err
	}
	if revision == "" {
		return fmt.Errorf("skill file_tree_revision is required: %w", domain.ErrInvalid)
	}
	if s.files == nil {
		return fmt.Errorf("workspace file store unavailable: %w", domain.ErrNotFound)
	}
	return s.validateReferencedTree(ctx, ws, ref, description, revision)
}

func (s *skillStore) validateReferencedTree(ctx context.Context, ws string, ref domain.SkillRef, description, revision string) error {
	tree, err := s.files.GetTree(ctx, ws, revision)
	if err != nil {
		return err
	}
	manifest := make([]domain.SkillFileTreeFile, 0, len(tree.Files))
	for _, file := range tree.Files {
		body, err := s.files.Download(ctx, ws, revision, file.Path)
		if err != nil {
			return err
		}
		manifest = append(manifest, domain.SkillFileTreeFile{
			Path: file.Path, Bytes: body, MediaType: file.MediaType, Executable: file.Executable,
		})
	}
	snapshot, err := domain.ValidateSkillFileTree(manifest)
	if err != nil {
		return err
	}
	if snapshot.Name != ref.Name || snapshot.Description != description {
		return fmt.Errorf("skill metadata does not match referenced SKILL.md: %w", domain.ErrIntegrity)
	}
	return nil
}

// validateTarget rejects what fleet-db rejects before the record exists: a
// missing workspace, a malformed ref, and a role-scoped skill pointed at a
// role that is not there (which the server answers 404, not 400).
func (s *skillStore) validateTarget(ws string, ref domain.SkillRef) error {
	if ws == "" {
		return fmt.Errorf("workspace_key required: %w", domain.ErrInvalid)
	}
	// SkillRef.Validate ends with ValidateSkillName, so the name is covered.
	if err := ref.Validate(); err != nil {
		return err
	}
	if ref.Scope == domain.SkillScopeRole && s.roles != nil && !s.roles.exists(ws, ref.RoleName) {
		return fmt.Errorf("role %q in workspace %q: %w", ref.RoleName, ws, domain.ErrNotFound)
	}
	return nil
}

func (s *skillStore) Get(_ context.Context, ws string, ref domain.SkillRef) (*domain.Skill, error) {
	if err := ref.Validate(); err != nil {
		return nil, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	sk, err := s.lookup(ws, ref)
	if err != nil {
		return nil, err
	}
	return sk.Clone(), nil
}

func (s *skillStore) lookup(ws string, ref domain.SkillRef) (*domain.Skill, error) {
	sk, ok := s.items[ws][ref.String()]
	if !ok {
		return nil, fmt.Errorf("skill %q in workspace %q: %w", ref, ws, domain.ErrNotFound)
	}
	return sk, nil
}

func (s *skillStore) List(_ context.Context, ws string, filter store.SkillFilter) ([]*domain.Skill, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]*domain.Skill, 0, len(s.items[ws]))
	for _, sk := range s.items[ws] {
		if filter.Scope != "" && sk.Scope != filter.Scope {
			continue
		}
		if filter.RoleName != "" && sk.RoleName != filter.RoleName {
			continue
		}
		out = append(out, sk.Clone())
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Name != out[j].Name {
			return out[i].Name < out[j].Name
		}
		return out[i].Ref().String() < out[j].Ref().String()
	})
	return out, nil
}

func (s *skillStore) Upsert(ctx context.Context, in store.SkillUpsert) (*domain.Skill, bool, error) {
	if err := s.validateWrite(ctx, in.Skill.WorkspaceKey, in.Skill.Ref, in.Skill.Description, in.Skill.FileTreeRevision); err != nil {
		return nil, false, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	ws, key := in.Skill.WorkspaceKey, in.Skill.Ref.String()
	existing := s.items[ws][key]
	if err := s.checkProvenance(existing, in.Skill.Source, in.Force); err != nil {
		return nil, false, err
	}
	if existing == nil {
		if s.items[ws] == nil {
			s.items[ws] = make(map[string]*domain.Skill)
		}
		sk := s.newSkill(in.Skill)
		s.items[ws][key] = sk
		return sk.Clone(), true, nil
	}
	// CreatedBy deliberately does not move: force is a privileged, audited act,
	// not a transfer of title, so the next overwrite costs the same permission.
	existing.Description = in.Skill.Description
	existing.FileTreeRevision = in.Skill.FileTreeRevision
	existing.Source = in.Skill.Source
	existing.SourceRef = in.Skill.SourceRef
	existing.UpdatedBy = s.actor
	existing.UpdatedAt = time.Now().UTC()
	return existing.Clone(), false, nil
}

func (s *skillStore) Update(ctx context.Context, ws string, ref domain.SkillRef, patch store.SkillUpdate) (*domain.Skill, error) {
	if err := ref.Validate(); err != nil {
		return nil, err
	}
	if patch.Description != nil && patch.FileTreeRevision == nil {
		return nil, fmt.Errorf("skill description change requires a file_tree_revision CAS: %w", domain.ErrInvalid)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	sk, err := s.lookup(ws, ref)
	if err != nil {
		return nil, err
	}
	if err := s.checkProvenance(sk, patch.Source, false); err != nil {
		return nil, err
	}
	if patch.Description != nil {
		if err := domain.ValidateSkillDescription(*patch.Description); err != nil {
			return nil, err
		}
	}
	if patch.FileTreeRevision != nil {
		if *patch.FileTreeRevision == "" {
			return nil, fmt.Errorf("skill file_tree_revision is required: %w", domain.ErrInvalid)
		}
		if patch.ExpectedFileTreeRevision != sk.FileTreeRevision {
			return nil, &domain.SkillPreconditionError{Ref: ref, Expected: patch.ExpectedFileTreeRevision, Stored: sk.FileTreeRevision}
		}
		description := sk.Description
		if patch.Description != nil {
			description = *patch.Description
		}
		if err := s.validateReferencedTree(ctx, ws, ref, description, *patch.FileTreeRevision); err != nil {
			return nil, err
		}
	}
	applySkillPatch(sk, patch)
	sk.UpdatedBy = s.actor
	sk.UpdatedAt = time.Now().UTC()
	return sk.Clone(), nil
}

func applySkillPatch(sk *domain.Skill, patch store.SkillUpdate) {
	if patch.Description != nil {
		sk.Description = *patch.Description
	}
	if patch.FileTreeRevision != nil {
		sk.FileTreeRevision = *patch.FileTreeRevision
	}
	if patch.SourceRef != nil {
		sk.SourceRef = *patch.SourceRef
	}
	if patch.Source != "" {
		sk.Source = patch.Source
	}
}

func (s *skillStore) Delete(_ context.Context, ws string, ref domain.SkillRef, del store.SkillDelete) error {
	if err := ref.Validate(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	sk, err := s.lookup(ws, ref)
	if err != nil {
		return err
	}
	// Guarded exactly like an overwrite: an unguarded delete is a force
	// overwrite in two steps.
	if err := s.checkProvenance(sk, del.Source, del.Force); err != nil {
		return err
	}
	delete(s.items[ws], ref.String())
	return nil
}

// hasRole reports whether any skill is scoped to a role, so deleting that role
// can be refused rather than orphaning or silently destroying its documents.
func (s *skillStore) hasRole(ws, roleName string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, sk := range s.items[ws] {
		if sk.Scope == domain.SkillScopeRole && sk.RoleName == roleName {
			return true
		}
	}
	return false
}

// --- SkillPackStore ---

func (s *skillPackStore) Create(_ context.Context, in store.SkillPackCreate) (*domain.SkillPack, error) {
	if in.WorkspaceKey == "" {
		return nil, fmt.Errorf("workspace_key required: %w", domain.ErrInvalid)
	}
	if err := domain.ValidateSkillPackName(in.Name); err != nil {
		return nil, err
	}
	if in.RepoURL == "" {
		return nil, fmt.Errorf("skill_pack repo_url is required: %w", domain.ErrInvalid)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.items[in.WorkspaceKey] == nil {
		s.items[in.WorkspaceKey] = make(map[string]*domain.SkillPack)
	}
	if _, ok := s.items[in.WorkspaceKey][in.Name]; ok {
		return nil, fmt.Errorf("skill_pack %q in workspace %q: %w", in.Name, in.WorkspaceKey, domain.ErrAlreadyExists)
	}
	now := time.Now().UTC()
	p := &domain.SkillPack{
		WorkspaceKey: in.WorkspaceKey,
		Name:         in.Name,
		RepoURL:      in.RepoURL,
		Ref:          in.Ref,
		Path:         in.Path,
		Description:  in.Description,
		CreatedBy:    s.actor,
		CreatedAt:    now,
		UpdatedAt:    now,
	}
	s.items[in.WorkspaceKey][in.Name] = p
	return p.Clone(), nil
}

func (s *skillPackStore) Get(_ context.Context, ws, name string) (*domain.SkillPack, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	p, ok := s.items[ws][name]
	if !ok {
		return nil, fmt.Errorf("skill_pack %q in workspace %q: %w", name, ws, domain.ErrNotFound)
	}
	return p.Clone(), nil
}

func (s *skillPackStore) List(_ context.Context, ws string) ([]*domain.SkillPack, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]*domain.SkillPack, 0, len(s.items[ws]))
	for _, p := range s.items[ws] {
		out = append(out, p.Clone())
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

func (s *skillPackStore) Update(_ context.Context, ws, name string, patch store.SkillPackUpdate) (*domain.SkillPack, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, ok := s.items[ws][name]
	if !ok {
		return nil, fmt.Errorf("skill_pack %q in workspace %q: %w", name, ws, domain.ErrNotFound)
	}
	if patch.RepoURL != nil {
		p.RepoURL = *patch.RepoURL
	}
	if patch.Ref != nil {
		p.Ref = *patch.Ref
	}
	if patch.Path != nil {
		p.Path = *patch.Path
	}
	if patch.Description != nil {
		p.Description = *patch.Description
	}
	applyPackSync(p, patch.RecordSync)
	p.UpdatedAt = time.Now().UTC()
	return p.Clone(), nil
}

// applyPackSync writes the last-sync block as a unit. A successful run clears
// the previous error; a failed one leaves the installed commit alone, because
// what is installed did not change just because a refresh failed.
func applyPackSync(p *domain.SkillPack, sync *domain.SkillPackSync) {
	if sync == nil {
		return
	}
	p.LastSyncedAt = time.Now().UTC()
	p.LastSyncStatus = sync.Status
	if sync.Status == domain.SkillPackSyncOK {
		p.LastSyncError = ""
		p.LastSyncedCommit = sync.Commit
		p.LastSyncedSkills = append([]string(nil), sync.Skills...)
		return
	}
	p.LastSyncError = sync.Error
}

func (s *skillPackStore) Delete(_ context.Context, ws, name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.items[ws][name]; !ok {
		return fmt.Errorf("skill_pack %q in workspace %q: %w", name, ws, domain.ErrNotFound)
	}
	// Deleting a pack leaves the skills it synced in place; only the record of
	// where they came from goes away.
	delete(s.items[ws], name)
	return nil
}
