// Package skillmat materializes FleetDB skills into agent working directories.
package skillmat

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"syscall"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/store"
)

const (
	// AgentsSkillsDir is the canonical skill directory relative to an agent's working directory.
	AgentsSkillsDir = ".agents/skills"
	// ClaudeSkillsDir contains relative compatibility links to AgentsSkillsDir.
	ClaudeSkillsDir = ".claude/skills"
	// markerPath records the projection hash and every managed file or link.
	markerPath = AgentsSkillsDir + "/.loom-skills-marker.json"
	// indexPath is the live catalog of skills in the current projection.
	indexPath = AgentsSkillsDir + "/INDEX.md"
	// catalogSkillName is the synthetic skill that points agents at indexPath.
	catalogSkillName = "loom-skill-catalog"

	markerVersion  = 1
	maxMarkerBytes = 1 << 20

	projectionTempPrefix = ".loom-skill-tmp-"

	catalogSkillDescription = "Read this before listing or choosing skills. Loom adds/removes skills between turns; a session-start skill list may be stale. The live catalog is .agents/skills/INDEX.md."
	catalogSkillDocument    = "---\n" +
		"name: loom-skill-catalog\n" +
		"description: " + catalogSkillDescription + "\n" +
		"---\n" +
		"Loom manages the skills in this directory centrally. The set can change\n" +
		"between your turns: skills are added, updated, and removed while your\n" +
		"session is running.\n" +
		"\n" +
		"- The authoritative, always-current catalog: `.agents/skills/INDEX.md`.\n" +
		"- Any skill list captured at session start may be stale; prefer INDEX.md.\n" +
		"- To use a skill, read `.agents/skills/<name>/SKILL.md` and follow it.\n"
	skillIndexPreamble = "# Loom skills — live catalog\n" +
		"\n" +
		"Current as of the last turn boundary. Loom rewrites this file when skills\n" +
		"change; it supersedes any skill list captured at session start.\n" +
		"\n"
)

type marker struct {
	Version int      `json:"version"`
	Hash    string   `json:"hash"`
	Paths   []string `json:"paths"`
}

type entryKind string

const (
	entryFile    entryKind = "file"
	entrySymlink entryKind = "symlink"
)

type desiredEntry struct {
	Path       string
	Kind       entryKind
	Content    []byte
	Mode       os.FileMode
	LinkTarget string
	Skill      string
	SourcePath string
}

type plannedSkillTree struct {
	Skill *domain.Skill
	Tree  *domain.WorkspaceFileTree
}

// materializationPlan freezes the selected Skill metadata and exact immutable
// manifests before any lease is acquired or provider bytes are downloaded.
type materializationPlan struct {
	Resolved      []domain.ResolvedSkill
	Skills        []plannedSkillTree
	TreeRevisions []string
}

// StoreUnavailableError reports that an authoritative remote dependency could
// not be read or fenced before the local projection was touched. Callers may
// preserve the last safe projection without hiding integrity/filesystem bugs.
type StoreUnavailableError struct {
	Err error
}

func (e *StoreUnavailableError) Error() string {
	return fmt.Sprintf("materialize skills: dependency unavailable: %v", e.Err)
}
func (e *StoreUnavailableError) Unwrap() error { return e.Err }

// IsStoreUnavailable reports whether materialization stopped before touching
// the target because the backing skill store could not be read.
func IsStoreUnavailable(err error) bool {
	var unavailable *StoreUnavailableError
	return errors.As(err, &unavailable)
}

// materialize resolves workspace and role skills and atomically replaces
// targetDir's Loom-managed skill projection. A matching marker is a no-op.
// Store read failures are returned as StoreUnavailableError before the
// filesystem is touched; every other error leaves the current generation
// selected and is a fail-closed local preparation failure.
//
//nolint:cyclop,funlen,unparam // Workspace is semantic input; current fixtures intentionally share one workspace.
func materialize(ctx context.Context, st store.Store, workspace, roleName, targetDir string) error {
	return materializeWithRootOpener(ctx, st, workspace, roleName, targetDir, openSecureRoot)
}

type secureRootOpener func(string) (secureRoot, error)

