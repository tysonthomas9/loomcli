package publish

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/loomgit"
	"github.com/tysonthomas9/loomcli/internal/loomgit/apply"
	"github.com/tysonthomas9/loomcli/internal/loomgit/internal/gitexec"
	"github.com/tysonthomas9/loomcli/internal/loomgit/internal/journal"
	"github.com/tysonthomas9/loomcli/internal/loomgit/internal/layout/refname"
	"github.com/tysonthomas9/loomcli/internal/loomgit/review"
)

// fixupFixture is lead L's working area holding task layers A and B, each
// approved with Approve and create PR and published in mode, like D29 leaves
// them once both PRs are open.
type fixupFixture struct {
	fixture
	forge *fakeForge
	a, b  loomgit.Revision
}

func newFixupFixture(t *testing.T, mode string) fixupFixture {
	t.Helper()
	fx, forge := approvalFixture(t, mode)
	localFlagForTask = func(context.Context, string, string) (string, error) { return "", nil }
	ctx := context.Background()
	a := fixupTask(t, fx, "A", fx.base)
	b := fixupTask(t, fx, "B", a.HeadSHA)
	approveForLead(t, fx, a, reviewer, true, "applied")
	approveForLead(t, fx, b, reviewer, true, "applied")
	branch, err := refname.InteractiveBranch("W", "L")
	if err != nil {
		t.Fatal(err)
	}
	git(t, fx.repo, "checkout", "-q", "-B", branch, b.HeadSHA)
	outcomes, err := PublishApproved(ctx, "W", "L", nil)
	if err != nil || len(outcomes) != 2 || outcomes[0].Status != "published" || outcomes[1].Status != "published" {
		t.Fatalf("publish A and B = %+v, %v", outcomes, err)
	}
	return fixupFixture{fixture: fx, forge: forge, a: a, b: b}
}

// fixupTask is appliedTask with one task per change in either delivery mode.
func fixupTask(t *testing.T, fx fixture, change, parent string) loomgit.Revision {
	t.Helper()
	revision := stackRevision(t, fx, change, 1, parent)
	if _, err := fx.store.DriverChange(context.Background(), "W", "task-"+change, "repo", change); err != nil {
		t.Fatal(err)
	}
	return revision
}

