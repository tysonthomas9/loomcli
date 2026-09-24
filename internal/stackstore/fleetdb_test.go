package stackstore

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/infra/fleetdb"
	sl "github.com/tysonthomas9/loomcli/internal/stacklineage"
)

// stub-server helpers ----------------------------------------------------------

// TestMain drops UpdateNode's retry backoff so lost-race tests run instantly.
func TestMain(m *testing.M) {
	updateNodeBackoff = func(int) time.Duration { return 0 }
	os.Exit(m.Run())
}

func newStubFleetDB(t *testing.T, h http.Handler) *FleetDBStore {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	c, err := fleetdb.New(fleetdb.Config{BaseURL: srv.URL})
	require.NoError(t, err)
	return NewFleetDB(c.Stacks())
}

// writeFleetErr writes fleet-db's structured error envelope
// ({"error":{"code":..,"message":..}}, internal/api WriteError).
func writeFleetErr(w http.ResponseWriter, status int, code, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]string{"code": code, "message": msg}})
}

func writeFleetJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// mapFleetErr ---------------------------------------------------------------------

func TestMapFleetErr(t *testing.T) {
	cases := []struct {
		name     string
		status   int
		code     string
		msg      string
		sentinel error // stackstore/stacklineage sentinel; nil = pass-through
		domain   error // generic classification kept in the chain; nil = not asserted
	}{
		{"stack not found", 404, "not_found", "stack not found", ErrStackNotFound, domain.ErrNotFound},
		{"stack node not found", 404, "not_found", "stack node not found", ErrNodeNotFound, domain.ErrNotFound},
		{"node exists", 409, "already_exists", "task is already in the stack", ErrNodeExists, domain.ErrAlreadyExists},
		{"node terminal", 409, "invalid_transition", "stack node is merged and cannot be changed", ErrNodeTerminal, domain.ErrInvalidTransition},
		{"contention", 409, "conflict", "stack is being modified concurrently; retry", ErrConcurrentUpdate, domain.ErrConflict},
		{"revision mismatch", 412, "precondition_failed", "stack node changed since it was read", ErrConcurrentUpdate, domain.ErrConflict},
		{"lineage cycle", 422, "validation_failed", "invalid stack lineage: stack lineage cycle detected", sl.ErrCycle, domain.ErrInvalid},
		{"lineage missing base", 422, "validation_failed", "invalid stack lineage: stack base task not found in stack", sl.ErrMissingPredecessor, domain.ErrInvalid},
		{"lineage branching", 422, "validation_failed", "invalid stack lineage: stack unit has multiple successors (chains must stay linear)", sl.ErrBranching, domain.ErrInvalid},
		{"lineage no root", 422, "validation_failed", "invalid stack lineage: stack lineage has no root unit", sl.ErrNoRoot, domain.ErrInvalid},
		// Unmapped: pass through unchanged.
		{"workspace not found", 404, "not_found", "workspace not found", nil, domain.ErrNotFound},
		{"lineage branch clash", 422, "validation_failed", "invalid stack lineage: stack output branch is assigned to more than one unit", nil, domain.ErrInvalid},
		{"generic validation", 400, "validation_failed", "state \"bogus\" is invalid", nil, domain.ErrInvalid},
		{"unavailable", 503, "stack_inconsistent", "stack change was recorded but not yet applied; re-read before retrying", nil, nil},
		{"internal", 500, "internal_error", "boom", nil, nil},
	}
	allSentinels := []error{
		ErrStackNotFound, ErrNodeNotFound, ErrNodeExists, ErrNodeTerminal, ErrConcurrentUpdate,
		sl.ErrCycle, sl.ErrMissingPredecessor, sl.ErrBranching, sl.ErrNoRoot,
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := newStubFleetDB(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				writeFleetErr(w, tc.status, tc.code, tc.msg)
			}))
			_, err := s.GetStack(context.Background(), "WS", "epic:E1")
			require.Error(t, err)

			var apiErr *fleetdb.StackAPIError
			require.ErrorAs(t, err, &apiErr, "the fleet-db error stays in the chain")
			assert.Equal(t, tc.status, apiErr.Status)
			assert.Equal(t, tc.code, apiErr.Code)
			assert.Equal(t, tc.msg, apiErr.Message)
			if tc.domain != nil {
				assert.ErrorIs(t, err, tc.domain)
			}
			for _, s := range allSentinels {
				if s == tc.sentinel {
					assert.ErrorIs(t, err, s)
				} else {
					assert.NotErrorIs(t, err, s)
				}
			}
			if tc.sentinel == nil {
				// Pass-through returns the client error itself.
				assert.Same(t, apiErr, err)
			}
		})
	}
}

