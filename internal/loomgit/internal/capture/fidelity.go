package capture

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/tysonthomas9/loomcli/internal/loomgit"
)

// inventory records filesystem content that a Git tree cannot represent.
func inventory(repo string, ignored []string) ([]Entry, []string, error) {
	var entries []Entry
	var nested []string
	ignoredSet := make(map[string]bool, len(ignored))
	for _, path := range ignored {
		ignoredSet[strings.TrimSuffix(path, "/")] = true
	}
	err := filepath.WalkDir(repo, func(full string, item fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if full == repo {
			return nil
		}
		path, err := filepath.Rel(repo, full)
		if err != nil {
			return err
		}
		path = filepath.ToSlash(path)
		if path == ".git" || ignoredSet[path] {
			return skipInventoryEntry(item)
		}
		info, err := os.Lstat(full)
		if err != nil {
			return err
		}
		if item.IsDir() {
			if _, err := os.Lstat(filepath.Join(full, ".git")); err == nil {
				nested = append(nested, path)
				entries = append(entries, Entry{Path: path + "/", Class: Incomplete, Reason: "nested git repository: " + path})
				return filepath.SkipDir
			} else if !errors.Is(err, os.ErrNotExist) {
				return err
			}
			children, err := os.ReadDir(full)
			if err != nil {
				return err
			}
			if len(children) == 0 {
				entries = append(entries, Entry{Path: path + "/", Class: Listed, Reason: "empty directory"})
			}
		} else if !info.Mode().IsRegular() && info.Mode()&os.ModeSymlink == 0 {
			entries = append(entries, Entry{Path: path, Class: Incomplete, Size: info.Size(), Reason: "unsupported file type"})
		}
		if xattr, ok := inventoryXattr(full, path, info.Size()); ok {
			entries = append(entries, xattr)
		}
		return nil
	})
	return entries, nested, err
}

func skipInventoryEntry(item fs.DirEntry) error {
	if item.IsDir() {
		return filepath.SkipDir
	}
	return nil
}

func inventoryXattr(full, path string, size int64) (Entry, bool) {
	count, err := countXattrs(full)
	if err != nil {
		return Entry{Path: path, Class: Incomplete, Size: size, Reason: "extended attribute scan: " + err.Error()}, true
	}
	if count > 0 {
		return Entry{Path: path, Class: Incomplete, Size: size, Reason: "extended attributes cannot be captured"}, true
	}
	return Entry{}, false
}

func nestedRoot(path string, roots []string) string {
	for _, root := range roots {
		if path == root || strings.HasPrefix(path, root+"/") {
			return root
		}
	}
	return ""
}

func verifyAfterWrite(ctx context.Context, runner loomgit.RepoStore, repo string, env map[string]string, manifest *Manifest) error {
	changed, err := runner.RunWithEnv(ctx, env, "diff", "--name-only", "-z")
	if err != nil {
		return err
	}
	paths, err := collectWorkingPaths(ctx, runner)
	if err != nil {
		return err
	}
	extras, _, err := inventory(repo, paths[3])
	if err != nil {
		return err
	}
	known := make(map[string]Entry, len(manifest.Entries))
	for _, entry := range manifest.Entries {
		known[entry.Path] = entry
	}
	for _, path := range lines(changed) {
		if excludedRuntimePath(repo, path) {
			continue
		}
		markChanged(manifest, known, path, "changed after capture write")
	}
	for _, path := range append(paths[2], paths[3]...) {
		if excludedRuntimePath(repo, path) {
			continue
		}
		if _, ok := known[path]; !ok {
			markChanged(manifest, known, path, "appeared after capture write")
		}
	}
	verifyExtras(manifest, repo, known, extras)
	manifest.Retained = !manifest.Complete
	return nil
}

func verifyExtras(manifest *Manifest, repo string, known map[string]Entry, extras []Entry) {
	for _, entry := range extras {
		if excludedRuntimePath(repo, entry.Path) {
			continue
		}
		if old, ok := known[entry.Path]; !ok || old.Class != entry.Class || old.Reason != entry.Reason {
			markChanged(manifest, known, entry.Path, "changed after capture write")
		}
	}
	for _, old := range manifest.Entries {
		if old.Reason != "empty directory" && old.Reason != "extended attributes cannot be captured" && old.Reason != "unsupported file type" && !strings.HasPrefix(old.Reason, "nested git repository:") {
			continue
		}
		found := false
		for _, entry := range extras {
			if entry.Path == old.Path && entry.Class == old.Class && entry.Reason == old.Reason {
				found = true
				break
			}
		}
		if !found {
			markChanged(manifest, known, old.Path, "changed after capture write")
		}
	}
}

func finishCapture(ctx context.Context, runner loomgit.RepoStore, repo string, env map[string]string, before map[string]bool, manifest *Manifest) error {
	if err := syncNewObjects(ctx, runner, repo, before); err != nil {
		return err
	}
	return verifyAfterWrite(ctx, runner, repo, env, manifest)
}

func markChanged(manifest *Manifest, known map[string]Entry, path, reason string) {
	entry, ok := known[path]
	if ok && entry.Class == Incomplete && entry.Reason == reason {
		return
	}
	entry.Path, entry.Class, entry.Reason = path, Incomplete, reason
	known[path] = entry
	if ok {
		for i := range manifest.Entries {
			if manifest.Entries[i].Path == path {
				manifest.Entries[i] = entry
				break
			}
		}
	} else {
		manifest.Entries = append(manifest.Entries, entry)
	}
	manifest.Complete = false
}

func objectDirectory(ctx context.Context, runner loomgit.RepoStore, repo string) (string, error) {
	path, err := git(ctx, runner, "rev-parse", "--git-path", "objects")
	if err != nil {
		return "", err
	}
	if !filepath.IsAbs(path) {
		path = filepath.Join(repo, path)
	}
	return path, nil
}

func looseObjects(ctx context.Context, runner loomgit.RepoStore, repo string) (map[string]bool, error) {
	dir, err := objectDirectory(ctx, runner, repo)
	if err != nil {
		return nil, err
	}
	objects := make(map[string]bool)
	err = filepath.WalkDir(dir, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if !entry.IsDir() && len(entry.Name()) == 38 && len(filepath.Base(filepath.Dir(path))) == 2 {
			objects[path] = true
		}
		return nil
	})
	return objects, err
}

// syncObjectFile is replaceable by tests to record the syscall boundary.
var syncObjectFile = func(path string) error {
	file, err := os.Open(path) //nolint:gosec // Paths come from Git's own objects directory inventory.
	if err != nil {
		return err
	}
	defer file.Close()
	return file.Sync()
}

func syncNewObjects(ctx context.Context, runner loomgit.RepoStore, repo string, before map[string]bool) error {
	after, err := looseObjects(ctx, runner, repo)
	if err != nil {
		return err
	}
	for path := range after {
		if !before[path] {
			if err := syncObjectFile(path); err != nil {
				return fmt.Errorf("fsync git object %s: %w", filepath.Base(path), err)
			}
		}
	}
	return nil
}