//nolint:cyclop,funlen,gocognit // The reconcile pipeline reads as one ordered sequence of gates.
func materializeWithRootOpener(
	ctx context.Context, st store.Store, workspace, roleName, targetDir string, openRoot secureRootOpener,
) error {
	if err := validateMaterializationInputs(st, targetDir); err != nil {
		return err
	}
	plan, err := resolveMaterializationPlan(ctx, st, workspace, roleName)
	if err != nil {
		return err
	}
	return materializePlanWithRootOpener(ctx, st, workspace, targetDir, plan, openRoot, nil)
}

type beforeMaterializationCommit func(context.Context) error

//nolint:cyclop,funlen,gocognit // The reconcile pipeline reads as one ordered sequence of gates.
func materializePlanWithRootOpener(
	ctx context.Context,
	st store.Store,
	workspace string,
	targetDir string,
	plan *materializationPlan,
	openRoot secureRootOpener,
	beforeCommit beforeMaterializationCommit,
) error {
	if err := ensurePlatformSupported(); err != nil {
		return err
	}
	if err := ensureAtomicProjectionSupported(); err != nil {
		return err
	}
	if err := validateMaterializationInputs(st, targetDir); err != nil {
		return err
	}
	if plan == nil {
		return fmt.Errorf("materialize skills: immutable plan is required")
	}
	entries, err := hydrateMaterializationPlan(ctx, st.WorkspaceFiles(), workspace, plan)
	if err != nil {
		return err
	}
	projectionHash, err := hashEntries(entries)
	if err != nil {
		return fmt.Errorf("hash skill projection: %w", err)
	}
	paths := entryPaths(entries)

	root, err := openRoot(targetDir)
	if err != nil {
		return fmt.Errorf("open skill target %q: %w", targetDir, err)
	}
	defer root.Close()

	if err := root.MkdirAll(projectionStateDir, 0o700); err != nil {
		return fmt.Errorf("create skill projection state directory: %w", err)
	}
	projectionLock, err := root.Lock(ctx, projectionLockPath)
	if err != nil {
		return fmt.Errorf("lock skill projection target: %w", err)
	}
	defer func() { _ = projectionLock.Close() }()

	if err := ensureProjectionAliases(root, targetDir, entries); err != nil {
		return err
	}
	if err := cleanupAbandonedGenerations(root); err != nil {
		return err
	}
	currentGenerationPath, err := currentGeneration(root)
	if err != nil {
		return err
	}
	var currentRoot secureRoot
	if currentGenerationPath != "" {
		currentRoot = generationRoot{secureRoot: root, prefix: currentGenerationPath}
	}
	var previous *marker
	if currentRoot != nil {
		previous, err = readMarker(currentRoot)
		if err != nil {
			return err
		}
	}
	if currentRoot != nil {
		if err := sweepTemporaryFiles(currentRoot); err != nil {
			return fmt.Errorf("sweep skill materialization temporaries: %w", err)
		}
	}
	current := false
	if currentRoot != nil {
		current, err = projectionIsCurrent(currentRoot, previous, projectionHash, paths, entries)
	}
	if err != nil {
		return err
	}
	if current {
		return ensureSkillGitExcludes(ctx, targetDir)
	}

	if err := detectDesiredCollisions(entries); err != nil {
		return err
	}
	if currentRoot != nil {
		physicalTarget := filepath.Join(targetDir, filepath.FromSlash(currentGenerationPath))
		if err := detectExistingCollisions(currentRoot, physicalTarget, entries, previous); err != nil {
			return err
		}
	}
	if err := ensureSkillGitExcludes(ctx, targetDir); err != nil {
		return err
	}
	markerBytes, err := json.MarshalIndent(marker{Version: markerVersion, Hash: projectionHash, Paths: paths}, "", "  ")
	if err != nil {
		return fmt.Errorf("encode skill marker: %w", err)
	}
	markerBytes = append(markerBytes, '\n')

	stagedGeneration, err := prepareGeneration(root, currentGenerationPath, previous, entries, paths, markerBytes)
	if err != nil {
		return err
	}
	if beforeCommit != nil {
		if err := beforeCommit(ctx); err != nil {
			return cleanupFailedGeneration(root, stagedGeneration, fmt.Errorf("verify skill materialization lease before commit: %w", err))
		}
	}
	if err := commitGeneration(root, stagedGeneration); err != nil {
		return cleanupFailedGeneration(root, stagedGeneration, err)
	}
	if err := removeProjectionTree(root, stagedGeneration); err != nil {
		return fmt.Errorf("skill projection committed but remove displaced generation: %w", err)
	}
	return nil
}

