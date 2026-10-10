package prwatch

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/connector/providers"
	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/loomstore"
)

// fakeHost answers the host GitHub reads from pages: op -> its pages of
// items (JSON-decoded, so ids are float64), pr for pr_view, and fail by
// "op#page".
type fakeHost struct {
	viewer string
	pr     map[string]any
	pages  map[string][][]any
	status []map[string]any // commit_status's item, by page
	fail   map[string]error
	calls  []string
	onRead func(op string)
}

func (f *fakeHost) Viewer(context.Context, string, string, string) (string, error) {
	if f.viewer == "" {
		return "", errors.New("GitHub is not configured on this Loom host")
	}
	return f.viewer, nil
}

func (f *fakeHost) Read(_ context.Context, ws, owner, repo, op string, args map[string]any) (map[string]any, error) {
	if ws != "ws" || owner != "octocat" || repo != "hello" {
		return nil, domain.ErrNotOwner
	}
	page, _ := args["page"].(int)
	key := op + "#" + strconv.Itoa(page)
	f.calls = append(f.calls, key)
	if f.onRead != nil {
		f.onRead(op)
	}
	if err := f.fail[key]; err != nil {
		return nil, err
	}
	if op == "pr_view" {
		return map[string]any{"op": op, "item": f.pr}, nil
	}
	if op == "commit_status" {
		body := map[string]any{"op": op, "item": map[string]any{}}
		if page >= 1 && page <= len(f.status) {
			body["item"] = f.status[page-1]
		}
		if page < len(f.status) {
			body["next"] = strconv.Itoa(page + 1)
		}
		return body, nil
	}
	pages := f.pages[op]
	body := map[string]any{"op": op, "items": []any{}}
	if page >= 1 && page <= len(pages) {
		body["items"] = pages[page-1]
	}
	if page < len(pages) {
		body["next"] = strconv.Itoa(page + 1)
	}
	return body, nil
}

func (f *fakeHost) Repo(_ context.Context, ws, repoPath string) (string, string, error) {
	if ws == "ws" && repoPath == "/clones/hello" {
		return "octocat", "hello", nil
	}
	return "", "", domain.ErrNotOwner
}

func newHost() *fakeHost {
	return &fakeHost{
		viewer: "loom-host",
		pr:     map[string]any{"number": float64(8), "state": "open", "merged": false, "mergeable_state": "clean", "head": map[string]any{"sha": "sha-1"}},
		pages: map[string][][]any{
			"check_runs":         {{map[string]any{"id": float64(7), "name": "test", "status": "completed", "conclusion": "success"}}},
			"issue_comments":     {{comment(4, "alice")}},
			"pr_review_comments": {{comment(3, "bob")}},
			"pr_reviews":         {{map[string]any{"id": float64(2), "state": "APPROVED", "user": map[string]any{"login": "carol"}}}},
		},
		fail: map[string]error{},
	}
}

func comment(id int, login string) map[string]any {
	return map[string]any{"id": float64(id), "body": "c", "user": map[string]any{"login": login}, "created_at": "2026-10-01T00:00:00Z"}
}

