package capture

import (
	"context"
	"errors"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/tysonthomas9/loomcli/internal/loomgit/internal/gitexec"
)

// ScreenStaged applies the capture rules for untracked paths (D18) to a tree
// staged in a private index, such as a runner's flat patch. A path the patch
// adds that parent does not track is untracked: an ignored one is listed, a
// secret-pattern one is secret_suspect, and neither stays in the index.
// The returned entries are sorted; any secret_suspect makes the capture
// incomplete. Tracked secret-pattern paths are captured as they are.
func ScreenStaged(ctx context.Context, runner *gitexec.Runner, env map[string]string, parent string) ([]Entry, error) {
	out, err := runner.RunWithEnv(ctx, env, "diff", "--cached", "--no-renames", "--name-only", "--diff-filter=A", "-z", parent)
	if err != nil {
		return nil, err
	}
	added := lines(out)
	if len(added) == 0 {
		return nil, nil
	}
	ignored, err := ignoredPaths(ctx, runner, added)
	if err != nil {
		return nil, err
	}
	var excluded []string
	for _, path := range added {
		if ignored[path] || SecretPath(path) {
			excluded = append(excluded, path)
		}
	}
	if len(excluded) == 0 {
		return nil, nil
	}
	entries := make([]Entry, 0, len(excluded))
	for _, path := range excluded {
		entry := Entry{Path: path, Class: SecretSuspect}
		if ignored[path] {
			entry.Class = Listed
		}
		size, err := runner.RunWithEnv(ctx, env, "cat-file", "-s", ":"+path)
		if err != nil {
			return nil, err
		}
		entry.Size, err = strconv.ParseInt(strings.TrimSpace(string(size)), 10, 64)
		if err != nil {
			return nil, err
		}
		entries = append(entries, entry)
	}
	input := strings.Join(excluded, "\x00") + "\x00"
	if _, err := runner.RunWithInput(ctx, []byte(input), env, "update-index", "--force-remove", "-z", "--stdin"); err != nil {
		return nil, err
	}
	return entries, nil
}

// ignoredPaths reports which paths the repository's exclude rules ignore,
// whether or not the files exist in this working tree.
func ignoredPaths(ctx context.Context, runner *gitexec.Runner, paths []string) (map[string]bool, error) {
	input := strings.Join(paths, "\x00") + "\x00"
	out, err := runner.RunWithInput(ctx, []byte(input), nil, "check-ignore", "--no-index", "-z", "--stdin")
	var exit *exec.ExitError
	if errors.As(err, &exit) && exit.ExitCode() == 1 {
		return map[string]bool{}, nil // Exit 1: no path is ignored.
	}
	if err != nil {
		return nil, err
	}
	return pathSet(lines(out)), nil
}

// IgnoredEntries lists the task copy's ignored files the way Capture records
// them: listed with their size and never captured. An ignored path Capture
// could not measure is incomplete.
func IgnoredEntries(ctx context.Context, runner *gitexec.Runner, repo string) ([]Entry, error) {
	out, err := runner.Run(ctx, "ls-files", "--others", "--ignored", "--exclude-standard", "--directory", "-z")
	if err != nil {
		return nil, err
	}
	manifest := Manifest{Complete: true}
	recordIgnored(&manifest, repo, lines(out), map[string]bool{})
	return manifest.Entries, nil
}

// Complete reports whether entries leave every path captured or listed.
func Complete(entries []Entry) bool {
	for _, entry := range entries {
		if entry.Class == Incomplete || entry.Class == SecretSuspect {
			return false
		}
	}
	return true
}

// SaveManifest records a capture manifest where Capture saves its own.
func SaveManifest(ctx context.Context, runner *gitexec.Runner, repo string, manifest Manifest) (string, error) {
	return saveManifest(ctx, runner, repo, manifest)
}

// ScreenTaskCopy lists what a capture of the task copy leaves out without
// capturing anything (D18): untracked, non-ignored secret-pattern files are
// secret_suspect and ignored files are listed with their size. A freeze of a
// run that changed nothing uses it so a left-out secret file still makes the
// revision incomplete.
func ScreenTaskCopy(ctx context.Context, runner *gitexec.Runner, repo string) ([]Entry, error) {
	out, err := runner.Run(ctx, "ls-files", "--others", "--exclude-standard", "-z")
	if err != nil {
		return nil, err
	}
	var entries []Entry
	for _, path := range lines(out) {
		if !SecretPath(path) || excludedRuntimePath(repo, path, false) {
			continue
		}
		entry := Entry{Path: path, Class: SecretSuspect}
		if size, err := fileSize(filepath.Join(repo, filepath.FromSlash(path))); err == nil {
			entry.Size = size
		}
		entries = append(entries, entry)
	}
	ignored, err := IgnoredEntries(ctx, runner, repo)
	if err != nil {
		return nil, err
	}
	entries = append(entries, ignored...)
	sort.Slice(entries, func(i, j int) bool { return entries[i].Path < entries[j].Path })
	return entries, nil
}