func validateMaterializationInputs(st store.Store, targetDir string) error {
	if st == nil || st.Skills() == nil {
		return fmt.Errorf("materialize skills: skill store is not configured")
	}
	if strings.TrimSpace(targetDir) == "" {
		return fmt.Errorf("materialize skills: target directory is required")
	}
	return nil
}

func ensureSkillGitExcludes(ctx context.Context, targetDir string) error {
	if err := ensureGitExcludes(ctx, targetDir); err != nil {
		return fmt.Errorf("ensure skill git excludes: %w", err)
	}
	return nil
}

// projectionIsCurrent reports whether the projection already on disk is the one
// entries describes, so materialization can stop without touching anything.
//
// A recorded marker whose hash matches but whose path list does not is refused
// rather than reconciled: the paths are what cleanup deletes from, and acting on
// a list that disagrees with the hash would delete from the wrong set.
func projectionIsCurrent(
	root secureRoot, previous *marker, projectionHash string, paths []string, entries []desiredEntry,
) (bool, error) {
	if previous == nil || previous.Hash != projectionHash {
		return false, nil
	}
	if !slices.Equal(previous.Paths, paths) {
		return false, fmt.Errorf("skill marker hash matches but recorded paths differ; refusing cleanup")
	}
	matches, err := projectionMatches(root, entries)
	if err != nil {
		return false, fmt.Errorf("verify materialized skill projection: %w", err)
	}
	return matches, nil
}

func findStalePaths(previous *marker, desiredPaths []string) []string {
	if previous == nil {
		return nil
	}
	desired := make(map[string]bool, len(desiredPaths))
	for _, name := range desiredPaths {
		desired[name] = true
	}
	paths := make([]string, 0, len(previous.Paths))
	for _, recorded := range previous.Paths {
		if !desired[recorded] {
			paths = append(paths, recorded)
		}
	}
	return paths
}

func cleanupStale(root secureRoot, paths []string) error {
	sort.Slice(paths, func(i, j int) bool {
		if pathDepth(paths[i]) != pathDepth(paths[j]) {
			return pathDepth(paths[i]) > pathDepth(paths[j])
		}
		return paths[i] > paths[j]
	})
	for _, recorded := range paths {
		if err := root.Remove(recorded); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("remove stale materialized path %q: %w", recorded, err)
		}
	}
	return removeEmptyParents(root, paths)
}

func writeProjection(root secureRoot, entries []desiredEntry, stalePaths []string, preDeleted map[string]bool) error {
	if err := root.MkdirAll(AgentsSkillsDir, 0o755); err != nil {
		return fmt.Errorf("create canonical skills directory: %w", err)
	}
	if err := root.MkdirAll(ClaudeSkillsDir, 0o755); err != nil {
		return fmt.Errorf("create Claude skills directory: %w", err)
	}
	for _, entry := range entries {
		// Stale ancestors and descendants block managed file/directory
		// transitions. A case-insensitive filesystem may also resolve a stale
		// spelling to the desired destination. Remove only those recorded
		// blockers before inspecting or installing this entry; all unrelated
		// stale paths remain until every desired entry has been upserted.
		var blockers []string
		wantKey := collisionKey(entry.Path)
		for _, stale := range stalePaths {
			if preDeleted[stale] {
				continue
			}
			staleKey := collisionKey(stale)
			if staleKey == wantKey || strings.HasPrefix(wantKey, staleKey+"/") || strings.HasPrefix(staleKey, wantKey+"/") {
				blockers = append(blockers, stale)
			}
		}
		if err := cleanupStale(root, blockers); err != nil {
			return fmt.Errorf("remove stale blocker for %q: %w", entry.Path, err)
		}
		for _, stale := range blockers {
			preDeleted[stale] = true
		}
		matches, err := entryExactlyMatches(root, entry)
		if err != nil {
			return fmt.Errorf("inspect materialized path %q: %w", entry.Path, err)
		}
		if matches {
			continue
		}
		if err := root.MkdirAll(path.Dir(entry.Path), 0o755); err != nil {
			return fmt.Errorf("create parent for %q: %w", entry.Path, err)
		}
		if err := writeEntryAtomically(root, entry); err != nil {
			return fmt.Errorf("write materialized path %q: %w", entry.Path, err)
		}
	}
	return nil
}

