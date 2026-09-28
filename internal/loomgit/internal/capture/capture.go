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

func git(ctx context.Context, runner *gitexec.Runner, args ...string) (string, error) {
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

func classifyPath(repo, path string, total int64, tracked, changed, ignored bool) (Entry, bool) {
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
	case !changed:
		// HEAD already contains this blob. It needs no staging or size budget.
		return entry, false
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

func stagePaths(ctx context.Context, runner *gitexec.Runner, env map[string]string, paths []string) error {
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

func saveManifest(ctx context.Context, runner *gitexec.Runner, repo string, manifest Manifest) (string, error) {
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

func scanWorkingTree(ctx context.Context, runner *gitexec.Runner, repo string, env map[string]string, manifest *Manifest) error {
	tracked, err := runner.Run(ctx, "ls-tree", "-r", "--name-only", "-z", "HEAD")
	if err != nil {
		return err
	}
	changed, err := runner.Run(ctx, "diff", "--name-only", "-z", "HEAD")
	if err != nil {
		return err
	}
	untracked, err := runner.Run(ctx, "ls-files", "--others", "--exclude-standard", "-z")
	if err != nil {
		return err
	}
	ignored, err := runner.Run(ctx, "ls-files", "--others", "--ignored", "--exclude-standard", "--directory", "-z")
	if err != nil {
		return err
	}
	ignoredIndex, err := runner.Run(ctx, "ls-files", "--cached", "--ignored", "--exclude-standard", "-z")
	if err != nil {
		return err
	}
	ignoredSet := make(map[string]bool)
	for _, path := range lines(ignoredIndex) {
		ignoredSet[path] = true
	}
	trackedSet := make(map[string]bool)
	for _, path := range lines(tracked) {
		trackedSet[path] = true
	}
	changedSet := make(map[string]bool)
	for _, path := range lines(changed) {
		changedSet[path] = true
	}
	seen := make(map[string]bool)
	var toStage []string
	var total int64
	for _, path := range append(append(lines(tracked), lines(changed)...), lines(untracked)...) {
		if seen[path] {
			continue
		}
		seen[path] = true
		entry, stage := classifyPath(repo, path, total, trackedSet[path], changedSet[path] || !trackedSet[path], ignoredSet[path])
		if stage {
			toStage = append(toStage, path)
			total += entry.Size
		}
		if entry.Class == Incomplete || entry.Class == SecretSuspect {
			manifest.Complete = false
		}
		manifest.Entries = append(manifest.Entries, entry)
	}
	recordIgnored(manifest, repo, lines(ignored), seen)
	if err := stagePaths(ctx, runner, env, toStage); err != nil {
		return err
	}
	sort.Slice(manifest.Entries, func(i, j int) bool { return manifest.Entries[i].Path < manifest.Entries[j].Path })
	manifest.Retained = !manifest.Complete
	return nil
}

func captureCommit(ctx context.Context, runner *gitexec.Runner, head string, p Params, tree []byte) (string, error) {
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
func Capture(ctx context.Context, runner *gitexec.Runner, repo string, p Params) (Result, error) {
	var result Result
	ref, err := refname.AttemptCapture(p.Workspace, p.Attempt)
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
	result.ManifestPath, err = saveManifest(ctx, runner, repo, result.Manifest)
	if err != nil {
		return result, err
	}
	if result.CaptureSHA != "" {
		expected := strings.Repeat("0", len(head))
		exists, probeErr := gitexec.RefExists(repo, ref)
		if probeErr != nil {
			return result, probeErr
		}
		if exists {
			expected, err = git(ctx, runner, "rev-parse", "--verify", ref)
			if err != nil {
				return result, err
			}
		}
		if err = runner.UpdateRef(ctx, ref, result.CaptureSHA, expected); err != nil {
			return result, err
		}
	}
	return result, nil
}
