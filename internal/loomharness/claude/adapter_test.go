package claude

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/tysonthomas9/loomcli/internal/loomharness"
)

// newAdapter returns a fake-claude adapter, an open feed, and its fixture.
func newAdapter(t *testing.T) (*Adapter, loomharness.Feed, *fixture) {
	t.Helper()
	f, cfg := newFixture(t, "2.1.285")
	cfg.OnFrame = nil
	a := New(cfg)
	feed, err := a.Feed(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = feed.Close()
		a.mu.Lock()
		all := make([]*Session, 0, len(a.sessions))
		for _, s := range a.sessions {
			all = append(all, s)
		}
		a.mu.Unlock()
		for _, s := range all {
			_ = s.Close(context.Background())
		}
	})
	return a, feed, f
}

func open(t *testing.T, a *Adapter, f *fixture, key string) (loomharness.NativeRef, loomharness.Session) {
	t.Helper()
	spec := loomharness.OpenSpec{Key: key, Dir: t.TempDir(), Launch: loomharness.Launch{Root: f.root}}
	if err := isolated(a.cfg, ProcessSpec{Launch: spec.Launch, Dir: spec.Dir}); err != nil {
		t.Fatal(err)
	}
	ref, err := a.Open(context.Background(), spec)
	if err != nil {
		t.Fatal(err)
	}
	return ref, a.Session(ref)
}

// until collects events up to and including the first of type typ.
func until(t *testing.T, feed loomharness.Feed, typ loomharness.EventType) []loomharness.Event {
	t.Helper()
	var got []loomharness.Event
	for {
		select {
		case e := <-feed.Events():
			got = append(got, e)
			if e.Type == typ {
				return got
			}
		case <-time.After(10 * time.Second):
			t.Fatalf("no %s event; got %v", typ, types(got))
		}
	}
}

func types(es []loomharness.Event) []loomharness.EventType {
	out := make([]loomharness.EventType, len(es))
	for i, e := range es {
		out[i] = e.Type
	}
	return out
}

func prompt(t *testing.T, s loomharness.Session, text string) string {
	t.Helper()
	key := uuid.NewString()
	if err := s.Prompt(context.Background(), loomharness.Input{Key: key, Text: text}); err != nil {
		t.Fatal(err)
	}
	return key
}

func TestClaudeFramesMapToEvents(t *testing.T) {
	a, feed, f := newAdapter(t)
	ref, s := open(t, a, f, "agent-frames")
	key := prompt(t, s, "tool task")
	got := until(t, feed, loomharness.EventTurnCompleted)
	want := []loomharness.EventType{
		loomharness.EventTurnStarted, loomharness.EventMessageDelivered,
		loomharness.EventItemStarted, loomharness.EventItemCompleted, // the tool call and its result
		loomharness.EventSubagentStarted,
		loomharness.EventItemStarted, loomharness.EventDelta, loomharness.EventItemCompleted, // the text block
		loomharness.EventUsage, loomharness.EventTurnCompleted,
	}
	if !slices.Equal(types(got), want) {
		t.Fatalf("events = %v, want %v", types(got), want)
	}
	turn := got[0].TurnID
	for i, e := range got {
		if e.TurnID != turn || turn == "" || e.Session != ref || (i > 0 && e.Seq <= got[i-1].Seq) {
			t.Fatalf("event %d %+v: want turn %s, session %v, rising seq", i, e, turn, ref)
		}
	}
	if got[0].InputKey != key {
		t.Fatalf("turn.started InputKey = %q, want the handed key %s", got[0].InputKey, key)
	}
	if d := got[1]; d.InputKey != key || d.ItemID != key {
		t.Fatalf("delivered = %+v, want key %s", d, key)
	}
	if got[2].ItemID != "msg_1/tool/toolu_1" || got[3].ItemID != got[2].ItemID || got[3].ItemKind != "tool" {
		t.Fatalf("tool items = %+v / %+v", got[2], got[3])
	}
	if got[4].ItemID != "task_1" {
		t.Fatalf("subagent = %+v", got[4])
	}
	if got[5].ItemID != "msg_1/text/1" || got[6].ItemID != got[5].ItemID || got[7].ItemID != got[5].ItemID ||
		got[6].Text != "hi" || got[7].Text != "hi" || got[7].ItemKind != "message" {
		t.Fatalf("text items = %+v %+v %+v", got[5], got[6], got[7])
	}
	if got[9].StopReason != "completed" {
		t.Fatalf("stop = %q", got[9].StopReason)
	}

	// The next turn has its own id; a resume_reason frame maps to turn.resumed.
	prompt(t, s, "resume")
	next := until(t, feed, loomharness.EventTurnCompleted)
	if next[0].Type != loomharness.EventTurnStarted || next[0].TurnID == turn ||
		!slices.ContainsFunc(next, func(e loomharness.Event) bool {
			return e.Type == loomharness.EventTurnResumed && e.Text == "interrupted_turn"
		}) {
		t.Fatalf("second turn = %v", types(next))
	}
	if st, _ := s.Status(context.Background()); st.Running || st.TurnID != "" {
		t.Fatalf("status after the turn = %+v", st)
	}
}

