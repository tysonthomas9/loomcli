// Package changeset freezes source and derived change revisions. Callers hold
// the repository lock while recording a revision or importing one.
package changeset

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/tysonthomas9/loomcli/internal/loomgit"
	"github.com/tysonthomas9/loomcli/internal/loomgit/internal/capture"
	"github.com/tysonthomas9/loomcli/internal/loomgit/internal/gitexec"
	"github.com/tysonthomas9/loomcli/internal/loomgit/internal/layout/refname"
)

var fullSHA = regexp.MustCompile(`^(?:[0-9a-f]{40}|[0-9a-f]{64})$`)

type SourceInput struct {
	Workspace, Change, RequestID string
	Attempt, TaskID              string
	BaseSHA, CaptureSHA          string
	Outcome                      string
	Complete                     bool
}

type ImportInput struct {
	Workspace, Change, RequestID string
	BaseSHA, HeadSHA, TreeHash   string
	Outcome                      string
}

type DerivedInput struct {
	Workspace, Change, RequestID string
	FromNumber                   int
	Operation                    string
	BaseSHA, HeadSHA             string
	Outcome                      string
}

func tree(ctx context.Context, runner *gitexec.Runner, commit string) (string, error) {
	if !fullSHA.MatchString(commit) {
		return "", errors.New("full commit SHA required")
	}
	resolved, err := runner.Run(ctx, "rev-parse", "--verify", commit+"^{commit}")
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(string(resolved)) != commit {
		return "", fmt.Errorf("commit %s did not resolve exactly", commit)
	}
	out, err := runner.Run(ctx, "rev-parse", commit+"^{tree}")
	return strings.TrimSpace(string(out)), err
}

func validateNames(workspace, change string) error {
	_, err := refname.RevisionHead(workspace, change, "1")
	return err
}

func install(ctx context.Context, runner *gitexec.Runner, ref, sha string) error {
	current, err := runner.Run(ctx, "show-ref", "--verify", "--hash", ref)
	if err == nil {
		if strings.TrimSpace(string(current)) == sha {
			return nil
		}
		return fmt.Errorf("%w: %s", gitexec.ErrStale, ref)
	}
	if err := runner.UpdateRef(ctx, ref, sha, strings.Repeat("0", len(sha))); err != nil {
		return err
	}
	return nil
}

func installRevision(ctx context.Context, store loomgit.RevisionStore, runner *gitexec.Runner, r loomgit.Revision, head string) (loomgit.Revision, error) {
	r, err := installRefs(ctx, runner, r, head)
	if err != nil {
		return r, err
	}
	r.Ready = true
	if err := store.FinishRevision(ctx, r); err != nil {
		return r, err
	}
	return r, nil
}

// installRefs points the revision's refs at its base and head; the revision
// stays an unfinished reservation until the store finishes it.
func installRefs(ctx context.Context, runner *gitexec.Runner, r loomgit.Revision, head string) (loomgit.Revision, error) {
	baseRef, err := refname.RevisionBase(r.Workspace, r.Change, strconv.Itoa(r.Number))
	if err != nil {
		return r, err
	}
	headRef, err := refname.RevisionHead(r.Workspace, r.Change, strconv.Itoa(r.Number))
	if err != nil {
		return r, err
	}
	if err := install(ctx, runner, baseRef, r.BaseSHA); err != nil {
		return r, err
	}
	if err := install(ctx, runner, headRef, head); err != nil {
		return r, err
	}
	r.HeadSHA = head
	return r, nil
}

// FreezeSource records a task-copy capture as a source revision.
// The original capture ref remains at the unrewritten capture commit.
func FreezeSource(ctx context.Context, store loomgit.RevisionStore, runner *gitexec.Runner, in SourceInput) (loomgit.Revision, error) {
	if !in.Complete && in.Outcome != "cancelled" && in.Outcome != "failed" && in.Outcome != "abandoned" {
		return loomgit.Revision{}, loomgit.NewError(loomgit.CaptureIncomplete, "capture is incomplete", nil)
	}
	if err := validateNames(in.Workspace, in.Change); err != nil {
		return loomgit.Revision{}, err
	}
	baseTree, err := tree(ctx, runner, in.BaseSHA)
	if err != nil {
		return loomgit.Revision{}, err
	}
	hash, err := tree(ctx, runner, in.CaptureSHA)
	if err != nil {
		return loomgit.Revision{}, err
	}
	// An incomplete capture may have left work out, so only a complete one
	// with the base's exact tree counts as "no changes".
	r, err := store.ReserveRevision(ctx, loomgit.Revision{Workspace: in.Workspace, Change: in.Change,
		RequestID: in.RequestID, Kind: "source", Operation: "snapshot", Outcome: in.Outcome,
		BaseSHA: in.BaseSHA, TreeHash: hash, SourceHeadSHA: in.CaptureSHA, Incomplete: !in.Complete,
		NoChanges: in.Complete && hash == baseTree})
	if err != nil || r.Ready {
		return r, err
	}
	headRef, err := refname.RevisionHead(r.Workspace, r.Change, strconv.Itoa(r.Number))
	if err != nil {
		return r, err
	}
	var head string
	if existing, lookupErr := runner.Run(ctx, "show-ref", "--verify", "--hash", headRef); lookupErr == nil {
		head = strings.TrimSpace(string(existing))
	} else {
		head, err = capture.RewriteSource(ctx, runner, capture.FreezeParams{Workspace: in.Workspace,
			ChangeID: in.Change, Revision: strconv.Itoa(r.Number), Attempt: in.Attempt,
			TaskID: in.TaskID, BaseSHA: in.BaseSHA, HeadSHA: in.CaptureSHA})
		if err != nil {
			return r, err
		}
	}
	actual, err := tree(ctx, runner, head)
	if err != nil {
		return r, err
	}
	if actual != hash {
		return r, loomgit.NewError(loomgit.HashMismatch, "rewritten source tree differs from capture", nil)
	}
	return installRevision(ctx, store, runner, r, head)
}

