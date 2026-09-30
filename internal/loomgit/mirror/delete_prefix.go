package mirror

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/tysonthomas9/loomcli/internal/loomgit/internal/gitexec"
	"github.com/tysonthomas9/loomcli/internal/loomgit/internal/journal"
	"github.com/tysonthomas9/loomcli/internal/loomgit/internal/layout/refname"
)

// DeleteWorkspacePrefix removes refs from the recorded provider with an exact
// lease for every ref. Without a recorded remote it leaves the tombstone.
func DeleteWorkspacePrefix(ctx context.Context, store *journal.SQLite, prefix string) error {
	if !strings.HasPrefix(prefix, refname.WorkspaceRefPrefix) || !strings.HasSuffix(prefix, "/") {
		return errors.New("invalid workspace ref prefix")
	}
	if err := store.EnsureMirrorSchema(ctx); err != nil {
		return err
	}
	rows, err := store.MirrorRecords(ctx)
	if err != nil {
		return err
	}
	remotes, err := recordedRemotes(rows, prefix)
	if err != nil {
		return err
	}
	if len(remotes) == 0 {
		return errors.New("no recorded provider remote for workspace")
	}
	tmp, err := os.MkdirTemp("", "loom-retention-*")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(tmp) }()
	runner, err := gitexec.New(tmp, gitexec.Options{FallbackIdentity: gitexec.Identity{Name: "Loom", Email: "loom@localhost"}})
	if err != nil {
		return err
	}
	if _, err := runner.Run(ctx, "init", "--bare", filepath.Join(tmp, "repo.git")); err != nil {
		return err
	}
	runner, err = gitexec.New(filepath.Join(tmp, "repo.git"), gitexec.Options{FallbackIdentity: gitexec.Identity{Name: "Loom", Email: "loom@localhost"}})
	if err != nil {
		return err
	}
	pusher := gitPusher{runner: runner}
	for remote, known := range remotes {
		if err := deleteRemotePrefix(ctx, pusher, remote, prefix, known); err != nil {
			return err
		}
	}
	for _, row := range rows {
		if strings.HasPrefix(row.Ref, prefix) {
			if err := store.DeleteMirrorRecord(ctx, row.Repo, row.Ref); err != nil {
				return err
			}
		}
	}
	return nil
}

func recordedRemotes(rows []journal.MirrorRecord, prefix string) (map[string]map[string]string, error) {
	remotes := map[string]map[string]string{}
	for _, row := range rows {
		if !strings.HasPrefix(row.Ref, prefix) {
			continue
		}
		if row.Remote == "" || row.SHA == "" {
			return nil, errors.New("mirrored ref has no provider remote or captured SHA")
		}
		if remotes[row.Remote] == nil {
			remotes[row.Remote] = map[string]string{}
		}
		remotes[row.Remote][row.Ref] = row.SHA
	}
	return remotes, nil
}

func deleteRemotePrefix(ctx context.Context, pusher gitPusher, remote, prefix string, known map[string]string) error {
	listed, err := pusher.run(ctx, remote, "ls-remote", remote, prefix+"*")
	if err != nil {
		return err
	}
	type remoteRef struct{ name, sha string }
	var refs []remoteRef
	for _, line := range strings.Split(strings.TrimSpace(string(listed)), "\n") {
		if line == "" {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) != 2 || !strings.HasPrefix(fields[1], prefix) {
			return errors.New("unexpected provider ref listing")
		}
		if known[fields[1]] != fields[0] {
			return errors.New("provider ref is unknown or changed")
		}
		refs = append(refs, remoteRef{name: fields[1], sha: fields[0]})
	}
	for _, ref := range refs {
		if err := pusher.Push(ctx, remote, ref.name, "", ref.sha); err != nil {
			return fmt.Errorf("delete provider ref %s: %w", ref.name, err)
		}
	}
	return nil
}