func TestMapFleetErr_NilAndNonAPI(t *testing.T) {
	assert.NoError(t, mapFleetErr(nil))
	plain := errors.New("dial tcp: refused")
	assert.Same(t, plain, mapFleetErr(plain))
}

// UpdateNode ------------------------------------------------------------------------

// fakeNodeAPI serves ListNodes for one stack and records PATCHes. onPatch
// decides each PATCH's outcome; the default applies it as a CAS.
type fakeNodeAPI struct {
	mu      sync.Mutex
	nodes   []fleetdb.StackNodeWire
	lists   int
	patches []map[string]any
	onPatch func(f *fakeNodeAPI, i int, body map[string]any, w http.ResponseWriter) bool // true = handled
}

func (f *fakeNodeAPI) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	switch {
	case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/nodes"):
		f.lists++
		writeFleetJSON(w, 200, map[string]any{"nodes": f.nodes, "count": len(f.nodes)})
	case r.Method == http.MethodPatch:
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeFleetErr(w, 400, "invalid_json", err.Error())
			return
		}
		i := len(f.patches)
		f.patches = append(f.patches, body)
		if f.onPatch != nil && f.onPatch(f, i, body, w) {
			return
		}
		f.applyPatch(r, body, w)
	default:
		writeFleetErr(w, 405, "method_not_allowed", r.Method+" "+r.URL.Path)
	}
}

func (f *fakeNodeAPI) node(taskID string) *fleetdb.StackNodeWire {
	for i := range f.nodes {
		if f.nodes[i].TaskID == taskID {
			return &f.nodes[i]
		}
	}
	return nil
}

func (f *fakeNodeAPI) applyPatch(r *http.Request, body map[string]any, w http.ResponseWriter) {
	taskID := r.PathValue("task_id")
	if taskID == "" {
		parts := strings.Split(r.URL.Path, "/")
		taskID = parts[len(parts)-1]
	}
	n := f.node(taskID)
	if n == nil {
		writeFleetErr(w, 404, "not_found", "stack node not found")
		return
	}
	if rev, ok := body["expected_revision"].(float64); ok && int64(rev) != n.Revision {
		writeFleetErr(w, 412, "precondition_failed", "stack node changed since it was read")
		return
	}
	if v, ok := body["state"].(string); ok {
		n.State = v
	}
	if v, ok := body["pr_number"].(float64); ok {
		n.PRNumber = int(v)
	}
	if v, ok := body["pr_url"].(string); ok {
		n.PRURL = v
	}
	if v, ok := body["output_sha"].(string); ok {
		n.OutputSHA = v
	}
	if v, ok := body["commit_mode"].(string); ok {
		n.CommitMode = v
	}
	if v, ok := body["last_published_at"].(string); ok {
		ts, err := time.Parse(time.RFC3339Nano, v)
		if err != nil {
			writeFleetErr(w, 400, "invalid_json", err.Error())
			return
		}
		n.LastPublishedAt = &ts
	}
	n.Revision++
	writeFleetJSON(w, 200, n)
}

