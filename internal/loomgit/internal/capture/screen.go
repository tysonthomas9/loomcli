package capture

import (
	"context"
	"errors"
	"os/exec"
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
