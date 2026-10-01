package codex

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/agentprofile"
	"github.com/tysonthomas9/loomcli/internal/loomharness"
	"github.com/tysonthomas9/loomcli/internal/sessions"
)

// fakeThread is one thread in the fake codex's store, $CODEX_HOME/threads.json.
type fakeThread struct {
	Cwd    string `json:"cwd"`
	Name   string `json:"name"`
	Active bool   `json:"active"`
	Listed bool   `json:"listed"` // materialized: codex lists a thread only after its first user message
}

type fakeStore struct {
	Next    int                   `json:"next"`
	Threads map[string]fakeThread `json:"threads"`
}

func loadStore(home string) fakeStore {
	s := fakeStore{Threads: map[string]fakeThread{}}
	if b, err := os.ReadFile(filepath.Join(home, "threads.json")); err == nil {
		_ = json.Unmarshal(b, &s)
	}
	return s
}

func saveStore(home string, s fakeStore) {
	b, _ := json.Marshal(s)
	_ = os.MkdirAll(home, 0o700)
	_ = os.WriteFile(filepath.Join(home, "threads.json"), b, 0o600)
}

// fakeThreads serves the thread methods from the store, with codex 0.157.1's
// error messages. thread/list pages one listed thread at a time; thread/turns/list
// returns $CODEX_HOME/turns-<id>.json, and a thread without one is not
// materialized. Deleting fails while $CODEX_HOME/fail-delete exists, and
// naming while $CODEX_HOME/fail-name exists; with $CODEX_HOME/slow-delete a
// delete writes $CODEX_HOME/deleting and takes 3 s.
func fakeThreads(home, method string, raw json.RawMessage) (any, error) {
	var p struct {
		ThreadID, Cwd, Name, SearchTerm, Cursor string
	}
	_ = json.Unmarshal(raw, &p)
	s := loadStore(home)
	t, ok := s.Threads[p.ThreadID]
	switch method {
	case "thread/start":
		s.Next++
		id := "t-" + strconv.Itoa(s.Next)
		s.Threads[id] = fakeThread{Cwd: p.Cwd}
		saveStore(home, s)
		return map[string]any{"thread": map[string]any{"id": id}}, nil
	case "thread/list":
		var ids []string
		for id, t := range s.Threads {
			if t.Listed && t.Cwd == p.Cwd && strings.Contains(t.Name, p.SearchTerm) {
				ids = append(ids, id)
			}
		}
		sort.Strings(ids)
		i, _ := strconv.Atoi(p.Cursor)
		page := map[string]any{"data": []any{}}
		if i < len(ids) {
			page["data"] = []any{map[string]any{"id": ids[i], "name": s.Threads[ids[i]].Name}}
		}
		if i+1 < len(ids) {
			page["nextCursor"] = strconv.Itoa(i + 1)
		}
		return page, nil
	case "thread/delete":
		if _, err := os.Stat(filepath.Join(home, "fail-delete")); err == nil {
			return nil, errors.New("disk full")
		}
		if _, err := os.Stat(filepath.Join(home, "slow-delete")); err == nil {
			_ = os.WriteFile(filepath.Join(home, "deleting"), nil, 0o600)
			time.Sleep(3 * time.Second)
		}
		if !ok {
			return nil, fmt.Errorf("no rollout found for thread id %s", p.ThreadID)
		}
		delete(s.Threads, p.ThreadID)
		saveStore(home, s)
		return map[string]any{}, nil
	}
	if !ok {
		return nil, fmt.Errorf("thread not loaded: %s", p.ThreadID)
	}
	switch method {
	case "thread/name/set":
		if _, err := os.Stat(filepath.Join(home, "fail-name")); err == nil {
			return nil, errors.New("name store unavailable")
		}
		t.Name = p.Name
		s.Threads[p.ThreadID] = t
		saveStore(home, s)
		return map[string]any{}, nil
	case "thread/read":
		status := "idle"
		if t.Active {
			status = "active"
		}
		return map[string]any{"thread": map[string]any{"id": p.ThreadID, "status": map[string]string{"type": status}}}, nil
	case "thread/turns/list":
		b, err := os.ReadFile(filepath.Join(home, "turns-"+p.ThreadID+".json"))
		if err != nil && t.Name != "" {
			return nil, fmt.Errorf("invalid paginated history lineage for %s: missing source rollout", p.ThreadID)
		}
		if err != nil {
			return nil, fmt.Errorf("thread %s is not materialized yet; thread/turns/list is unavailable before first user message", p.ThreadID)
		}
		return json.RawMessage(b), nil
	}
	return nil, fmt.Errorf("unknown method %s", method)
}

