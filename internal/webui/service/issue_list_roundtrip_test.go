package service

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/backend"
	"github.com/tysonthomas9/loomcli/internal/backend/fleet"
	"github.com/tysonthomas9/loomcli/internal/rpc"
)

// A characterisation test for the cost of one board refresh.
//
// The kanban board issues a single ListIssues with exclude_status=tombstone and
// include_blocked=true. Measured against the live fleet-db before this change,
// that one call cost ~69 HTTP requests: the list itself, three aux views, then a
// per-parent Get for every child of a tombstoned epic — each Get being three
// requests of its own (issue + deps + comments), with the deps rows triggering
// yet another summary lookup apiece.
//
// What the tests below pin is not a number but a shape: the request count must
// not grow with the number of issues or the number of distinct parents.

// fleetFixture is a tiny in-memory fleet-db: enough of the API surface for
// ListIssues to run end to end against a real fleet.FleetBackend, with every
// request counted by normalized path.
type fleetFixture struct {
	mu     sync.Mutex
	counts map[string]int

	issues   []map[string]interface{}          // the list response, in order
	storage  map[string]map[string]interface{} // every issue, listed or not
	deps     map[string][]map[string]string    // issue ID -> /deps rows
	blocked  []map[string]interface{}
	ready    []map[string]interface{}
	deferred []map[string]interface{}
}

// newFleetFixture builds n open issues plus k tombstoned parent epics. fleet-db
// returns the epics in the list response — its ListOpts has no exclude filter —
// and loomcli drops them client-side for exclude_status=tombstone. So every one
// of the n children references a parent that is in hand but not in the returned
// rows: exactly the case that used to cost one backfill Get per epic. Some
// issues carry dependency rows so the nested hydration path is exercised too.
func newFleetFixture(n, k int) *fleetFixture {
	f := &fleetFixture{
		counts:  map[string]int{},
		storage: map[string]map[string]interface{}{},
		deps:    map[string][]map[string]string{},
	}
	issue := func(id, title, status, parent string) map[string]interface{} {
		d := map[string]interface{}{"id": id, "title": title, "status": status, "priority": 2, "type": "task"}
		if parent != "" {
			d["parent_id"] = parent
		}
		f.storage[id] = d
		return d
	}
	for e := 0; e < k; e++ {
		epic := fmt.Sprintf("EPIC-%d", e)
		f.issues = append(f.issues, issue(epic, "Epic "+epic, "tombstone", ""))
	}
	for i := 0; i < n; i++ {
		id := fmt.Sprintf("T-%d", i)
		parent := ""
		if k > 0 {
			parent = fmt.Sprintf("EPIC-%d", i%k)
		}
		f.issues = append(f.issues, issue(id, "Task "+id, "open", parent))
		if i%5 == 0 && i > 0 {
			// A dependency row on an earlier issue. fleet-db serializes only
			// the edge, so every row has an empty title.
			f.deps[id] = []map[string]string{
				{"issue_id": id, "depends_on_id": fmt.Sprintf("T-%d", i-1), "type": "blocks"},
			}
		}
	}
	return f
}

func (f *fleetFixture) count(path string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.counts[path]++
}

func (f *fleetFixture) total() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	total := 0
	for _, c := range f.counts {
		total += c
	}
	return total
}

func (f *fleetFixture) snapshot() map[string]int {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make(map[string]int, len(f.counts))
	for k, v := range f.counts {
		out[k] = v
	}
	return out
}

func (f *fleetFixture) writeOK(w http.ResponseWriter, data interface{}) {
	raw, _ := json.Marshal(data)
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{"success": true, "data": json.RawMessage(raw)})
}

const fixturePrefix = "/api/v1/test-ws"

