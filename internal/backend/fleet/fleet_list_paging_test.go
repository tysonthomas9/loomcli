package fleet

import (
	"context"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/backend"
	"github.com/tysonthomas9/loomcli/internal/types"
)

// fleet-db caps a page at 200 rows whatever limit is asked for, and reports the
// truncation in has_more. List used to return that first page and drop the rest
// silently. Measured on the live workspace 2026-09-08: `--limit 500` answered
// with 200 of 527 closed issues, and the 327 it dropped were the NEWEST — so
// "has this been closed?" got a confident false negative for everything closed
// that day. These tests pin the paging that fixes it.

// pagedIssues builds n issues named <prefix>-<i>.
func pagedIssues(prefix string, from, n int) []*types.IssueWithCounts {
	now := time.Now().UTC().Truncate(time.Second)
	out := make([]*types.IssueWithCounts, 0, n)
	for i := from; i < from+n; i++ {
		out = append(out, &types.IssueWithCounts{
			Issue: &types.Issue{
				ID: fmt.Sprintf("%s-%d", prefix, i), Title: "T",
				Status: types.StatusClosed, CreatedAt: now, UpdatedAt: now,
			},
		})
	}
	return out
}

// respondPage writes the wrapper dialect fleet-db uses for a truncated list.
func respondPage(w http.ResponseWriter, issues []*types.IssueWithCounts, hasMore bool) {
	respondOK(w, map[string]any{"issues": issues, "has_more": hasMore})
}

func TestList_PagesUntilServerHasNoMore(t *testing.T) {
	const page, total = 200, 527
	var offsets []string
	fb, ts := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		off := r.URL.Query().Get("offset")
		offsets = append(offsets, off)
		start := 0
		fmt.Sscanf(off, "%d", &start)
		n := page
		if start+n > total {
			n = total - start
		}
		respondPage(w, pagedIssues("PUPPET", start, n), start+n < total)
	})
	defer ts.Close()

	// Limit 0 means "everything the server has".
	got, err := fb.List(context.Background(), backend.ListOpts{Status: "closed"})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != total {
		t.Fatalf("len = %d, want %d — the tail of the result set was dropped", len(got), total)
	}
	// The newest rows are the ones that used to vanish; assert the last page landed.
	if got[len(got)-1].ID != fmt.Sprintf("PUPPET-%d", total-1) {
		t.Errorf("last id = %q, want PUPPET-%d", got[len(got)-1].ID, total-1)
	}
	want := []string{"", "200", "400"}
	if len(offsets) != len(want) {
		t.Fatalf("offsets = %v, want %v", offsets, want)
	}
	for i, w := range want {
		if offsets[i] != w {
			t.Errorf("offsets[%d] = %q, want %q", i, offsets[i], w)
		}
	}
}

func TestList_StopsAtCallerLimit(t *testing.T) {
	calls := 0
	fb, ts := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		respondPage(w, pagedIssues("A", (calls-1)*200, 200), true)
	})
	defer ts.Close()

	got, err := fb.List(context.Background(), backend.ListOpts{Limit: 250})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 250 {
		t.Fatalf("len = %d, want 250 (trimmed to the caller's limit)", len(got))
	}
	if calls != 2 {
		t.Errorf("server calls = %d, want 2 — paging must stop once the limit is met", calls)
	}
}

// A limit the first page already satisfies must cost exactly one request. This
// is what keeps paging off the hot paths: List has many callers, several of
// them polling, and turning every one into a multi-request walk would multiply
// load on fleet-db.
func TestList_SinglePageWhenLimitFits(t *testing.T) {
	calls := 0
	fb, ts := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		respondPage(w, pagedIssues("B", 0, 10), false)
	})
	defer ts.Close()

	got, err := fb.List(context.Background(), backend.ListOpts{Limit: 50})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 10 || calls != 1 {
		t.Fatalf("len = %d, calls = %d; want 10 and 1", len(got), calls)
	}
}

// A server that always claims has_more must not spin forever. A short page ends
// the walk regardless of the flag.
func TestList_ShortPageEndsWalkDespiteHasMore(t *testing.T) {
	calls := 0
	fb, ts := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		respondPage(w, pagedIssues("C", 0, 3), true) // lies: always more
	})
	defer ts.Close()

	got, err := fb.List(context.Background(), backend.ListOpts{})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if calls > maxListPages {
		t.Fatalf("calls = %d, want <= maxListPages (%d)", calls, maxListPages)
	}
	if len(got) == 0 {
		t.Fatal("no issues returned")
	}
}

// The bare-array dialect carries no envelope, so it is by definition complete.
func TestList_BareArrayIsOnePage(t *testing.T) {
	calls := 0
	fb, ts := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		respondOK(w, pagedIssues("D", 0, 4))
	})
	defer ts.Close()

	got, err := fb.List(context.Background(), backend.ListOpts{})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 4 || calls != 1 {
		t.Fatalf("len = %d, calls = %d; want 4 and 1", len(got), calls)
	}
}