func newFakeNodeAPI() *fakeNodeAPI {
	return &fakeNodeAPI{nodes: []fleetdb.StackNodeWire{
		{TaskID: "T1", OutputBranch: "loom/stack/epic-E1/T1", CommitMode: "loom_commit", State: "pending", Revision: 7},
		{TaskID: "T2", BaseTaskID: "T1", OutputBranch: "loom/stack/epic-E1/T2", CommitMode: "loom_commit", State: "pending", Revision: 3},
	}}
}

func patchKeys(body map[string]any) []string {
	keys := make([]string, 0, len(body))
	for k := range body {
		keys = append(keys, k)
	}
	return keys
}

func TestFleetDBUpdateNode_PatchCarriesOnlyChangedFields(t *testing.T) {
	api := newFakeNodeAPI()
	s := newStubFleetDB(t, api)
	published := time.Date(2026, 9, 1, 12, 0, 0, 0, time.FixedZone("X", 3600))

	require.NoError(t, s.UpdateNode(context.Background(), "WS", "epic:E1", "T1", func(n *sl.Node) error {
		n.State = sl.NodeStatePublished
		n.PRNumber = 42
		n.CommitMode = sl.CommitModeLoom // unchanged value: must not be sent
		n.LastPublishedAt = &published
		return nil
	}))

	require.Len(t, api.patches, 1)
	body := api.patches[0]
	assert.ElementsMatch(t, []string{"state", "pr_number", "last_published_at", "expected_revision"}, patchKeys(body))
	assert.Equal(t, "published", body["state"])
	assert.EqualValues(t, 42, body["pr_number"])
	assert.EqualValues(t, 7, body["expected_revision"])
	assert.Equal(t, "2026-09-01T11:00:00Z", body["last_published_at"], "timestamp is sent in UTC")
}

func TestFleetDBUpdateNode_NoPatchWhenUnchanged(t *testing.T) {
	api := newFakeNodeAPI()
	s := newStubFleetDB(t, api)
	calls := 0
	require.NoError(t, s.UpdateNode(context.Background(), "WS", "epic:E1", "T1", func(n *sl.Node) error {
		calls++
		n.State = sl.NodeStatePending // same value
		return nil
	}))
	assert.Equal(t, 1, calls)
	assert.Empty(t, api.patches)
}

func TestFleetDBUpdateNode_SameLastPublishedAtIsNoop(t *testing.T) {
	api := newFakeNodeAPI()
	ts := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	api.nodes[0].LastPublishedAt = &ts
	s := newStubFleetDB(t, api)
	require.NoError(t, s.UpdateNode(context.Background(), "WS", "epic:E1", "T1", func(n *sl.Node) error {
		same := ts.In(time.FixedZone("Y", -7200))
		n.LastPublishedAt = &same
		return nil
	}))
	assert.Empty(t, api.patches)
}

func TestFleetDBUpdateNode_FnMutatingTimestampDoesNotAliasCurrent(t *testing.T) {
	// fn gets a copy of LastPublishedAt; mutating it in place must be seen as
	// a change (the diff base must not alias the pointer fn received).
	api := newFakeNodeAPI()
	ts := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	api.nodes[0].LastPublishedAt = &ts
	s := newStubFleetDB(t, api)
	require.NoError(t, s.UpdateNode(context.Background(), "WS", "epic:E1", "T1", func(n *sl.Node) error {
		*n.LastPublishedAt = n.LastPublishedAt.Add(time.Hour)
		return nil
	}))
	require.Len(t, api.patches, 1)
	assert.Equal(t, "2026-09-01T13:00:00Z", api.patches[0]["last_published_at"])
}