// commitOn commits files on top of parent without touching any checkout.
func commitOn(t *testing.T, dir, parent string, files map[string]string, message string) string {
	t.Helper()
	index := filepath.Join(t.TempDir(), "index")
	run := func(args ...string) string {
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...) //nolint:norawexec // Disposable Git repositories are the test subject.
		cmd.Env = append(os.Environ(), "GIT_INDEX_FILE="+index)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	run("read-tree", parent)
	for path, content := range files {
		blob := filepath.Join(t.TempDir(), "blob")
		if err := os.WriteFile(blob, []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
		sha := run("hash-object", "-w", blob)
		run("update-index", "--add", "--cacheinfo", "100644,"+sha+","+path)
	}
	tree := run("write-tree")
	return run("commit-tree", tree, "-p", parent, "-m", message)
}

// fixupRevision records a completed fix-up source revision of change, made on
// base (normally its PR head), as a feedback task leaves one.
func fixupRevision(t *testing.T, fx fixture, change string, number int, base string, files map[string]string, incomplete bool) loomgit.Revision {
	t.Helper()
	head := commitOn(t, fx.repo, base, files, change+" fix-up "+strconv.Itoa(number))
	ctx := context.Background()
	revision, err := fx.store.ReserveRevision(ctx, loomgit.Revision{Workspace: "W", Change: change,
		RequestID: "fixup-" + change + strconv.Itoa(number), Kind: "source", Operation: "snapshot", Outcome: "completed",
		BaseSHA: base, TreeHash: head, SourceHeadSHA: head, Incomplete: incomplete})
	if err != nil {
		t.Fatal(err)
	}
	revision.HeadSHA = head
	if err := fx.store.FinishRevision(ctx, revision); err != nil {
		t.Fatal(err)
	}
	return revision
}

func reconcileFixups(t *testing.T, fx fixupFixture) {
	t.Helper()
	ctx := context.Background()
	if err := ReconcileFeedbackUpdatesAt(ctx, fx.storePath); err != nil {
		t.Fatalf("reconcile fix-ups: %v", err)
	}
	if _, err := publishApprovals(ctx, fx.store, nil, "", "", true); err != nil {
		t.Fatalf("push fix-ups: %v", err)
	}
	if err := ReconcileFeedbackUpdatesAt(ctx, fx.storePath); err != nil {
		t.Fatalf("settle fix-ups: %v", err)
	}
}

func remoteSHA(t *testing.T, fx fixture, change string) string {
	t.Helper()
	sha, _, _ := strings.Cut(remoteHead(t, fx, change), "\t")
	return sha
}

func fileAt(t *testing.T, fx fixture, commit, path string) string {
	t.Helper()
	return git(t, fx.repo, "show", commit+":"+path)
}

func taskLayers(t *testing.T, fx fixture) []loomgit.AppliedLayer {
	t.Helper()
	runner, err := gitexec.New(fx.repo, gitexec.Options{})
	if err != nil {
		t.Fatal(err)
	}
	layers, err := apply.New(fx.store, nil, runner).AppliedLog(context.Background(), "W", "L")
	if err != nil {
		t.Fatal(err)
	}
	return layers
}

func feedbackState(t *testing.T, fx fixture, change string, revision int) journal.FeedbackUpdateState {
	t.Helper()
	state, found, err := fx.store.FeedbackUpdateFor(context.Background(), "W", change, revision)
	if err != nil || !found {
		t.Fatalf("feedback update %s %d: found=%v err=%v", change, revision, found, err)
	}
	return state
}

func attentionEvents(t *testing.T, fx fixture, change string) []map[string]any {
	t.Helper()
	events, err := fx.store.PendingEvents(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var out []map[string]any
	for _, event := range events {
		if event.Kind != "git.attention_required" {
			continue
		}
		var payload map[string]any
		if err := json.Unmarshal(event.Payload, &payload); err != nil {
			t.Fatal(err)
		}
		if payload["change_id"] == change {
			out = append(out, payload)
		}
	}
	return out
}

// taskView is what the task's Revisions section shows for one revision.
func taskView(t *testing.T, change string, number int) review.TaskRevision {
	t.Helper()
	local, err := review.OpenLocal()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = local.Close() }()
	revisions, err := local.TaskRevisionsForLead(context.Background(), "W", "task-"+change, "L")
	if err != nil {
		t.Fatal(err)
	}
	for _, revision := range revisions {
		if revision.ChangeID == change && revision.Number == number {
			return revision
		}
	}
	t.Fatalf("revision %s %d is not in the task view: %+v", change, number, revisions)
	return review.TaskRevision{}
}

func humanVerdicts(t *testing.T, fx fixture, change string, number int) int {
	t.Helper()
	revision, err := fx.store.GetRevision(context.Background(), "W", change, number)
	if err != nil {
		t.Fatal(err)
	}
	verdict, err := fx.store.LatestVerdict(context.Background(), revision)
	if errors.Is(err, journal.ErrNotFound) {
		return 0
	}
	if err != nil {
		t.Fatal(err)
	}
	if verdict.ActorKind == "human" {
		return 1
	}
	return 0
}

func TestFixupUpdatesOpenStackPRWithoutApprove(t *testing.T) {
	fx := newFixupFixture(t, "stack")
	ctx := context.Background()
	oldB := remoteSHA(t, fx.fixture, "B")
	fix := fixupRevision(t, fx.fixture, "A", 2, fx.a.HeadSHA, map[string]string{"A": "A fixed"}, false)
	reconcileFixups(t, fx)

	layers := taskLayers(t, fx.fixture)
	if len(layers) != 2 || layers[0].Change != "A" || layers[1].Change != "B" {
		t.Fatalf("layers after fix-up = %+v", layers)
	}
	headA, headB := remoteSHA(t, fx.fixture, "A"), remoteSHA(t, fx.fixture, "B")
	if headA != layers[0].NewTip || headA != fix.HeadSHA || fileAt(t, fx.fixture, headA, "A") != "A fixed" {
		t.Fatalf("PR A head %s, layer %s, fix-up %s", headA, layers[0].NewTip, fix.HeadSHA)
	}
	if headB == oldB || headB != layers[1].NewTip || git(t, fx.repo, "rev-parse", headB+"^") != headA ||
		fileAt(t, fx.fixture, headB, "A") != "A fixed" || fileAt(t, fx.fixture, headB, "B") != "B1" {
		t.Fatalf("PR B was not replayed onto the fix-up: %s (was %s)", headB, oldB)
	}
	if head := git(t, fx.repo, "rev-parse", "HEAD"); head != headB {
		t.Fatalf("working area HEAD %s, want %s", head, headB)
	}
	if len(fx.forge.prs) != 2 || fx.forge.creates != 2 {
		t.Fatalf("fix-up opened a PR: %+v", fx.forge.prs)
	}
	if humanVerdicts(t, fx.fixture, "A", fix.Number) != 0 {
		t.Fatal("the fix-up needed a human verdict")
	}
	verdict, err := fx.store.LatestVerdict(ctx, fix)
	if err != nil || verdict.Kind != "feedback" || verdict.ActorKind != "system" {
		t.Fatalf("fix-up verdict = %+v, %v", verdict, err)
	}
	if state := feedbackState(t, fx.fixture, "A", fix.Number); state.Status != FeedbackPushed {
		t.Fatalf("feedback state = %+v", state)
	}
	if view := taskView(t, "A", fix.Number); view.FeedbackStatus != FeedbackPushed || view.Verdict != "feedback" || view.PRNumber != 1 {
		t.Fatalf("task view of the pushed fix-up = %+v", view)
	}
	// The pushed head stays approvable for Approve and merge.
	if err := requireApprovableHead(ctx, fx.store, "W", "A", headA, headA); err != nil {
		t.Fatalf("pushed fix-up head is not approvable: %v", err)
	}
	// A second pass changes nothing.
	creates := fx.forge.creates
	reconcileFixups(t, fx)
	if remoteSHA(t, fx.fixture, "A") != headA || remoteSHA(t, fx.fixture, "B") != headB || fx.forge.creates != creates {
		t.Fatal("a second pass moved the PRs")
	}
}

func TestFixupNotBasedOnHeadReplacesLayerAndSecondFixupFollows(t *testing.T) {
	fx := newFixupFixture(t, "stack")
	first := fixupRevision(t, fx.fixture, "A", 2, fx.a.HeadSHA, map[string]string{"A": "A fixed"}, false)
	reconcileFixups(t, fx)
	// A second round of feedback, on the new PR head.
	second := fixupRevision(t, fx.fixture, "A", 3, remoteSHA(t, fx.fixture, "A"), map[string]string{"A-notes": "more"}, false)
	reconcileFixups(t, fx)
	headA := remoteSHA(t, fx.fixture, "A")
	if fileAt(t, fx.fixture, headA, "A") != "A fixed" || fileAt(t, fx.fixture, headA, "A-notes") != "more" || headA != second.HeadSHA {
		t.Fatalf("second fix-up head %s lost the first (%s)", headA, first.HeadSHA)
	}
	// A retry made from the trunk replaces the layer instead of stacking on it.
	retry := fixupRevision(t, fx.fixture, "A", 4, fx.base, map[string]string{"A": "A retried"}, false)
	reconcileFixups(t, fx)
	layers := taskLayers(t, fx.fixture)
	headA = remoteSHA(t, fx.fixture, "A")
	if len(layers) != 2 || layers[0].OldTip != fx.base || headA != layers[0].NewTip || headA != retry.HeadSHA {
		t.Fatalf("retry layers = %+v, PR A %s", layers, headA)
	}
	if _, err := fx.store.GetRevision(context.Background(), "W", "A", retry.Number); err != nil {
		t.Fatal(err)
	}
	if out := git(t, fx.repo, "ls-tree", "--name-only", headA); strings.Contains(out, "A-notes") {
		t.Fatalf("retry kept the replaced attempt's files: %s", out)
	}
}

func TestFixupUpdatesOwnTrunkPR(t *testing.T) {
	fx := newFixupFixture(t, "trunk")
	oldB := remoteSHA(t, fx.fixture, "B")
	fix := fixupRevision(t, fx.fixture, "B", 2, remoteSHA(t, fx.fixture, "B"), map[string]string{"B": "B fixed"}, false)
	reconcileFixups(t, fx)
	headB := remoteSHA(t, fx.fixture, "B")
	if headB == oldB || fileAt(t, fx.fixture, headB, "B") != "B fixed" {
		t.Fatalf("trunk PR B head %s (was %s)", headB, oldB)
	}
	if parent := git(t, fx.repo, "rev-parse", headB+"^^"); parent != fx.base {
		t.Fatalf("trunk PR B is not on trunk: grandparent %s", parent)
	}
	if len(fx.forge.prs) != 2 || fx.forge.prs[1].Base != "develop" {
		t.Fatalf("trunk PRs = %+v", fx.forge.prs)
	}
	if state := feedbackState(t, fx.fixture, "B", fix.Number); state.Status != FeedbackPushed {
		t.Fatalf("feedback state = %+v", state)
	}
}

func TestFixupGuardsNeverPush(t *testing.T) {
	cases := []struct {
		name       string
		files      map[string]string
		incomplete bool
		reason     string
	}{
		{name: "incomplete", files: map[string]string{"A": "partial"}, incomplete: true, reason: "incomplete"},
		{name: "secret", files: map[string]string{"A": "ok", "config/.env": "TOKEN=x"}, reason: "config/.env"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fx := newFixupFixture(t, "stack")
			oldA, oldB := remoteSHA(t, fx.fixture, "A"), remoteSHA(t, fx.fixture, "B")
			fix := fixupRevision(t, fx.fixture, "A", 2, fx.a.HeadSHA, tc.files, tc.incomplete)
			reconcileFixups(t, fx)
			reconcileFixups(t, fx)
			if remoteSHA(t, fx.fixture, "A") != oldA || remoteSHA(t, fx.fixture, "B") != oldB {
				t.Fatal("a guarded fix-up was pushed")
			}
			state := feedbackState(t, fx.fixture, "A", fix.Number)
			if state.Status != FeedbackNotPushed || !strings.Contains(state.Reason, tc.reason) {
				t.Fatalf("feedback state = %+v", state)
			}
			if _, err := fx.store.LatestVerdict(context.Background(), fix); !errors.Is(err, journal.ErrNotFound) {
				t.Fatalf("guarded fix-up got a verdict: %v", err)
			}
			events := attentionEvents(t, fx.fixture, "A")
			if len(events) != 1 || events[0]["status"] != FeedbackNotPushed || events[0]["lead"] != "L" {
				t.Fatalf("lead notices = %+v", events)
			}
		})
	}
}