// randomTempSuffix returns the unpredictable tail of an atomic write's
// temporary name. Unpredictable rather than merely unique: the temporary is
// created inside a directory the agent can write to, so a guessable name is a
// name someone can occupy first.
func randomTempSuffix() (string, error) {
	var nonce [12]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(nonce[:]), nil
}

func writeEntryAtomically(root secureRoot, entry desiredEntry) (retErr error) {
	suffix, err := randomTempSuffix()
	if err != nil {
		return fmt.Errorf("generate projection temporary name: %w", err)
	}
	temporary := path.Join(path.Dir(entry.Path), projectionTempPrefix+suffix)
	switch entry.Kind {
	case entryFile:
		if err := root.CreateFile(temporary, entry.Content, entry.Mode); err != nil {
			return err
		}
	case entrySymlink:
		if err := root.Symlink(entry.LinkTarget, temporary); err != nil {
			return err
		}
	default:
		return fmt.Errorf("materialized path %q has unknown kind %q", entry.Path, entry.Kind)
	}
	defer func() {
		if retErr != nil {
			_ = root.Remove(temporary)
		}
	}()
	// rename(2) replaces files and symlinks atomically, but it cannot replace a
	// directory with a non-directory. Collision validation has already proved
	// that an existing directory here is managed and contains no unrecorded
	// child; remove it only after the complete temporary entry is ready.
	info, err := root.Lstat(entry.Path)
	if err == nil && info.Mode.IsDir() {
		if err := root.Remove(entry.Path); err != nil {
			return err
		}
	} else if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	if err := root.Rename(temporary, entry.Path); err != nil {
		return err
	}
	return nil
}

var fleetDBServerErrorPattern = regexp.MustCompile(`\bHTTP 5[0-9][0-9]\b`)

