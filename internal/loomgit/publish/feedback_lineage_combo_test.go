package publish

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/cli/config"
	"github.com/tysonthomas9/loomcli/internal/loomgit/landing"
	"github.com/tysonthomas9/loomcli/internal/loomgit/review"
)

// A review fix-up of B in an approve-built stack keeps the stack order that
// P2.19c reads from the publications, and once A lands B is restacked from
// the replaced layer: the fix-up reaches trunk's PR and stays approved.
func TestFixupKeepsApproveBuiltOrderThroughLanding(t *testing.T) {
	fx, forge, revisions := approvedStack(t, "loom")
	localFlagForTask = func(context.Context, string, string) (string, error) { return "", nil }
	ctx := context.Background()
	published, found, err := fx.store.Publication(ctx, "W", "B")
	if err != nil || !found {
		t.Fatalf("B publication = %+v, %v", published, err)
	}
	fix := fixupRevision(t, fx, "B", 2, published.Head, map[string]string{"review-B": "B fixed"}, false)
	reconcileFixups(t, fixupFixture{fixture: fx, forge: forge, a: revisions[0], b: revisions[1]})
	if state := feedbackState(t, fx, "B", fix.Number); state.Status != FeedbackPushed {
		t.Fatalf("fix-up state = %+v", state)
	}
	wantDependents(t, map[string][]string{"A": {"task-B@repo"}, "B": {"task-C@repo"}, "C": {}})
	if layers := taskLayers(t, fx); len(layers) != 3 || layers[1].Change != "B" {
		t.Fatalf("layers after fix-up = %+v", layers)
	}

	trunk := landBottom(t, fx, forge)
	options := landing.Options{Dependents: landing.LocalDependents, Restack: RestackOffer}
	if err := landing.RunAtWithOptions(ctx, filepath.Join(config.GetConfigDir(), "loomgit", "store.db"),
		forge, "", options); err != nil {
		t.Fatal(err)
	}
	restacked, err := fx.store.SourceRevision(ctx, "W", "B")
	if err != nil {
		t.Fatal(err)
	}
	derived, err := fx.store.GetRevision(ctx, "W", "B", restacked)
	if err != nil || derived.Operation != "restack" || derived.BaseSHA != trunk {
		t.Fatalf("B after A landed = %+v, %v", derived, err)
	}
	if fileAt(t, fx, derived.HeadSHA, "review-B") != "B fixed" {
		t.Fatal("the restack dropped B's fix-up")
	}
	if err := review.RequireVerdict(ctx, fx.store, "W", "B", derived.Number, derived.HeadSHA, "publish", ""); err != nil {
		t.Fatalf("restacked fix-up lost its approval: %v", err)
	}
	after, _, err := fx.store.Publication(ctx, "W", "B")
	if err != nil || after.Head != derived.HeadSHA || after.Trunk != "develop" || forge.prs[2].Base != forge.prs[1].Head {
		t.Fatalf("B publication after landing = %+v, %v; PRs %+v", after, err, forge.prs)
	}
	wantDependents(t, map[string][]string{"B": {"task-C@repo"}})
}