func TestFleetDBUpdateNode_RetriesAfterLostRace(t *testing.T) {
	for _, loss := range []struct {
		name   string
		status int
		code   string
	}{
		{"412 precondition", 412, "precondition_failed"},
		{"409 contention", 409, "conflict"},
	} {
		t.Run(loss.name, func(t *testing.T) {
			api := newFakeNodeAPI()
			api.nodes[0].PRNumber = 10
			api.onPatch = func(f *fakeNodeAPI, i int, _ map[string]any, w http.ResponseWriter) bool {
				if i > 0 {
					return false
				}
				// Another writer commits first: PRNumber 10 → 11, revision 7 → 8.
				n := f.node("T1")
				n.PRNumber = 11
				n.Revision = 8
				writeFleetErr(w, loss.status, loss.code, "lost race")
				return true
			}
			s := newStubFleetDB(t, api)
			var seen []int
			require.NoError(t, s.UpdateNode(context.Background(), "WS", "epic:E1", "T1", func(n *sl.Node) error {
				seen = append(seen, n.PRNumber)
				n.PRNumber++
				return nil
			}))
			assert.Equal(t, []int{10, 11}, seen, "fn re-applied to the fresh read")
			assert.Equal(t, 2, api.lists, "re-read after the lost race")
			require.Len(t, api.patches, 2)
			assert.EqualValues(t, 7, api.patches[0]["expected_revision"])
			assert.EqualValues(t, 8, api.patches[1]["expected_revision"])
			assert.EqualValues(t, 12, api.patches[1]["pr_number"])
			assert.Equal(t, 12, api.node("T1").PRNumber)
		})
	}
}

func TestFleetDBUpdateNode_GivesUpAfterRepeatedLostRaces(t *testing.T) {
	api := newFakeNodeAPI()
	api.onPatch = func(f *fakeNodeAPI, _ int, _ map[string]any, w http.ResponseWriter) bool {
		f.node("T1").Revision++
		writeFleetErr(w, 412, "precondition_failed", "stack node changed since it was read")
		return true
	}
	s := newStubFleetDB(t, api)
	calls := 0
	err := s.UpdateNode(context.Background(), "WS", "epic:E1", "T1", func(n *sl.Node) error {
		calls++
		n.OutputSHA = "abc"
		return nil
	})
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrConcurrentUpdate)
	var apiErr *fleetdb.StackAPIError
	assert.ErrorAs(t, err, &apiErr)
	assert.Equal(t, updateNodeAttempts, calls)
	assert.Len(t, api.patches, updateNodeAttempts)
}

func TestFleetDBUpdateNode_NonRetryableWriteErrorIsMapped(t *testing.T) {
	api := newFakeNodeAPI()
	api.onPatch = func(_ *fakeNodeAPI, _ int, _ map[string]any, w http.ResponseWriter) bool {
		writeFleetErr(w, 409, "invalid_transition", "stack node is merged and cannot be changed")
		return true
	}
	s := newStubFleetDB(t, api)
	err := s.UpdateNode(context.Background(), "WS", "epic:E1", "T1", func(n *sl.Node) error {
		n.State = sl.NodeStateClosed
		return nil
	})
	assert.ErrorIs(t, err, ErrNodeTerminal)
	assert.Len(t, api.patches, 1, "no retry for a non-race failure")
}

func TestFleetDBUpdateNode_UnsupportedUpdates(t *testing.T) {
	ts := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	cases := []struct {
		name string
		fn   func(*sl.Node)
	}{
		{"base task", func(n *sl.Node) { n.BaseTaskID = "" }},
		{"output branch", func(n *sl.Node) { n.OutputBranch = "other" }},
		{"task id", func(n *sl.Node) { n.TaskID = "T9" }},
		{"stack id", func(n *sl.Node) { n.StackID = "epic:other" }},
		{"clear last published", func(n *sl.Node) { n.LastPublishedAt = nil }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			api := newFakeNodeAPI()
			api.nodes[1].LastPublishedAt = &ts
			s := newStubFleetDB(t, api)
			err := s.UpdateNode(context.Background(), "WS", "epic:E1", "T2", func(n *sl.Node) error {
				n.PRNumber = 5 // a legit change alongside must not be written either
				tc.fn(n)
				return nil
			})
			assert.ErrorIs(t, err, ErrUnsupportedUpdate)
			assert.Empty(t, api.patches, "nothing written")
		})
	}
}

