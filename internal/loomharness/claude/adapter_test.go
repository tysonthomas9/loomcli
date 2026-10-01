package claude

import (
	"context"
	"errors"
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
	if err := s.SetModel(ctx, "sonnet"); err != nil {
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
	inherited, err := a.Open(ctx, loomharness.OpenSpec{Key: "agent-inherited", Dir: t.TempDir()})
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
	if err := s.Reply(ctx, "ask", loomharness.Reply{Allow: true}); !errors.Is(err, loomharness.ErrUnavailable) {
		t.Fatalf("Reply = %v", err)
	}
	if _, err := s.HasInput(ctx, "k"); !errors.Is(err, loomharness.ErrUnavailable) {
		t.Fatalf("HasInput = %v", err)
	}
	if _, err := s.Messages(ctx, "", 10); !errors.Is(err, loomharness.ErrUnavailable) {
		t.Fatalf("Messages = %v", err)
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
	outside := t.TempDir()
	victim := writeTranscript(t, outside, "-elsewhere", n+".jsonl")

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
	for name, root := range map[string]string{"project dir": linkedProject, "projects dir": linkedProjects, "transcript": linkedFile} {
		if err := New(Config{}).Purge(ctx, []loomharness.NativeRef{{Root: root, NativeID: n}}); err == nil {
			t.Errorf("symlinked %s: purge did not refuse", name)
		}
		if !exists(victim) {
			t.Fatalf("symlinked %s: the transcript outside the recorded root was deleted", name)
		}
	}
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
	newRef, err := a.Open(ctx, loomharness.OpenSpec{Key: "agent-rooted", Dir: t.TempDir(), Launch: loomharness.Launch{Root: newRoot}})
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