func openStore(t *testing.T, path string) *loomstore.Store {
	t.Helper()
	s, err := loomstore.Open(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

var key = loomstore.PRWatchKey{AgentID: "a1", Owner: "octocat", Repo: "hello", Number: 8}

// TestPRWatchRegistrationRestart: a registration, its cursors, wake count
// and last-told survive a restart (a reopened store).
func TestPRWatchRegistrationRestart(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "agents.db")
	s := openStore(t, path)
	w, created, err := Register(ctx, s, newHost(), "ws", "a1", "/clones/hello", 8)
	if err != nil || !created {
		t.Fatalf("Register = %+v, %v, %v", w, created, err)
	}
	if w.Viewer != "loom-host" || w.Cursor.Head != "sha-1" || w.Cursor.Checks == "" || w.Cursor.Comments != `{"issue":4,"review":2,"reviewComment":3}` {
		t.Fatalf("registered watch = %+v", w)
	}
	adv := loomstore.PRWatchCursor{Head: "sha-2", Checks: "c2", Comments: `{"issue":9,"review":2,"reviewComment":3}`}
	if err := s.AdvancePRWatch(ctx, key, "loom-host", adv, 3, "checks finished"); err != nil {
		t.Fatal(err)
	}
	_ = s.Close()

	s = openStore(t, path)
	defer s.Close()
	got, err := s.PRWatches(ctx, "ws")
	if err != nil || len(got) != 1 {
		t.Fatalf("PRWatches after restart = %+v, %v", got, err)
	}
	if g := got[0]; g.PRWatchKey != key || g.WorkspaceID != "ws" || g.Viewer != "loom-host" || g.Cursor != adv || g.WakeCount != 3 || g.LastTold != "checks finished" {
		t.Fatalf("watch after restart = %+v", g)
	}
	if other, _ := s.PRWatches(ctx, "other"); len(other) != 0 {
		t.Fatalf("another workspace's watches = %+v", other)
	}
	if ok, err := s.UnregisterPRWatch(ctx, key); !ok || err != nil {
		t.Fatalf("Unregister = %v, %v", ok, err)
	}
	if ok, err := s.UnregisterPRWatch(ctx, key); ok || err != nil {
		t.Fatalf("second Unregister = %v, %v; want false", ok, err)
	}
	if _, err := s.PRWatch(ctx, key); !errors.Is(err, loomstore.ErrNotFound) {
		t.Fatalf("PRWatch after Unregister = %v; want ErrNotFound", err)
	}
}

// TestPRWatchDuplicateRegistration: registering a watched PR again keeps
// the one row with its cursors, wake count and last-told.
func TestPRWatchDuplicateRegistration(t *testing.T) {
	ctx := context.Background()
	s := openStore(t, filepath.Join(t.TempDir(), "agents.db"))
	defer s.Close()
	h := newHost()
	if _, _, err := Register(ctx, s, h, "ws", "a1", "/clones/hello", 8); err != nil {
		t.Fatal(err)
	}
	adv := loomstore.PRWatchCursor{Head: "sha-0", Checks: "old", Comments: "{}"}
	if err := s.AdvancePRWatch(ctx, key, "loom-host", adv, 2, "told"); err != nil {
		t.Fatal(err)
	}
	h.pr["head"] = map[string]any{"sha": "sha-9"}
	w, created, err := Register(ctx, s, h, "ws", "a1", "/clones/hello", 8)
	if err != nil || created || w.Cursor != adv || w.WakeCount != 2 || w.LastTold != "told" {
		t.Fatalf("duplicate Register = %+v, created %v, %v; want the saved watch unchanged", w, created, err)
	}
	if all, _ := s.PRWatches(ctx, "ws"); len(all) != 1 {
		t.Fatalf("watches = %d; want 1", len(all))
	}
}

// TestPRWatchViewerMismatch: after the host credential moves to another
// user, re-registering records the new viewer (keeping the cursors) and an
// observation reports it; registration with no viewer fails clearly.
func TestPRWatchViewerMismatch(t *testing.T) {
	ctx := context.Background()
	s := openStore(t, filepath.Join(t.TempDir(), "agents.db"))
	defer s.Close()
	h := newHost()
	first, _, err := Register(ctx, s, h, "ws", "a1", "/clones/hello", 8)
	if err != nil {
		t.Fatal(err)
	}
	h.viewer = "bob"
	w, created, err := Register(ctx, s, h, "ws", "a1", "/clones/hello", 8)
	if err != nil || created || w.Viewer != "bob" || w.Cursor != first.Cursor {
		t.Fatalf("re-Register as bob = %+v, %v, %v; want viewer bob with the cursors kept", w, created, err)
	}
	snap, err := Observe(ctx, h, "ws", "octocat", "hello", 8)
	if err != nil || snap.Viewer != "bob" {
		t.Fatalf("Observe viewer = %q, %v; want bob", snap.Viewer, err)
	}
	h.viewer = ""
	if _, _, err := Register(ctx, s, h, "ws", "a1", "/clones/hello", 9); err == nil {
		t.Fatal("Register with no host viewer succeeded")
	}
	if _, err := s.PRWatch(ctx, loomstore.PRWatchKey{AgentID: "a1", Owner: "octocat", Repo: "hello", Number: 9}); !errors.Is(err, loomstore.ErrNotFound) {
		t.Fatalf("watch saved without a viewer: %v", err)
	}
	if _, _, err := Register(ctx, s, newHost(), "ws", "a1", "/clones/other", 8); !errors.Is(err, domain.ErrNotOwner) {
		t.Fatalf("Register on another repo = %v; want ErrNotOwner", err)
	}
}

// TestPRWatchPaginationBoundary: a full page with a next page reads on and
// keeps every item; a full last page asks for no more.
func TestPRWatchPaginationBoundary(t *testing.T) {
	page := func(from, n int) []any {
		out := make([]any, n)
		for i := range n {
			out[i] = comment(from+i, fmt.Sprint("u", from+i))
		}
		return out
	}
	h := newHost()
	h.pages["issue_comments"] = [][]any{page(1, 100), page(101, 1)}
	snap, err := Observe(context.Background(), h, "ws", "octocat", "hello", 8)
	if err != nil {
		t.Fatal(err)
	}
	issue := 0
	for _, c := range snap.Comments {
		if c.Kind == "issue" {
			issue++
		}
	}
	if issue != 101 || snap.Cursor.Comments != `{"issue":101,"review":2,"reviewComment":3}` {
		t.Fatalf("issue comments = %d, cursor %s; want 101 and issue 101", issue, snap.Cursor.Comments)
	}

	h = newHost()
	h.pages["issue_comments"] = [][]any{page(1, 100)}
	if _, err := Observe(context.Background(), h, "ws", "octocat", "hello", 8); err != nil {
		t.Fatal(err)
	}
	for _, c := range h.calls {
		if c == "issue_comments#2" {
			t.Fatalf("read past the last full page: %v", h.calls)
		}
	}
}

// TestPRWatchRateLimitNoCursorAdvance: a rate limit on any page fails the
// whole observation, so no partial snapshot or cursor exists: a new
// registration saves nothing and a saved watch keeps its cursors.
func TestPRWatchRateLimitNoCursorAdvance(t *testing.T) {
	ctx := context.Background()
	s := openStore(t, filepath.Join(t.TempDir(), "agents.db"))
	defer s.Close()
	h := newHost()
	saved, _, err := Register(ctx, s, h, "ws", "a1", "/clones/hello", 8)
	if err != nil {
		t.Fatal(err)
	}
	h.pages["check_runs"] = [][]any{{map[string]any{"id": float64(7), "status": "completed"}}, {map[string]any{"id": float64(8), "status": "queued"}}}
	h.fail["check_runs#2"] = &providers.RateLimited{Action: providers.ActionGitHubRead, Status: 403}
	var rl *providers.RateLimited
	if snap, err := Observe(ctx, h, "ws", "octocat", "hello", 8); !errors.As(err, &rl) || snap.Cursor != (loomstore.PRWatchCursor{}) {
		t.Fatalf("Observe = %+v, %v; want the rate limit and no snapshot", snap, err)
	}
	if _, _, err := Register(ctx, s, h, "ws", "a1", "/clones/hello", 8); !errors.As(err, &rl) {
		t.Fatalf("re-Register = %v; want the rate limit", err)
	}
	if got, err := s.PRWatch(ctx, key); err != nil || got.Cursor != saved.Cursor {
		t.Fatalf("watch after the rate limit = %+v, %v; want cursors %+v", got, err, saved.Cursor)
	}
	if _, _, err := Register(ctx, s, h, "ws", "a1", "/clones/hello", 9); !errors.As(err, &rl) {
		t.Fatalf("Register #9 = %v; want the rate limit", err)
	}
	if all, _ := s.PRWatches(ctx, "ws"); len(all) != 1 {
		t.Fatalf("watches = %d; want only the first", len(all))
	}
}

// A closed or merged PR cannot be watched.
func TestPRWatchRefusesClosedPR(t *testing.T) {
	s := openStore(t, filepath.Join(t.TempDir(), "agents.db"))
	defer s.Close()
	h := newHost()
	h.pr["state"] = "closed"
	if _, _, err := Register(context.Background(), s, h, "ws", "a1", "/clones/hello", 8); !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("Register on a closed PR = %v; want ErrInvalid", err)
	}
}