func newAdapter(t *testing.T, f fixture) *Adapter {
	t.Helper()
	a := NewAdapter(Config{Bin: os.Args[0], Env: f.env})
	t.Cleanup(a.Stop)
	return a
}

func spec(key, dir, root string) loomharness.OpenSpec {
	return loomharness.OpenSpec{Key: key, Dir: dir, Launch: loomharness.Launch{Root: root}}
}

// TestCodexReturnsActualNativeRef: a fresh Open returns the id codex gave
// and the canonical root the thread lives under and names it for its key; a
// repeat returns it again before codex would list it, and after a restart
// lost the record the named thread is adopted.
func TestCodexReturnsActualNativeRef(t *testing.T) {
	f := newFixture(t, "codex-cli 0.157.1")
	a, ctx := newAdapter(t, f), context.Background()
	fresh, err := a.Open(ctx, spec("k1", "/work", f.profile))
	if err != nil {
		t.Fatal(err)
	}
	want := loomharness.NativeRef{Root: a.Root(f.profile), NativeID: "t-1"}
	if fresh != want {
		t.Fatalf("fresh ref %+v, want %+v", fresh, want)
	}
	if got := loadStore(want.Root).Threads["t-1"]; got.Name != "loom:k1" || got.Cwd != "/work" {
		t.Fatalf("stored thread %+v", got)
	}
	again, err := a.Open(ctx, spec("k1", "/work", f.profile))
	if err != nil || again != want {
		t.Fatalf("adopted ref %+v %v, want %+v", again, err, want)
	}
	if n := len(loadStore(want.Root).Threads); n != 1 {
		t.Fatalf("%d threads after a repeat Open, want 1", n)
	}

	st := loadStore(want.Root) // the first user message materializes it
	th := st.Threads["t-1"]
	th.Listed = true
	st.Threads["t-1"] = th
	saveStore(want.Root, st)
	adopted, err := newAdapter(t, f).Open(ctx, spec("k1", "/work", f.profile))
	if err != nil || adopted != want {
		t.Fatalf("after a restart: %+v %v, want %+v", adopted, err, want)
	}
}

// TestCodexOrphanPick: Open adopts only an exact name match in the same
// directory, and refuses to pick between several.
func TestCodexOrphanPick(t *testing.T) {
	f := newFixture(t, "codex-cli 0.157.1")
	a, ctx := newAdapter(t, f), context.Background()
	root := a.Root("")
	saveStore(root, fakeStore{Next: 10, Threads: map[string]fakeThread{
		"t-a": {Cwd: "/work", Name: "loom:k10", Listed: true}, // a longer key that contains loom:k1
		"t-b": {Cwd: "/other", Name: "loom:k1", Listed: true}, // another directory
		"t-c": {Cwd: "/work", Name: "loom:k1", Listed: true},
		"t-d": {Cwd: "/work", Name: "loom:k2", Listed: true},
		"t-e": {Cwd: "/work", Name: "loom:k2", Listed: true},
	}})
	ref, err := a.Open(ctx, spec("k1", "/work", ""))
	if err != nil || ref != (loomharness.NativeRef{Root: root, NativeID: "t-c"}) {
		t.Fatalf("one match: %+v %v, want t-c", ref, err)
	}
	if _, err := a.Open(ctx, spec("k2", "/work", "")); err == nil || !strings.Contains(err.Error(), "2 threads") {
		t.Fatalf("two matches: %v, want a refusal", err)
	}
	ref, err = a.Open(ctx, spec("k3", "/work", ""))
	if err != nil || ref.NativeID != "t-11" {
		t.Fatalf("no match: %+v %v, want a new thread", ref, err)
	}
}