func TestClaudeInterruptCompletesCancelled(t *testing.T) {
	a, feed, f := newAdapter(t)
	_, s := open(t, a, f, "agent-stop")
	prompt(t, s, "hang")
	until(t, feed, loomharness.EventTurnStarted)
	if st, _ := s.Status(context.Background()); !st.Running || st.TurnID == "" {
		t.Fatalf("status during the turn = %+v", st)
	}
	if ok, err := s.Interrupt(context.Background()); !ok || err != nil {
		t.Fatalf("interrupt = %v, %v", ok, err)
	}
	got := until(t, feed, loomharness.EventTurnCompleted)
	if got[len(got)-1].StopReason != "cancelled" {
		t.Fatalf("stop = %q", got[len(got)-1].StopReason)
	}
	if st, _ := s.Status(context.Background()); st.Running || !st.LastTurnInterrupt {
		t.Fatalf("status = %+v", st)
	}
	if ok, err := s.Interrupt(context.Background()); ok || err != nil {
		t.Fatalf("interrupt when idle = %v, %v", ok, err)
	}
}

func TestClaudeSetModelAndMoveAtTurnBoundary(t *testing.T) {
	a, feed, f := newAdapter(t)
	ref, s := open(t, a, f, "agent-switch")
	ctx := context.Background()
	prompt(t, s, "hang")
	until(t, feed, loomharness.EventTurnStarted)
	dir := t.TempDir()
	if err := s.SetModel(ctx, "sonnet", []loomharness.Option{{ID: loomharness.OptionEffort, Value: "xhigh"}}); err != nil {
		t.Fatal(err)
	}
	if err := s.Move(ctx, dir); err != nil {
		t.Fatal(err)
	}
	if err := s.Move(ctx, filepath.Join(dir, "missing")); err == nil {
		t.Fatal("Move to a missing directory must fail")
	}
	if err := s.Prompt(ctx, loomharness.Input{Key: uuid.NewString(), Text: "x"}); !errors.Is(err, loomharness.ErrBusy) {
		t.Fatalf("prompt mid-turn = %v", err)
	}
	if got := launches(t, f.dumpPath); len(got) != 1 {
		t.Fatalf("the running turn must keep its process; %d launches", len(got))
	}
	if _, err := s.Interrupt(ctx); err != nil {
		t.Fatal(err)
	}
	until(t, feed, loomharness.EventTurnCompleted)
	prompt(t, s, "next")
	until(t, feed, loomharness.EventTurnCompleted)
	got := launches(t, f.dumpPath)
	cwd, _ := filepath.EvalSymlinks(dir)
	effort := slices.Index(got[len(got)-1].Args, "--effort")
	if effort < 0 || got[len(got)-1].Args[effort+1] != "xhigh" || slices.Contains(got[0].Args, "--effort") {
		t.Fatalf("want --effort xhigh on the relaunch only; got %v then %v", got[0].Args, got[len(got)-1].Args)
	}
	if len(got) != 2 || !slices.Contains(got[1].Args, "sonnet") || !slices.Contains(got[1].Args, "--resume") ||
		!slices.Contains(got[1].Args, ref.NativeID) || got[1].Cwd != cwd {
		t.Fatalf("want a --resume %s relaunch with sonnet in %s; got %d launches, last %v in %s", ref.NativeID, cwd, len(got), got[len(got)-1].Args, got[len(got)-1].Cwd)
	}
}

func TestClaudeReturnsActualNativeRef(t *testing.T) {
	a, _, f := newAdapter(t)
	ctx := context.Background()
	ref, s := open(t, a, f, "agent-ref")
	if ref != (loomharness.NativeRef{Root: f.root, NativeID: SessionID("agent-ref")}) {
		t.Fatalf("fresh ref = %+v", ref)
	}
	again, _ := open(t, a, f, "agent-ref")
	if again != ref {
		t.Fatalf("repeat Open = %+v, want %+v", again, ref)
	}
	user := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", user)
	inherited, err := New(Config{}).Open(ctx, loomharness.OpenSpec{Key: "agent-inherited", Dir: t.TempDir()})
	if err != nil || inherited.Root != user {
		t.Fatalf("inherited-login ref = %+v, %v; want the user's root %s", inherited, err, user)
	}
	// Move reuses the same native session: the ref does not change.
	if err := s.Move(ctx, t.TempDir()); err != nil {
		t.Fatal(err)
	}
	if got := s.(*Session).ref; got != ref {
		t.Fatalf("ref after Move = %+v", got)
	}
	// Resume (and so a changed ref) belongs to 5.3; until then it fails closed.
	if _, err := s.Resume(ctx, loomharness.Launch{Root: f.root}, nil); !errors.Is(err, loomharness.ErrUnavailable) {
		t.Fatalf("Resume = %v, want an explicit failure", err)
	}
	if got := launches(t, f.dumpPath); len(got) != 0 {
		t.Fatal("Open, Move and a failed Resume must not launch")
	}
}

