package taskcopy

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/tysonthomas9/loomcli/internal/loomgit"
	"github.com/tysonthomas9/loomcli/internal/loomgit/internal/gitexec"
	"github.com/tysonthomas9/loomcli/internal/loomgit/internal/journal"
	"github.com/tysonthomas9/loomcli/internal/loomgit/internal/layout/refname"
	"github.com/tysonthomas9/loomcli/internal/loomgit/internal/pool"
)

// ImportSnapshot moves a clone's completed capture and revision refs to its
// source repository. An atomic fetch makes the refs all-or-nothing.
func ImportSnapshot(ctx context.Context, journalPath, source, copyPath, workspace, attempt, change string, revision int) error {
	store, err := journal.OpenSQLite(journalPath)
	if err != nil {
		return err
	}
	defer func() { _ = store.Close() }()
	options := gitexec.Options{FallbackIdentity: gitexec.Identity{Name: "Loom", Email: "loom@localhost"}}
	repo, err := pool.New(store, options).Admit(ctx, source)
	if err != nil {
		return err
	}
	copy, err := pool.New(store, options).Admit(ctx, copyPath)
	if err != nil {
		return err
	}
	if repo.SameStore(copy) {
		return nil
	}
	base, err := refname.RevisionBase(workspace, change, strconv.Itoa(revision))
	if err != nil {
		return err
	}
	head, err := refname.RevisionHead(workspace, change, strconv.Itoa(revision))
	if err != nil {
		return err
	}
	refs := []string{base, head}
	capture, err := refname.AttemptCapture(workspace, attempt)
	if err != nil {
		return err
	}
	if _, err := copy.Run(ctx, "show-ref", "--verify", "--hash", capture); err == nil {
		refs = append(refs, capture)
	}
	return repo.WithLock(ctx, func(ctx context.Context) error {
		args := []string{"fetch", "--atomic", "--no-tags", "--no-write-fetch-head", copyPath}
		for _, ref := range refs {
			args = append(args, ref+":"+ref)
		}
		if _, err := repo.Run(ctx, args...); err != nil {
			return fmt.Errorf("import task copy snapshot: %w", err)
		}
		return matchingTrees(ctx, repo, copy, refs)
	})
}

func matchingTrees(ctx context.Context, repo, copy *pool.LocalRepo, refs []string) error {
	for _, ref := range refs {
		want, err := copy.Run(ctx, "rev-parse", ref+"^{tree}")
		if err != nil {
			return err
		}
		got, err := repo.Run(ctx, "rev-parse", ref+"^{tree}")
		if err != nil {
			return err
		}
		if strings.TrimSpace(string(got)) != strings.TrimSpace(string(want)) {
			return loomgit.NewError(loomgit.TaskCopyCreateFailed, "imported tree hash mismatch", fmt.Errorf("%s", ref))
		}
	}
	return nil
}
