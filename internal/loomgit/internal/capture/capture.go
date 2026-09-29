// Package capture saves task-copy edits without changing its HEAD or index.
package capture

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/tysonthomas9/loomcli/internal/loomgit"
	"github.com/tysonthomas9/loomcli/internal/loomgit/internal/gitexec"
	"github.com/tysonthomas9/loomcli/internal/loomgit/internal/layout/refname"
)

const (
	MaxFileBytes    int64 = 100 << 20
	MaxCaptureBytes int64 = 2 << 30
	Captured              = "captured"
	Listed                = "listed"
	SecretSuspect         = "secret_suspect"
	Incomplete            = "incomplete"
)

type Entry struct {
	Path   string `json:"path"`
	Class  string `json:"class"`
	Size   int64  `json:"size"`
	Reason string `json:"reason,omitempty"`
}

type Manifest struct {
	Workspace string  `json:"workspace"`
	Attempt   string  `json:"attempt"`
	Entries   []Entry `json:"entries"`
	Complete  bool    `json:"complete"`
	Retained  bool    `json:"retained"`
}

type Params struct {
	Workspace string
	Attempt   string
	TaskID    string
	TaskTitle string
	Ref       string // Optional working-area WIP ref; task copies use AttemptCapture.
}

type Result struct {
	Manifest     Manifest
	ManifestPath string
	CaptureRef   string
	CaptureSHA   string
	HeadSHA      string
}

// SecretPath is the one path-pattern list shared by capture and future mirror
// and proxy callers. It checks path components, including nested credentials.
func SecretPath(path string) bool {
	for _, part := range strings.Split(filepath.ToSlash(path), "/") {
		name := strings.ToLower(part)
		sshKey := strings.TrimSuffix(name, ".pub")
		if strings.HasPrefix(name, ".env") || strings.HasSuffix(name, ".pem") ||
			strings.HasSuffix(name, ".key") ||
			sshKey == "id_rsa" || sshKey == "id_dsa" || sshKey == "id_ecdsa" || sshKey == "id_ed25519" ||
			name == ".npmrc" || name == ".netrc" || name == "credentials.json" {
			return true
		}
	}
	return false
}

func lines(data []byte) []string {
	if len(data) == 0 {
		return nil
	}
	parts := strings.Split(strings.TrimSuffix(string(data), "\x00"), "\x00")
	sort.Strings(parts)
	return parts
}

func git(ctx context.Context, runner loomgit.RepoStore, args ...string) (string, error) {
	out, err := runner.Run(ctx, args...)
	return strings.TrimSpace(string(out)), err
}

func fileSize(path string) (int64, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return 0, err
	}
	if !info.IsDir() {
		return info.Size(), nil
	}
	var size int64
	err = filepath.WalkDir(path, func(_ string, item fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if !item.IsDir() {
			info, err := item.Info()
			if err != nil {
				return err
			}
			size += info.Size()
		}
		return nil
	})
	return size, err
}

func classifyPath(repo, path string, total int64, tracked, ignored bool) (Entry, bool) {
	entry := Entry{Path: path, Class: Captured}
	info, statErr := os.Lstat(filepath.Join(repo, filepath.FromSlash(path)))
	if statErr == nil {
		entry.Size = info.Size()
	}
	switch {
	case !tracked && ignored:
		entry.Class = Listed
	case !tracked && SecretPath(path):
		entry.Class = SecretSuspect
	case statErr != nil && !errors.Is(statErr, os.ErrNotExist):
		entry.Class, entry.Reason = Incomplete, statErr.Error()
	case errors.Is(statErr, os.ErrNotExist) && !tracked:
		entry.Class, entry.Reason = Incomplete, "new file disappeared"
	case statErr == nil && !info.Mode().IsRegular() && info.Mode()&os.ModeSymlink == 0:
		entry.Class, entry.Reason = Incomplete, "unsupported file type"
	case statErr == nil && info.Mode().IsRegular() && info.Mode().Perm()&0444 == 0:
		entry.Class, entry.Reason = Incomplete, "unreadable file"
	case entry.Size > MaxFileBytes:
		entry.Class, entry.Reason = Incomplete, "per-file cap exceeded"
	case total+entry.Size > MaxCaptureBytes:
		entry.Class, entry.Reason = Incomplete, "capture cap exceeded"
	default:
		return entry, true
	}
	return entry, false
}

