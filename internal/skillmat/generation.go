package skillmat

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"regexp"
	"sort"
)

const (
	projectionStateDir       = ".loom-skill-projections"
	projectionGenerationsDir = projectionStateDir + "/generations"
	projectionCurrentPath    = projectionStateDir + "/current"
	projectionLockPath       = projectionStateDir + "/materialize.lock"
	agentsSkillsAliasTarget  = "../" + projectionCurrentPath + "/" + AgentsSkillsDir
	claudeSkillsAliasTarget  = "../" + projectionCurrentPath + "/" + ClaudeSkillsDir

	maxPreservedProjectionFileBytes  = 16 << 20
	maxPreservedProjectionTotalBytes = 64 << 20
)

var generationBasePattern = regexp.MustCompile(`^[0-9a-f]{24}$`)

// generationRoot presents one immutable generation through the logical
// .agents/skills and .claude/skills names used by the materializer. It is an
// internal seam: callers still see only Materialize, while all preparation is
// redirected away from the currently visible generation.
type generationRoot struct {
	secureRoot
	prefix string
}

func (r generationRoot) Close() error { return nil }
func (r generationRoot) name(name string) string {
	return path.Join(r.prefix, name)
}
func (r generationRoot) ReadFile(name string, maxBytes int64) ([]byte, os.FileMode, error) {
	return r.secureRoot.ReadFile(r.name(name), maxBytes)
}
func (r generationRoot) ReadDir(name string) ([]string, error) {
	return r.secureRoot.ReadDir(r.name(name))
}
func (r generationRoot) Lstat(name string) (securePathInfo, error) {
	return r.secureRoot.Lstat(r.name(name))
}
func (r generationRoot) MkdirAll(name string, perm os.FileMode) error {
	return r.secureRoot.MkdirAll(r.name(name), perm)
}
func (r generationRoot) CreateFile(name string, content []byte, perm os.FileMode) error {
	return r.secureRoot.CreateFile(r.name(name), content, perm)
}
func (r generationRoot) CopyFile(sourceName, destinationName string, perm os.FileMode, maxBytes int64) (int64, error) {
	return r.secureRoot.CopyFile(r.name(sourceName), r.name(destinationName), perm, maxBytes)
}
func (r generationRoot) AppendFile(name string, content []byte, perm os.FileMode) error {
	return r.secureRoot.AppendFile(r.name(name), content, perm)
}
func (r generationRoot) Symlink(target, name string) error {
	return r.secureRoot.Symlink(target, r.name(name))
}
func (r generationRoot) Rename(oldName, newName string) error {
	return r.secureRoot.Rename(r.name(oldName), r.name(newName))
}
func (r generationRoot) Swap(firstName, secondName string) error {
	return r.secureRoot.Swap(r.name(firstName), r.name(secondName))
}
func (r generationRoot) Lock(ctx context.Context, name string) (io.Closer, error) {
	return r.secureRoot.Lock(ctx, r.name(name))
}
func (r generationRoot) Remove(name string) error {
	return r.secureRoot.Remove(r.name(name))
}
func (r generationRoot) RemoveDir(name string) error {
	return r.secureRoot.RemoveDir(r.name(name))
}

func currentGeneration(root secureRoot) (string, error) {
	info, err := root.Lstat(projectionCurrentPath)
	if errors.Is(err, fs.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("inspect current skill projection: %w", err)
	}
	if !info.Mode.IsDir() {
		return "", fmt.Errorf("current skill projection is not a directory")
	}
	return projectionCurrentPath, nil
}

func ensureProjectionAliases(root secureRoot) error {
	if err := root.MkdirAll(".agents", 0o755); err != nil {
		return fmt.Errorf("create .agents directory: %w", err)
	}
	if err := root.MkdirAll(".claude", 0o755); err != nil {
		return fmt.Errorf("create .claude directory: %w", err)
	}
	if err := root.MkdirAll(projectionGenerationsDir, 0o700); err != nil {
		return fmt.Errorf("create skill projection generations directory: %w", err)
	}
	for _, alias := range []struct{ name, target string }{
		{AgentsSkillsDir, agentsSkillsAliasTarget},
		{ClaudeSkillsDir, claudeSkillsAliasTarget},
	} {
		info, err := root.Lstat(alias.name)
		if errors.Is(err, fs.ErrNotExist) {
			if err := root.Symlink(alias.target, alias.name); err != nil {
				return fmt.Errorf("create projection alias %q: %w", alias.name, err)
			}
			continue
		}
		if err != nil {
			return fmt.Errorf("inspect projection alias %q: %w", alias.name, err)
		}
		if info.Mode&os.ModeSymlink == 0 || info.LinkTarget != alias.target {
			return fmt.Errorf("materialized directory %q collides with existing path", alias.name)
		}
	}
	return nil
}

func newGeneration(root secureRoot) (string, error) {
	suffix, err := randomTempSuffix()
	if err != nil {
		return "", fmt.Errorf("generate skill projection generation name: %w", err)
	}
	name := path.Join(projectionGenerationsDir, suffix)
	if err := root.MkdirAll(path.Join(name, AgentsSkillsDir), 0o755); err != nil {
		return name, fmt.Errorf("create staged canonical skills directory: %w", err)
	}
	if err := root.MkdirAll(path.Join(name, ClaudeSkillsDir), 0o755); err != nil {
		return name, fmt.Errorf("create staged Claude skills directory: %w", err)
	}
	return name, nil
}

