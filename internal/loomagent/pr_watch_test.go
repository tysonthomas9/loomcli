package loomagent

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/loomharness/fake"
	"github.com/tysonthomas9/loomcli/internal/loomstore"
	"github.com/tysonthomas9/loomcli/internal/prwatch"
)

// prHost is a host GitHub connector serving one PR, octo/hello#7, as the
// viewer loom-host; fail names an op whose reads fail.
type prHost struct {
	mu       sync.Mutex
	pr       map[string]any
	runs     []any
	statuses []any
	comments map[string][]any // by op
	fail     string
	onRead   func(op string) // runs before each read, unlocked
}

func newPRHost() *prHost {
	return &prHost{pr: map[string]any{"state": "open", "merged": false, "mergeable_state": "clean",
		"head": map[string]any{"sha": "sha1"}}, comments: map[string][]any{}}
}

func (h *prHost) Repo(context.Context, string, string) (string, string, error) {
	return "octo", "hello", nil
}

func (h *prHost) Viewer(context.Context, string, string, string) (string, error) {
	return "loom-host", nil
}

func (h *prHost) Read(_ context.Context, _, _, _, op string, _ map[string]any) (map[string]any, error) {
	if h.onRead != nil {
		h.onRead(op)
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if op == h.fail {
		return nil, errors.New("GitHub answered 502")
	}
	switch op {
	case "pr_view":
		return map[string]any{"item": h.pr}, nil
	case "check_runs":
		return map[string]any{"items": h.runs}, nil
	case "commit_status":
		return map[string]any{"item": map[string]any{"state": "success", "statuses": h.statuses}}, nil
	}
	return map[string]any{"items": h.comments[op]}, nil
}

func (h *prHost) set(f func(h *prHost)) { h.mu.Lock(); defer h.mu.Unlock(); f(h) }

func (h *prHost) comment(op string, id float64, author string) {
	h.set(func(h *prHost) {
		h.comments[op] = append(h.comments[op], map[string]any{"id": id, "user": map[string]any{"login": author},
			"created_at": "2026-10-10T12:00:00Z"})
	})
}

func (h *prHost) run(id float64, status, conclusion string) {
	h.set(func(h *prHost) {
		h.runs = []any{map[string]any{"id": id, "name": "ci", "status": status, "conclusion": conclusion}}
	})
}

// prEnv is a Lead watching octo/hello#7 on a limitEnv.
type prEnv struct {
	*limitEnv
	host *prHost
	key  loomstore.PRWatchKey
}

func newPREnv(t *testing.T, harness string) *prEnv {
	t.Helper()
	host := newPRHost()
	p := &prEnv{limitEnv: newLimitEnvWith(t, harness, false, ServiceConfig{PRWatchHost: host}), host: host}
	w, _, err := prwatch.Register(context.Background(), p.s.store, p.host, "ws", p.a.AgentID, "/repo", 7)
	if err != nil {
		t.Fatal(err)
	}
	p.key = w.PRWatchKey
	return p
}

// sweep runs the next PR-watch sweep and waits for any turn it started.
func (p *prEnv) sweep() {
	p.t.Helper()
	p.advance(prWatchInterval)
	drained(p.t, p.s, "the agent idle", func() bool { return p.s.get(p.t, p.a.AgentID).State == StateIdle })
}

// wakes are the agent's PR-watch wakes' texts, oldest first.
func (p *prEnv) wakes() []string {
	p.t.Helper()
	rs, err := p.s.store.SenderReceipts(context.Background(), p.a.AgentID, prWatchSender)
	if err != nil {
		p.t.Fatal(err)
	}
	var out []string
	for _, r := range rs {
		out = append(out, *r.Body)
	}
	return out
}

// wantWakes requires n wakes, the last containing each of has and none of hasnt.
func (p *prEnv) wantWakes(what string, n int, has []string, hasnt ...string) {
	p.t.Helper()
	got := p.wakes()
	if len(got) != n {
		p.t.Fatalf("%s: %d wakes %q; want %d", what, len(got), got, n)
	}
	if n == 0 {
		return
	}
	for _, s := range has {
		if !strings.Contains(got[n-1], s) {
			p.t.Fatalf("%s: wake %q lacks %q", what, got[n-1], s)
		}
	}
	for _, s := range hasnt {
		if strings.Contains(got[n-1], s) {
			p.t.Fatalf("%s: wake %q has %q", what, got[n-1], s)
		}
	}
}

func (p *prEnv) watch() (loomstore.PRWatch, bool) {
	p.t.Helper()
	w, err := p.s.store.PRWatch(context.Background(), p.key)
	if errors.Is(err, loomstore.ErrNotFound) {
		return w, false
	} else if err != nil {
		p.t.Fatal(err)
	}
	return w, true
}

func eachHarness(t *testing.T, f func(t *testing.T, p *prEnv)) {
	for _, h := range Harnesses {
		t.Run(h, func(t *testing.T) { f(t, newPREnv(t, h)) })
	}
}

// TestPRWatchCrashAtSendCommit (OR8): the wake's receipt and its cursor
// commit in one transaction. A failure at the cursor write saves no receipt
// and keeps the cursor, and the next sweep wakes once; a crash after the
// commit keeps both, so after a restart the news is not repeated, and a
// comment that came after the crash is the only news of the next wake.
func TestPRWatchCrashAtSendCommit(t *testing.T) {
	eachHarness(t, func(t *testing.T, p *prEnv) {
		ctx := context.Background()
		db, err := sql.Open("sqlite", p.e.path)
		if err != nil {
			t.Fatal(err)
		}
		defer db.Close()
		if _, err := db.Exec(`CREATE TRIGGER crash BEFORE UPDATE ON pr_watches BEGIN SELECT RAISE(ABORT, 'crash'); END`); err != nil {
			t.Fatal(err)
		}
		before, _ := p.watch()
		p.host.comment("issue_comments", 11, "bob")
		p.sweep()
		p.wantWakes("a crash before the commit", 0, nil)
		if w, _ := p.watch(); w.Cursor != before.Cursor || w.WakeCount != 0 {
			t.Fatalf("cursor after a crash before the commit = %+v; want %+v", w, before)
		}
		if _, err := db.Exec(`DROP TRIGGER crash`); err != nil {
			t.Fatal(err)
		}
		p.sweep()
		p.wantWakes("the sweep after the crash", 1, []string{"bob"})

		p.host.comment("issue_comments", 12, "bob")
		w, _ := p.watch()
		commitStateCrash = func() { commitStateCrash = func() {}; panic("crash") }
		t.Cleanup(func() { commitStateCrash = func() {} })
		func() {
			defer func() {
				if recover() == nil {
					t.Fatal("did not crash")
				}
			}()
			_ = p.s.prWatch(ctx, w)
		}()
		after, _ := p.watch()
		p.wantWakes("a crash after the commit", 2, []string{"bob"})
		if after.Cursor == w.Cursor {
			t.Fatal("the cursor did not commit with the receipt")
		}
		p.host.comment("pr_reviews", 30, "carol")
		p.restart()
		drained(t, p.s, "the crashed wake told", func() bool { return p.s.get(t, p.a.AgentID).State == StateIdle })
		p.sweep()
		p.wantWakes("the sweep after a restart", 3, []string{"carol"}, "bob")
		p.sweep()
		p.wantWakes("no more news", 3, nil)
	})
}

// TestPRWatchSameSecondCommentIds (OR8): comments made in the same second
// are told apart by ID: each is told once.
func TestPRWatchSameSecondCommentIds(t *testing.T) {
	eachHarness(t, func(t *testing.T, p *prEnv) {
		p.host.comment("issue_comments", 101, "alice")
		p.sweep()
		p.wantWakes("the first", 1, []string{"alice"})
		p.host.comment("issue_comments", 102, "dave")
		p.sweep()
		p.wantWakes("the second, same second", 2, []string{"dave"}, "alice")
		p.sweep()
		p.wantWakes("nothing new", 2, nil)
	})
}

// TestPRWatchIgnoresOwnComments (OR8): comments by the host viewer never
// wake the agent; another's comment does, and only it is told.
func TestPRWatchIgnoresOwnComments(t *testing.T) {
	eachHarness(t, func(t *testing.T, p *prEnv) {
		p.host.comment("issue_comments", 5, "loom-host")
		p.host.comment("pr_review_comments", 6, "Loom-Host")
		p.sweep()
		p.wantWakes("own comments", 0, nil)
		p.host.comment("pr_review_comments", 7, "erin")
		p.sweep()
		p.wantWakes("another's comment", 1, []string{"erin", "1 new comment"}, "loom-host", "Loom-Host")
	})
}

// TestPRWatchCheckRerun (OR8): checks wake when they finish, not while they
// run; a rerun that finishes wakes again.
func TestPRWatchCheckRerun(t *testing.T) {
	eachHarness(t, func(t *testing.T, p *prEnv) {
		p.host.run(1, "in_progress", "")
		p.sweep()
		p.wantWakes("a running check", 0, nil)
		p.host.run(1, "completed", "failure")
		p.sweep()
		p.wantWakes("the check failed", 1, []string{"failed", "ci"})
		p.host.run(2, "in_progress", "")
		p.sweep()
		p.wantWakes("the rerun runs", 1, nil)
		p.host.run(2, "completed", "success")
		p.sweep()
		p.wantWakes("the rerun passed", 2, []string{"none failed"}, "failed: ci")
		p.sweep()
		p.wantWakes("nothing new", 2, nil)
	})
}

// TestPRWatchConflict (OR8): a new conflict wakes once, and GitHub still
// computing it ("unknown") does not make it new again; once it cleared, the
// next conflict is new.
func TestPRWatchConflict(t *testing.T) {
	eachHarness(t, func(t *testing.T, p *prEnv) {
		p.host.set(func(h *prHost) { h.pr["mergeable_state"] = "dirty" })
		p.sweep()
		p.wantWakes("a conflict", 1, []string{"conflicts"})
		p.host.set(func(h *prHost) { h.pr["mergeable_state"] = "unknown" })
		p.host.comment("issue_comments", 70, "jo")
		p.sweep()
		p.wantWakes("a comment while GitHub computes", 2, []string{"jo"}, "conflicts")
		p.host.set(func(h *prHost) { h.pr["mergeable_state"] = "dirty" })
		p.sweep()
		p.wantWakes("still conflicting", 2, nil)
		p.host.set(func(h *prHost) { h.pr["mergeable_state"] = "clean" })
		p.sweep()
		p.wantWakes("the conflict cleared", 2, nil)
		p.host.set(func(h *prHost) { h.pr["mergeable_state"] = "dirty" })
		p.sweep()
		p.wantWakes("a new conflict", 3, []string{"conflicts"})
	})
}

// TestPRWatchPartialFailureNoCursorAdvance (OR8): one failed read leaves
// the cursor and wakes no one; the next good sweep wakes once.
func TestPRWatchPartialFailureNoCursorAdvance(t *testing.T) {
	eachHarness(t, func(t *testing.T, p *prEnv) {
		before, _ := p.watch()
		p.host.comment("issue_comments", 40, "frank")
		p.host.run(3, "completed", "success")
		p.host.set(func(h *prHost) { h.fail = "pr_review_comments" })
		p.sweep()
		p.wantWakes("a failed read", 0, nil)
		if w, _ := p.watch(); w.Cursor != before.Cursor {
			t.Fatalf("cursor after a failed read = %+v; want %+v", w.Cursor, before.Cursor)
		}
		p.host.set(func(h *prHost) { h.fail = "" })
		p.sweep()
		p.wantWakes("the next good read", 1, []string{"frank", "none failed"})
		p.sweep()
		p.wantWakes("nothing new", 1, nil)
	})
}

// TestPRWatchSweepInterval (OR8): watched PRs are read once every
// prWatchInterval, not on every resync tick.
func TestPRWatchSweepInterval(t *testing.T) {
	p := newPREnv(t, "opencode")
	p.host.comment("issue_comments", 80, "kim")
	p.sweep()
	p.wantWakes("the first sweep", 1, []string{"kim"})
	p.host.comment("issue_comments", 81, "lee")
	p.advance(prWatchInterval - time.Second)
	p.wantWakes("before the next sweep is due", 1, nil)
	p.sweep()
	p.wantWakes("the next sweep", 2, []string{"lee"})
}

// TestPRWatchRestartDedupe (OR8): news is told once across ticks and
// restarts.
func TestPRWatchRestartDedupe(t *testing.T) {
	eachHarness(t, func(t *testing.T, p *prEnv) {
		p.host.comment("issue_comments", 50, "gina")
		p.sweep()
		p.sweep()
		p.restart()
		p.sweep()
		p.restart()
		p.sweep()
		p.wantWakes("after restarts", 1, []string{"gina"})
	})
}

// TestPRWatchWakeCap (OR8): the watch stops after 10 comment-only wakes in
// a row, saying so in the 10th; check news resets the count.
func TestPRWatchWakeCap(t *testing.T) {
	eachHarness(t, func(t *testing.T, p *prEnv) {
		id := 100.0
		comment := func() { id++; p.host.comment("issue_comments", id, "bot") }
		for range 9 {
			comment()
			p.sweep()
		}
		if w, _ := p.watch(); w.WakeCount != 9 {
			t.Fatalf("wake count %d; want 9", w.WakeCount)
		}
		comment()
		p.host.run(9, "completed", "success")
		p.sweep()
		if w, _ := p.watch(); w.WakeCount != 0 {
			t.Fatalf("wake count after check news %d; want 0", w.WakeCount)
		}
		for range 9 {
			comment()
			p.sweep()
		}
		p.wantWakes("nine comment-only wakes", 19, nil, "stopped")
		comment()
		p.sweep()
		p.wantWakes("the tenth", 20, []string{"stopped watching"})
		if _, ok := p.watch(); ok {
			t.Fatal("the watch survived its tenth comment-only wake")
		}
		comment()
		p.sweep()
		p.wantWakes("after the cap", 20, nil)
	})
}

// TestPRWatchEndsOnMergeOrClose (OR8): a merged PR ends its watch quietly;
// a closed one tells the agent, once, and ends it.
func TestPRWatchEndsOnMergeOrClose(t *testing.T) {
	eachHarness(t, func(t *testing.T, p *prEnv) {
		p.host.set(func(h *prHost) { h.pr["state"], h.pr["merged"] = "closed", true })
		p.sweep()
		p.wantWakes("merged", 0, nil)
		if _, ok := p.watch(); ok {
			t.Fatal("a merged PR's watch survived")
		}
		p.host.set(func(h *prHost) { h.pr["state"], h.pr["merged"] = "open", false })
		if _, _, err := prwatch.Register(context.Background(), p.s.store, p.host, "ws", p.a.AgentID, "/repo", 7); err != nil {
			t.Fatal(err)
		}
		p.host.set(func(h *prHost) { h.pr["state"] = "closed" })
		p.sweep()
		p.sweep()
		p.wantWakes("closed", 1, []string{"closed", "stopped watching"})
		if _, ok := p.watch(); ok {
			t.Fatal("a closed PR's watch survived")
		}
		p.host.set(func(h *prHost) { h.pr["state"] = "open" }) // reopened, watched again, closed again
		if _, _, err := prwatch.Register(context.Background(), p.s.store, p.host, "ws", p.a.AgentID, "/repo", 7); err != nil {
			t.Fatal(err)
		}
		p.host.set(func(h *prHost) { h.pr["state"] = "closed" })
		p.sweep()
		p.wantWakes("closed again", 2, []string{"closed"})
		if _, ok := p.watch(); ok {
			t.Fatal("the second watch survived its PR's close")
		}
	})
}

// TestPRWatchSweepDropsDeletedAgents (OR8, from OR10): a deleted agent's
// watches are removed, not only hidden.
func TestPRWatchSweepDropsDeletedAgents(t *testing.T) {
	p := newPREnv(t, "opencode")
	if err := p.s.store.Tombstone(context.Background(), p.a.AgentID, p.at); err != nil {
		t.Fatal(err)
	}
	p.advance(prWatchInterval)
	db, err := sql.Open("sqlite", p.e.path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var n int
	if err := db.QueryRow(`SELECT count(*) FROM pr_watches`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("watches of a deleted agent: %d (%v); want 0", n, err)
	}
}

// TestPRWatchWaitsForPendingWake (OR8): while the agent's previous wake
// still waits, news is not sent over it (that would replace its text and
// lose its news); once it is handed over, the next sweep tells the rest.
func TestPRWatchWaitsForPendingWake(t *testing.T) {
	eachHarness(t, func(t *testing.T, p *prEnv) {
		p.fh.Script(p.a.AgentID, fake.Turn{Steps: []fake.Step{{Ask: "a1"}}})
		mustSendMsg(t, p.s, sendReq(p.a.AgentID, "u1", "go", user))
		drained(t, p.s, "the agent waits on approval", func() bool { return p.s.get(t, p.a.AgentID).State == StateWaiting })
		p.host.comment("issue_comments", 60, "hana")
		p.advance(prWatchInterval)
		p.wantWakes("the first wake waits", 1, []string{"hana"})
		p.host.comment("issue_comments", 61, "ivan")
		p.advance(prWatchInterval)
		p.wantWakes("news while it waits", 1, nil)
		if err := p.s.Respond(context.Background(), RespondRequest{AgentID: p.a.AgentID, AskID: "a1", Decision: "allow_once"}); err != nil {
			t.Fatal(err)
		}
		drained(t, p.s, "the wake told", func() bool { return p.s.get(t, p.a.AgentID).State == StateIdle })
		p.sweep()
		p.wantWakes("after it was handed over", 2, []string{"ivan"}, "hana")
	})
}

// TestPRWatchRewatchDuringSweep (OR8): a watch removed and made again while
// a sweep reads its PR is a new watch: the sweep neither tells its news nor
// writes over it.
func TestPRWatchRewatchDuringSweep(t *testing.T) {
	p := newPREnv(t, "opencode")
	p.host.comment("issue_comments", 90, "max")
	var done atomic.Bool
	p.host.onRead = func(op string) {
		if op != "pr_reviews" || done.Swap(true) {
			return
		}
		ctx := context.Background()
		if _, err := prwatch.Unregister(ctx, p.s.store, p.host, "ws", p.a.AgentID, "/repo", 7); err != nil {
			t.Error(err)
		}
		if _, _, err := prwatch.Register(ctx, p.s.store, p.host, "ws", p.a.AgentID, "/repo", 7); err != nil {
			t.Error(err)
		}
	}
	p.sweep()
	p.host.onRead = nil
	p.wantWakes("a rewatch during the read", 0, nil)
	w, _ := p.watch()
	if w.WakeCount != 0 || w.LastTold != "" {
		t.Fatalf("the new watch was written over: %+v", w)
	}
	p.sweep()
	p.wantWakes("the new watch saw the comment when made", 0, nil)
}

// TestPRWatchStatusRerun (OR8): a commit status that runs again and ends
// in the same state is news again (the connector keeps no status id).
func TestPRWatchStatusRerun(t *testing.T) {
	p := newPREnv(t, "opencode")
	status := func(state, at string) {
		p.host.set(func(h *prHost) {
			h.statuses = []any{map[string]any{"context": "lint", "state": state, "updated_at": at}}
		})
	}
	status("failure", "2026-10-10T12:00:00Z")
	p.sweep()
	p.wantWakes("the status failed", 1, []string{"failed: lint"})
	status("pending", "2026-10-10T12:05:00Z")
	p.sweep()
	status("failure", "2026-10-10T12:09:00Z")
	p.sweep()
	p.wantWakes("it failed again", 2, []string{"failed: lint"})
}