func TestFixupConflictIsHeldAndTellsLeadOnce(t *testing.T) {
	fx := newFixupFixture(t, "stack")
	oldA, oldB := remoteSHA(t, fx.fixture, "A"), remoteSHA(t, fx.fixture, "B")
	head := git(t, fx.repo, "rev-parse", "HEAD")
	// B adds file B; this fix-up of A adds a different file B.
	fix := fixupRevision(t, fx.fixture, "A", 2, fx.a.HeadSHA, map[string]string{"B": "clash"}, false)
	reconcileFixups(t, fx)
	reconcileFixups(t, fx)
	if remoteSHA(t, fx.fixture, "A") != oldA || remoteSHA(t, fx.fixture, "B") != oldB || git(t, fx.repo, "rev-parse", "HEAD") != head {
		t.Fatal("a conflicting fix-up moved the PRs or the working area")
	}
	state := feedbackState(t, fx.fixture, "A", fix.Number)
	if state.FollowStatus != "conflict" || len(state.Paths) != 1 || state.Paths[0] != "B" {
		t.Fatalf("held state = %+v", state)
	}
	if view := taskView(t, "A", fix.Number); view.FeedbackStatus != FeedbackHeld ||
		!strings.Contains(view.FeedbackReason, "conflicts") || !strings.Contains(view.FeedbackReason, "(B)") {
		t.Fatalf("task view of the held fix-up = %+v", view)
	}
	events := attentionEvents(t, fx.fixture, "A")
	if len(events) != 1 || events[0]["status"] != "conflict" || !strings.Contains(events[0]["message"].(string), "conflicts") {
		t.Fatalf("lead notices = %+v", events)
	}
	if layers := taskLayers(t, fx.fixture); len(layers) != 2 || layers[0].Revision != fx.a.Number {
		t.Fatalf("held fix-up changed the layers: %+v", layers)
	}
}

