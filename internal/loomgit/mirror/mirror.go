// Package mirror copies Loom's private refs to the configured Git provider.
package mirror

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"sort"
	"strings"

	"github.com/tysonthomas9/loomcli/internal/githubtoken"
	"github.com/tysonthomas9/loomcli/internal/loomgit/internal/capture"
	"github.com/tysonthomas9/loomcli/internal/loomgit/internal/cred"
	"github.com/tysonthomas9/loomcli/internal/loomgit/internal/gitexec"
	"github.com/tysonthomas9/loomcli/internal/loomgit/internal/journal"
	"github.com/tysonthomas9/loomcli/internal/loomgit/internal/layout/refname"
)

type Store interface {
	MirrorState(context.Context, string, string) (journal.MirrorRecord, bool, error)
	MirrorRecords(context.Context) ([]journal.MirrorRecord, error)
	PutMirrorRecord(context.Context, journal.MirrorRecord) error
	DeleteMirrorRecord(context.Context, string, string) error
}

type RefPusher interface {
	RemoteSHA(context.Context, string, string) (string, error)
	Push(context.Context, string, string, string, string) error
}

type gitPusher struct{ runner *gitexec.Runner }

func (p gitPusher) run(ctx context.Context, remote string, args ...string) ([]byte, error) {
	if strings.HasPrefix(remote, "git@") {
		return p.runner.Run(ctx, args...)
	}
	u, err := url.Parse(remote)
	if err != nil {
		return nil, err
	}
	if u.Scheme == "https" {
		if !strings.EqualFold(u.Host, "github.com") {
			return nil, fmt.Errorf("no credential resolver for %s", u.Host)
		}
		token := githubtoken.GitHub(ctx)
		if token == "" {
			return nil, errors.New("GitHub host credential unavailable")
		}
		return p.runner.RunWithCredential(ctx, cred.New(remote, token), remote, args...)
	}
	if u.Scheme != "" && u.Scheme != "file" && u.Scheme != "ssh" {
		return nil, fmt.Errorf("unsupported Git remote scheme %q", u.Scheme)
	}
	return p.runner.Run(ctx, args...)
}

func (p gitPusher) RemoteSHA(ctx context.Context, remote, ref string) (string, error) {
	out, err := p.run(ctx, remote, "ls-remote", remote, ref)
	if err != nil {
		return "", err
	}
	if len(out) == 0 {
		return "", nil
	}
	fields := strings.Fields(string(out))
	if len(fields) != 2 || fields[1] != ref {
		return "", errors.New("unexpected remote ref response")
	}
	return fields[0], nil
}

func (p gitPusher) Push(ctx context.Context, remote, ref, localSHA, expected string) error {
	_, err := p.run(ctx, remote, "push", "--force-with-lease="+ref+":"+expected, remote, localSHA+":"+ref)
	return err
}

// SyncRepo scans local Loom refs. Each pass is independent; failed refs stay
// recorded for the next pass, and no caller waits for this before a reset.
func SyncRepo(ctx context.Context, store Store, repo, baseSHA string) error {
	runner, err := gitexec.New(repo, gitexec.Options{FallbackIdentity: gitexec.Identity{Name: "Loom", Email: "loom@localhost"}})
	if err != nil {
		return err
	}
	remoteBytes, err := runner.Run(ctx, "remote", "get-url", "--push", "origin")
	if err != nil {
		return err
	}
	remote := strings.TrimSpace(string(remoteBytes))
	if remote == "" {
		return errors.New("origin remote is empty")
	}
	local, err := localRefs(ctx, runner)
	if err != nil {
		return err
	}
	refs := make([]string, 0, len(local))
	for ref := range local {
		refs = append(refs, ref)
	}
	sort.Strings(refs)
	var failures []error
	push := gitPusher{runner}
	for _, ref := range refs {
		if err := syncRef(ctx, store, push, runner, repo, remote, ref, local[ref], baseSHA); err != nil {
			failures = append(failures, fmt.Errorf("%s: %w", ref, err))
		}
	}
	rows, err := store.MirrorRecords(ctx)
	if err != nil {
		return errors.Join(append(failures, err)...)
	}
	for _, row := range rows {
		if row.Repo == repo && local[row.Ref] == "" && row.SHA == "" {
			if err := store.DeleteMirrorRecord(ctx, repo, row.Ref); err != nil {
				failures = append(failures, err)
			}
		}
		if row.Repo == repo && local[row.Ref] == "" && row.SHA != "" {
			if err := DeleteMirrored(ctx, store, push, row); err != nil {
				failures = append(failures, fmt.Errorf("delete %s: %w", row.Ref, err))
			}
		}
	}
	return errors.Join(failures...)
}