func (f *fleetFixture) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, fixturePrefix)
	f.count(path)

	switch path {
	case "/issues":
		f.writeOK(w, f.issues)
		return
	case "/issues/blocked":
		f.writeOK(w, f.blocked)
		return
	case "/issues/ready":
		f.writeOK(w, f.ready)
		return
	case "/issues/deferred":
		f.writeOK(w, f.deferred)
		return
	}

	switch {
	case strings.HasSuffix(path, "/deps"):
		id := strings.TrimSuffix(strings.TrimPrefix(path, "/issues/"), "/deps")
		f.writeOK(w, map[string]interface{}{"dependencies": f.deps[id]})
	case strings.HasSuffix(path, "/comments"):
		f.writeOK(w, map[string]interface{}{"comments": []interface{}{}})
	case strings.HasPrefix(path, "/issues/"):
		id := strings.TrimPrefix(path, "/issues/")
		if issue, ok := f.storage[id]; ok {
			f.writeOK(w, issue)
			return
		}
		w.WriteHeader(http.StatusNotFound)
		f.writeOK(w, nil)
	default:
		http.Error(w, "unexpected path "+path, http.StatusNotFound)
	}
}

// newFixtureService wires a real fleet backend at ts.URL into an issue service.
func newFixtureService(t *testing.T, url string) IssueService {
	t.Helper()
	fb, err := fleet.New(fleet.Config{BaseURL: url, WorkspaceID: "test-ws", AuthToken: "test-token"})
	if err != nil {
		t.Fatalf("fleet.New: %v", err)
	}
	return NewIssueServiceWithBackend(nil, nil, nil, func(_ context.Context) backend.IssueBackend { return fb })
}

// listCost runs one board-shaped ListIssues against a fresh fixture and returns
// the number of HTTP requests it cost.
func listCost(t *testing.T, n, k int, includeBlocked bool) (int, map[string]int) {
	t.Helper()
	f := newFleetFixture(n, k)
	ts := httptest.NewServer(f)
	defer ts.Close()

	svc := newFixtureService(t, ts.URL)
	res, err := svc.ListIssues(context.Background(), ListIssuesParams{
		Args:           &rpc.ListArgs{Limit: 500},
		ExcludeStatus:  []string{"tombstone"},
		IncludeBlocked: includeBlocked,
	})
	if err != nil {
		t.Fatalf("ListIssues(n=%d,k=%d): %v", n, k, err)
	}
	got := len(res.Issues) + len(res.KanbanIssues)
	if got != n {
		t.Fatalf("ListIssues(n=%d,k=%d) returned %d rows, want %d", n, k, got, n)
	}
	return f.total(), f.snapshot()
}

func TestListIssues_RoundTripCount_DoesNotScaleWithIssueCount(t *testing.T) {
	// The board's own call shape: kanban, tombstones excluded.
	const kanbanCeiling = 8 // list + blocked + ready + deferred, with headroom

	small, smallBy := listCost(t, 20, 3, true)
	large, largeBy := listCost(t, 200, 9, true)
	t.Logf("kanban round-trips: 20 issues/3 parents = %d, 200 issues/9 parents = %d "+
		"(13 and 31 on the pre-fix code)", small, large)

	if small != large {
		t.Errorf("kanban round-trips scale with the result set: 20 issues/3 parents = %d (%v), "+
			"200 issues/9 parents = %d (%v)", small, smallBy, large, largeBy)
	}
	if large > kanbanCeiling {
		t.Errorf("kanban list cost %d round-trips (%v), want <= %d", large, largeBy, kanbanCeiling)
	}

	// The plain list path skips the three aux views.
	const plainCeiling = kanbanCeiling - 3

	smallPlain, smallPlainBy := listCost(t, 20, 3, false)
	largePlain, largePlainBy := listCost(t, 200, 9, false)
	t.Logf("plain-list round-trips: 20 issues/3 parents = %d, 200 issues/9 parents = %d "+
		"(10 and 28 on the pre-fix code)", smallPlain, largePlain)

	if smallPlain != largePlain {
		t.Errorf("plain-list round-trips scale with the result set: 20 issues/3 parents = %d (%v), "+
			"200 issues/9 parents = %d (%v)", smallPlain, smallPlainBy, largePlain, largePlainBy)
	}
	if largePlain > plainCeiling {
		t.Errorf("plain list cost %d round-trips (%v), want <= %d", largePlain, largePlainBy, plainCeiling)
	}
}
