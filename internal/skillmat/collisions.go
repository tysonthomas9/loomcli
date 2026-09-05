package skillmat

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"

	"golang.org/x/text/cases"
	"golang.org/x/text/unicode/norm"
)

type desiredNode struct {
	Path  string
	Entry desiredEntry
	Dir   bool
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
		for parent := path.Dir(entry.Path); parent != "." && parent != "/"; parent = path.Dir(parent) {
			parentKey := collisionKey(parent)
			if file, ok := files[parentKey]; ok {
				return collisionError(entry.Skill, file.SourcePath, entry.SourcePath)
			}
			if _, ok := dirs[parentKey]; !ok {
				dirs[parentKey] = entry
			}
		}
	}
	return nil
}

func collisionError(skill, first, second string) error {
	return fmt.Errorf("materialize skill %q: paths %q and %q collide when written", skill, first, second)
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
		if !info.Mode.IsRegular() || info.Mode.Perm() != entry.Mode.Perm() || info.Size != int64(len(entry.Content)) {
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

func detectExistingCollisions(root secureRoot, targetDir string, entries []desiredEntry, previous *marker) error {
	return detectExistingCollisionsInRoots(root, targetDir, entries, previous, []string{AgentsSkillsDir, ClaudeSkillsDir})
}

//nolint:gocognit,cyclop,funlen // The collision matrix (file/dir/symlink x managed/foreign) is deliberately exhaustive in one place.
func detectExistingCollisionsInRoots(
	root secureRoot, targetDir string, entries []desiredEntry, previous *marker, rootNames []string,
) error {
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
	for _, rootName := range rootNames {
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
				case want.Dir && realDir, want.Dir && managed[rel], !want.Dir && managed[rel], !want.Dir && realDir && managedDirs[rel]:
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