func TestClaudeOpenRefusesRulesItCannotInstall(t *testing.T) {
	a, _, _ := newAdapter(t)
	_, err := a.Open(context.Background(), loomharness.OpenSpec{Key: "k", Dir: t.TempDir(),
		Rules: []loomharness.PermissionRule{{Action: "bash", Resource: "*", Effect: "deny"}}})
	if !errors.Is(err, loomharness.ErrUnavailable) {
		t.Fatalf("Open with rules = %v", err)
	}
}

func TestClaudeCloseStopsOnlyThisSession(t *testing.T) {
	a, feed, f := newAdapter(t)
	_, one := open(t, a, f, "agent-one")
	two := uuid.NewString()
	_, other := open(t, a, f, two)
	ctx := context.Background()
	prompt(t, one, "a")
	until(t, feed, loomharness.EventTurnCompleted)
	prompt(t, other, "b")
	until(t, feed, loomharness.EventTurnCompleted)
	if err := one.Close(ctx); err != nil {
		t.Fatal(err)
	}
	prompt(t, other, "c")
	until(t, feed, loomharness.EventTurnCompleted)
	if got := launches(t, f.dumpPath); len(got) != 2 {
		t.Fatalf("closing one session must not touch the other; %d launches", len(got))
	}
	if m, _ := filepath.Glob(filepath.Join(f.root, "projects", "*", SessionID("agent-one")+".jsonl")); len(m) != 1 {
		t.Fatal("Close must keep the native transcript")
	}
}

func TestClaudeProcessDeathEmitsFeedGap(t *testing.T) {
	a, feed, f := newAdapter(t)
	_, s := open(t, a, f, "agent-die")
	prompt(t, s, "die")
	until(t, feed, loomharness.EventFeedGap)
	if st, _ := s.Status(context.Background()); st.Running {
		t.Fatalf("status after the process died = %+v", st)
	}
	prompt(t, s, "back")
	until(t, feed, loomharness.EventTurnCompleted)
}

func TestClaudeUnsupportedMethodsFailExplicitly(t *testing.T) {
	a, _, f := newAdapter(t)
	_, s := open(t, a, f, "agent-later")
	ctx := context.Background()
	// Asks arrive with the 5.2 permission tool: until then every reply,
	// including an always-allow and a question's answer, fails explicitly
	// rather than being narrowed or dropped.
	for _, r := range []loomharness.Reply{{Allow: true}, {Allow: true, Always: true}, {Answer: "yes"}} {
		if err := s.Reply(ctx, "ask", r); !errors.Is(err, loomharness.ErrUnavailable) {
			t.Fatalf("Reply(%+v) = %v", r, err)
		}
	}
	if _, err := s.HasInput(ctx, "k"); !errors.Is(err, loomharness.ErrUnavailable) {
		t.Fatalf("HasInput = %v", err)
	}
	if _, err := s.Messages(ctx, "", 10); !errors.Is(err, loomharness.ErrUnavailable) {
		t.Fatalf("Messages = %v", err)
	}
}

// TestClaudeOpensNoAsksBefore52: no frame opens an ask before the 5.2
// permission tool, so no ask can carry the wrong kind; Claude's
// AskUserQuestion stays a tool item until 5.2 maps it to a question ask.
func TestClaudeOpensNoAsksBefore52(t *testing.T) {
	m := newMapper(loomharness.NativeRef{NativeID: "s"})
	for _, raw := range []string{
		`{"type":"assistant","message":{"id":"msg_q","content":[{"type":"tool_use","id":"toolu_q","name":"AskUserQuestion","input":{"questions":[]}}]}}`,
		`{"type":"assistant","message":{"id":"msg_b","content":[{"type":"tool_use","id":"toolu_b","name":"Bash","input":{"command":"ls"}}]}}`,
		`{"type":"result","subtype":"success"}`,
	} {
		for _, e := range m.frame([]byte(raw)) {
			if e.Type == loomharness.EventAskOpened || e.ItemKind == "question" || e.ItemKind == "approval" {
				t.Fatalf("frame %s opened an ask: %+v", raw, e)
			}
		}
	}
}