// A deleted agent's watches are not listed, so no sweep reads for it.
func TestPRWatchDeletedAgentNotListed(t *testing.T) {
	ctx := context.Background()
	s := openStore(t, filepath.Join(t.TempDir(), "agents.db"))
	defer s.Close()
	when := "2026-10-01T00:00:00Z"
	gone := loomstore.Agent{AgentID: "a1", WorkspaceID: "ws", Name: "a1", ProfileKey: "a1", Preset: "lead", PresetVersion: "1",
		Mode: "persistent", InteractionMode: "interactive", RoleKind: "interactive", SpecJSON: "{}", SpecVersion: 1,
		OwnerKind: "user", OwnerID: "local", CreatedByKind: "user", CreatedByID: "local", CreateRequestID: "req-a1",
		Repo: "/clones/hello", Harness: "fake", State: "archived", ArchivedAt: &when, DeletedAt: &when}
	if err := s.InsertAgent(ctx, gone); err != nil {
		t.Fatal(err)
	}
	if _, _, err := Register(ctx, s, newHost(), "ws", "a1", "/clones/hello", 8); err != nil {
		t.Fatal(err)
	}
	if _, _, err := Register(ctx, s, newHost(), "ws", "a2", "/clones/hello", 8); err != nil {
		t.Fatal(err)
	}
	if all, err := s.PRWatches(ctx, "ws"); err != nil || len(all) != 1 || all[0].AgentID != "a2" {
		t.Fatalf("PRWatches = %+v, %v; want only a2's", all, err)
	}
}