func TestFleetDBUpdateNode_FnErrorPropagatesWithoutWrite(t *testing.T) {
	api := newFakeNodeAPI()
	s := newStubFleetDB(t, api)
	boom := errors.New("boom")
	err := s.UpdateNode(context.Background(), "WS", "epic:E1", "T1", func(n *sl.Node) error {
		n.PRNumber = 99
		return boom
	})
	assert.ErrorIs(t, err, boom)
	assert.Empty(t, api.patches)
}

func TestFleetDBUpdateNode_Missing(t *testing.T) {
	t.Run("node", func(t *testing.T) {
		api := newFakeNodeAPI()
		s := newStubFleetDB(t, api)
		called := false
		err := s.UpdateNode(context.Background(), "WS", "epic:E1", "ghost", func(*sl.Node) error {
			called = true
			return nil
		})
		assert.ErrorIs(t, err, ErrNodeNotFound)
		assert.False(t, called)
		assert.Empty(t, api.patches)
	})
	t.Run("stack", func(t *testing.T) {
		s := newStubFleetDB(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			writeFleetErr(w, 404, "not_found", "stack not found")
		}))
		err := s.UpdateNode(context.Background(), "WS", "epic:E1", "T1", func(*sl.Node) error { return nil })
		assert.ErrorIs(t, err, ErrStackNotFound)
	})
}

// planMove ----------------------------------------------------------------------------

// chainNodes builds nodes from "task:base" specs ("T1:" = root).
func chainNodes(specs ...string) []sl.Node {
	out := make([]sl.Node, 0, len(specs))
	for _, s := range specs {
		task, base, _ := strings.Cut(s, ":")
		out = append(out, sl.Node{StackID: "epic:E1", TaskID: task, BaseTaskID: base, State: sl.NodeStatePending})
	}
	return out
}

func baseMap(nodes []sl.Node) map[string]string {
	m := make(map[string]string, len(nodes))
	for _, n := range nodes {
		m[n.TaskID] = n.BaseTaskID
	}
	return m
}

func nodesWithBases(nodes []sl.Node, bases map[string]string) []sl.Node {
	out := make([]sl.Node, 0, len(nodes))
	for _, n := range nodes {
		n.BaseTaskID = bases[n.TaskID]
		out = append(out, n)
	}
	return out
}

// localMoveResult runs LocalStore.MoveNode on the same lineage.
func localMoveResult(t *testing.T, nodes []sl.Node, taskID, after string) map[string]string {
	t.Helper()
	ctx := context.Background()
	s := New(t.TempDir())
	seedStack(t, s)
	ordered, err := sl.Ordered(nodes)
	require.NoError(t, err)
	for _, n := range ordered {
		_, err := s.AddNode(ctx, ws, "epic:E1", n.TaskID, n.BaseTaskID, "")
		require.NoError(t, err)
	}
	require.NoError(t, s.MoveNode(ctx, ws, "epic:E1", taskID, after))
	return baseMap(mustNodes(t, s))
}