func cloneGeneration(root secureRoot, source, destination string) error {
	if source == "" {
		return nil
	}
	remaining := int64(maxPreservedProjectionTotalBytes)
	for _, logicalRoot := range []string{AgentsSkillsDir, ClaudeSkillsDir} {
		if err := cloneProjectionDir(root, path.Join(source, logicalRoot), path.Join(destination, logicalRoot), &remaining); err != nil {
			return err
		}
	}
	return nil
}

func cloneProjectionDir(root secureRoot, source, destination string, remaining *int64) error {
	names, err := root.ReadDir(source)
	if err != nil {
		return fmt.Errorf("read current projection directory %q: %w", source, err)
	}
	sort.Strings(names)
	for _, base := range names {
		src := path.Join(source, base)
		dst := path.Join(destination, base)
		info, err := root.Lstat(src)
		if err != nil {
			return fmt.Errorf("inspect current projection path %q: %w", src, err)
		}
		switch {
		case info.Mode.IsDir():
			if err := root.MkdirAll(dst, info.Mode.Perm()); err != nil {
				return fmt.Errorf("create staged projection directory %q: %w", dst, err)
			}
			if err := cloneProjectionDir(root, src, dst, remaining); err != nil {
				return err
			}
		case info.Mode.IsRegular():
			limit := min(*remaining, int64(maxPreservedProjectionFileBytes))
			written, err := root.CopyFile(src, dst, info.Mode.Perm(), limit)
			if err != nil {
				return fmt.Errorf("copy staged projection path %q: %w", dst, err)
			}
			*remaining -= written
		case info.Mode&os.ModeSymlink != 0:
			if err := root.Symlink(info.LinkTarget, dst); err != nil {
				return fmt.Errorf("copy staged projection link %q: %w", dst, err)
			}
		default:
			return fmt.Errorf("current projection path %q has unsupported mode %v", src, info.Mode)
		}
	}
	return nil
}

func cleanupAbandonedGenerations(root secureRoot) error {
	names, err := root.ReadDir(projectionGenerationsDir)
	if err != nil {
		return fmt.Errorf("list abandoned skill projection generations: %w", err)
	}
	sort.Strings(names)
	for _, base := range names {
		if !generationBasePattern.MatchString(base) {
			return fmt.Errorf("unexpected path %q in skill projection generations", base)
		}
		if err := removeProjectionTree(root, path.Join(projectionGenerationsDir, base)); err != nil {
			return fmt.Errorf("remove abandoned skill projection generation %q: %w", base, err)
		}
	}
	return nil
}

func removeProjectionTree(root secureRoot, name string) error {
	info, err := root.Lstat(name)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if !info.Mode.IsDir() {
		return root.Remove(name)
	}
	names, err := root.ReadDir(name)
	if err != nil {
		return err
	}
	sort.Strings(names)
	for _, base := range names {
		if err := removeProjectionTree(root, path.Join(name, base)); err != nil {
			return err
		}
	}
	return root.RemoveDir(name)
}

func cleanupFailedGeneration(root secureRoot, generation string, cause error) error {
	if cleanupErr := removeProjectionTree(root, generation); cleanupErr != nil {
		return errors.Join(cause, fmt.Errorf("remove failed staged skill projection: %w", cleanupErr))
	}
	return cause
}

func prepareGeneration(
	root secureRoot,
	currentPath string,
	previous *marker,
	entries []desiredEntry,
	desiredPaths []string,
	markerBytes []byte,
) (generation string, retErr error) {
	generation, err := newGeneration(root)
	if err != nil {
		if generation != "" {
			return generation, cleanupFailedGeneration(root, generation, err)
		}
		return "", err
	}
	defer func() {
		if retErr != nil {
			retErr = cleanupFailedGeneration(root, generation, retErr)
		}
	}()
	if err := cloneGeneration(root, currentPath, generation); err != nil {
		return generation, fmt.Errorf("stage current skill projection: %w", err)
	}
	stagedRoot := generationRoot{secureRoot: root, prefix: generation}
	if err := sweepTemporaryFiles(stagedRoot); err != nil {
		return generation, fmt.Errorf("sweep staged skill materialization temporaries: %w", err)
	}
	stalePaths := findStalePaths(previous, desiredPaths)
	preDeleted := make(map[string]bool)
	if err := writeProjection(stagedRoot, entries, stalePaths, preDeleted); err != nil {
		return generation, err
	}
	remainingStale := stalePaths[:0]
	for _, stale := range stalePaths {
		if !preDeleted[stale] {
			remainingStale = append(remainingStale, stale)
		}
	}
	if err := cleanupStale(stagedRoot, remainingStale); err != nil {
		return generation, err
	}
	if err := writeMarkerAtomically(stagedRoot, markerBytes); err != nil {
		return generation, fmt.Errorf("write skill marker: %w", err)
	}
	return generation, nil
}

func commitGeneration(root secureRoot, generation string) (retErr error) {
	_, err := root.Lstat(projectionCurrentPath)
	if errors.Is(err, fs.ErrNotExist) {
		if err := root.Rename(generation, projectionCurrentPath); err != nil {
			return fmt.Errorf("commit initial skill projection: %w", err)
		}
		return nil
	}
	if err != nil {
		return fmt.Errorf("inspect current skill projection before commit: %w", err)
	}
	if err := root.Swap(generation, projectionCurrentPath); err != nil {
		return fmt.Errorf("commit current skill projection: %w", err)
	}
	return nil
}