func stagePaths(ctx context.Context, runner loomgit.RepoStore, env map[string]string, paths []string) error {
	if len(paths) == 0 {
		return nil
	}
	file, err := os.CreateTemp("", "loom-capture-pathspec-*")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	for _, path := range paths {
		if _, err := file.WriteString(path + "\x00"); err != nil {
			_ = file.Close()
			return err
		}
	}
	if err := file.Close(); err != nil {
		return err
	}
	_, err = runner.RunWithEnv(ctx, env, "add", "-A", "--pathspec-from-file="+file.Name(), "--pathspec-file-nul")
	return err
}

func saveManifest(ctx context.Context, runner loomgit.RepoStore, repo string, manifest Manifest) (string, error) {
	gitPath, err := git(ctx, runner, "rev-parse", "--git-path", "loom/capture")
	if err != nil {
		return "", err
	}
	if !filepath.IsAbs(gitPath) {
		gitPath = filepath.Join(repo, gitPath)
	}
	if err := os.MkdirAll(gitPath, 0700); err != nil {
		return "", err
	}
	path := filepath.Join(gitPath, manifest.Workspace+"-"+manifest.Attempt+".json")
	data, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return "", err
	}
	return path, os.WriteFile(path, append(data, '\n'), 0600)
}

func recordIgnored(manifest *Manifest, repo string, paths []string, seen map[string]bool) {
	for _, path := range paths {
		if seen[path] {
			continue
		}
		size, err := fileSize(filepath.Join(repo, filepath.FromSlash(strings.TrimSuffix(path, "/"))))
		entry := Entry{Path: path, Class: Listed, Size: size}
		if err != nil {
			entry.Class, entry.Reason = Incomplete, err.Error()
			manifest.Complete = false
		}
		manifest.Entries = append(manifest.Entries, entry)
	}
}

// ListIgnored returns the same ignored-path entries that Capture records,
// without creating a commit or ref. Callers can show these before confirmation.
func ListIgnored(ctx context.Context, runner loomgit.RepoStore, repo string) ([]Entry, error) {
	out, err := runner.Run(ctx, "ls-files", "--others", "--ignored", "--exclude-standard", "--directory", "-z")
	if err != nil {
		return nil, err
	}
	manifest := Manifest{Complete: true}
	recordIgnored(&manifest, repo, lines(out), map[string]bool{})
	if !manifest.Complete {
		return manifest.Entries, fmt.Errorf("cannot list all ignored paths")
	}
	return manifest.Entries, nil
}

func collectWorkingPaths(ctx context.Context, runner loomgit.RepoStore) ([][]string, error) {
	commands := [][]string{
		{"ls-tree", "-r", "--name-only", "-z", "HEAD"},
		{"diff", "--no-renames", "--name-only", "-z", "HEAD"},
		{"ls-files", "--others", "--exclude-standard", "-z"},
		{"ls-files", "--others", "--ignored", "--exclude-standard", "--directory", "-z"},
		{"ls-files", "--cached", "--ignored", "--exclude-standard", "-z"},
	}
	paths := make([][]string, len(commands))
	for i, args := range commands {
		out, err := runner.Run(ctx, args...)
		if err != nil {
			return nil, err
		}
		paths[i] = lines(out)
	}
	return paths, nil
}

func scanWorkingTree(ctx context.Context, runner loomgit.RepoStore, repo string, env map[string]string, manifest *Manifest) error {
	paths, err := collectWorkingPaths(ctx, runner)
	if err != nil {
		return err
	}
	tracked, changed, untracked, ignored, ignoredIndex := paths[0], paths[1], paths[2], paths[3], paths[4]
	ignoredSet := pathSet(ignoredIndex)
	trackedSet := pathSet(tracked)
	changedSet := pathSet(changed)
	seen := make(map[string]bool)
	extras, nested, err := inventory(repo, ignored)
	if err != nil {
		return err
	}
	extraByPath := make(map[string]Entry, len(extras))
	for _, entry := range extras {
		extraByPath[entry.Path] = entry
	}
	var toStage []string
	var total int64
	for _, path := range append(append(tracked, changed...), untracked...) {
		if seen[path] {
			continue
		}
		seen[path] = true
		if trackedSet[path] && !changedSet[path] {
			// HEAD already contains this blob; the manifest records only work to capture.
			continue
		}
		entry, stage := classifyPath(repo, path, total, trackedSet[path], ignoredSet[path])
		entry, stage = specialEntry(entry, stage, nested, extraByPath)
		if stage {
			toStage = append(toStage, path)
			total += entry.Size
		}
		if entry.Class == Incomplete || entry.Class == SecretSuspect {
			manifest.Complete = false
		}
		manifest.Entries = append(manifest.Entries, entry)
	}
	recordIgnored(manifest, repo, ignored, seen)
	for _, path := range ignored {
		seen[path] = true
	}
	recordExtras(manifest, extras, seen)
	if err := stagePaths(ctx, runner, env, toStage); err != nil {
		return err
	}
	sort.Slice(manifest.Entries, func(i, j int) bool { return manifest.Entries[i].Path < manifest.Entries[j].Path })
	manifest.Retained = !manifest.Complete
	return nil
}