// isTransportError reports whether err is the call failing to complete rather
// than fleet-db answering. A cancelled context is excluded deliberately: that
// is the caller giving up, not the store being unreachable, and treating it as
// unavailable would turn a shutdown into a spurious degraded-mode warning.
func isTransportError(err error) bool {
	if errors.Is(err, context.Canceled) {
		return false
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	var networkError net.Error
	return errors.As(err, &networkError)
}

func isUnavailableStoreError(err error) bool {
	// The Canceled short-circuit inside isTransportError has to stay ahead of
	// the 5xx match: a cancelled request whose error text happens to quote an
	// earlier "HTTP 503" must still read as cancelled.
	if errors.Is(err, context.Canceled) {
		return false
	}
	return isTransportError(err) || fleetDBServerErrorPattern.MatchString(err.Error())
}

func resolveMaterializationPlan(ctx context.Context, st store.Store, workspace, roleName string) (*materializationPlan, error) {
	if st == nil || st.Skills() == nil || st.WorkspaceFiles() == nil {
		return nil, fmt.Errorf("materialize skills: Skill and workspace-file stores are required")
	}
	skills, err := st.Skills().List(ctx, workspace, store.SkillFilter{})
	if err != nil {
		if isUnavailableStoreError(err) {
			return nil, &StoreUnavailableError{Err: err}
		}
		return nil, fmt.Errorf("load skills: %w", err)
	}
	resolved := domain.ResolveSkillChainDetail(skills, roleName)
	for index := range resolved {
		resolved[index].Skill = resolved[index].Skill.Clone()
		resolved[index].Shadowed = resolved[index].Shadowed.Clone()
	}
	plan := &materializationPlan{Resolved: resolved}
	revisions := make(map[string]struct{})
	for _, item := range resolved {
		if item.Skill == nil {
			continue
		}
		tree, err := loadMaterializationManifest(ctx, st.WorkspaceFiles(), workspace, item.Skill)
		if err != nil {
			if isUnavailableStoreError(err) {
				return nil, &StoreUnavailableError{Err: err}
			}
			return nil, err
		}
		plan.Skills = append(plan.Skills, plannedSkillTree{Skill: item.Skill, Tree: tree})
		revisions[tree.Revision] = struct{}{}
	}
	plan.TreeRevisions = make([]string, 0, len(revisions))
	for revision := range revisions {
		plan.TreeRevisions = append(plan.TreeRevisions, revision)
	}
	sort.Strings(plan.TreeRevisions)
	return plan, nil
}

func loadMaterializationManifest(ctx context.Context, files store.WorkspaceFileStore, workspace string, skill *domain.Skill) (*domain.WorkspaceFileTree, error) {
	if files == nil || skill == nil || skill.FileTreeRevision == "" {
		return nil, fmt.Errorf("load skill tree: %w", domain.ErrIntegrity)
	}
	tree, err := files.GetTree(ctx, workspace, skill.FileTreeRevision)
	if err != nil {
		return nil, fmt.Errorf("load skill %q tree %q: %w", skill.Name, skill.FileTreeRevision, err)
	}
	if tree == nil || tree.Revision != skill.FileTreeRevision || tree.WorkspaceKey != workspace {
		return nil, fmt.Errorf("skill %q tree identity mismatch: %w", skill.Name, domain.ErrIntegrity)
	}
	canonical, err := domain.CanonicalWorkspaceFileManifest(tree.Files)
	if err != nil || domain.WorkspaceFileTreeRevision(canonical) != tree.Revision {
		return nil, fmt.Errorf("skill %q tree manifest is not canonical: %w", skill.Name, domain.ErrIntegrity)
	}
	clone := tree.Clone()
	clone.Files = canonical
	return clone, nil
}

// The complete desired projection is hydrated and validated from the exact
// manifests frozen in plan. A missing byte or integrity failure therefore
// leaves the previous materialization intact instead of replacing it with a
// partial projection.
func hydrateMaterializationPlan(ctx context.Context, files store.WorkspaceFileStore, workspace string, plan *materializationPlan) ([]desiredEntry, error) {
	entries := make([]desiredEntry, 0, len(plan.Resolved)*2+3)
	for _, item := range plan.Skills {
		snapshot, err := hydrateMaterializedSkillTree(ctx, files, workspace, item.Skill, item.Tree)
		if err != nil {
			if isUnavailableStoreError(err) {
				return nil, &StoreUnavailableError{Err: err}
			}
			return nil, err
		}
		skillEntries, err := entriesForSkill(item.Skill, snapshot)
		if err != nil {
			return nil, err
		}
		entries = append(entries, skillEntries...)
	}
	entries = append(entries, catalogEntries(plan.Resolved)...)
	sort.Slice(entries, func(i, j int) bool { return entries[i].Path < entries[j].Path })
	return entries, nil
}

func desiredEntries(ctx context.Context, files store.WorkspaceFileStore, workspace string, resolved []domain.ResolvedSkill) ([]desiredEntry, error) {
	plan := &materializationPlan{Resolved: resolved}
	for _, item := range resolved {
		if item.Skill == nil {
			continue
		}
		tree, err := loadMaterializationManifest(ctx, files, workspace, item.Skill)
		if err != nil {
			return nil, err
		}
		plan.Skills = append(plan.Skills, plannedSkillTree{Skill: item.Skill, Tree: tree})
	}
	return hydrateMaterializationPlan(ctx, files, workspace, plan)
}

// entriesForSkill derives the projection entries for one skill: its SKILL.md,
// one entry per bundled file, and the .claude symlink that points at the
// directory. An error means the skill is unprojectable as stored.
func entriesForSkill(skill *domain.Skill, snapshot domain.SkillFileTreeSnapshot) ([]desiredEntry, error) {
	if err := domain.ValidateSkillName(skill.Name); err != nil {
		return nil, fmt.Errorf("skill name: %w", err)
	}
	skillDir := path.Join(AgentsSkillsDir, skill.Name)
	entries := make([]desiredEntry, 0, len(snapshot.Files)+1)
	for _, file := range snapshot.Files {
		mode := os.FileMode(0o644)
		if file.Path != domain.SkillFileNameSKILLMD && file.Executable {
			mode = 0o755
		}
		entries = append(entries, desiredEntry{
			Path:       path.Join(skillDir, file.Path),
			Kind:       entryFile,
			Content:    append([]byte(nil), file.Bytes...),
			Mode:       mode,
			Skill:      skill.Name,
			SourcePath: file.Path,
		})
	}
	entries = append(entries, desiredEntry{
		Path:       path.Join(ClaudeSkillsDir, skill.Name),
		Kind:       entrySymlink,
		LinkTarget: "../../.agents/skills/" + skill.Name,
		Skill:      skill.Name,
		SourcePath: skill.Name,
	})
	// Paths that collide when written are a property of this skill's own file
	// list, so catching them here makes a colliding skill skippable like any
	// other unprojectable one. detectDesiredCollisions still runs over the whole
	// projection afterwards; that pass now only has cross-skill collisions left
	// to find, which would be a bug in the projection rather than in one record.
	if err := detectDesiredCollisions(entries); err != nil {
		return nil, err
	}
	return entries, nil
}

func hydrateMaterializedSkillTree(ctx context.Context, files store.WorkspaceFileStore, workspace string, skill *domain.Skill, tree *domain.WorkspaceFileTree) (domain.SkillFileTreeSnapshot, error) {
	if files == nil || skill == nil || tree == nil || tree.Revision != skill.FileTreeRevision || tree.WorkspaceKey != workspace {
		return domain.SkillFileTreeSnapshot{}, fmt.Errorf("hydrate skill tree: %w", domain.ErrIntegrity)
	}
	manifest := make([]domain.SkillFileTreeFile, 0, len(tree.Files))
	for _, file := range tree.Files {
		body, err := files.Download(ctx, workspace, tree.Revision, file.Path)
		if err != nil {
			return domain.SkillFileTreeSnapshot{}, fmt.Errorf("download skill %q file %q: %w", skill.Name, file.Path, err)
		}
		digest := sha256.Sum256(body)
		if int64(len(body)) != file.SizeBytes || fmt.Sprintf("sha256:%x", digest) != file.ContentHash {
			return domain.SkillFileTreeSnapshot{}, fmt.Errorf("skill %q file %q bytes do not match immutable metadata: %w", skill.Name, file.Path, domain.ErrIntegrity)
		}
		manifest = append(manifest, domain.SkillFileTreeFile{Path: file.Path, Bytes: body, MediaType: file.MediaType, Executable: file.Executable})
	}
	snapshot, err := domain.ValidateSkillFileTree(manifest)
	if err != nil {
		return domain.SkillFileTreeSnapshot{}, fmt.Errorf("validate skill %q tree: %w", skill.Name, err)
	}
	if snapshot.Name != skill.Name || snapshot.Description != skill.Description {
		return domain.SkillFileTreeSnapshot{}, fmt.Errorf("skill %q metadata does not match SKILL.md: %w", skill.Name, domain.ErrIntegrity)
	}
	return *snapshot, nil
}

func loadMaterializedSkillTree(ctx context.Context, files store.WorkspaceFileStore, workspace string, skill *domain.Skill) (domain.SkillFileTreeSnapshot, error) {
	tree, err := loadMaterializationManifest(ctx, files, workspace, skill)
	if err != nil {
		return domain.SkillFileTreeSnapshot{}, err
	}
	return hydrateMaterializedSkillTree(ctx, files, workspace, skill, tree)
}

func catalogEntries(resolved []domain.ResolvedSkill) []desiredEntry {
	catalogDir := path.Join(AgentsSkillsDir, catalogSkillName)
	return []desiredEntry{
		{
			Path:       indexPath,
			Kind:       entryFile,
			Content:    renderSkillIndex(resolved),
			Mode:       0o644,
			Skill:      catalogSkillName,
			SourcePath: path.Base(indexPath),
		},
		{
			Path:       path.Join(catalogDir, domain.SkillFileNameSKILLMD),
			Kind:       entryFile,
			Content:    []byte(catalogSkillDocument),
			Mode:       0o644,
			Skill:      catalogSkillName,
			SourcePath: domain.SkillFileNameSKILLMD,
		},
		{
			Path:       path.Join(ClaudeSkillsDir, catalogSkillName),
			Kind:       entrySymlink,
			LinkTarget: "../../.agents/skills/" + catalogSkillName,
			Skill:      catalogSkillName,
			SourcePath: catalogSkillName,
		},
	}
}

func renderSkillIndex(resolved []domain.ResolvedSkill) []byte {
	var content strings.Builder
	content.WriteString(skillIndexPreamble)
	catalogWritten := false
	for _, item := range resolved {
		if item.Skill == nil {
			continue
		}
		if !catalogWritten && catalogSkillName < item.Skill.Name {
			writeSkillIndexLine(&content, catalogSkillName, catalogSkillDescription, false)
			catalogWritten = true
		}
		writeSkillIndexLine(&content, item.Skill.Name, item.Skill.Description, item.Shadowed != nil)
	}
	if !catalogWritten {
		writeSkillIndexLine(&content, catalogSkillName, catalogSkillDescription, false)
	}
	return []byte(content.String())
}

func writeSkillIndexLine(content *strings.Builder, name, description string, shadowed bool) {
	content.WriteString("- **")
	content.WriteString(name)
	content.WriteString("** — ")
	content.WriteString(description)
	if shadowed {
		content.WriteString(" (overrides the workspace skill of the same name)")
	}
	content.WriteString(" → read `")
	content.WriteString(path.Join(AgentsSkillsDir, name, domain.SkillFileNameSKILLMD))
	content.WriteString("`\n")
}

func hashEntries(entries []desiredEntry) (string, error) {
	type hashEntry struct {
		Path       string    `json:"path"`
		Kind       entryKind `json:"kind"`
		Content    []byte    `json:"content,omitempty"`
		Mode       uint32    `json:"mode,omitempty"`
		LinkTarget string    `json:"link_target,omitempty"`
	}
	canonical := make([]hashEntry, 0, len(entries))
	for _, entry := range entries {
		canonical = append(canonical, hashEntry{
			Path:       entry.Path,
			Kind:       entry.Kind,
			Content:    entry.Content,
			Mode:       uint32(entry.Mode.Perm()),
			LinkTarget: entry.LinkTarget,
		})
	}
	b, err := json.Marshal(canonical)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:]), nil
}