func TestPlanMove(t *testing.T) {
	chain4 := []string{"T1:", "T2:T1", "T3:T2", "T4:T3"}
	cases := []struct {
		name      string
		specs     []string
		merged    []string
		task      string
		after     string
		wantOrder []string // lineage order of the result (single chain cases)
	}{
		{name: "middle later", specs: chain4, task: "T2", after: "T4", wantOrder: []string{"T1", "T3", "T4", "T2"}},
		{name: "after own successor", specs: chain4, task: "T2", after: "T3", wantOrder: []string{"T1", "T3", "T2", "T4"}},
		{name: "middle earlier", specs: chain4, task: "T3", after: "T1", wantOrder: []string{"T1", "T3", "T2", "T4"}},
		{name: "tail after root", specs: chain4, task: "T4", after: "T1", wantOrder: []string{"T1", "T4", "T2", "T3"}},
		{name: "root", specs: chain4, task: "T1", after: "T3", wantOrder: []string{"T2", "T3", "T1", "T4"}},
		{name: "root after own successor", specs: chain4, task: "T1", after: "T2", wantOrder: []string{"T2", "T1", "T3", "T4"}},
		{name: "root to after tail", specs: chain4, task: "T1", after: "T4", wantOrder: []string{"T2", "T3", "T4", "T1"}},
		{name: "tail neighbor to after tail", specs: chain4, task: "T3", after: "T4", wantOrder: []string{"T1", "T2", "T4", "T3"}},
		{name: "merged root untouched", specs: chain4, merged: []string{"T1"}, task: "T4", after: "T1", wantOrder: []string{"T1", "T4", "T2", "T3"}},
		{name: "two nodes swap", specs: []string{"T1:", "T2:T1"}, task: "T1", after: "T2", wantOrder: []string{"T2", "T1"}},
		{name: "across parallel chains", specs: []string{"A1:", "A2:A1", "B1:", "B2:B1"}, task: "B1", after: "A1"},
		{name: "chain tail onto other chain", specs: []string{"A1:", "A2:A1", "B1:", "B2:B1"}, task: "B2", after: "A2"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			nodes := chainNodes(tc.specs...)
			for i := range nodes {
				for _, m := range tc.merged {
					if nodes[i].TaskID == m {
						nodes[i].State = sl.NodeStateMerged
					}
				}
			}
			steps, err := planMove(nodes, tc.task, tc.after)
			require.NoError(t, err)
			require.NotEmpty(t, steps)

			bases := baseMap(nodes)
			byTask := sl.ByTask(nodes)
			for i, st := range steps {
				if byTask[st.taskID].State.Terminal() {
					assert.Equal(t, bases[st.taskID], st.base, "step %d retargets merged %s", i+1, st.taskID)
				}
				bases[st.taskID] = st.base
				_, err := sl.Ordered(nodesWithBases(nodes, bases))
				require.NoError(t, err, "lineage invalid after step %d (%+v) of %+v", i+1, st, steps)
			}
			assert.Equal(t, localMoveResult(t, nodes, tc.task, tc.after), bases, "final lineage matches LocalStore.MoveNode")
			if tc.wantOrder != nil {
				ordered, err := sl.Ordered(nodesWithBases(nodes, bases))
				require.NoError(t, err)
				got := make([]string, len(ordered))
				for i, n := range ordered {
					got[i] = n.TaskID
				}
				assert.Equal(t, tc.wantOrder, got)
			}
		})
	}
}

func TestPlanMove_AlreadyInPlace(t *testing.T) {
	nodes := chainNodes("T1:", "T2:T1", "T3:T2")
	steps, err := planMove(nodes, "T3", "T2")
	require.NoError(t, err)
	assert.Empty(t, steps)
}

func TestPlanMove_Rejections(t *testing.T) {
	chain := func(merged ...string) []sl.Node {
		nodes := chainNodes("T1:", "T2:T1", "T3:T2", "T4:T3")
		for i := range nodes {
			for _, m := range merged {
				if nodes[i].TaskID == m {
					nodes[i].State = sl.NodeStateMerged
				}
			}
		}
		return nodes
	}
	cases := []struct {
		name  string
		nodes []sl.Node
		task  string
		after string
		want  error
	}{
		{"merged node moved", chain("T2"), "T2", "T4", ErrNodeTerminal},
		{"merged successor rides to old base", chain("T3"), "T2", "T4", ErrNodeTerminal},
		{"merged after-successor rehung", chain("T2"), "T3", "T1", ErrNodeTerminal},
		{"unknown task", chain(), "ghost", "T1", ErrNodeNotFound},
		{"unknown after", chain(), "T1", "ghost", ErrNodeNotFound},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			steps, err := planMove(tc.nodes, tc.task, tc.after)
			assert.ErrorIs(t, err, tc.want)
			assert.Nil(t, steps)
		})
	}
}

