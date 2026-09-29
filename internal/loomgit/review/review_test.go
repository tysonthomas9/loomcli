package review

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/loomgit"
	"github.com/tysonthomas9/loomcli/internal/loomgit/internal/journal"
)

func newStore(t *testing.T) *journal.SQLite {
	t.Helper()
	s, err := journal.OpenSQLite(filepath.Join(t.TempDir(), "store.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func revision(t *testing.T, s *journal.SQLite, request, kind string) loomgit.Revision {
	t.Helper()
	ctx := context.Background()
	r, err := s.ReserveRevision(ctx, loomgit.Revision{Workspace: "W", Change: "C", RequestID: request, Kind: kind, Operation: "snapshot", Outcome: "completed", BaseSHA: strings.Repeat("a", 40), TreeHash: strings.Repeat("b", 40), SourceHeadSHA: strings.Repeat("c", 40)})
	if err != nil {
		t.Fatal(err)
	}
	r.HeadSHA = strings.Repeat(request[:1], 40)
	if err := s.FinishRevision(ctx, r); err != nil {
		t.Fatal(err)
	}
	r.Ready = true
	return r
}

func codeIs(t *testing.T, err error, code loomgit.Code) {
	t.Helper()
	var coded *loomgit.Error
	if !errors.As(err, &coded) || coded.Kind != code {
		t.Fatalf("error=%v, want %s", err, code)
	}
}

func TestVerdictsBindExactHeadAndSupersedeOnlyOnNewSource(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	r1 := revision(t, s, "1", "source")
	v, err := Submit(ctx, s, "W", "C", r1.Number, r1.HeadSHA, "approve", "", Actor{"human", "user"})
	if err != nil || v.HeadSHA != r1.HeadSHA {
		t.Fatalf("verdict=%+v err=%v", v, err)
	}
	if err := RequireVerdict(ctx, s, "W", "C", r1.Number, r1.HeadSHA, "publish", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := Submit(ctx, s, "W", "C", r1.Number, r1.HeadSHA, "reject", "", Actor{"human", "user"}); err != nil {
		t.Fatal(err)
	}
	codeIs(t, RequireVerdict(ctx, s, "W", "C", r1.Number, r1.HeadSHA, "publish", ""), loomgit.ReviewRequired)
	if _, err := Submit(ctx, s, "W", "C", r1.Number, r1.HeadSHA, "approve", "", Actor{"human", "user"}); err != nil {
		t.Fatal(err)
	}
	if err := RequireVerdict(ctx, s, "W", "C", r1.Number, strings.Repeat("f", 40), "apply", "lead"); err == nil {
		t.Fatal("wrong head passed")
	}
	derived := revision(t, s, "2", "derived")
	if _, err := Submit(ctx, s, "W", "C", r1.Number, r1.HeadSHA, "approve", "", Actor{"human", "user"}); err != nil {
		t.Fatalf("derived revision superseded source: %v", err)
	}
	codeIs(t, RequireVerdict(ctx, s, "W", "C", derived.Number, derived.HeadSHA, "apply", "lead"), loomgit.ReviewRequired)
	r2 := revision(t, s, "3", "source")
	codeIs(t, RequireVerdict(ctx, s, "W", "C", r1.Number, r1.HeadSHA, "apply", "lead"), loomgit.RevisionSuperseded)
	codeIs(t, func() error {
		_, err := Submit(ctx, s, "W", "C", r1.Number, r1.HeadSHA, "approve", "", Actor{"human", "user"})
		return err
	}(), loomgit.RevisionSuperseded)
	if _, err := Submit(ctx, s, "W", "C", r2.Number, r2.HeadSHA, "approve", "", Actor{"human", "user"}); err != nil {
		t.Fatal(err)
	}
}

func TestOverrideAndActorPolicy(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	r := revision(t, s, "1", "source")
	if err := s.SetRevisionAuthor(ctx, r, "agent", "worker"); err != nil {
		t.Fatal(err)
	}
	codeIs(t, func() error {
		_, err := Submit(ctx, s, "W", "C", r.Number, r.HeadSHA, "approve", "", Actor{"agent", "worker"})
		return err
	}(), loomgit.ReviewRequired)
	codeIs(t, func() error {
		_, err := Submit(ctx, s, "W", "C", r.Number, r.HeadSHA, "override", "reason", Actor{"lead", "L1"})
		return err
	}(), loomgit.ReviewRequired)
	codeIs(t, func() error {
		_, err := Submit(ctx, s, "W", "C", r.Number, r.HeadSHA, "override", "", Actor{"human", "user"})
		return err
	}(), loomgit.ReviewRequired)
	v, err := Submit(ctx, s, "W", "C", r.Number, r.HeadSHA, "override", "urgent", Actor{"human", "user"})
	if err != nil || v.ActorID != "user" || v.Reason != "urgent" {
		t.Fatalf("override=%+v err=%v", v, err)
	}
	if err := RequireVerdict(ctx, s, "W", "C", r.Number, r.HeadSHA, "publish", ""); err != nil {
		t.Fatal(err)
	}
	v, err = Submit(ctx, s, "W", "C", r.Number, r.HeadSHA, "approve", "", Actor{"lead", "L1"})
	if err != nil || v.Kind != "policy" || v.Reason != "policy" {
		t.Fatalf("policy=%+v err=%v", v, err)
	}
	if err := RequireVerdict(ctx, s, "W", "C", r.Number, r.HeadSHA, "publish", ""); err != nil {
		t.Fatal(err)
	}
	if err := RequireVerdict(ctx, s, "W", "C", r.Number, r.HeadSHA, "apply", "L1"); err != nil {
		t.Fatal(err)
	}
	codeIs(t, RequireVerdict(ctx, s, "W", "C", r.Number, r.HeadSHA, "apply", "L2"), loomgit.ReviewRequired)
	if err := s.SetLeadMayApprovePublish(ctx, "W", false); err != nil {
		t.Fatal(err)
	}
	codeIs(t, func() error {
		_, err := Submit(ctx, s, "W", "C", r.Number, r.HeadSHA, "approve", "", Actor{"lead", "L1"})
		return err
	}(), loomgit.ReviewRequired)
}

func TestOwnLayerAndIncompleteRevision(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	r := revision(t, s, "1", "source")
	if err := s.SetRevisionAuthor(ctx, r, "lead", "L1"); err != nil {
		t.Fatal(err)
	}
	if err := s.SetLeadMayApprovePublish(ctx, "W", false); err != nil {
		t.Fatal(err)
	}
	codeIs(t, func() error {
		_, err := Submit(ctx, s, "W", "C", r.Number, r.HeadSHA, "approve", "", Actor{"lead", "L1"})
		return err
	}(), loomgit.ReviewRequired)
	if err := s.SetLeadMayApprovePublish(ctx, "W", true); err != nil {
		t.Fatal(err)
	}
	v, err := Submit(ctx, s, "W", "C", r.Number, r.HeadSHA, "approve", "", Actor{"lead", "L1"})
	if err != nil || v.Kind != "policy" || v.Reason != "own_layer" {
		t.Fatalf("verdict=%+v err=%v", v, err)
	}
	if err := s.SetRevisionIncomplete(ctx, r); err != nil {
		t.Fatal(err)
	}
	codeIs(t, RequireVerdict(ctx, s, "W", "C", r.Number, r.HeadSHA, "publish", ""), loomgit.CaptureIncomplete)
	codeIs(t, func() error {
		_, err := Submit(ctx, s, "W", "C", r.Number, r.HeadSHA, "approve", "", Actor{"human", "user"})
		return err
	}(), loomgit.CaptureIncomplete)
}

func TestCancelledIncompleteRevisionCannotBeApprovedOrPublished(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	r, err := s.ReserveRevision(ctx, loomgit.Revision{
		Workspace: "W", Change: "C", RequestID: "cancelled-1", Kind: "source",
		Operation: "snapshot", Outcome: "cancelled", BaseSHA: strings.Repeat("a", 40),
		TreeHash: strings.Repeat("b", 40), SourceHeadSHA: strings.Repeat("c", 40),
	})
	if err != nil {
		t.Fatal(err)
	}
	r.HeadSHA = strings.Repeat("d", 40)
	if err := s.FinishRevision(ctx, r); err != nil {
		t.Fatal(err)
	}
	if err := s.SetRevisionIncomplete(ctx, r); err != nil {
		t.Fatal(err)
	}
	codeIs(t, func() error {
		_, err := Submit(ctx, s, "W", "C", r.Number, r.HeadSHA, "approve", "", Actor{"human", "user"})
		return err
	}(), loomgit.CaptureIncomplete)
	codeIs(t, RequireVerdict(ctx, s, "W", "C", r.Number, r.HeadSHA, "publish", ""), loomgit.CaptureIncomplete)
}