func entryPaths(entries []desiredEntry) []string {
	paths := make([]string, 0, len(entries))
	for _, entry := range entries {
		paths = append(paths, entry.Path)
	}
	return paths
}

func readMarker(root secureRoot) (*marker, error) {
	b, _, err := root.ReadFile(markerPath, maxMarkerBytes)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read skill marker: %w", err)
	}
	var m marker
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, fmt.Errorf("decode skill marker: %w", err)
	}
	if m.Version != markerVersion || m.Hash == "" {
		return nil, fmt.Errorf("skill marker has unsupported or incomplete format")
	}
	seen := make(map[string]bool, len(m.Paths))
	for _, recorded := range m.Paths {
		if !validManagedPath(recorded) {
			return nil, fmt.Errorf("skill marker contains unsafe path %q", recorded)
		}
		if seen[recorded] {
			return nil, fmt.Errorf("skill marker records path %q more than once", recorded)
		}
		seen[recorded] = true
	}
	if !sort.StringsAreSorted(m.Paths) {
		return nil, fmt.Errorf("skill marker paths are not sorted")
	}
	return &m, nil
}

func validManagedPath(name string) bool {
	if name == "" || strings.ContainsRune(name, '\\') || path.Clean(name) != name || strings.HasPrefix(name, "/") {
		return false
	}
	if name == markerPath {
		return false
	}
	if strings.HasPrefix(name, AgentsSkillsDir+"/") || strings.HasPrefix(name, ClaudeSkillsDir+"/") {
		for _, part := range strings.Split(name, "/") {
			if part == "" || part == "." || part == ".." || isTemporaryBase(part) {
				return false
			}
		}
		return true
	}
	return false
}

