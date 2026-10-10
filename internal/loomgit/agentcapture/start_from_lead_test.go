package agentcapture

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/loomgit"
	"github.com/tysonthomas9/loomcli/internal/loomgit/internal/journal"
)

type leadFixture struct {
	journal, source, lead, agent, base string
	store                              *journal.SQLite
}

// newLeadFixture builds a repo whose lead working area and agent checkout are
// worktrees of one source, both at the trunk base.
func newLeadFixture(t *testing.T) leadFixture {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	if err := os.WriteFile(filepath.Join(home, ".gitconfig"), []byte("[user]\nname = Test\nemail = test@example.com\n"), 0600); err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	f := leadFixture{journal: filepath.Join(t.TempDir(), "loomgit", "store.db"), source: filepath.Join(root, "source"),
		lead: filepath.Join(root, "lead"), agent: filepath.Join(root, "agent")}
	if err := os.MkdirAll(f.source, 0700); err != nil {
		t.Fatal(err)
	}
	gitForCapture(t, f.source, "init", "-q")
	writeFile(t, f.source, "main.txt", "trunk\n")
	gitForCapture(t, f.source, "add", "main.txt")
	gitForCapture(t, f.source, "commit", "-qm", "trunk")
	f.base = gitForCapture(t, f.source, "rev-parse", "HEAD")
	gitForCapture(t, f.source, "worktree", "add", "-q", "-b", "loom/ws/W/interactive/L", f.lead, f.base)
	gitForCapture(t, f.source, "worktree", "add", "-q", "-b", "local-coder", f.agent, f.base)
	if err := os.MkdirAll(filepath.Dir(f.journal), 0700); err != nil {
		t.Fatal(err)
	}
	store, err := journal.OpenSQLite(f.journal)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	f.store = store
	if err := store.SaveWorkingAreas(context.Background(), []journal.WorkingArea{{Workspace: "W", Lead: "L", Repo: "repo",
		Path: f.lead, Branch: "loom/ws/W/interactive/L", BaseSHA: f.base, Mode: "worktree"}}); err != nil {
		t.Fatal(err)
	}
	return f
}

func writeFile(t *testing.T, dir, name, content string) {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
}

// commitIn commits name=content in dir and returns the new head.
func commitIn(t *testing.T, dir, name, content string) string {
	t.Helper()
	writeFile(t, dir, name, content)
	gitForCapture(t, dir, "add", name)
	gitForCapture(t, dir, "commit", "-qm", "change "+name)
	return gitForCapture(t, dir, "rev-parse", "HEAD")
}

// freezeSource records a complete revision frozen from sha, as the daemon's
// exit freeze does for an attempt's commit.
func (f leadFixture) freezeSource(t *testing.T, sha string) {
	t.Helper()
	ctx := context.Background()
	rev, err := f.store.ReserveRevision(ctx, loomgit.Revision{Workspace: "W", Change: "C", RequestID: "driver:" + sha,
		Kind: "source", Operation: "snapshot", Outcome: "completed", BaseSHA: f.base, TreeHash: "tree", SourceHeadSHA: sha})
	if err != nil {
		t.Fatal(err)
	}
	rev.HeadSHA = sha
	if err := f.store.FinishRevision(ctx, rev); err != nil {
		t.Fatal(err)
	}
}

func (f leadFixture) start(t *testing.T) (StartResult, error) {
	t.Helper()
	return StartFromLeadAt(context.Background(), f.journal, f.source, f.agent, "W", "L", "repo", "")
}

func assertUnsavedWork(t *testing.T, err error) {
	t.Helper()
	var coded *loomgit.Error
	if !errors.As(err, &coded) || coded.Code() != string(loomgit.UnsavedWork) {
		t.Fatalf("err = %v, want %s", err, loomgit.UnsavedWork)
	}
}

