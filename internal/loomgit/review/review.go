// Package review stores SHA-bound revision decisions and checks delivery gates.
package review

import (
	"context"
	"errors"
	"fmt"

	"github.com/tysonthomas9/loomcli/internal/loomgit"
	"github.com/tysonthomas9/loomcli/internal/loomgit/internal/journal"
)

type Actor struct{ Kind, ID string }

type Store interface {
	GetRevision(context.Context, string, string, int) (loomgit.Revision, error)
	RevisionAuthor(context.Context, loomgit.Revision) (string, string, error)
	LatestSourceNumber(context.Context, string, string) (int, error)
	LeadMayApprovePublish(context.Context, string) (bool, error)
	RecordVerdict(context.Context, loomgit.Verdict) (loomgit.Verdict, error)
	LatestVerdict(context.Context, loomgit.Revision) (loomgit.Verdict, error)
	VerdictByID(context.Context, int64) (loomgit.Verdict, error)
}

// maxVerdictChain bounds how many carried or feedback verdicts RequireVerdict
// follows back to an approval.
const maxVerdictChain = 64

func required(message string) error { return loomgit.NewError(loomgit.ReviewRequired, message, nil) }

func current(ctx context.Context, store Store, r loomgit.Revision) error {
	if !r.Ready || r.HeadSHA == "" {
		return required("revision is not ready")
	}
	if r.Incomplete {
		return loomgit.NewError(loomgit.CaptureIncomplete, "revision capture is incomplete", nil)
	}
	if r.Kind == "source" {
		latest, err := store.LatestSourceNumber(ctx, r.Workspace, r.Change)
		if err != nil {
			return err
		}
		if latest > r.Number {
			return loomgit.NewError(loomgit.RevisionSuperseded, "a newer source revision exists", nil)
		}
	}
	if r.NoChanges {
		return loomgit.NewError(loomgit.NoChanges, "revision has no code changes; the task closed without review", nil)
	}
	return nil
}

// Submit records a human decision, or a lead policy approval when enabled.
func Submit(ctx context.Context, store Store, workspace, change string, number int, headSHA, kind, reason string, actor Actor) (loomgit.Verdict, error) {
	return submit(ctx, store, workspace, change, number, headSHA, kind, reason, actor, "", false)
}

func SubmitForLead(ctx context.Context, store Store, workspace, change string, number int, headSHA, kind, reason string, actor Actor, lead string) (loomgit.Verdict, error) {
	return SubmitForLeadPublishing(ctx, store, workspace, change, number, headSHA, kind, reason, actor, lead, false)
}

// SubmitForLeadPublishing records the verdict for lead; with publish, an
// approval also records the intent to open the change's PR once it is applied
// (D29 Approve and create PR). A rejection never publishes.
func SubmitForLeadPublishing(ctx context.Context, store Store, workspace, change string, number int, headSHA, kind, reason string, actor Actor, lead string, publish bool) (loomgit.Verdict, error) {
	if lead == "" {
		return loomgit.Verdict{}, errors.New("target lead is required")
	}
	return submit(ctx, store, workspace, change, number, headSHA, kind, reason, actor, lead, publish && kind != "reject")
}

func submit(ctx context.Context, store Store, workspace, change string, number int, headSHA, kind, reason string, actor Actor, lead string, publish bool) (loomgit.Verdict, error) {
	if actor.ID == "" || (actor.Kind != "human" && actor.Kind != "agent" && actor.Kind != "lead") {
		return loomgit.Verdict{}, errors.New("actor kind and ID are required")
	}
	r, err := store.GetRevision(ctx, workspace, change, number)
	if err != nil {
		return loomgit.Verdict{}, err
	}
	if err := current(ctx, store, r); err != nil {
		return loomgit.Verdict{}, err
	}
	if headSHA != r.HeadSHA {
		return loomgit.Verdict{}, loomgit.NewError(loomgit.StaleSubject, "revision head changed", nil)
	}
	kind, reason, err = authorize(ctx, store, r, kind, reason, actor)
	if err != nil {
		return loomgit.Verdict{}, err
	}
	v := loomgit.Verdict{Workspace: workspace, Change: change, Number: number,
		HeadSHA: r.HeadSHA, Kind: kind, ActorKind: actor.Kind, ActorID: actor.ID, Reason: reason, TargetLead: lead, Publish: publish}
	v, err = store.RecordVerdict(ctx, v)
	if err != nil {
		if latest, lookupErr := store.LatestSourceNumber(ctx, workspace, change); lookupErr == nil && r.Kind == "source" && latest > r.Number {
			return loomgit.Verdict{}, loomgit.NewError(loomgit.RevisionSuperseded, "a newer source revision exists", err)
		}
		return loomgit.Verdict{}, err
	}
	return v, nil
}

func authorize(ctx context.Context, store Store, r loomgit.Revision, kind, reason string, actor Actor) (string, string, error) {
	if kind != "approve" && kind != "reject" && kind != "override" {
		return "", "", fmt.Errorf("invalid verdict %q", kind)
	}
	if kind == "override" && (actor.Kind != "human" || reason == "") {
		return "", "", required("override requires a human actor and reason")
	}
	authorKind, authorID, err := store.RevisionAuthor(ctx, r)
	if err != nil {
		return "", "", err
	}
	if kind != "approve" {
		return kind, reason, nil
	}
	if actor.Kind == "agent" && (authorID == "" || actor.ID == authorID) {
		return "", "", required("agent approval requires a different recorded author")
	}
	if actor.Kind != "lead" {
		return kind, reason, nil
	}
	enabled, err := store.LeadMayApprovePublish(ctx, r.Workspace)
	if err != nil {
		return "", "", err
	}
	if !enabled {
		return "", "", required("lead approval policy is off")
	}
	if authorKind == "lead" && authorID == actor.ID {
		return "policy", "own_layer", nil
	}
	return "policy", "policy", nil
}

// RequireVerdict is the delivery gate P2.6 and P2.10 call before Apply or Publish.
// targetLead is required when applying into a lead's working area.
func RequireVerdict(ctx context.Context, store Store, workspace, change string, number int, headSHA, operation, targetLead string) error {
	if operation != "publish" && operation != "apply" {
		return fmt.Errorf("invalid review operation %q", operation)
	}
	if operation == "apply" && targetLead == "" {
		return errors.New("target lead is required for apply")
	}
	r, err := store.GetRevision(ctx, workspace, change, number)
	if err != nil {
		return err
	}
	if err := current(ctx, store, r); err != nil {
		return err
	}
	if headSHA != r.HeadSHA {
		return required("verdict does not match revision head")
	}
	v, err := store.LatestVerdict(ctx, r)
	if errors.Is(err, journal.ErrNotFound) {
		return required("revision has no approval")
	}
	if err != nil {
		return err
	}
	if v.HeadSHA != r.HeadSHA {
		return required("verdict does not match revision head")
	}
	// A carried verdict inherits a patch-equivalent replay's approval; Loom's
	// feedback verdict on a review fix-up inherits the approval its open PR
	// was published under (D29 (6)). Either resolves to the human or policy
	// approval it chains to.
	for hops := 0; v.Kind == "carried" || v.Kind == "feedback"; hops++ {
		if hops > maxVerdictChain || v.SourceVerdictID == 0 {
			return required("revision needs an applicable approval")
		}
		v, err = store.VerdictByID(ctx, v.SourceVerdictID)
		if err != nil {
			return err
		}
	}
	switch v.Kind {
	case "approve", "override":
		return nil
	case "policy":
		if operation == "publish" || v.ActorID == targetLead {
			return nil
		}
	}
	return required("revision needs an applicable approval")
}