func isTemporaryBase(base string) bool {
	return strings.HasPrefix(base, projectionTempPrefix) || strings.HasPrefix(base, path.Base(markerPath)+".tmp-")
}

// sweepTemporaryFiles removes only names from Loom's reserved temporary
// namespace, below the two managed roots. Directory enumeration, metadata
// checks, and removals all remain rooted at the already-open target fd.
func sweepTemporaryFiles(root secureRoot) error {
	for _, rootName := range []string{AgentsSkillsDir, ClaudeSkillsDir} {
		if err := sweepTemporaryDir(root, rootName); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return err
		}
	}
	return nil
}

func sweepTemporaryDir(root secureRoot, dir string) error {
	names, err := root.ReadDir(dir)
	if err != nil {
		return err
	}
	sort.Strings(names)
	for _, base := range names {
		name := path.Join(dir, base)
		info, err := root.Lstat(name)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return err
		}
		if isTemporaryBase(base) {
			if info.Mode.IsDir() {
				return fmt.Errorf("reserved temporary path %q is a directory", name)
			}
			if err := root.Remove(name); err != nil && !errors.Is(err, fs.ErrNotExist) {
				return fmt.Errorf("remove temporary path %q: %w", name, err)
			}
			continue
		}
		if info.Mode.IsDir() {
			if err := sweepTemporaryDir(root, name); err != nil {
				return err
			}
		}
	}
	return nil
}