func TestFixupHeldByLeadEditsPushesOnceTheyMove(t *testing.T) {
	fx := newFixupFixture(t, "stack")
	oldA := remoteSHA(t, fx.fixture, "A")
	if err := os.WriteFile(filepath.Join(fx.repo, "A"), []byte("lead edit"), 0600); err != nil {
		t.Fatal(err)
	}
	fix := fixupRevision(t, fx.fixture, "A", 2, fx.a.HeadSHA, map[string]string{"A": "A fixed"}, false)
	reconcileFixups(t, fx)
	state := feedbackState(t, fx.fixture, "A", fix.Number)
	if state.FollowStatus != "apply_pending" || remoteSHA(t, fx.fixture, "A") != oldA {
		t.Fatalf("held state = %+v", state)
	}
	if events := attentionEvents(t, fx.fixture, "A"); len(events) != 1 || events[0]["status"] != "apply_pending" {
		t.Fatalf("lead notices = %+v", events)
	}
	if content, _ := os.ReadFile(filepath.Join(fx.repo, "A")); string(content) != "lead edit" {
		t.Fatalf("held fix-up touched the lead's edit: %q", content)
	}
	git(t, fx.repo, "checkout", "--", "A")
	reconcileFixups(t, fx)
	if head := remoteSHA(t, fx.fixture, "A"); head != fix.HeadSHA {
		t.Fatalf("fix-up not pushed after the edit moved: %s", head)
	}
}