// writeTranscript writes <root>/projects/<project>/<name>.
func writeTranscript(t *testing.T, root, project, name string) string {
	t.Helper()
	p := filepath.Join(root, "projects", project, name)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func exists(p string) bool { _, err := os.Stat(p); return err == nil }

// TestClaudePurgeUsesRecordedRoot: session N was created under the user's
// inherited root; a profile was added later. Purge deletes N's transcript
// from its recorded root only; the profile and unrelated transcripts survive.
func TestClaudePurgeUsesRecordedRoot(t *testing.T) {
	inherited, profile := t.TempDir(), t.TempDir()
	n := SessionID("agent-n")
	owned := writeTranscript(t, inherited, "-tmp-wt", n+".jsonl")
	unrelated := writeTranscript(t, inherited, "-tmp-wt", uuid.NewString()+".jsonl")
	otherProject := writeTranscript(t, inherited, "-tmp-other", uuid.NewString()+".jsonl")
	profileOwn := writeTranscript(t, profile, "-tmp-wt", SessionID("agent-n-later")+".jsonl")
	settings := filepath.Join(profile, "settings.json")
	if err := os.WriteFile(settings, []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	a := New(Config{})
	if err := a.Purge(context.Background(), []loomharness.NativeRef{{Root: inherited, NativeID: n}}); err != nil {
		t.Fatal(err)
	}
	if exists(owned) {
		t.Fatal("the owned transcript survived")
	}
	for _, p := range []string{unrelated, otherProject, profileOwn, settings} {
		if !exists(p) {
			t.Fatalf("%s was deleted", p)
		}
	}
	// Already gone is fine: a purge can be repeated.
	if err := a.Purge(context.Background(), []loomharness.NativeRef{{Root: inherited, NativeID: n}}); err != nil {
		t.Fatal(err)
	}
}

// TestClaudeSymlinkedRootOpensCanonical: a config root reached through a
// symlink (a dotfile-managed ~/.claude) is recorded canonical at Open, so
// Purge, which refuses symlinks, still removes its transcript. The child's
// CLAUDE_CONFIG_DIR stays as given.
func TestClaudeSymlinkedRootOpensCanonical(t *testing.T) {
	ctx := context.Background()
	real := t.TempDir()
	link := filepath.Join(t.TempDir(), "claude")
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}
	a := New(Config{})
	l := loomharness.Launch{Root: link, Env: map[string]string{"CLAUDE_CONFIG_DIR": link}}
	ref, err := a.Open(ctx, loomharness.OpenSpec{Key: "agent-linked", Launch: l, Dir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	if ref.Root != real {
		t.Fatalf("ref root = %s, want the canonical %s", ref.Root, real)
	}
	if got := a.session(ref).spec.Launch.Env["CLAUDE_CONFIG_DIR"]; got != link {
		t.Fatalf("CLAUDE_CONFIG_DIR = %s, want it unchanged (%s)", got, link)
	}
	transcript := writeTranscript(t, real, "-wt", ref.NativeID+".jsonl")
	if err := a.Purge(ctx, []loomharness.NativeRef{ref}); err != nil {
		t.Fatal(err)
	}
	if exists(transcript) {
		t.Fatal("the transcript under the symlinked root survived")
	}
}

// TestClaudeOpenRefusesConfigRootDivergence: Open fails unless the config dir
// the child will use resolves to the root it records, so Purge never misses
// a transcript written elsewhere.
func TestClaudeOpenRefusesConfigRootDivergence(t *testing.T) {
	ctx := context.Background()
	root, other, home := t.TempDir(), t.TempDir(), t.TempDir()
	rootHome := filepath.Join(t.TempDir(), ".claude") // the default root of the first HOME
	for name, c := range map[string]struct {
		env []string
		l   loomharness.Launch
	}{
		"launch config elsewhere":        {[]string{"HOME=" + home}, loomharness.Launch{Root: root, Env: map[string]string{"CLAUDE_CONFIG_DIR": other}}},
		"inherited config elsewhere":     {[]string{"HOME=" + home, "CLAUDE_CONFIG_DIR=" + other}, loomharness.Launch{Root: root}},
		"default HOME/.claude elsewhere": {[]string{"HOME=" + home}, loomharness.Launch{Root: root}},
		// os/exec gives the child the last of duplicate entries.
		"duplicate config, last elsewhere": {[]string{"HOME=" + home, "CLAUDE_CONFIG_DIR=" + root, "CLAUDE_CONFIG_DIR=" + other}, loomharness.Launch{Root: root}},
		"duplicate HOME, last elsewhere":   {[]string{"HOME=" + filepath.Dir(rootHome), "HOME=" + home}, loomharness.Launch{Root: rootHome}},
	} {
		if _, err := New(Config{Env: c.env}).Open(ctx, loomharness.OpenSpec{Key: "k", Dir: t.TempDir(), Launch: c.l}); err == nil {
			t.Errorf("%s: Open accepted a config dir that is not the recorded root", name)
		}
	}
	if _, err := New(Config{Env: []string{"HOME=" + home}}).Open(ctx, loomharness.OpenSpec{Key: "k", Dir: t.TempDir(),
		Launch: loomharness.Launch{Root: filepath.Join(home, ".claude")}}); err != nil {
		t.Fatalf("default HOME/.claude as the root refused: %v", err)
	}
}

// TestClaudeLaunchRefusesRetargetedAlias: a config alias retargeted after Open
// is refused at launch, never followed.
func TestClaudeLaunchRefusesRetargetedAlias(t *testing.T) {
	a, _, f := newAdapter(t)
	ctx := context.Background()
	elsewhere := t.TempDir()
	link := filepath.Join(t.TempDir(), "claude")
	if err := os.Symlink(f.root, link); err != nil {
		t.Fatal(err)
	}
	ref, err := a.Open(ctx, loomharness.OpenSpec{Key: "agent-alias", Dir: t.TempDir(),
		Launch: loomharness.Launch{Root: link, Env: map[string]string{"CLAUDE_CONFIG_DIR": link}}})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(link); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(elsewhere, link); err != nil {
		t.Fatal(err)
	}
	if err := a.Session(ref).Prompt(ctx, loomharness.Input{Key: "k1", Text: "hello"}); err == nil {
		t.Fatal("a launch through a retargeted alias was not refused")
	}
	if got := launches(t, f.dumpPath); len(got) != 0 {
		t.Fatalf("claude launched %d times through the retargeted alias", len(got))
	}
}

// TestClaudeLaunchRefusesRetargetedRoot: the recorded root itself replaced by
// a symlink after Open is refused at launch, never followed.
func TestClaudeLaunchRefusesRetargetedRoot(t *testing.T) {
	a, _, f := newAdapter(t)
	ctx := context.Background()
	root := filepath.Join(t.TempDir(), "root")
	if err := os.Mkdir(root, 0o755); err != nil {
		t.Fatal(err)
	}
	ref, err := a.Open(ctx, loomharness.OpenSpec{Key: "agent-moved", Dir: t.TempDir(),
		Launch: loomharness.Launch{Root: root, Env: map[string]string{"CLAUDE_CONFIG_DIR": root}}})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(root, root+".moved"); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(t.TempDir(), root); err != nil {
		t.Fatal(err)
	}
	if err := a.Session(ref).Prompt(ctx, loomharness.Input{Key: "k1", Text: "hello"}); err == nil {
		t.Fatal("a launch through a recorded root replaced by a symlink was not refused")
	}
	if got := launches(t, f.dumpPath); len(got) != 0 {
		t.Fatalf("claude launched %d times through the retargeted root", len(got))
	}
}

func TestClaudePurgeFailsVisiblyAndRetries(t *testing.T) {
	root := t.TempDir()
	n := uuid.NewString()
	ctx := context.Background()
	for _, ref := range []loomharness.NativeRef{
		{Root: "relative/root", NativeID: n},
		{Root: root, NativeID: "../escape"},
		{Root: filepath.Join(root, "missing"), NativeID: n},
	} {
		if err := New(Config{}).Purge(ctx, []loomharness.NativeRef{ref}); err == nil {
			t.Fatalf("Purge(%+v) must fail", ref)
		}
	}
	owned := writeTranscript(t, root, "-p", n+".jsonl")
	dir := filepath.Dir(owned)
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o755) })
	if err := New(Config{}).Purge(ctx, []loomharness.NativeRef{{Root: root, NativeID: n}}); err == nil || !exists(owned) {
		t.Fatalf("a failed delete must be reported; err %v", err)
	}
	if err := os.Chmod(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := New(Config{}).Purge(ctx, []loomharness.NativeRef{{Root: root, NativeID: n}}); err != nil || exists(owned) {
		t.Fatalf("retry after restart = %v", err)
	}
}

// TestClaudeNoProjectPurgeDelete: no adapter code calls `claude project purge`.
func TestClaudeNoProjectPurgeDelete(t *testing.T) {
	files, _ := filepath.Glob("*.go")
	for _, name := range files {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		src, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(src), `"purge"`) || strings.Contains(string(src), `"project"`) {
			t.Fatalf("%s passes project/purge as a claude argument", name)
		}
	}
}

