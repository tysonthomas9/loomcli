package memstore

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
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
	actor string
}

func newSkillStore(roles *roleStore) *skillStore {
	return &skillStore{
		items: make(map[string]map[string]*domain.Skill),
		roles: roles,
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

// --- revisions ---
//
// Derived, never stored: a revision is a function of the content, so keeping a
// copy would create a second value that can disagree with the first. Stamped
// on the way out only, exactly as fleet-db does it, and with the same
// derivation so a revision a test captures here has the same shape as one off
// the wire.

func skillRevision(content string, executable bool) string {
	h := sha256.New()
	if executable {
		h.Write([]byte{1})
	} else {
		h.Write([]byte{0})
	}
	h.Write([]byte(content))
	return hex.EncodeToString(h.Sum(nil))[:16]
}

func skillFileRevision(f domain.SkillFile) string { return skillRevision(f.Content, f.Executable) }

func skillContentRevision(content string) string { return skillRevision(content, false) }

// withRevisions clones a skill with every derived revision filled in.
func withRevisions(s *domain.Skill) *domain.Skill {
	out := s.Clone()
	if out == nil {
		return nil
	}
	out.ContentRevision = skillContentRevision(out.Content)
	for i := range out.Files {
		out.Files[i].Revision = skillFileRevision(out.Files[i])
	}
	return out
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

func (s *skillStore) Create(_ context.Context, in store.SkillCreate) (*domain.Skill, error) {
	if err := s.validateTarget(in.WorkspaceKey, in.Ref); err != nil {
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
	return withRevisions(sk), nil
}

func (s *skillStore) newSkill(in store.SkillCreate) *domain.Skill {
	now := time.Now().UTC()
	return &domain.Skill{
		WorkspaceKey: in.WorkspaceKey,
		Name:         in.Ref.Name,
		Scope:        in.Ref.Scope,
		RoleName:     in.Ref.RoleName,
		Description:  in.Description,
		Content:      in.Content,
		Files:        append([]domain.SkillFile(nil), in.Files...),
		CreatedBy:    s.actor,
		UpdatedBy:    s.actor,
		Source:       in.Source,
		SourceRef:    in.SourceRef,
		CreatedAt:    now,
		UpdatedAt:    now,
	}
}

// validateTarget rejects what fleet-db rejects before the record exists: a
// missing workspace, a malformed ref, and a role-scoped skill pointed at a
// role that is not there (which the server answers 404, not 400).
func (s *skillStore) validateTarget(ws string, ref domain.SkillRef) error {
	if ws == "" {
		return fmt.Errorf("workspace_key required: %w", domain.ErrInvalid)
	}
	if err := ref.Validate(); err != nil {
		return err
	}
	if err := domain.ValidateSkillName(ref.Name); err != nil {
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
	return withRevisions(sk), nil
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
		out = append(out, withRevisions(sk))
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Name != out[j].Name {
			return out[i].Name < out[j].Name
		}
		return out[i].Ref().String() < out[j].Ref().String()
	})
	return out, nil
}

func (s *skillStore) Upsert(_ context.Context, in store.SkillUpsert) (*domain.Skill, bool, error) {
	if err := s.validateTarget(in.Skill.WorkspaceKey, in.Skill.Ref); err != nil {
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
		return withRevisions(sk), true, nil
	}
	// CreatedBy deliberately does not move: force is a privileged, audited act,
	// not a transfer of title, so the next overwrite costs the same permission.
	existing.Description = in.Skill.Description
	existing.Content = in.Skill.Content
	existing.Files = append([]domain.SkillFile(nil), in.Skill.Files...)
	existing.Source = in.Skill.Source
	existing.SourceRef = in.Skill.SourceRef
	existing.UpdatedBy = s.actor
	existing.UpdatedAt = time.Now().UTC()
	return withRevisions(existing), false, nil
}

func (s *skillStore) Update(_ context.Context, ws string, ref domain.SkillRef, patch store.SkillUpdate) (*domain.Skill, error) {
	if err := ref.Validate(); err != nil {
		return nil, err
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
	applySkillPatch(sk, patch)
	sk.UpdatedBy = s.actor
	sk.UpdatedAt = time.Now().UTC()
	return withRevisions(sk), nil
}

func applySkillPatch(sk *domain.Skill, patch store.SkillUpdate) {
	if patch.Description != nil {
		sk.Description = *patch.Description
	}
	if patch.Content != nil {
		sk.Content = *patch.Content
	}
	if patch.Files != nil {
		sk.Files = append([]domain.SkillFile(nil), (*patch.Files)...)
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

// --- per-document lane ---

func (s *skillStore) GetFile(_ context.Context, ws string, ref domain.SkillRef, filePath string) (*domain.SkillDocument, error) {
	if err := ref.Validate(); err != nil {
		return nil, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	sk, err := s.lookup(ws, ref)
	if err != nil {
		return nil, err
	}
	doc, ok := skillDocument(sk, filePath)
	if !ok {
		return nil, fmt.Errorf("skill %q file %q: %w", ref, filePath, domain.ErrNotFound)
	}
	return doc, nil
}

// skillDocument projects one document out of a skill. SKILL.md addresses the
// body, which exists as soon as the skill does.
func skillDocument(sk *domain.Skill, filePath string) (*domain.SkillDocument, bool) {
	if filePath == domain.SkillFileNameSKILLMD {
		return &domain.SkillDocument{
			Ref:      sk.Ref(),
			Path:     domain.SkillFileNameSKILLMD,
			Content:  sk.Content,
			Revision: skillContentRevision(sk.Content),
		}, true
	}
	f, ok := sk.FindFile(filePath)
	if !ok {
		return nil, false
	}
	return &domain.SkillDocument{
		Ref:        sk.Ref(),
		Path:       f.Path,
		Content:    f.Content,
		Executable: f.Executable,
		Revision:   skillFileRevision(f),
	}, true
}

func (s *skillStore) PutFile(_ context.Context, ws string, ref domain.SkillRef, write store.SkillFileWrite) (*domain.SkillDocument, error) {
	if err := ref.Validate(); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	sk, err := s.lookup(ws, ref)
	if err != nil {
		return nil, err
	}
	if err := s.checkProvenance(sk, write.Source, false); err != nil {
		return nil, err
	}
	ifMatch, err := domain.NormalizeSkillRevision(write.IfMatch)
	if err != nil {
		return nil, err
	}
	stored, exists := storedRevision(sk, write.Path)
	if err := checkPrecondition(ref, write.Path, ifMatch, write.IfNoneMatchAny, stored, exists); err != nil {
		return nil, err
	}
	if write.Path == domain.SkillFileNameSKILLMD {
		sk.Content = write.Content
	} else {
		sk.Files = replaceSkillFile(sk.Files, domain.SkillFile{
			Path: write.Path, Content: write.Content, Executable: write.Executable,
		})
	}
	sk.UpdatedBy = s.actor
	sk.UpdatedAt = time.Now().UTC()
	doc, _ := skillDocument(sk, write.Path)
	return doc, nil
}

func (s *skillStore) DeleteFile(_ context.Context, ws string, ref domain.SkillRef, del store.SkillFileDelete) error {
	if err := ref.Validate(); err != nil {
		return err
	}
	if del.Path == domain.SkillFileNameSKILLMD {
		// The body IS the skill; removing it means deleting the skill.
		return fmt.Errorf("%s cannot be deleted; delete the skill instead: %w",
			domain.SkillFileNameSKILLMD, domain.ErrInvalid)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	sk, err := s.lookup(ws, ref)
	if err != nil {
		return err
	}
	if err := s.checkProvenance(sk, del.Source, false); err != nil {
		return err
	}
	ifMatch, err := domain.NormalizeSkillRevision(del.IfMatch)
	if err != nil {
		return err
	}
	stored, exists := storedRevision(sk, del.Path)
	if !exists {
		return fmt.Errorf("skill %q file %q: %w", ref, del.Path, domain.ErrNotFound)
	}
	if err := checkPrecondition(ref, del.Path, ifMatch, false, stored, exists); err != nil {
		return err
	}
	files := make([]domain.SkillFile, 0, len(sk.Files))
	for _, f := range sk.Files {
		if f.Path != del.Path {
			files = append(files, f)
		}
	}
	sk.Files = files
	sk.UpdatedBy = s.actor
	sk.UpdatedAt = time.Now().UTC()
	return nil
}

func storedRevision(sk *domain.Skill, filePath string) (revision string, exists bool) {
	doc, ok := skillDocument(sk, filePath)
	if !ok {
		return "", false
	}
	return doc.Revision, true
}

// checkPrecondition mirrors fleet-db's conditional-write rule: If-None-Match
// any refuses an existing document, and a non-empty If-Match must equal the
// stored revision of a document that exists. ifMatch arrives already
// normalized to a bare revision, as it is on the HTTP client.
func checkPrecondition(ref domain.SkillRef, filePath, ifMatch string, ifNoneMatchAny bool, stored string, exists bool) error {
	if exists && ifNoneMatchAny {
		return &domain.SkillPreconditionError{Ref: ref, Path: filePath, Stored: stored}
	}
	if ifMatch == "" {
		return nil
	}
	// "*" is the wildcard precondition: any revision, but it must exist —
	// which the exists check above already established.
	if ifMatch == "*" {
		return nil
	}
	if !exists {
		return &domain.SkillPreconditionError{Ref: ref, Path: filePath, Expected: ifMatch}
	}
	if ifMatch == stored {
		return nil
	}
	return &domain.SkillPreconditionError{Ref: ref, Path: filePath, Expected: ifMatch, Stored: stored}
}

// replaceSkillFile swaps the entry at the same path or appends a new one,
// keeping the existing order so an unrelated edit never reshuffles the set.
func replaceSkillFile(files []domain.SkillFile, replacement domain.SkillFile) []domain.SkillFile {
	out := make([]domain.SkillFile, 0, len(files)+1)
	replaced := false
	for _, f := range files {
		if f.Path == replacement.Path {
			out = append(out, replacement)
			replaced = true
			continue
		}
		out = append(out, f)
	}
	if !replaced {
		out = append(out, replacement)
	}
	return out
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
