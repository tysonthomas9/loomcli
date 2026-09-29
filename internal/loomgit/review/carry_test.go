package review

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/loomgit"
	"github.com/tysonthomas9/loomcli/internal/loomgit/internal/gitexec"
	"github.com/tysonthomas9/loomcli/internal/loomgit/internal/journal"
	"github.com/tysonthomas9/loomcli/internal/loomgit/internal/replay"
)

func git(t *testing.T, runner *gitexec.Runner, args ...string) string {
	t.Helper()
	out, err := runner.Run(context.Background(), args...)
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(string(out))
}

func commit(t *testing.T, runner *gitexec.Runner, dir, name, content string) string {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	git(t, runner, "add", name)
	git(t, runner, "commit", "-qm", name)
	return git(t, runner, "rev-parse", "HEAD")
}

func recorded(t *testing.T, s *journal.SQLite, request, kind, base, head string, source int) loomgit.Revision {
	t.Helper()
	r, err := s.ReserveRevision(context.Background(), loomgit.Revision{Workspace: "W", Change: "C", RequestID: request, Kind: kind, Operation: "restack", Outcome: "completed", BaseSHA: base, TreeHash: base, SourceHeadSHA: head, DerivedFromChange: map[bool]string{true: "C", false: ""}[source > 0], DerivedFromNumber: source})
	if err != nil {
		t.Fatal(err)
	}
	r.HeadSHA = head
	if err := s.FinishRevision(context.Background(), r); err != nil {
		t.Fatal(err)
	}
	r.Ready = true
	return r
}

func TestCarryForwardCleanPatchIDsAndEmptyDroppedCommit(t *testing.T) {
	dir := t.TempDir()
	if out, err := exec.Command("git", "init", "-q", dir).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v %s", err, out)
	} //nolint:norawexec // Real temporary Git fixture.
	configPath := filepath.Join(t.TempDir(), "config")
	if err := os.WriteFile(configPath, []byte("[user]\nname = Test\nemail = test@example.com\n"), 0600); err != nil {
		t.Fatal(err)
	}
	runner, err := gitexec.New(dir, gitexec.Options{GlobalConfig: configPath, SystemConfig: os.DevNull})
	if err != nil {
		t.Fatal(err)
	}
	s := newStore(t)
	ctx := context.Background()
	base := commit(t, runner, dir, "base", "base")
	dropped := commit(t, runner, dir, "a", "same")
	sourceHead := commit(t, runner, dir, "b", "work")
	source := recorded(t, s, "source", "source", base, sourceHead, 0)
	if _, err := Submit(ctx, s, "W", "C", source.Number, source.HeadSHA, "approve", "", Actor{"human", "user"}); err != nil {
		t.Fatal(err)
	}
	git(t, runner, "checkout", "-q", "-b", "trunk", base)
	trunk := commit(t, runner, dir, "a", "same")
	derivedHead := commit(t, runner, dir, "b", "work")
	derived := recorded(t, s, "derived", "derived", trunk, derivedHead, source.Number)
	v, ok, err := CarryForward(ctx, s, runner, source, derived, replay.Result{HeadSHA: derivedHead, DroppedCommits: []string{dropped}})
	if err != nil || !ok || v.Kind != "carried" || v.SourceVerdictID == 0 {
		t.Fatalf("carried=%+v ok=%v err=%v", v, ok, err)
	}
	if err := RequireVerdict(ctx, s, "W", "C", derived.Number, derived.HeadSHA, "publish", ""); err != nil {
		t.Fatal(err)
	}
	git(t, runner, "checkout", "-q", "-b", "different", trunk)
	differentHead := commit(t, runner, dir, "b", "different")
	different := recorded(t, s, "different", "derived", trunk, differentHead, source.Number)
	if _, ok, err := CarryForward(ctx, s, runner, source, different, replay.Result{DroppedCommits: []string{dropped}}); err != nil || ok {
		t.Fatalf("changed patch carried: ok=%v err=%v", ok, err)
	}
	codeIs(t, RequireVerdict(ctx, s, "W", "C", different.Number, different.HeadSHA, "publish", ""), loomgit.ReviewRequired)
	if _, ok, err := CarryForward(ctx, s, runner, source, different, replay.Result{ConflictCommit: dropped}); err != nil || ok {
		t.Fatalf("conflict carried: ok=%v err=%v", ok, err)
	}
}