func TestFixupCancelsMergeAfterApproval(t *testing.T) {
	fx := newFixupFixture(t, "stack")
	ctx := context.Background()
	headB := remoteSHA(t, fx.fixture, "B")
	if _, err := fx.store.RecordMergeApproval(ctx, journal.MergeApproval{Workspace: "W", Change: "B", Lead: "L",
		StackID: LeadStackID("L"), Head: headB, ActorKind: "human", ActorID: "Tyson",
		Status: MergeApprovalWaiting, Reason: "merges after #1", CreatedAt: 1}); err != nil {
		t.Fatal(err)
	}
	fix := fixupRevision(t, fx.fixture, "B", 2, headB, map[string]string{"B": "B fixed"}, false)
	reconcileFixups(t, fx)
	approval, found, err := fx.store.MergeApproval(ctx, "W", "B")
	if err != nil || !found || approval.Status != MergeApprovalCancelled || approval.Reason != FeedbackMergeCancelReason {
		t.Fatalf("merge approval after fix-up = %+v, %v", approval, err)
	}
	state := feedbackState(t, fx.fixture, "B", fix.Number)
	if !state.MergeCancelled || state.Status != FeedbackPushed || remoteSHA(t, fx.fixture, "B") != fix.HeadSHA {
		t.Fatalf("feedback state = %+v", state)
	}
	if view := taskView(t, "B", fix.Number); !view.FeedbackMergeCancelled || view.MergeStatus != MergeApprovalCancelled {
		t.Fatalf("task view after the cancel = %+v", view)
	}
}

func TestFixupWaitsWhileMergeIsRunning(t *testing.T) {
	fx := newFixupFixture(t, "stack")
	ctx := context.Background()
	headA := remoteSHA(t, fx.fixture, "A")
	recorded, err := fx.store.RecordMergeApproval(ctx, journal.MergeApproval{Workspace: "W", Change: "A", Lead: "L",
		StackID: LeadStackID("L"), Head: headA, ActorKind: "human", ActorID: "Tyson", Status: MergeApprovalWaiting, CreatedAt: 1})
	if err != nil {
		t.Fatal(err)
	}
	merging := recorded
	merging.Status, merging.Attempt, merging.MergeRequestID = MergeApprovalMerging, 1, "approval-merge:W:A:1"
	if err := fx.store.AdvanceMergeApproval(ctx, recorded, merging); err != nil {
		t.Fatal(err)
	}
	fix := fixupRevision(t, fx.fixture, "A", 2, headA, map[string]string{"A": "A fixed"}, false)
	if err := ReconcileFeedbackUpdatesAt(ctx, fx.storePath); err == nil {
		t.Fatal("a fix-up during a running merge reported no wait")
	}
	if _, found, _ := fx.store.FeedbackUpdateFor(ctx, "W", "A", fix.Number); found || remoteSHA(t, fx.fixture, "A") != headA {
		t.Fatal("a fix-up was recorded or pushed while the merge ran")
	}
	if approval, _, _ := fx.store.MergeApproval(ctx, "W", "A"); approval.Status != MergeApprovalMerging {
		t.Fatalf("running merge was changed: %+v", approval)
	}
}