func localRefs(ctx context.Context, runner *gitexec.Runner) (map[string]string, error) {
	refsBytes, err := runner.Run(ctx, "for-each-ref", "--format=%(refname) %(objectname)", refname.WorkspaceRefPrefix)
	if err != nil {
		return nil, err
	}
	local := make(map[string]string)
	for _, line := range strings.Split(strings.TrimSpace(string(refsBytes)), "\n") {
		if line == "" {
			continue
		}
		parts := strings.Fields(line)
		if len(parts) != 2 || !strings.HasPrefix(parts[0], refname.WorkspaceRefPrefix) {
			return nil, errors.New("invalid Loom ref listing")
		}
		local[parts[0]] = parts[1]
	}
	return local, nil
}

func syncRef(ctx context.Context, store Store, push RefPusher, runner *gitexec.Runner, repo, remote, ref, sha, baseSHA string) error {
	prior, found, err := store.MirrorState(ctx, repo, ref)
	if err != nil {
		return err
	}
	if found && prior.SHA == sha && prior.State == "mirrored" && prior.Remote == remote {
		return nil
	}
	row := journal.MirrorRecord{Repo: repo, Ref: ref, Remote: remote, SHA: prior.SHA, State: "pending"}
	if err := store.PutMirrorRecord(ctx, row); err != nil {
		return err
	}
	if found && prior.SHA != "" && prior.Remote != "" && prior.Remote != remote {
		return recordFailure(ctx, store, row, "origin remote changed")
	}
	if path, err := newSecretPath(ctx, runner, ref, sha, baseSHA); err != nil {
		return recordFailure(ctx, store, row, err.Error())
	} else if path != "" {
		return recordFailure(ctx, store, row, "secret-pattern path: "+path)
	}
	actual, err := push.RemoteSHA(ctx, remote, ref)
	if err != nil {
		return recordFailure(ctx, store, row, err.Error())
	}
	if actual == sha {
		row.SHA, row.State, row.Reason = sha, "mirrored", ""
		return store.PutMirrorRecord(ctx, row)
	}
	if actual != prior.SHA {
		return recordFailure(ctx, store, row, "remote ref moved or already exists")
	}
	if err := push.Push(ctx, remote, ref, sha, prior.SHA); err != nil {
		return recordFailure(ctx, store, row, err.Error())
	}
	row.SHA, row.State, row.Reason = sha, "mirrored", ""
	return store.PutMirrorRecord(ctx, row)
}

func recordFailure(ctx context.Context, store Store, row journal.MirrorRecord, reason string) error {
	row.State, row.Reason = "not_mirrored", reason
	if err := store.PutMirrorRecord(ctx, row); err != nil {
		return err
	}
	return errors.New(reason)
}

func newSecretPath(ctx context.Context, runner *gitexec.Runner, ref, sha, base string) (string, error) {
	if strings.HasSuffix(ref, "/capture") || strings.HasSuffix(ref, "/head") {
		baseRef := ref[:strings.LastIndex(ref, "/")+1] + "base"
		if exists, err := gitexec.RefExists(runner.Path(), baseRef); err != nil {
			return "", err
		} else if exists {
			base = baseRef
		}
	} else if strings.HasSuffix(ref, "/base") {
		base = sha
	}
	if base == "" {
		return "", errors.New("workspace base SHA unavailable")
	}
	basePaths, err := treePaths(ctx, runner, base)
	if err != nil {
		return "", err
	}
	commits, err := runner.Run(ctx, "rev-list", sha, "^"+base)
	if err != nil {
		return "", err
	}
	for _, commit := range strings.Fields(string(commits)) {
		paths, err := treePaths(ctx, runner, commit)
		if err != nil {
			return "", err
		}
		for path := range paths {
			if capture.SecretPath(path) && !basePaths[path] {
				return path, nil
			}
		}
	}
	return "", nil
}

func treePaths(ctx context.Context, runner *gitexec.Runner, sha string) (map[string]bool, error) {
	out, err := runner.Run(ctx, "ls-tree", "-r", "-z", "--name-only", sha)
	if err != nil {
		return nil, err
	}
	paths := make(map[string]bool)
	for _, path := range strings.Split(strings.TrimSuffix(string(out), "\x00"), "\x00") {
		if path != "" {
			paths[path] = true
		}
	}
	return paths, nil
}

// DeleteMirrored removes only the exact previously mirrored SHA.
func DeleteMirrored(ctx context.Context, store Store, push RefPusher, row journal.MirrorRecord) error {
	actual, err := push.RemoteSHA(ctx, row.Remote, row.Ref)
	if err != nil {
		return recordFailure(ctx, store, row, err.Error())
	}
	if actual == "" {
		return store.DeleteMirrorRecord(ctx, row.Repo, row.Ref)
	}
	if actual != row.SHA {
		return recordFailure(ctx, store, row, "remote ref moved")
	}
	if err := push.Push(ctx, row.Remote, row.Ref, "", row.SHA); err != nil {
		return recordFailure(ctx, store, row, err.Error())
	}
	return store.DeleteMirrorRecord(ctx, row.Repo, row.Ref)
}