// TestCodexOpenRefusesRules: rules cannot be installed until 4.2, so Open
// fails closed and creates nothing.
func TestCodexOpenRefusesRules(t *testing.T) {
	f := newFixture(t, "codex-cli 0.157.1")
	a := newAdapter(t, f)
	s := spec("k1", "/work", "")
	s.Rules = []loomharness.PermissionRule{{Action: "bash", Resource: "*", Effect: "deny"}}
	if _, err := a.Open(context.Background(), s); err == nil {
		t.Fatal("Open with rules succeeded")
	}
	if len(f.spawns(t)) != 0 {
		t.Fatal("Open with rules started an app-server")
	}
}

// TestCodexPurgeUsesRecordedRoot: Purge deletes each ref on its own root's
// app-server only (the same id under the other root survives), a thread
// already gone is fine, a ref without a root is refused, and any other
// failure is returned so the purge can be retried.
func TestCodexPurgeUsesRecordedRoot(t *testing.T) {
	f := newFixture(t, "codex-cli 0.157.1")
	a, ctx := newAdapter(t, f), context.Background()
	inherited, profile := a.Root(""), a.Root(f.profile)
	for _, root := range []string{inherited, profile} {
		saveStore(root, fakeStore{Threads: map[string]fakeThread{"t-1": {}, "t-2": {}}})
	}
	if err := a.Purge(ctx, []loomharness.NativeRef{{Root: profile, NativeID: "t-1"}}); err != nil {
		t.Fatal(err)
	}
	if _, ok := loadStore(profile).Threads["t-1"]; ok {
		t.Fatal("the profile thread survived Purge")
	}
	if len(loadStore(inherited).Threads) != 2 {
		t.Fatal("Purge touched the inherited root")
	}
	if err := a.Purge(ctx, []loomharness.NativeRef{{Root: profile, NativeID: "t-1"}}); err != nil {
		t.Fatalf("purging a deleted thread: %v", err)
	}
	if err := a.Purge(ctx, []loomharness.NativeRef{{NativeID: "t-2"}}); err == nil {
		t.Fatal("Purge resolved a root for a ref without one")
	}
	if len(loadStore(inherited).Threads) != 2 || len(loadStore(profile).Threads) != 1 {
		t.Fatal("a refused Purge deleted something")
	}
}

