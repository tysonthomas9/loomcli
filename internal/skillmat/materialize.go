// Package skillmat materializes FleetDB skills into agent working directories.
package skillmat

import (
	"bytes"
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
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	"golang.org/x/text/cases"
	"golang.org/x/text/unicode/norm"
	"gopkg.in/yaml.v3"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/store"
)

const (
	// AgentsSkillsDir is the canonical skill directory relative to an agent's working directory.
	AgentsSkillsDir = ".agents/skills"
	// ClaudeSkillsDir contains relative compatibility links to AgentsSkillsDir.
	ClaudeSkillsDir = ".claude/skills"
	// MarkerPath records the projection hash and every managed file or link.
	MarkerPath = AgentsSkillsDir + "/.loom-skills-marker.json"
	// IndexPath is the live catalog of skills in the current projection.
	IndexPath = AgentsSkillsDir + "/INDEX.md"
	// CatalogSkillName is the synthetic skill that points agents at IndexPath.
	CatalogSkillName = "loom-skill-catalog"

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

type desiredNode struct {
	Path  string
	Entry desiredEntry
	Dir   bool
}

// StoreUnavailableError reports that the skill listing could not be loaded.
// Callers use it to preserve the existing spawn-time degraded-mode behavior
// without hiding local collision or filesystem failures.
type StoreUnavailableError struct {
	Err error
}

func (e *StoreUnavailableError) Error() string { return fmt.Sprintf("load skills: %v", e.Err) }
func (e *StoreUnavailableError) Unwrap() error { return e.Err }

// IsStoreUnavailable reports whether materialization stopped before touching
// the target because the backing skill store could not be read.
func IsStoreUnavailable(err error) bool {
	var unavailable *StoreUnavailableError
	return errors.As(err, &unavailable)
}

// Materialize resolves workspace and role skills and reconciles targetDir's
// Loom-managed skill projection. A matching marker is a no-op. Store read
// failures are returned as StoreUnavailableError before the filesystem is
// touched; every other error is a fail-closed local preparation failure.
//
//nolint:cyclop,funlen // The reconcile pipeline reads as one ordered sequence of gates.
func Materialize(ctx context.Context, st store.Store, workspace, roleName, targetDir string) error {
	if err := ensurePlatformSupported(); err != nil {
		return err
	}
	if st == nil || st.Skills() == nil {
		return fmt.Errorf("materialize skills: skill store is not configured")
	}
	if strings.TrimSpace(targetDir) == "" {
		return fmt.Errorf("materialize skills: target directory is required")
	}

	skills, err := st.Skills().List(ctx, workspace, store.SkillFilter{})
	if err != nil {
		if isUnavailableStoreError(err) {
			return &StoreUnavailableError{Err: err}
		}
		return fmt.Errorf("load skills: %w", err)
	}
	entries, err := desiredEntries(domain.ResolveSkillChainDetail(skills, roleName))
	if err != nil {
		return err
	}
	projectionHash, err := hashEntries(entries)
	if err != nil {
		return fmt.Errorf("hash skill projection: %w", err)
	}
	paths := entryPaths(entries)

	root, err := openSecureRoot(targetDir)
	if err != nil {
		return fmt.Errorf("open skill target %q: %w", targetDir, err)
	}
	defer root.Close()

	previous, err := readMarker(root)
	if err != nil {
		return err
	}
	if err := sweepTemporaryFiles(root); err != nil {
		return fmt.Errorf("sweep skill materialization temporaries: %w", err)
	}
	if previous != nil && previous.Hash == projectionHash {
		if !equalStrings(previous.Paths, paths) {
			return fmt.Errorf("skill marker hash matches but recorded paths differ; refusing cleanup")
		}
		matches, err := projectionMatches(root, entries)
		if err != nil {
			return fmt.Errorf("verify materialized skill projection: %w", err)
		}
		if matches {
			return ensureGitExcludes(ctx, targetDir)
		}
	}

	if err := detectDesiredCollisions(entries); err != nil {
		return err
	}
	if err := detectExistingCollisions(root, targetDir, entries, previous); err != nil {
		return err
	}
	stalePaths := findStalePaths(previous, paths)
	preDeleted := make(map[string]bool)
	if err := writeProjection(root, entries, stalePaths, preDeleted); err != nil {
		return err
	}
	remainingStale := stalePaths[:0]
	for _, stale := range stalePaths {
		if !preDeleted[stale] {
			remainingStale = append(remainingStale, stale)
		}
	}
	if err := cleanupStale(root, remainingStale); err != nil {
		return err
	}
	markerBytes, err := json.MarshalIndent(marker{Version: markerVersion, Hash: projectionHash, Paths: paths}, "", "  ")
	if err != nil {
		return fmt.Errorf("encode skill marker: %w", err)
	}
	markerBytes = append(markerBytes, '\n')
	if err := writeMarkerAtomically(root, markerBytes); err != nil {
		return fmt.Errorf("write skill marker: %w", err)
	}
	if err := ensureGitExcludes(ctx, targetDir); err != nil {
		return fmt.Errorf("ensure skill git excludes: %w", err)
	}
	return nil
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
	removeEmptyParents(root, paths)
	return nil
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

func writeEntryAtomically(root secureRoot, entry desiredEntry) (retErr error) {
	var nonce [12]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return fmt.Errorf("generate projection temporary name: %w", err)
	}
	temporary := path.Join(path.Dir(entry.Path), projectionTempPrefix+hex.EncodeToString(nonce[:]))
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

func isUnavailableStoreError(err error) bool {
	if errors.Is(err, context.Canceled) {
		return false
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	var networkError net.Error
	if errors.As(err, &networkError) {
		return true
	}
	return fleetDBServerErrorPattern.MatchString(err.Error())
}

//nolint:funlen // One skill's projection entries are derived in one pass.
func desiredEntries(resolved []domain.ResolvedSkill) ([]desiredEntry, error) {
	entries := make([]desiredEntry, 0, len(resolved)*2+3)
	for _, item := range resolved {
		skill := item.Skill
		if skill == nil {
			continue
		}
		if err := domain.ValidateSkillName(skill.Name); err != nil {
			return nil, fmt.Errorf("materialize skill %q: %w", skill.Name, err)
		}
		frontmatter, err := yaml.Marshal(struct {
			Name        string `yaml:"name"`
			Description string `yaml:"description"`
		}{Name: skill.Name, Description: skill.Description})
		if err != nil {
			return nil, fmt.Errorf("render skill %q frontmatter: %w", skill.Name, err)
		}
		skillDir := path.Join(AgentsSkillsDir, skill.Name)
		entries = append(entries, desiredEntry{
			Path:       path.Join(skillDir, domain.SkillFileNameSKILLMD),
			Kind:       entryFile,
			Content:    []byte("---\n" + string(frontmatter) + "---\n" + skill.Content),
			Mode:       0o644,
			Skill:      skill.Name,
			SourcePath: domain.SkillFileNameSKILLMD,
		})
		for _, file := range skill.Files {
			if err := validateBundledPath(file.Path); err != nil {
				return nil, fmt.Errorf("materialize skill %q file %q: %w", skill.Name, file.Path, err)
			}
			mode := os.FileMode(0o644)
			if file.Executable {
				mode = 0o755
			}
			entries = append(entries, desiredEntry{
				Path:       path.Join(skillDir, file.Path),
				Kind:       entryFile,
				Content:    []byte(file.Content),
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
	}
	entries = append(entries, catalogEntries(resolved)...)
	sort.Slice(entries, func(i, j int) bool { return entries[i].Path < entries[j].Path })
	return entries, nil
}

func catalogEntries(resolved []domain.ResolvedSkill) []desiredEntry {
	catalogDir := path.Join(AgentsSkillsDir, CatalogSkillName)
	return []desiredEntry{
		{
			Path:       IndexPath,
			Kind:       entryFile,
			Content:    renderSkillIndex(resolved),
			Mode:       0o644,
			Skill:      CatalogSkillName,
			SourcePath: path.Base(IndexPath),
		},
		{
			Path:       path.Join(catalogDir, domain.SkillFileNameSKILLMD),
			Kind:       entryFile,
			Content:    []byte(catalogSkillDocument),
			Mode:       0o644,
			Skill:      CatalogSkillName,
			SourcePath: domain.SkillFileNameSKILLMD,
		},
		{
			Path:       path.Join(ClaudeSkillsDir, CatalogSkillName),
			Kind:       entrySymlink,
			LinkTarget: "../../.agents/skills/" + CatalogSkillName,
			Skill:      CatalogSkillName,
			SourcePath: CatalogSkillName,
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
		if !catalogWritten && CatalogSkillName < item.Skill.Name {
			writeSkillIndexLine(&content, CatalogSkillName, catalogSkillDescription, false)
			catalogWritten = true
		}
		writeSkillIndexLine(&content, item.Skill.Name, item.Skill.Description, item.Shadowed != nil)
	}
	if !catalogWritten {
		writeSkillIndexLine(&content, CatalogSkillName, catalogSkillDescription, false)
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

func validateBundledPath(name string) error {
	if name == "" || !utf8.ValidString(name) {
		return fmt.Errorf("path must be non-empty valid UTF-8")
	}
	if strings.Contains(name, `\`) || strings.HasPrefix(name, "/") || strings.HasPrefix(name, "~") || strings.Contains(name, ":") {
		return fmt.Errorf("path %q must be a normalized relative path", name)
	}
	for _, r := range name {
		if unicode.IsControl(r) {
			return fmt.Errorf("path %q contains a control character", name)
		}
	}
	for _, part := range strings.Split(name, "/") {
		if part == "" || part == "." || part == ".." {
			return fmt.Errorf("path %q contains an unsafe component", name)
		}
	}
	if path.Clean(name) != name {
		return fmt.Errorf("path %q is not normalized", name)
	}
	return nil
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
	b, _, err := root.ReadFile(MarkerPath, maxMarkerBytes)
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
	if name == MarkerPath {
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
	return strings.HasPrefix(base, projectionTempPrefix) || strings.HasPrefix(base, path.Base(MarkerPath)+".tmp-")
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

func collisionKey(name string) string {
	return cases.Fold().String(norm.NFC.String(name))
}

func detectDesiredCollisions(entries []desiredEntry) error {
	files := make(map[string]desiredEntry, len(entries))
	dirs := make(map[string]desiredEntry, len(entries)*2)
	for _, entry := range entries {
		key := collisionKey(entry.Path)
		if previous, ok := files[key]; ok {
			return collisionError(entry.Skill, previous.SourcePath, entry.SourcePath)
		}
		if child, ok := dirs[key]; ok {
			return collisionError(entry.Skill, entry.SourcePath, child.SourcePath)
		}
		files[key] = entry
		parent := path.Dir(entry.Path)
		for parent != "." && parent != "/" {
			parentKey := collisionKey(parent)
			if file, ok := files[parentKey]; ok {
				return collisionError(entry.Skill, file.SourcePath, entry.SourcePath)
			}
			if _, ok := dirs[parentKey]; !ok {
				dirs[parentKey] = entry
			}
			parent = path.Dir(parent)
		}
	}
	return nil
}

func collisionError(skill, first, second string) error {
	return fmt.Errorf("materialize skill %q: paths %q and %q collide when written", skill, first, second)
}

func writeMarkerAtomically(root secureRoot, markerBytes []byte) (retErr error) {
	var nonce [12]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return fmt.Errorf("generate marker temporary name: %w", err)
	}
	temporary := MarkerPath + ".tmp-" + hex.EncodeToString(nonce[:])
	if err := root.CreateFile(temporary, markerBytes, 0o644); err != nil {
		return err
	}
	defer func() {
		if retErr != nil {
			_ = root.Remove(temporary)
		}
	}()
	if err := root.Rename(temporary, MarkerPath); err != nil {
		return err
	}
	return nil
}

func removeEmptyParents(root secureRoot, names []string) {
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
		_ = root.RemoveDir(dir)
	}
}

func pathDepth(name string) int { return strings.Count(name, "/") }

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

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

func entryExactlyMatches(root secureRoot, entry desiredEntry) (bool, error) {
	switch entry.Kind {
	case entryFile:
		info, err := root.Lstat(entry.Path)
		if errors.Is(err, fs.ErrNotExist) {
			return false, nil
		}
		if err != nil {
			return false, err
		}
		if !info.Mode.IsRegular() || info.Mode.Perm() != entry.Mode.Perm() {
			return false, nil
		}
		if info.Size != int64(len(entry.Content)) {
			return false, nil
		}
		content, mode, err := root.ReadFile(entry.Path, int64(len(entry.Content))+1)
		if errors.Is(err, fs.ErrNotExist) {
			return false, nil
		}
		if err != nil {
			return false, err
		}
		return mode.IsRegular() && mode.Perm() == entry.Mode.Perm() && bytes.Equal(content, entry.Content), nil
	case entrySymlink:
		info, err := root.Lstat(entry.Path)
		if errors.Is(err, fs.ErrNotExist) {
			return false, nil
		}
		if err != nil {
			return false, err
		}
		return info.Mode&os.ModeSymlink != 0 && info.LinkTarget == entry.LinkTarget, nil
	default:
		return false, fmt.Errorf("materialized path %q has unknown kind %q", entry.Path, entry.Kind)
	}
}

//nolint:gocognit,cyclop,funlen // The collision matrix (file/dir/symlink × managed/foreign) is deliberately exhaustive in one place.
func detectExistingCollisions(root secureRoot, targetDir string, entries []desiredEntry, previous *marker) error {
	managed := map[string]bool{}
	managedDirs := map[string]bool{}
	if previous != nil {
		for _, recorded := range previous.Paths {
			managed[recorded] = true
			for parent := path.Dir(recorded); parent != "."; parent = path.Dir(parent) {
				managedDirs[parent] = true
			}
		}
	}
	desired := make(map[string]desiredNode, len(entries)*2)
	for _, entry := range entries {
		desired[collisionKey(entry.Path)] = desiredNode{Path: entry.Path, Entry: entry}
		for parent := path.Dir(entry.Path); parent != "."; parent = path.Dir(parent) {
			key := collisionKey(parent)
			if _, ok := desired[key]; !ok {
				desired[key] = desiredNode{Path: parent, Entry: entry, Dir: true}
			}
		}
	}
	for _, rootName := range []string{AgentsSkillsDir, ClaudeSkillsDir} {
		absoluteRoot := filepath.Join(targetDir, filepath.FromSlash(rootName))
		err := filepath.WalkDir(absoluteRoot, func(name string, item fs.DirEntry, walkErr error) error {
			if errors.Is(walkErr, fs.ErrNotExist) {
				return nil
			}
			if walkErr != nil {
				return walkErr
			}
			rel, err := filepath.Rel(targetDir, name)
			if err != nil {
				return err
			}
			rel = filepath.ToSlash(rel)
			if rel == rootName {
				if item.Type()&os.ModeSymlink != 0 || !item.IsDir() {
					return fmt.Errorf("materialized directory %q collides with existing path %q", rootName, rel)
				}
				return nil
			}
			want, exactCollision := desired[collisionKey(rel)]
			if exactCollision {
				realDir := item.IsDir() && item.Type()&os.ModeSymlink == 0
				switch {
				case want.Dir && realDir && managed[rel]:
					return fmt.Errorf("materialize skill %q: previously managed path %q was replaced by a directory", want.Entry.Skill, rel)
				case want.Dir && realDir:
					return nil
				case want.Dir && managed[rel]:
					return nil
				case !want.Dir && managed[rel]:
					return nil
				case !want.Dir && realDir && managedDirs[rel]:
					return nil
				}
				if !want.Dir {
					matches, err := entryExactlyMatches(root, want.Entry)
					if err != nil {
						return fmt.Errorf("inspect existing path %q: %w", rel, err)
					}
					if matches {
						return nil
					}
				}
				return fmt.Errorf("materialize skill %q: desired path %q collides with existing unrecorded path %q", want.Entry.Skill, want.Entry.Path, rel)
			}
			if ancestor, ok := desiredFileAncestor(desired, rel); ok && !managed[rel] && !managedDirs[rel] {
				return fmt.Errorf("materialize skill %q: desired path %q collides with existing unrecorded path %q", ancestor.Entry.Skill, ancestor.Entry.Path, rel)
			}
			return nil
		})
		if err != nil && !errors.Is(err, fs.ErrNotExist) {
			return err
		}
	}
	return nil
}

func desiredFileAncestor(desired map[string]desiredNode, name string) (desiredNode, bool) {
	for current := path.Dir(name); current != "." && current != "/"; current = path.Dir(current) {
		if node, ok := desired[collisionKey(current)]; ok && !node.Dir {
			return node, true
		}
	}
	return desiredNode{}, false
}

//nolint:funlen // Exclude-file reconciliation keeps its read/merge/write steps inline.
func ensureGitExcludes(ctx context.Context, targetDir string) error {
	inside := gitCommandContext(ctx, targetDir, "rev-parse", "--is-inside-work-tree")
	out, err := inside.Output()
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return nil
	}
	if strings.TrimSpace(string(out)) != "true" {
		return nil
	}
	resolve := gitCommandContext(ctx, targetDir, "rev-parse", "--git-path", "info/exclude")
	out, err = resolve.Output()
	if err != nil {
		return fmt.Errorf("resolve git info/exclude: %w", err)
	}
	excludePath := strings.TrimSpace(string(out))
	if !filepath.IsAbs(excludePath) {
		excludePath = filepath.Join(targetDir, excludePath)
	}
	excludePath = filepath.Clean(excludePath)
	excludeRootPath := filepath.Dir(filepath.Dir(excludePath))
	excludeName, err := filepath.Rel(excludeRootPath, excludePath)
	if err != nil {
		return fmt.Errorf("resolve git exclude relative path: %w", err)
	}
	excludeName = filepath.ToSlash(excludeName)
	excludeRoot, err := openSecureRoot(excludeRootPath)
	if err != nil {
		return fmt.Errorf("open git metadata root %q: %w", excludeRootPath, err)
	}
	defer excludeRoot.Close()
	b, _, err := excludeRoot.ReadFile(excludeName, 0)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	wanted := []string{AgentsSkillsDir + "/", ClaudeSkillsDir + "/"}
	present := make(map[string]bool, len(wanted))
	for _, line := range strings.Split(strings.ReplaceAll(string(b), "\r\n", "\n"), "\n") {
		present[line] = true
	}
	var addition strings.Builder
	if len(b) > 0 && b[len(b)-1] != '\n' {
		addition.WriteByte('\n')
	}
	for _, line := range wanted {
		if !present[line] {
			addition.WriteString(line)
			addition.WriteByte('\n')
		}
	}
	if addition.Len() == 0 {
		return nil
	}
	return excludeRoot.AppendFile(excludeName, []byte(addition.String()), 0o644)
}

func gitCommandContext(ctx context.Context, targetDir string, args ...string) *exec.Cmd {
	commandArgs := append([]string{"-C", targetDir}, args...)
	cmd := exec.CommandContext(ctx, "git", commandArgs...) //nolint:gosec,norawexec // fixed git inspection command
	cmd.Env = sanitizedGitEnv(os.Environ())
	return cmd
}

func sanitizedGitEnv(env []string) []string {
	clean := make([]string, 0, len(env))
	for _, item := range env {
		key, _, _ := strings.Cut(item, "=")
		if strings.HasPrefix(strings.ToUpper(key), "GIT_") {
			continue
		}
		clean = append(clean, item)
	}
	return clean
}
