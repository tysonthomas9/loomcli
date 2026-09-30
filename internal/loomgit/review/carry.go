package review

import (
	"context"
	"errors"
	"strings"

	"github.com/tysonthomas9/loomcli/internal/loomgit"
	"github.com/tysonthomas9/loomcli/internal/loomgit/internal/gitexec"
	"github.com/tysonthomas9/loomcli/internal/loomgit/internal/replay"
)

func commitList(ctx context.Context, runner *gitexec.Runner, base, head string) ([]string, error) {
	out, err := runner.Run(ctx, "rev-list", "--reverse", base+".."+head)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(string(out)) == "" {
		return nil, nil
	}
	return strings.Fields(string(out)), nil
}

func patchID(ctx context.Context, runner *gitexec.Runner, commit string) (string, error) {
	diff, err := runner.Run(ctx, "diff-tree", "-p", commit+"^", commit)
	if err != nil {
		return "", err
	}
	out, err := runner.RunWithInput(ctx, diff, nil, "patch-id", "--stable")
	if err != nil {
		return "", err
	}
	fields := strings.Fields(string(out))
	if len(fields) == 0 {
		return "", errors.New("commit has no patch ID")
	}
	return fields[0], nil
}

// CarryForward records an inherited verdict only for a clean, patch-equivalent replay.
// Empty-dropped source commits do not prevent carry-forward.
func CarryForward(ctx context.Context, store Store, runner *gitexec.Runner, source, derived loomgit.Revision, result replay.Result) (loomgit.Verdict, bool, error) {
	if result.ConflictCommit != "" || derived.DerivedFromChange != source.Change || derived.DerivedFromNumber != source.Number {
		return loomgit.Verdict{}, false, nil
	}
	if err := current(ctx, store, derived); err != nil {
		return loomgit.Verdict{}, false, err
	}
	prior, err := store.LatestVerdict(ctx, source)
	if err != nil {
		return loomgit.Verdict{}, false, nil
	}
	if prior.Kind == "reject" || prior.HeadSHA != source.HeadSHA {
		return loomgit.Verdict{}, false, nil
	}
	match, err := patchesMatch(ctx, runner, source, derived, result.DroppedCommits)
	if err != nil || !match {
		return loomgit.Verdict{}, false, err
	}
	v := loomgit.Verdict{Workspace: derived.Workspace, Change: derived.Change, Number: derived.Number,
		HeadSHA: derived.HeadSHA, Kind: "carried", ActorKind: prior.ActorKind, ActorID: prior.ActorID,
		Reason: "patch_equivalent", SourceVerdictID: prior.ID}
	v, err = store.RecordVerdict(ctx, v)
	return v, err == nil, err
}

func patchesMatch(ctx context.Context, runner *gitexec.Runner, source, derived loomgit.Revision, droppedCommits []string) (bool, error) {
	sourceCommits, err := commitList(ctx, runner, source.BaseSHA, source.HeadSHA)
	if err != nil {
		return false, err
	}
	derivedCommits, err := commitList(ctx, runner, derived.BaseSHA, derived.HeadSHA)
	if err != nil {
		return false, err
	}
	dropped := make(map[string]bool, len(droppedCommits))
	for _, sha := range droppedCommits {
		dropped[sha] = true
	}
	remaining := make([]string, 0, len(sourceCommits))
	for _, sha := range sourceCommits {
		if !dropped[sha] {
			remaining = append(remaining, sha)
		}
	}
	if len(remaining) != len(derivedCommits) {
		return false, nil
	}
	for i, sha := range remaining {
		left, err := patchID(ctx, runner, sha)
		if err != nil {
			return false, err
		}
		right, err := patchID(ctx, runner, derivedCommits[i])
		if err != nil {
			return false, err
		}
		if left != right {
			return false, nil
		}
	}
	return true, nil
}