// TestCodexPurgeDeleteFailure: a delete that fails is an error that leaves
// the recorded refs in place, and the same Purge after a serve restart
// deletes them.
func TestCodexPurgeDeleteFailure(t *testing.T) {
	f := newFixture(t, "codex-cli 0.157.1")
	a, ctx := newAdapter(t, f), context.Background()
	root := a.Root("")
	saveStore(root, fakeStore{Threads: map[string]fakeThread{"t-1": {}, "t-2": {}}})
	if err := os.WriteFile(filepath.Join(root, "fail-delete"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	refs := []loomharness.NativeRef{{Root: root, NativeID: "t-1"}, {Root: root, NativeID: "t-2"}}
	if err := a.Purge(ctx, refs); err == nil || !strings.Contains(err.Error(), "disk full") {
		t.Fatalf("got %v, want the delete failure", err)
	}
	if len(loadStore(root).Threads) != 2 {
		t.Fatal("Purge went on after a failure")
	}
	a.Stop()
	if err := os.Remove(filepath.Join(root, "fail-delete")); err != nil {
		t.Fatal(err)
	}
	if err := newAdapter(t, f).Purge(ctx, refs); err != nil {
		t.Fatalf("retry after restart: %v", err)
	}
	if n := len(loadStore(root).Threads); n != 0 {
		t.Fatalf("%d threads left after the retry", n)
	}
}

// writeCodexProfile provisions a verified codex profile for agent.
func writeCodexProfile(t *testing.T, project, agent string) string {
	t.Helper()
	dir := filepath.Join(agentprofile.Dir(project, agent), "codex")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte("model = \"m\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	sum, err := agentprofile.Fingerprint(dir, []string{"config.toml"})
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(agentprofile.Manifest{Files: []string{"config.toml"}, Fingerprint: sum, HarnessVersion: "codex-cli 0.157.1"})
	if err := os.WriteFile(filepath.Join(dir, agentprofile.ManifestName), b, 0o600); err != nil {
		t.Fatal(err)
	}
	return dir
}

// TestCodexAgentProfileEnv: an agent with a verified profile launches on
// that root with CODEX_HOME set to it, one without runs on the user's
// inherited root, an invalid profile is refused (never the user's root), and
// a renamed agent's original key keeps selecting its profile.
func TestCodexAgentProfileEnv(t *testing.T) {
	prev := agentprofile.ProbeVersionFunc
	agentprofile.ProbeVersionFunc = func(string) string { return "codex-cli 0.157.1" }
	agentprofile.ResetVersionCache()
	t.Cleanup(func() { agentprofile.ProbeVersionFunc = prev; agentprofile.ResetVersionCache() })

	f := newFixture(t, "codex-cli 0.157.1")
	a, ctx := newAdapter(t, f), context.Background()
	project := t.TempDir()
	dir := writeCodexProfile(t, project, "orig")
	root := a.Root(dir)

	l, err := a.LaunchFor(project, "orig") // the original key, after a rename too
	if err != nil || l.Root != root || l.Env["CODEX_HOME"] != dir {
		t.Fatalf("present profile: %+v %v", l, err)
	}
	if err := os.Mkdir(filepath.Join(dir, "sessions"), 0o700); err != nil {
		t.Fatal(err)
	}
	if got := a.Root(filepath.Dir(sessions.CodexSessionsRootFor(project, "orig"))); got != l.Root {
		t.Fatalf("transcript discovery resolves %s, launch %s", got, l.Root)
	}
	ref, err := a.Open(ctx, loomharness.OpenSpec{Key: "renamed", Dir: "/work", Launch: l})
	if err != nil || ref.Root != root {
		t.Fatalf("open on the profile: %+v %v", ref, err)
	}
	if _, ok := loadStore(root).Threads[ref.NativeID]; !ok {
		t.Fatal("the thread is not under the profile root")
	}

	l, err = a.LaunchFor(project, "absent")
	if err != nil || l.Root != a.Root("") || l.Env != nil {
		t.Fatalf("absent profile: %+v %v, want the inherited root", l, err)
	}

	if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte("tampered"), 0o600); err != nil {
		t.Fatal(err)
	}
	if l, err := a.LaunchFor(project, "orig"); !errors.Is(err, agentprofile.ErrFingerprintMismatch) || l.Root != "" {
		t.Fatalf("invalid profile: %+v %v, want ErrFingerprintMismatch", l, err)
	}
}

// TestCodexProfiledAndInheritedConcurrentAgents: agents on the inherited
// root and on a profile root open at once; each root gets one app-server
// with its own CODEX_HOME, and each thread lives under its agent's root.
func TestCodexProfiledAndInheritedConcurrentAgents(t *testing.T) {
	f := newFixture(t, "codex-cli 0.157.1")
	a, ctx := newAdapter(t, f), context.Background()
	roots := []string{"", f.profile}
	var wg sync.WaitGroup
	refs := make([]loomharness.NativeRef, 8)
	errs := make([]error, 8)
	for i := range refs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			refs[i], errs[i] = a.Open(ctx, spec("k"+strconv.Itoa(i), "/work", roots[i%2]))
		}()
	}
	wg.Wait()
	for i, ref := range refs {
		if errs[i] != nil || ref.Root != a.Root(roots[i%2]) {
			t.Fatalf("agent %d: %+v %v", i, ref, errs[i])
		}
		if loadStore(ref.Root).Threads[ref.NativeID].Name != "loom:k"+strconv.Itoa(i) {
			t.Fatalf("agent %d's thread is not under its root", i)
		}
	}
	var homes []string
	for i, field := range f.spawns(t) {
		if i%2 == 1 { // "<pid> <CODEX_HOME>" per spawn
			homes = append(homes, field)
		}
	}
	slices.Sort(homes)
	if want := []string{f.inherited, a.Root(f.profile)}; !slices.Equal(homes, slices.Sorted(slices.Values(want))) {
		t.Fatalf("app-servers by CODEX_HOME %v, want one each: %v", homes, want)
	}
}