// MoveNode over the wire -------------------------------------------------------------------

type recordedSetBase struct{ taskID, base string }

func moveStub(t *testing.T, nodes []fleetdb.StackNodeWire, failAt int) (*FleetDBStore, *[]recordedSetBase, *int) {
	t.Helper()
	var mu sync.Mutex
	var calls []recordedSetBase
	requests := 0
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/{workspace}/stacks/{stack_id}/nodes", func(w http.ResponseWriter, _ *http.Request) {
		mu.Lock()
		requests++
		mu.Unlock()
		writeFleetJSON(w, 200, map[string]any{"nodes": nodes})
	})
	mux.HandleFunc("PUT /api/v1/{workspace}/stacks/{stack_id}/nodes/{task_id}/base", func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		requests++
		var body struct {
			BaseTaskID string `json:"base_task_id"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		calls = append(calls, recordedSetBase{r.PathValue("task_id"), body.BaseTaskID})
		if len(calls) == failAt {
			writeFleetErr(w, 409, "conflict", "stack is being modified concurrently; retry")
			return
		}
		writeFleetJSON(w, 200, fleetdb.StackNodeWire{TaskID: r.PathValue("task_id"), BaseTaskID: body.BaseTaskID})
	})
	return newStubFleetDB(t, mux), &calls, &requests
}

func wireChain(states map[string]string) []fleetdb.StackNodeWire {
	var out []fleetdb.StackNodeWire
	prev := ""
	for _, id := range []string{"T1", "T2", "T3", "T4"} {
		st := states[id]
		if st == "" {
			st = "pending"
		}
		out = append(out, fleetdb.StackNodeWire{TaskID: id, BaseTaskID: prev, State: st})
		prev = id
	}
	return out
}

func TestFleetDBMoveNode_IssuesPlannedSetBases(t *testing.T) {
	s, calls, _ := moveStub(t, wireChain(nil), 0)
	require.NoError(t, s.MoveNode(context.Background(), "WS", "epic:E1", "T2", "T4"))
	assert.Equal(t, []recordedSetBase{{"T2", ""}, {"T3", "T1"}, {"T2", "T4"}}, *calls)
}

func TestFleetDBMoveNode_RejectsBeforeAnyWrite(t *testing.T) {
	t.Run("self", func(t *testing.T) {
		s, calls, requests := moveStub(t, wireChain(nil), 0)
		assert.ErrorIs(t, s.MoveNode(context.Background(), "WS", "epic:E1", "T2", "T2"), sl.ErrCycle)
		assert.Empty(t, *calls)
		assert.Zero(t, *requests, "rejected without a round trip")
	})
	t.Run("merged retarget", func(t *testing.T) {
		s, calls, _ := moveStub(t, wireChain(map[string]string{"T2": "merged"}), 0)
		assert.ErrorIs(t, s.MoveNode(context.Background(), "WS", "epic:E1", "T2", "T4"), ErrNodeTerminal)
		assert.Empty(t, *calls)
	})
	t.Run("already in place", func(t *testing.T) {
		s, calls, _ := moveStub(t, wireChain(nil), 0)
		require.NoError(t, s.MoveNode(context.Background(), "WS", "epic:E1", "T3", "T2"))
		assert.Empty(t, *calls)
	})
}

func TestFleetDBMoveNode_PartialFailure(t *testing.T) {
	t.Run("first step fails unwrapped", func(t *testing.T) {
		s, _, _ := moveStub(t, wireChain(nil), 1)
		err := s.MoveNode(context.Background(), "WS", "epic:E1", "T2", "T4")
		assert.ErrorIs(t, err, ErrConcurrentUpdate)
		assert.NotContains(t, err.Error(), "stopped at step")
	})
	t.Run("later step reports progress", func(t *testing.T) {
		s, calls, _ := moveStub(t, wireChain(nil), 2)
		err := s.MoveNode(context.Background(), "WS", "epic:E1", "T2", "T4")
		assert.ErrorIs(t, err, ErrConcurrentUpdate)
		assert.Contains(t, err.Error(), "stopped at step 2 of 3")
		assert.Len(t, *calls, 2, "stops at the failed step")
	})
}

// path encoding ------------------------------------------------------------------------------

func TestFleetDBStore_EscapesPathSegments(t *testing.T) {
	const stackID, taskID = "manual:repo/feature x", "T/1:a"
	var mu sync.Mutex
	var seen []string // escaped request paths
	var gotStack, gotTask []string
	mux := http.NewServeMux()
	record := func(r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		seen = append(seen, r.URL.EscapedPath())
		gotStack = append(gotStack, r.PathValue("stack_id"))
		if tid := r.PathValue("task_id"); tid != "" {
			gotTask = append(gotTask, tid)
		}
	}
	mux.HandleFunc("GET /api/v1/{workspace}/stacks/{stack_id}", func(w http.ResponseWriter, r *http.Request) {
		record(r)
		writeFleetJSON(w, 200, fleetdb.StackWire{ID: stackID, WorkspaceKey: "WS"})
	})
	mux.HandleFunc("GET /api/v1/{workspace}/stacks/{stack_id}/nodes", func(w http.ResponseWriter, r *http.Request) {
		record(r)
		writeFleetJSON(w, 200, map[string]any{"nodes": []fleetdb.StackNodeWire{{TaskID: taskID, State: "pending", Revision: 1}}})
	})
	mux.HandleFunc("PATCH /api/v1/{workspace}/stacks/{stack_id}/nodes/{task_id}", func(w http.ResponseWriter, r *http.Request) {
		record(r)
		writeFleetJSON(w, 200, fleetdb.StackNodeWire{TaskID: taskID})
	})
	mux.HandleFunc("PUT /api/v1/{workspace}/stacks/{stack_id}/nodes/{task_id}/base", func(w http.ResponseWriter, r *http.Request) {
		record(r)
		writeFleetJSON(w, 200, fleetdb.StackNodeWire{TaskID: taskID})
	})
	mux.HandleFunc("DELETE /api/v1/{workspace}/stacks/{stack_id}/nodes/{task_id}", func(w http.ResponseWriter, r *http.Request) {
		record(r)
		w.WriteHeader(http.StatusNoContent)
	})
	s := newStubFleetDB(t, mux)
	ctx := context.Background()

	st, err := s.GetStack(ctx, "WS", stackID)
	require.NoError(t, err)
	assert.Equal(t, sl.StackID(stackID), st.ID)
	require.NoError(t, s.UpdateNode(ctx, "WS", stackID, taskID, func(n *sl.Node) error { n.PRNumber = 1; return nil }))
	require.NoError(t, s.SetBase(ctx, "WS", stackID, taskID, ""))
	require.NoError(t, s.RemoveNode(ctx, "WS", stackID, taskID))

	require.Len(t, seen, 5, "every call routed to the stack_id pattern, none 404ed on an extra '/'")
	escStack := "/stacks/" + url.PathEscape(stackID)
	for _, p := range seen {
		assert.Contains(t, p, "/stacks/manual:repo%2Ffeature%20x", "'/' and ' ' in the stack ID are percent-encoded")
		assert.Contains(t, p, escStack)
	}
	for _, p := range seen[2:] {
		assert.Contains(t, p, "/nodes/T%2F1:a")
	}
	for _, got := range gotStack {
		assert.Equal(t, stackID, got, "server decodes the original stack ID")
	}
	for _, got := range gotTask {
		assert.Equal(t, taskID, got, "server decodes the original task ID")
	}
}