func TestFixupResumesAfterInterruptedPass(t *testing.T) {
	fx := newFixupFixture(t, "stack")
	ctx := context.Background()
	fix := fixupRevision(t, fx.fixture, "A", 2, fx.a.HeadSHA, map[string]string{"A": "A fixed"}, false)
	previous := followFeedbackLeads
	followFeedbackLeads = func(context.Context, *journal.SQLite, map[[2]string]bool) error {
		return errors.New("serve stopped")
	}
	if err := ReconcileFeedbackUpdatesAt(ctx, fx.storePath); err == nil {
		t.Fatal("interrupted pass reported success")
	}
	followFeedbackLeads = previous
	if state := feedbackState(t, fx.fixture, "A", fix.Number); state.FollowStatus != "approved" {
		t.Fatalf("interrupted state = %+v", state)
	}
	// Publishing before the apply finished pushes nothing.
	if _, err := publishApprovals(ctx, fx.store, nil, "", "", true); err != nil || remoteSHA(t, fx.fixture, "A") != fx.a.HeadSHA {
		t.Fatalf("unapplied fix-up was pushed: %v", err)
	}
	reconcileFixups(t, fx)
	if remoteSHA(t, fx.fixture, "A") != fix.HeadSHA {
		t.Fatal("the next pass did not resume the fix-up")
	}
}

func TestFixupPushRetriedAfterProviderFailure(t *testing.T) {
	fx := newFixupFixture(t, "stack")
	ctx := context.Background()
	fix := fixupRevision(t, fx.fixture, "A", 2, fx.a.HeadSHA, map[string]string{"A": "A fixed"}, false)
	git(t, fx.repo, "remote", "set-url", "--push", "origin", filepath.Join(t.TempDir(), "missing.git"))
	if err := ReconcileFeedbackUpdatesAt(ctx, fx.storePath); err != nil {
		t.Fatal(err)
	}
	if _, err := publishApprovals(ctx, fx.store, nil, "", "", true); err == nil {
		t.Fatal("push to a missing remote succeeded")
	}
	if state := feedbackState(t, fx.fixture, "A", fix.Number); state.PublishStatus != "pending" {
		t.Fatalf("failed push state = %+v", state)
	}
	git(t, fx.repo, "remote", "set-url", "--push", "origin", fx.remote)
	reconcileFixups(t, fx)
	if remoteSHA(t, fx.fixture, "A") != fix.HeadSHA || len(fx.forge.prs) != 2 {
		t.Fatalf("retry pushed %s with PRs %+v", remoteSHA(t, fx.fixture, "A"), fx.forge.prs)
	}
}

func TestHumanApprovalOfNewerRevisionReplacesLayerNotStackingIt(t *testing.T) {
	fx := newFixupFixture(t, "stack")
	ctx := context.Background()
	fix := fixupRevision(t, fx.fixture, "A", 2, fx.a.HeadSHA, map[string]string{"A": "A fixed"}, false)
	if _, err := review.SubmitForLeadPublishing(ctx, fx.store, "W", "A", fix.Number, fix.HeadSHA, "approve", "", reviewer, "L", true); err != nil {
		t.Fatal(err)
	}
	if err := followFeedbackLeads(ctx, fx.store, map[[2]string]bool{{"W", "L"}: true}); err != nil {
		t.Fatal(err)
	}
	layers := taskLayers(t, fx.fixture)
	if len(layers) != 2 || layers[0].Change != "A" || layers[1].Change != "B" {
		t.Fatalf("second revision stacked a second layer: %+v", layers)
	}
	outcomes, err := PublishApproved(ctx, "W", "L", nil)
	if err != nil || len(outcomes) != 1 || outcomes[0].Status != "published" {
		t.Fatalf("publish after replace = %+v, %v", outcomes, err)
	}
	if remoteSHA(t, fx.fixture, "A") != fix.HeadSHA {
		t.Fatal("PR A does not hold the approved revision")
	}
	if _, found, _ := fx.store.FeedbackUpdateFor(ctx, "W", "A", fix.Number); found {
		t.Fatal("a human-approved revision also got a feedback update")
	}
}