// recorded replays the frames real codex 0.157.1 sent for a turn that ran
// one approved command (testdata/turn.jsonl, recorded with a fake model).
func recorded(t *testing.T) []Message {
	t.Helper()
	b, err := os.ReadFile("testdata/turn.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	var out []Message
	sc := bufio.NewScanner(strings.NewReader(string(b)))
	sc.Buffer(nil, 1<<20)
	for sc.Scan() {
		var m inbound
		if err := json.Unmarshal(sc.Bytes(), &m); err != nil {
			t.Fatal(err)
		}
		out = append(out, Message{ID: m.ID, Method: m.Method, Params: m.Params, ThreadID: threadOf(m.Params)})
	}
	return out
}

func next(t *testing.T, f loomharness.Feed) loomharness.Event {
	t.Helper()
	select {
	case e := <-f.Events():
		return e
	case <-time.After(5 * time.Second):
		t.Fatal("no event")
	}
	return loomharness.Event{}
}

type brief struct {
	Type                          loomharness.EventType
	Thread, Turn, Item, Kind, Key string
	Ask, Text, Stop               string
}

func briefOf(e loomharness.Event) brief {
	return brief{e.Type, e.Session.NativeID, e.TurnID, e.ItemID, e.ItemKind, e.InputKey, e.AskID, e.Text, e.StopReason}
}

const (
	thread = "01a0f8b4-e922-71d0-af76-938cf303608d"
	turn   = "01a0f8b4-ebac-7ba2-88a5-2a7890138f03"
	userID = "01a0f8b4-ebe0-71b1-9eea-41ab690a42b6"
)

// TestCodexRecordedFrames maps a real turn's frames to the §5.2 events in
// order, with codex's own ids, and a later gap reports the root and loses
// the asks still open on it.
func TestCodexRecordedFrames(t *testing.T) {
	a := NewAdapter(Config{})
	feed, err := a.Feed(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = feed.Close() }()
	for _, m := range recorded(t) {
		a.receive("/root", m)
	}
	want := []brief{
		{Type: loomharness.EventTurnStarted, Thread: thread, Turn: turn, Key: "key-4"},
		{Type: loomharness.EventMessageDelivered, Thread: thread, Turn: turn, Item: userID, Kind: "message", Key: "key-4", Text: "run it"},
		{Type: loomharness.EventItemStarted, Thread: thread, Turn: turn, Item: "c1exec_command", Kind: "tool"},
		{Type: loomharness.EventAskOpened, Thread: thread, Turn: turn, Item: "c1exec_command", Ask: "0"},
		{Type: loomharness.EventAskResolved, Thread: thread, Ask: "0"},
		{Type: loomharness.EventItemCompleted, Thread: thread, Turn: turn, Item: "c1exec_command", Kind: "tool"},
		{Type: loomharness.EventUsage, Thread: thread, Turn: turn},
		{Type: loomharness.EventItemStarted, Thread: thread, Turn: turn, Item: "msg1", Kind: "message"},
		{Type: loomharness.EventDelta, Thread: thread, Turn: turn, Item: "msg1", Kind: "message", Text: "finished"},
		{Type: loomharness.EventItemCompleted, Thread: thread, Turn: turn, Item: "msg1", Kind: "message", Text: "finished"},
		{Type: loomharness.EventUsage, Thread: thread, Turn: turn},
		{Type: loomharness.EventTurnCompleted, Thread: thread, Turn: turn, Stop: "completed"},
	}
	for i, w := range want {
		e := next(t, feed)
		if got := briefOf(e); got != w || e.Session.Root != "/root" || e.Time.IsZero() {
			t.Fatalf("event %d: %+v (root %q), want %+v", i, got, e.Session.Root, w)
		}
	}

	// A turn with no user item keeps an empty key; the start a gap cut off
	// is dropped.
	a.receive("/root", Message{Method: "turn/started", ThreadID: "t-2", Params: json.RawMessage(`{"threadId":"t-2","turn":{"id":"u-1"}}`)})
	a.receive("/root", Message{Method: "turn/completed", ThreadID: "t-2", Params: json.RawMessage(`{"threadId":"t-2","turn":{"id":"u-1","status":"failed"}}`)})
	a.receive("/root", Message{Method: "turn/started", ThreadID: "t-2", Params: json.RawMessage(`{"threadId":"t-2","turn":{"id":"u-2"}}`)})
	for _, w := range []brief{
		{Type: loomharness.EventTurnStarted, Thread: "t-2", Turn: "u-1"},
		{Type: loomharness.EventTurnCompleted, Thread: "t-2", Turn: "u-1", Stop: "failed"},
	} {
		if got := briefOf(next(t, feed)); got != w {
			t.Fatalf("got %+v, want %+v", got, w)
		}
	}

	// The approval request again, never resolved this time.
	i := slices.IndexFunc(recorded(t), func(m Message) bool { return m.ID != nil })
	a.receive("/root", recorded(t)[i])
	a.receive("/other", Message{Gap: true})
	a.receive("/root", Message{Gap: true})
	for _, w := range []loomharness.Event{
		{Type: loomharness.EventAskOpened, Session: loomharness.NativeRef{Root: "/root", NativeID: thread}, AskID: "0"},
		{Type: loomharness.EventFeedGap, Session: loomharness.NativeRef{Root: "/other"}},
		{Type: loomharness.EventFeedGap, Session: loomharness.NativeRef{Root: "/root"}},
		{Type: loomharness.EventAskLost, Session: loomharness.NativeRef{Root: "/root", NativeID: thread}, AskID: "0"},
	} {
		if e := next(t, feed); e.Type != w.Type || e.Session != w.Session || e.AskID != w.AskID {
			t.Fatalf("got %+v, want %+v", e, w)
		}
	}
}

// TestCodexMessagesMatchLiveIDs: the catch-up read of the same turn (codex's
// stored history, recorded) gives the live feed's ItemIDs and input key, and
// a thread with no turns yet has none.
func TestCodexMessagesMatchLiveIDs(t *testing.T) {
	f := newFixture(t, "codex-cli 0.157.1")
	a, ctx := newAdapter(t, f), context.Background()
	root := a.Root("")
	saveStore(root, fakeStore{Threads: map[string]fakeThread{thread: {}, "t-new": {}, "t-named": {Name: "loom:k"}}})
	b, err := os.ReadFile("testdata/turns.json")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "turns-"+thread+".json"), b, 0o600); err != nil {
		t.Fatal(err)
	}
	page, err := a.Session(loomharness.NativeRef{Root: root, NativeID: thread}).Messages(ctx, "", 50)
	if err != nil {
		t.Fatal(err)
	}
	var got []brief
	for _, e := range page.Events {
		got = append(got, briefOf(e))
	}
	want := []brief{
		{Type: loomharness.EventMessageDelivered, Thread: thread, Turn: turn, Item: userID, Kind: "message", Key: "key-4", Text: "run it"},
		{Type: loomharness.EventItemCompleted, Thread: thread, Turn: turn, Item: "c1exec_command", Kind: "tool"},
		{Type: loomharness.EventItemCompleted, Thread: thread, Turn: turn, Item: "msg1", Kind: "message", Text: "finished"},
		{Type: loomharness.EventTurnCompleted, Thread: thread, Turn: turn, Stop: "completed"},
	}
	if !slices.Equal(got, want) || page.Next != "" {
		t.Fatalf("got %+v next %q\nwant %+v", got, page.Next, want)
	}
	s := a.Session(loomharness.NativeRef{Root: root, NativeID: thread})
	if l, err := s.HasInput(ctx, "key-4"); err != nil || l != loomharness.LandedFound {
		t.Fatalf("HasInput key-4: %v %v", l, err)
	}
	if l, err := s.HasInput(ctx, "key-5"); err != nil || l != loomharness.LandedNotFound {
		t.Fatalf("HasInput key-5: %v %v", l, err)
	}
	fresh := a.Session(loomharness.NativeRef{Root: root, NativeID: "t-new"})
	if page, err := fresh.Messages(ctx, "", 50); err != nil || len(page.Events) != 0 {
		t.Fatalf("unmaterialized thread: %+v %v", page, err)
	}
	if l, err := fresh.HasInput(ctx, "key-4"); err != nil || l != loomharness.LandedNotFound {
		t.Fatalf("unmaterialized HasInput: %v %v", l, err)
	}
	named := a.Session(loomharness.NativeRef{Root: root, NativeID: "t-named"})
	if l, err := named.HasInput(ctx, "key-4"); err != nil || l != loomharness.LandedNotFound {
		t.Fatalf("unmaterialized named HasInput: %v %v", l, err)
	}
	gone := a.Session(loomharness.NativeRef{Root: root, NativeID: "t-gone"})
	if _, err := gone.Status(ctx); !errors.Is(err, loomharness.ErrSessionNotFound) {
		t.Fatalf("missing thread: %v, want ErrSessionNotFound", err)
	}
}