// The F7 repro: attempt 1 left its frozen commit on the agent branch, the lead
// no longer has it (Unapply), and the lead moved on. The rerun must start at
// the lead's head, not on the leftover commit.
func TestStartFromLeadMovesLeftoverCheckoutToLeadHead(t *testing.T) {
	f := newLeadFixture(t)
	leftover := commitIn(t, f.agent, "feature.txt", "attempt one\n")
	f.freezeSource(t, leftover)
	leadHead := commitIn(t, f.lead, "lead.txt", "lead work\n")

	result, err := f.start(t)
	if err != nil {
		t.Fatal(err)
	}
	if !result.HasLead || !result.Moved || result.BaseSHA != leadHead {
		t.Fatalf("result = %+v, want moved to lead head %s", result, leadHead)
	}
	if head := gitForCapture(t, f.agent, "rev-parse", "HEAD"); head != leadHead {
		t.Fatalf("agent HEAD = %s, want lead head %s", head, leadHead)
	}
	if branch := gitForCapture(t, f.agent, "symbolic-ref", "--short", "HEAD"); branch != "local-coder" {
		t.Fatalf("agent branch = %q, want local-coder kept", branch)
	}
	if _, err := os.Stat(filepath.Join(f.agent, "feature.txt")); !os.IsNotExist(err) {
		t.Fatalf("leftover attempt file still in the new attempt's checkout: %v", err)
	}
	again, err := f.start(t)
	if err != nil || again.Moved || again.BaseSHA != leadHead {
		t.Fatalf("second start = %+v, %v; want no move at lead head", again, err)
	}
}

func TestStartFromLeadFastForwardsCheckoutAlreadyInLead(t *testing.T) {
	f := newLeadFixture(t)
	leadHead := commitIn(t, f.lead, "lead.txt", "lead work\n")
	result, err := f.start(t)
	if err != nil || !result.Moved || gitForCapture(t, f.agent, "rev-parse", "HEAD") != leadHead {
		t.Fatalf("result = %+v, %v; want checkout at lead head %s", result, err, leadHead)
	}
}

func TestStartFromLeadRefusesUncommittedFiles(t *testing.T) {
	for name, edit := range map[string]func(t *testing.T, f leadFixture){
		"tracked edit":   func(t *testing.T, f leadFixture) { writeFile(t, f.agent, "main.txt", "edited\n") },
		"untracked file": func(t *testing.T, f leadFixture) { writeFile(t, f.agent, "notes.txt", "unsaved\n") },
	} {
		t.Run(name, func(t *testing.T) {
			f := newLeadFixture(t)
			leadHead := commitIn(t, f.lead, "lead.txt", "lead work\n")
			edit(t, f)
			_, err := f.start(t)
			assertUnsavedWork(t, err)
			if head := gitForCapture(t, f.agent, "rev-parse", "HEAD"); head != f.base || head == leadHead {
				t.Fatalf("refused start moved HEAD to %s", head)
			}
			if status := gitForCapture(t, f.agent, "status", "--porcelain"); status == "" {
				t.Fatal("refused start discarded the unsaved file")
			}
		})
	}
}