// TestClaudePurgeRefusesSymlinks: a session transcript reached through a
// symlinked project directory, a symlinked projects directory or a
// symlinked transcript is refused and left in place, never followed.
func TestClaudePurgeRefusesSymlinks(t *testing.T) {
	ctx := context.Background()
	n := uuid.NewString()
	// outside is an owned sentinel tree holding the session's transcript;
	// every case must leave it byte-for-byte as it was.
	outside := t.TempDir()
	victim := writeTranscript(t, outside, "-elsewhere", n+".jsonl")
	if err := os.WriteFile(victim, []byte(`{"sentinel":"`+n+`"}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	want := snapshot(t, outside)

	linkedProject := t.TempDir()
	if err := os.MkdirAll(filepath.Join(linkedProject, "projects"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Dir(victim), filepath.Join(linkedProject, "projects", "-wt")); err != nil {
		t.Fatal(err)
	}
	linkedProjects := t.TempDir()
	if err := os.Symlink(filepath.Join(outside, "projects"), filepath.Join(linkedProjects, "projects")); err != nil {
		t.Fatal(err)
	}
	linkedFile := t.TempDir()
	if err := os.MkdirAll(filepath.Join(linkedFile, "projects", "-wt"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(victim, filepath.Join(linkedFile, "projects", "-wt", n+".jsonl")); err != nil {
		t.Fatal(err)
	}
	// The recorded root itself, or a directory above it, is a symlink to
	// the outside root.
	linkedRoot := filepath.Join(t.TempDir(), "root")
	if err := os.Symlink(outside, linkedRoot); err != nil {
		t.Fatal(err)
	}
	linkedParent := filepath.Join(t.TempDir(), "parent")
	if err := os.Symlink(filepath.Dir(outside), linkedParent); err != nil {
		t.Fatal(err)
	}
	for name, root := range map[string]string{
		"project dir": linkedProject, "projects dir": linkedProjects, "transcript": linkedFile,
		"root": linkedRoot, "root parent": filepath.Join(linkedParent, filepath.Base(outside)),
	} {
		if err := New(Config{}).Purge(ctx, []loomharness.NativeRef{{Root: root, NativeID: n}}); err == nil {
			t.Errorf("symlinked %s: purge did not refuse", name)
		}
		if got := snapshot(t, outside); !maps.Equal(got, want) {
			t.Fatalf("symlinked %s: the sentinel tree outside the recorded root changed: %v -> %v", name, want, got)
		}
	}
}

// snapshot maps each path under root (relative, not followed) to its type
// and content.
func snapshot(t *testing.T, root string) map[string]string {
	t.Helper()
	out := map[string]string{}
	r, err := os.OpenRoot(root)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = r.Close() }()
	err = filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, p)
		out[rel] = d.Type().String()
		if d.Type().IsRegular() {
			b, err := r.ReadFile(rel)
			out[rel] += " " + string(b)
			return err
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// TestClaudeAssistantBlocksKeepTheirIndex: a whole-message assistant frame
// with several blocks completes each block under its own index.
func TestClaudeAssistantBlocksKeepTheirIndex(t *testing.T) {
	m := newMapper(loomharness.NativeRef{NativeID: "s"})
	got := m.frame([]byte(`{"type":"assistant","message":{"id":"msg_x","content":[` +
		`{"type":"thinking","thinking":"a"},{"type":"text","text":"b"},{"type":"text","text":"c"}]}}`))
	var ids []string
	for _, e := range got {
		if e.Type == loomharness.EventItemCompleted {
			ids = append(ids, e.ItemID)
		}
	}
	if want := []string{"msg_x/reasoning/0", "msg_x/text/1", "msg_x/text/2"}; !slices.Equal(ids, want) {
		t.Fatalf("item ids = %v, want %v", ids, want)
	}
}

// TestClaudeSessionsKeyedByRootAndID: the same key opened under a new
// profile root is a distinct session with its own ref; the old root's
// session is untouched.
func TestClaudeSessionsKeyedByRootAndID(t *testing.T) {
	a, _, f := newAdapter(t)
	ctx := context.Background()
	oldRef, oldSession := open(t, a, f, "agent-rooted")
	newRoot := t.TempDir()
	newRef, err := a.Open(ctx, loomharness.OpenSpec{Key: "agent-rooted", Dir: t.TempDir(),
		Launch: loomharness.Launch{Root: newRoot, Env: map[string]string{"CLAUDE_CONFIG_DIR": newRoot}}})
	if err != nil {
		t.Fatal(err)
	}
	if newRef.NativeID != oldRef.NativeID || newRef.Root != newRoot {
		t.Fatalf("new ref = %+v", newRef)
	}
	newSession := a.Session(newRef).(*Session)
	if newSession == oldSession.(*Session) || newSession.ref != newRef || newSession.m.ref != newRef ||
		oldSession.(*Session).ref != oldRef || newSession.spec.Launch.Root != newRoot {
		t.Fatal("the new root's session must be its own, with its own ref")
	}
}

// TestClaudeHealthStripsGitHubTokens: the version child gets no GitHub token.
func TestClaudeHealthStripsGitHubTokens(t *testing.T) {
	dump := filepath.Join(t.TempDir(), "version.env")
	_, cfg := newFixture(t, "2.1.285", append([]string{"LOOM_FAKE_CLAUDE_VERSION_DUMP=" + dump}, seededGitHubTokens...)...)
	h, err := New(cfg).Health(context.Background())
	if err != nil || !h.OK {
		t.Fatalf("health = %+v, %v", h, err)
	}
	raw, err := os.ReadFile(dump)
	if err != nil {
		t.Fatal(err)
	}
	env := strings.Split(string(raw), "\n")
	for _, k := range []string{"GH_TOKEN", "GITHUB_TOKEN", "GITHUB_TOKEN_FILE"} {
		if _, ok := lookup(env, k); ok {
			t.Errorf("%s reached claude --version", k)
		}
	}
}

// TestClaudeSelfStartedTurnHasNoInputKey: a turn Claude starts without a
// handed input carries no InputKey; a handed key binds only the next turn.
func TestClaudeSelfStartedTurnHasNoInputKey(t *testing.T) {
	m := newMapper(loomharness.NativeRef{NativeID: "s"})
	start := func() loomharness.Event {
		got := m.frame([]byte(`{"type":"stream_event","event":{"type":"message_start","message":{"id":"msg_a"}}}`))
		if len(got) == 0 || got[0].Type != loomharness.EventTurnStarted {
			t.Fatalf("no turn.started: %v", types(got))
		}
		m.frame([]byte(`{"type":"result","subtype":"success"}`))
		return got[0]
	}
	if e := start(); e.InputKey != "" {
		t.Fatalf("self-started turn InputKey = %q", e.InputKey)
	}
	m.handed = "key-1"
	if e := start(); e.InputKey != "key-1" {
		t.Fatalf("handed turn InputKey = %q", e.InputKey)
	}
	if e := start(); e.InputKey != "" {
		t.Fatalf("the key bound a second turn: %q", e.InputKey)
	}
}

// TestClaudeUsageSumsTurnSteps: a turn's usage is the sum of its steps'
// stream usage (input and cache from message_start, output from
// message_delta), not the result's own counts, and the next turn starts at
// zero; a process exit emits the cut-off turn's partial usage as its own row.
func TestClaudeUsageSumsTurnSteps(t *testing.T) {
	m := newMapper(loomharness.NativeRef{NativeID: "s"})
	step := func(id string, in, read, write, out int) {
		m.frame([]byte(fmt.Sprintf(`{"type":"stream_event","event":{"type":"message_start","message":{"id":%q,"usage":{"input_tokens":%d,"cache_read_input_tokens":%d,"cache_creation_input_tokens":%d,"output_tokens":1}}}}`, id, in, read, write)))
		m.frame([]byte(fmt.Sprintf(`{"type":"stream_event","event":{"type":"message_delta","usage":{"output_tokens":%d}}}`, out)))
	}
	usage := func() loomharness.Event {
		for _, e := range m.frame([]byte(`{"type":"result","subtype":"success","usage":{"input_tokens":999,"output_tokens":999}}`)) {
			if e.Type == loomharness.EventUsage {
				return e
			}
		}
		t.Fatal("no usage")
		return loomharness.Event{}
	}
	step("msg_1", 10, 100, 5, 20)
	step("msg_2", 3, 200, 0, 7)
	u := usage()
	if want := (loomharness.Usage{InputTokens: 13, OutputTokens: 27, CacheReadTokens: 300, CacheWriteTokens: 5}); u.Usage != want || u.ItemID != u.TurnID+"/usage" {
		t.Fatalf("usage = %+v (item %q), want %+v", u.Usage, u.ItemID, want)
	}
	step("msg_3", 1, 0, 0, 2)
	if u2 := usage(); u2.Usage != (loomharness.Usage{InputTokens: 1, OutputTokens: 2}) || u2.ItemID == u.ItemID {
		t.Fatalf("second turn usage = %+v (item %q)", u2.Usage, u2.ItemID)
	}
	// A process that exits mid-turn: the exit emits the cut-off turn's own
	// usage row (no cost), and the next turn's row is only its own steps.
	step("msg_4", 10, 0, 0, 2)
	ex := m.exited()
	if len(ex) != 2 || ex[0].Type != loomharness.EventUsage || ex[0].Usage != (loomharness.Usage{InputTokens: 10, OutputTokens: 2}) ||
		ex[0].ItemID != ex[0].TurnID+"/usage" || ex[0].TurnID == "" || ex[1].Type != loomharness.EventFeedGap {
		t.Fatalf("exit events = %+v", ex)
	}
	if again := m.exited(); len(again) != 1 { // nothing left to count
		t.Fatalf("a second exit = %+v", again)
	}
	step("msg_5", 5, 0, 0, 3)
	u3 := usage()
	if u3.Usage != (loomharness.Usage{InputTokens: 5, OutputTokens: 3}) || u3.ItemID == ex[0].ItemID {
		t.Fatalf("usage after an exit = %+v (item %q), want 5/3 under a new id", u3.Usage, u3.ItemID)
	}
	if in, out := ex[0].Usage.InputTokens+u3.Usage.InputTokens, ex[0].Usage.OutputTokens+u3.Usage.OutputTokens; in != 15 || out != 5 {
		t.Fatalf("totals %d/%d, want 15/5", in, out)
	}
}

// TestClaudeCostIsSessionTotal: a turn's usage carries the result's
// total_cost_usd as the session's running total (CostTotalUSD) and no CostUSD
// of its own: loomagent turns the total into the turn's cost.
func TestClaudeCostIsSessionTotal(t *testing.T) {
	m := newMapper(loomharness.NativeRef{NativeID: "s"})
	for _, total := range []float64{0.25, 0.75} {
		for _, e := range m.frame([]byte(fmt.Sprintf(`{"type":"result","subtype":"success","total_cost_usd":%v}`, total))) {
			if e.Type == loomharness.EventUsage && (e.Usage.CostTotalUSD != total || e.Usage.CostUSD != 0) {
				t.Fatalf("usage = %+v, want total %v and no own cost", e.Usage, total)
			}
		}
	}
}

// TestClaudeCloseKeepsCutOffTurnUsage: a Close during a turn emits that
// turn's partial usage once; a Close between turns emits no usage.
func TestClaudeCloseKeepsCutOffTurnUsage(t *testing.T) {
	a, feed, f := newAdapter(t)
	_, s := open(t, a, f, "agent-close-usage")
	ctx := context.Background()
	prompt(t, s, "hang") // one step started: 4 input tokens, no result
	started := until(t, feed, loomharness.EventTurnStarted)
	if err := s.Close(ctx); err != nil {
		t.Fatal(err)
	}
	u := until(t, feed, loomharness.EventUsage)
	if got := u[len(u)-1]; got.Usage != (loomharness.Usage{InputTokens: 4}) || got.TurnID != started[len(started)-1].TurnID {
		t.Fatalf("usage at Close = %+v", got)
	}
	prompt(t, s, "go")
	until(t, feed, loomharness.EventTurnCompleted)
	if err := s.Close(ctx); err != nil { // between turns: nothing to count
		t.Fatal(err)
	}
	prompt(t, s, "go")
	if n := len(kindsOf(until(t, feed, loomharness.EventTurnCompleted), loomharness.EventUsage)); n != 1 {
		t.Fatalf("%d usage events in the turn after an idle Close, want only the turn's own", n)
	}
}

func kindsOf(es []loomharness.Event, typ loomharness.EventType) []loomharness.Event {
	var out []loomharness.Event
	for _, e := range es {
		if e.Type == typ {
			out = append(out, e)
		}
	}
	return out
}

// TestClaudeToolCallCarriesNameInputOutput: a streamed tool_use starts with
// its name; its whole block gives the input; its tool_result completes it
// with the result text and is_error.
func TestClaudeToolCallCarriesNameInputOutput(t *testing.T) {
	m := newMapper(loomharness.NativeRef{NativeID: "s"})
	var got []loomharness.Event
	for _, f := range []string{
		`{"type":"stream_event","event":{"type":"message_start","message":{"id":"msg_t"}}}`,
		`{"type":"stream_event","event":{"type":"content_block_start","index":0,"content_block":{"type":"tool_use","id":"toolu_1","name":"Bash","input":{}}}}`,
		`{"type":"assistant","message":{"id":"msg_t","content":[{"type":"tool_use","id":"toolu_1","name":"Bash","input":{"command":"ls"}}]}}`,
		`{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"toolu_1","content":[{"type":"text","text":"a.go"}]}]}}`,
		`{"type":"assistant","message":{"id":"msg_u","content":[{"type":"tool_use","id":"toolu_2","name":"Read","input":{"file_path":"x"}}]}}`,
		`{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"toolu_2","content":"no such file","is_error":true}]}}`,
	} {
		for _, e := range m.frame([]byte(f)) {
			if e.ItemKind == "tool" {
				got = append(got, e)
			}
		}
	}
	want := []struct {
		typ  loomharness.EventType
		tool loomharness.Tool
	}{
		{loomharness.EventItemStarted, loomharness.Tool{Name: "Bash"}},
		{loomharness.EventItemCompleted, loomharness.Tool{Name: "Bash", Input: `{"command":"ls"}`, Output: "a.go"}},
		{loomharness.EventItemStarted, loomharness.Tool{Name: "Read", Input: `{"file_path":"x"}`}},
		{loomharness.EventItemCompleted, loomharness.Tool{Name: "Read", Input: `{"file_path":"x"}`, Output: "no such file", Failed: true}},
	}
	if len(got) != len(want) {
		t.Fatalf("got %d tool events, want %d: %+v", len(got), len(want), got)
	}
	for i, w := range want {
		if got[i].Type != w.typ || got[i].Tool == nil || *got[i].Tool != w.tool {
			t.Errorf("event %d: %s %+v, want %s %+v", i, got[i].Type, got[i].Tool, w.typ, w.tool)
		}
	}
}

// TestClaudeModelsCatalog: the aliases carry the --effort option (high by
// default) except haiku, with context limits and input types; none is the
// default, which the CLI picks per account.
func TestClaudeModelsCatalog(t *testing.T) {
	a, _, _ := newAdapter(t)
	ms, err := a.Models(context.Background())
	if err != nil || len(ms) != 3 {
		t.Fatalf("Models = %+v, %v", ms, err)
	}
	for _, m := range ms {
		if m.Default || m.Provider != "anthropic" || !slices.Equal(m.Input, []string{"text", "image", "pdf"}) || m.ContextLimit == 0 {
			t.Fatalf("model = %+v", m)
		}
	}
	var choices []string
	for _, c := range ms[0].Options[0].Choices {
		choices = append(choices, c.ID)
	}
	if ms[0].ID != "opus" || ms[0].Options[0].ID != loomharness.OptionEffort || ms[0].Options[0].Current != "high" ||
		!slices.Equal(choices, []string{"low", "medium", "high", "xhigh", "max"}) || len(ms[2].Options) != 0 {
		t.Fatalf("catalog = %+v", ms)
	}
}

// TestClaudeFailedResultCarriesError: a non-success result ends the turn
// failed with its first user-facing error (never an [ede_diagnostic] entry),
// or its subtype when it lists none; a success carries no error.
func TestClaudeFailedResultCarriesError(t *testing.T) {
	for raw, want := range map[string][2]string{
		`{"type":"result","subtype":"error_during_execution","errors":["[ede_diagnostic] x","API Error: 401 invalid key"]}`: {"failed", "API Error: 401 invalid key"},
		`{"type":"result","subtype":"error_max_turns"}`:                                                                     {"failed", "error_max_turns"},
		`{"type":"result","subtype":"success","errors":["ignored"]}`:                                                        {"completed", ""},
	} {
		m := newMapper(loomharness.NativeRef{NativeID: "s"})
		m.frame([]byte(`{"type":"stream_event","event":{"type":"message_start","message":{"id":"msg_a"}}}`))
		var done loomharness.Event
		for _, e := range m.frame([]byte(raw)) {
			if e.Type == loomharness.EventTurnCompleted {
				done = e
			}
		}
		if done.StopReason != want[0] || done.Error != want[1] {
			t.Errorf("%s -> %q %q; want %q %q", raw, done.StopReason, done.Error, want[0], want[1])
		}
	}
}