// TestCodexPromptBusy: codex would steer an active turn with a new input,
// so Prompt on a running thread is ErrBusy and sends nothing.
func TestCodexPromptBusy(t *testing.T) {
	f := newFixture(t, "codex-cli 0.157.1")
	a := newAdapter(t, f)
	root := a.Root("")
	saveStore(root, fakeStore{Threads: map[string]fakeThread{"t-1": {Active: true}}})
	err := a.Session(loomharness.NativeRef{Root: root, NativeID: "t-1"}).Prompt(context.Background(), loomharness.Input{Key: "k", Text: "hi"})
	if !errors.Is(err, loomharness.ErrBusy) {
		t.Fatalf("got %v, want ErrBusy", err)
	}
}

// TestCodexRefusesOtherServerRequests: a server request that is not an ask
// (here a dynamic tool call Loom never registered) is answered with an
// error, never left hanging.
func TestCodexRefusesOtherServerRequests(t *testing.T) {
	f := newFixture(t, "codex-cli 0.157.1")
	a, ctx := newAdapter(t, f), context.Background()
	conn, err := a.Conn(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	var answer struct {
		Error *struct{ Code int64 }
	}
	if err := conn.Call(ctx, "ask", map[string]string{"threadId": "t-1", "method": "item/tool/call"}, &answer); err != nil {
		t.Fatal(err)
	}
	if answer.Error == nil || answer.Error.Code != -32601 {
		t.Fatalf("answer %+v, want a -32601 refusal", answer)
	}
}

// TestCodexOpenLeavesNothingOnError: when the new thread cannot be named,
// Open deletes it and returns the zero ref with the error; when the delete
// fails too, Open returns the thread's real ref with both errors, and a
// later Purge of that ref removes it.
func TestCodexOpenLeavesNothingOnError(t *testing.T) {
	f := newFixture(t, "codex-cli 0.157.1")
	a, ctx := newAdapter(t, f), context.Background()
	root := a.Root("")
	saveStore(root, fakeStore{Threads: map[string]fakeThread{}})
	if err := os.WriteFile(filepath.Join(root, "fail-name"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	ref, err := a.Open(ctx, spec("k1", "/work", ""))
	if err == nil || !strings.Contains(err.Error(), "name store unavailable") || ref != (loomharness.NativeRef{}) {
		t.Fatalf("got %+v, %v; want the zero ref and the naming error", ref, err)
	}
	if n := len(loadStore(root).Threads); n != 0 {
		t.Fatalf("%d threads left behind", n)
	}

	if err := os.WriteFile(filepath.Join(root, "fail-delete"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	ref, err = a.Open(ctx, spec("k1", "/work", ""))
	if err == nil || !strings.Contains(err.Error(), "name store unavailable") || !strings.Contains(err.Error(), "disk full") {
		t.Fatalf("got %v; want both errors", err)
	}
	if _, ok := loadStore(root).Threads[ref.NativeID]; !ok || ref.Root != root {
		t.Fatalf("got ref %+v; want the real ref of the thread left behind", ref)
	}

	_ = os.Remove(filepath.Join(root, "fail-name"))
	_ = os.Remove(filepath.Join(root, "fail-delete"))
	if err := a.Purge(ctx, []loomharness.NativeRef{ref}); err != nil {
		t.Fatal(err)
	}
	if n := len(loadStore(root).Threads); n != 0 {
		t.Fatalf("%d threads left after purging the returned ref", n)
	}
	if ref, err = a.Open(ctx, spec("k1", "/work", "")); err != nil || ref.NativeID == "" {
		t.Fatalf("Open after the failures: %+v %v", ref, err)
	}
}

// TestCodexSlowCleanupDoesNotBlockOpen: while Open deletes a thread it could
// not name, another Open (another key, on another root's server) is not held
// up by it.
func TestCodexSlowCleanupDoesNotBlockOpen(t *testing.T) {
	f := newFixture(t, "codex-cli 0.157.1")
	a, ctx := newAdapter(t, f), context.Background()
	root := a.Root("")
	saveStore(root, fakeStore{Threads: map[string]fakeThread{}})
	for _, name := range []string{"fail-name", "slow-delete"} {
		if err := os.WriteFile(filepath.Join(root, name), nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	type result struct {
		ref loomharness.NativeRef
		err error
	}
	failing := make(chan result, 1)
	go func() {
		ref, err := a.Open(ctx, spec("k1", "/work", ""))
		failing <- result{ref, err}
	}()
	for deadline := time.Now().Add(10 * time.Second); ; time.Sleep(20 * time.Millisecond) {
		if _, err := os.Stat(filepath.Join(root, "deleting")); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the cleanup delete never started")
		}
	}

	octx, cancel := context.WithTimeout(ctx, 2*time.Second) // well inside the 3 s delete
	defer cancel()
	other, err := a.Open(octx, spec("k2", "/work", f.profile))
	if err != nil || other.NativeID == "" {
		t.Fatalf("Open during another key's cleanup: %+v %v", other, err)
	}
	select {
	case r := <-failing:
		t.Fatalf("the failing Open returned before its slow delete: %+v %v", r.ref, r.err)
	default:
	}

	r := <-failing
	if r.err == nil || r.ref != (loomharness.NativeRef{}) || len(loadStore(root).Threads) != 0 {
		t.Fatalf("failing Open: %+v %v, threads %v; want the zero ref and nothing left", r.ref, r.err, loadStore(root).Threads)
	}
}