// The checks cursor covers every page of commit statuses: a status-only CI
// result on a later page moves it, and a reordered answer does not.
func TestPRWatchCommitStatusInChecksCursor(t *testing.T) {
	st := func(state string, contexts ...string) map[string]any {
		var list []any
		for _, c := range contexts {
			list = append(list, map[string]any{"context": c, "state": state})
		}
		return map[string]any{"state": state, "statuses": list}
	}
	observe := func(h *fakeHost) Snapshot {
		t.Helper()
		s, err := Observe(context.Background(), h, "ws", "octocat", "hello", 8)
		if err != nil {
			t.Fatal(err)
		}
		return s
	}
	h := newHost()
	h.status = []map[string]any{st("pending", "a", "b"), st("pending", "c")}
	before := observe(h)
	h.status = []map[string]any{st("pending", "b", "a"), st("pending", "c")}
	if again := observe(h); again.Cursor.Checks != before.Cursor.Checks {
		t.Fatalf("a reordered status answer moved the checks cursor %q -> %q", before.Cursor.Checks, again.Cursor.Checks)
	}
	h.status = []map[string]any{st("pending", "a", "b"), st("success", "c")}
	if after := observe(h); after.Cursor.Checks == before.Cursor.Checks {
		t.Fatalf("a status change on page 2 left the checks cursor at %q", before.Cursor.Checks)
	}
	h.status[0]["state"] = "success"
	if after := observe(h); after.Status != "success" {
		t.Fatalf("combined status = %q; want success", after.Status)
	}
}

// A viewer change during an observation fails it, so cursors read under
// one credential are never saved under another viewer.
func TestPRWatchViewerChangesMidObservation(t *testing.T) {
	h := newHost()
	h.onRead = func(op string) {
		if op == "pr_reviews" {
			h.viewer = "bob"
		}
	}
	if snap, err := Observe(context.Background(), h, "ws", "octocat", "hello", 8); err == nil || strings.Contains(err.Error(), "%!") {
		t.Fatalf("Observe = %+v, %v; want a clean viewer-changed failure", snap, err)
	}
}