func recordExtras(manifest *Manifest, extras []Entry, seen map[string]bool) {
	for _, entry := range extras {
		if seen[entry.Path] {
			continue
		}
		manifest.Entries = append(manifest.Entries, entry)
		if entry.Class == Incomplete {
			manifest.Complete = false
		}
	}
}

func pathSet(paths []string) map[string]bool {
	set := make(map[string]bool, len(paths))
	for _, path := range paths {
		set[path] = true
	}
	return set
}

func specialEntry(entry Entry, stage bool, nested []string, extras map[string]Entry) (Entry, bool) {
	if root := nestedRoot(entry.Path, nested); root != "" {
		entry.Class, entry.Reason = Incomplete, "nested git repository: "+root
		return entry, false
	}
	if extra, ok := extras[entry.Path]; ok && extra.Class == Incomplete {
		entry.Class, entry.Reason = Incomplete, extra.Reason
		return entry, false
	}
	return entry, stage
}

func advanceCaptureRef(ctx context.Context, runner loomgit.RepoStore, repo, ref, next, head string) error {
	expected := strings.Repeat("0", len(head))
	exists, err := gitexec.RefExists(repo, ref)
	if err != nil {
		return err
	}
	if exists {
		expected, err = git(ctx, runner, "rev-parse", "--verify", ref)
		if err != nil {
			return err
		}
	}
	return runner.UpdateRef(ctx, ref, next, expected)
}

func captureCommit(ctx context.Context, runner loomgit.RepoStore, head string, p Params, tree []byte) (string, error) {
	headTree, err := git(ctx, runner, "rev-parse", "HEAD^{tree}")
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(string(tree)) == headTree {
		return "", nil
	}
	message := fmt.Sprintf("loom: uncommitted work from %s\n\nLoom-Task: %s\nLoom-Attempt: %s", p.TaskTitle, p.TaskID, p.Attempt)
	commit, err := runner.Run(ctx, "commit-tree", strings.TrimSpace(string(tree)), "-p", head, "-m", message)
	return strings.TrimSpace(string(commit)), err
}

// Capture records the working tree against HEAD. Callers must hold the task
// copy's repository lock while invoking it.
func Capture(ctx context.Context, runner loomgit.RepoStore, repo string, p Params) (Result, error) {
	var result Result
	ref, err := refname.AttemptCapture(p.Workspace, p.Attempt)
	if p.Ref != "" {
		ref = p.Ref
		err = gitexec.CheckRefFormat(ref, false)
	}
	if err != nil {
		return result, err
	}
	head, err := git(ctx, runner, "rev-parse", "HEAD")
	if err != nil {
		return result, err
	}
	result.HeadSHA, result.CaptureRef = head, ref
	result.Manifest = Manifest{Workspace: p.Workspace, Attempt: p.Attempt, Complete: true, Entries: []Entry{}}
	index, err := os.CreateTemp("", "loom-capture-index-*")
	if err != nil {
		return result, err
	}
	indexPath := index.Name()
	_ = index.Close()
	defer os.Remove(indexPath)
	env := map[string]string{"GIT_INDEX_FILE": indexPath}
	if _, err = runner.RunWithEnv(ctx, env, "read-tree", head); err != nil {
		return result, err
	}
	beforeObjects, err := looseObjects(ctx, runner, repo)
	if err != nil {
		return result, err
	}
	if err = scanWorkingTree(ctx, runner, repo, env, &result.Manifest); err != nil {
		return result, err
	}
	tree, err := runner.RunWithEnv(ctx, env, "write-tree")
	if err != nil {
		return result, err
	}
	result.CaptureSHA, err = captureCommit(ctx, runner, head, p, tree)
	if err != nil {
		return result, err
	}
	if err = finishCapture(ctx, runner, repo, env, beforeObjects, &result.Manifest); err != nil {
		return result, err
	}
	result.ManifestPath, err = saveManifest(ctx, runner, repo, result.Manifest)
	if err != nil {
		return result, err
	}
	if result.CaptureSHA != "" {
		if err = advanceCaptureRef(ctx, runner, repo, ref, result.CaptureSHA, head); err != nil {
			return result, err
		}
	}
	return result, nil
}