func TestStartFromLeadIgnoresRuntimeAndIgnoredFiles(t *testing.T) {
	f := newLeadFixture(t)
	leadHead := commitIn(t, f.lead, "lead.txt", "lead work\n")
	writeFile(t, f.agent, ".agent.checkpoint.json", "{}")
	writeFile(t, f.agent, ".codex/hooks.json", "{}")
	exclude := gitForCapture(t, f.agent, "rev-parse", "--git-path", "info/exclude")
	if !filepath.IsAbs(exclude) {
		exclude = filepath.Join(f.agent, exclude)
	}
	if err := os.MkdirAll(filepath.Dir(exclude), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(exclude, []byte(".claude/skills/\n"), 0600); err != nil {
		t.Fatal(err)
	}
	writeFile(t, f.agent, ".claude/skills/s/SKILL.md", "skill")
	result, err := f.start(t)
	if err != nil || !result.Moved || gitForCapture(t, f.agent, "rev-parse", "HEAD") != leadHead {
		t.Fatalf("result = %+v, %v; runtime files must not block the move", result, err)
	}
	if _, err := os.Stat(filepath.Join(f.agent, ".claude/skills/s/SKILL.md")); err != nil {
		t.Fatalf("ignored file removed: %v", err)
	}
}

func TestStartFromLeadRefusesCommitsLoomHasNotSaved(t *testing.T) {
	f := newLeadFixture(t)
	unsaved := commitIn(t, f.agent, "feature.txt", "never frozen\n")
	_, err := f.start(t)
	assertUnsavedWork(t, err)
	if head := gitForCapture(t, f.agent, "rev-parse", "HEAD"); head != unsaved {
		t.Fatalf("refused start moved HEAD from %s to %s", unsaved, head)
	}
	gitForCapture(t, f.agent, "update-ref", "refs/loom/ws/W/attempt/a1/capture", unsaved)
	result, err := f.start(t)
	if err != nil || !result.Moved || gitForCapture(t, f.agent, "rev-parse", "HEAD") != f.base {
		t.Fatalf("result = %+v, %v; a commit kept by a Loom ref must allow the move", result, err)
	}
}

func TestStartFromLeadWithoutLeadWorkingAreaLeavesCheckout(t *testing.T) {
	f := newLeadFixture(t)
	head := commitIn(t, f.agent, "feature.txt", "work\n")
	result, err := StartFromLeadAt(context.Background(), f.journal, f.source, f.agent, "W", "other-lead", "repo", "")
	if err != nil || result.HasLead || result.Moved {
		t.Fatalf("result = %+v, %v; want untouched without a working area", result, err)
	}
	if got := gitForCapture(t, f.agent, "rev-parse", "HEAD"); got != head {
		t.Fatalf("HEAD = %s, want %s", got, head)
	}
}

func TestStartFromLeadRefusesSwitchedLeadBranch(t *testing.T) {
	f := newLeadFixture(t)
	gitForCapture(t, f.lead, "checkout", "-q", "-b", "elsewhere")
	if _, err := f.start(t); err == nil {
		t.Fatal("a lead working area on another branch must not be used as the attempt base")
	}
}

// A checkout already at the lead's head still refuses a cold start while it
// holds uncommitted files: the new attempt must not inherit them.
func TestStartFromLeadRefusesUncommittedFilesAtLeadHead(t *testing.T) {
	for name, edit := range map[string]func(t *testing.T, f leadFixture){
		"tracked edit":   func(t *testing.T, f leadFixture) { writeFile(t, f.agent, "main.txt", "edited\n") },
		"untracked file": func(t *testing.T, f leadFixture) { writeFile(t, f.agent, "notes.txt", "unsaved\n") },
	} {
		t.Run(name, func(t *testing.T) {
			f := newLeadFixture(t)
			edit(t, f)
			_, err := f.start(t)
			assertUnsavedWork(t, err)
			if head := gitForCapture(t, f.agent, "rev-parse", "HEAD"); head != f.base {
				t.Fatalf("refused start moved HEAD to %s", head)
			}
			if status := gitForCapture(t, f.agent, "status", "--porcelain"); status == "" {
				t.Fatal("refused start discarded the unsaved file")
			}
		})
	}
}

// A dependent task starts from its blocker's frozen revision, not the lead's
// head, even when the checkout sits on the lead's head.
func TestStartFromBaseUsesDependentBlockerRevision(t *testing.T) {
	f := newLeadFixture(t)
	leadHead := commitIn(t, f.lead, "lead.txt", "lead work\n")
	if _, err := f.start(t); err != nil {
		t.Fatal(err)
	}
	gitForCapture(t, f.source, "checkout", "-q", "-b", "blocker", f.base)
	blocker := commitIn(t, f.source, "blocker.txt", "blocker\n")
	gitForCapture(t, f.source, "update-ref", "refs/loom/ws/W/change/C/1/head", blocker)
	result, err := StartFromLeadAt(context.Background(), f.journal, f.source, f.agent, "W", "L", "repo", blocker)
	if err != nil || !result.Moved || result.BaseSHA != blocker {
		t.Fatalf("result = %+v, %v; want moved to the blocker revision %s", result, err, blocker)
	}
	if head := gitForCapture(t, f.agent, "rev-parse", "HEAD"); head != blocker || head == leadHead {
		t.Fatalf("agent HEAD = %s, want the blocker revision %s", head, blocker)
	}
	writeFile(t, f.agent, "notes.txt", "unsaved\n")
	_, err = StartFromLeadAt(context.Background(), f.journal, f.source, f.agent, "W", "L", "repo", blocker)
	assertUnsavedWork(t, err)
}