func writeMarkerAtomically(root secureRoot, markerBytes []byte) (retErr error) {
	suffix, err := randomTempSuffix()
	if err != nil {
		return fmt.Errorf("generate marker temporary name: %w", err)
	}
	temporary := markerPath + ".tmp-" + suffix
	if err := root.CreateFile(temporary, markerBytes, 0o644); err != nil {
		return err
	}
	defer func() {
		if retErr != nil {
			_ = root.Remove(temporary)
		}
	}()
	if err := root.Rename(temporary, markerPath); err != nil {
		return err
	}
	return nil
}

func removeEmptyParents(root secureRoot, names []string) error {
	dirs := make(map[string]bool)
	for _, name := range names {
		for parent := path.Dir(name); parent != "." && parent != AgentsSkillsDir && parent != ClaudeSkillsDir; parent = path.Dir(parent) {
			dirs[parent] = true
		}
	}
	ordered := make([]string, 0, len(dirs))
	for dir := range dirs {
		ordered = append(ordered, dir)
	}
	sort.Slice(ordered, func(i, j int) bool { return pathDepth(ordered[i]) > pathDepth(ordered[j]) })
	for _, dir := range ordered {
		if err := root.RemoveDir(dir); err != nil && !errors.Is(err, fs.ErrNotExist) && !errors.Is(err, syscall.ENOTEMPTY) && !errors.Is(err, syscall.EEXIST) {
			return fmt.Errorf("remove empty materialized directory %q: %w", dir, err)
		}
	}
	return nil
}

func pathDepth(name string) int { return strings.Count(name, "/") }

// projectionMatches checks that every managed path still exists with the right
// shape — regular file with the right mode, or symlink to the right target.
//
// It deliberately does NOT compare file contents, and that asymmetry with
// entryExactlyMatches is the point. This runs on the fast path, where the
// marker hash already says the projection is the one Loom wrote. An agent may
// have edited its own working copy of a SKILL.md since then, and that edit has
// to survive: re-materializing on every turn must not overwrite the agent's
// work. Structural drift is still repaired, because it means the projection is
// no longer the thing the marker describes.
//
// Locked by TestMaterializeMatchingHashIsNoOp. Adding a content comparison here
// would make that test fail, and it would be the right test to believe.
func projectionMatches(root secureRoot, entries []desiredEntry) (bool, error) {
	for _, entry := range entries {
		info, err := root.Lstat(entry.Path)
		if errors.Is(err, fs.ErrNotExist) {
			return false, nil
		}
		if err != nil {
			return false, err
		}
		switch entry.Kind {
		case entryFile:
			if !info.Mode.IsRegular() || info.Mode.Perm() != entry.Mode.Perm() {
				return false, nil
			}
		case entrySymlink:
			if info.Mode&os.ModeSymlink == 0 || info.LinkTarget != entry.LinkTarget {
				return false, nil
			}
		default:
			return false, fmt.Errorf("materialized path %q has unknown kind %q", entry.Path, entry.Kind)
		}
	}
	return true, nil
}