// ImportSource verifies the fetched or supplied commit against its recorded
// tree hash before reserving a revision or installing either ref.
func ImportSource(ctx context.Context, store loomgit.RevisionStore, runner *gitexec.Runner, in ImportInput) (loomgit.Revision, error) {
	if err := validateNames(in.Workspace, in.Change); err != nil {
		return loomgit.Revision{}, err
	}
	if !fullSHA.MatchString(in.TreeHash) {
		return loomgit.Revision{}, errors.New("full tree hash required")
	}
	baseTree, err := tree(ctx, runner, in.BaseSHA)
	if err != nil {
		return loomgit.Revision{}, err
	}
	actual, err := tree(ctx, runner, in.HeadSHA)
	if err != nil {
		return loomgit.Revision{}, err
	}
	if actual != in.TreeHash {
		return loomgit.Revision{}, loomgit.NewError(loomgit.HashMismatch, "imported tree differs from recorded hash", nil)
	}
	r, err := store.ReserveRevision(ctx, loomgit.Revision{Workspace: in.Workspace, Change: in.Change,
		RequestID: in.RequestID, Kind: "source", Operation: "import", Outcome: in.Outcome,
		BaseSHA: in.BaseSHA, TreeHash: in.TreeHash, SourceHeadSHA: in.HeadSHA, NoChanges: actual == baseTree})
	if err != nil || r.Ready {
		return r, err
	}
	return installRevision(ctx, store, runner, r, in.HeadSHA)
}

// RecordDerived records a new revision of the same change for an applied or
// rebuilt layer. Source refs are never updated.
func RecordDerived(ctx context.Context, store loomgit.RevisionStore, runner *gitexec.Runner, in DerivedInput) (loomgit.Revision, error) {
	r, err := PrepareDerived(ctx, store, runner, in)
	if err != nil || r.Ready {
		return r, err
	}
	r.Ready = true
	return r, store.FinishRevision(ctx, r)
}

// PrepareDerived reserves a derived revision and installs its refs without
// finishing it, so the caller can finish it atomically with the record that
// holds it. A revision already finished for this request is returned ready.
func PrepareDerived(ctx context.Context, store loomgit.RevisionStore, runner *gitexec.Runner, in DerivedInput) (loomgit.Revision, error) {
	if err := validateNames(in.Workspace, in.Change); err != nil {
		return loomgit.Revision{}, err
	}
	switch in.Operation {
	case "apply", "restack", "pull", "reorder", "unapply", "provider_restack":
	default:
		return loomgit.Revision{}, fmt.Errorf("invalid derived operation %q", in.Operation)
	}
	from, err := store.GetRevision(ctx, in.Workspace, in.Change, in.FromNumber)
	if err != nil {
		return loomgit.Revision{}, err
	}
	if !from.Ready {
		return loomgit.Revision{}, errors.New("source revision is unfinished")
	}
	if _, err := tree(ctx, runner, in.BaseSHA); err != nil {
		return loomgit.Revision{}, err
	}
	hash, err := tree(ctx, runner, in.HeadSHA)
	if err != nil {
		return loomgit.Revision{}, err
	}
	r, err := store.ReserveRevision(ctx, loomgit.Revision{Workspace: in.Workspace, Change: in.Change,
		RequestID: in.RequestID, Kind: "derived", Operation: in.Operation, Outcome: in.Outcome,
		BaseSHA: in.BaseSHA, TreeHash: hash, SourceHeadSHA: in.HeadSHA,
		DerivedFromChange: from.Change, DerivedFromNumber: from.Number, Incomplete: from.Incomplete})
	if err != nil || r.Ready {
		return r, err
	}
	return installRefs(ctx, runner, r, in.HeadSHA)
}
