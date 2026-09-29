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

// Atomic MoveNode over the wire -----------------------------------------------------------

type recordedMove struct {
	taskID           string
	afterTaskID      string
	expectedRevision *int64
}

// moveStub serves GET stack + POST .../move against an in-memory chain.
// staleFirst, when true, answers the first Move with 412 then succeeds.
func moveStub(t *testing.T, nodes []fleetdb.StackNodeWire, revision int64, staleFirst bool) (*FleetDBStore, *[]recordedMove, *int) {
	t.Helper()
	var mu sync.Mutex
	var calls []recordedMove
	requests := 0
	live := append([]fleetdb.StackNodeWire(nil), nodes...)
	rev := revision
	staleArmed := staleFirst
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/{workspace}/stacks/{stack_id}", func(w http.ResponseWriter, _ *http.Request) {
		mu.Lock()
		requests++
		snapshot := append([]fleetdb.StackNodeWire(nil), live...)
		curRev := rev
		mu.Unlock()
		writeFleetJSON(w, 200, fleetdb.StackWire{
			ID: "epic:E1", WorkspaceKey: "WS", Revision: curRev, Nodes: snapshot,
		})
	})
	mux.HandleFunc("GET /api/v1/{workspace}/stacks/{stack_id}/nodes", func(w http.ResponseWriter, _ *http.Request) {
		mu.Lock()
		requests++
		snapshot := append([]fleetdb.StackNodeWire(nil), live...)
		mu.Unlock()
		writeFleetJSON(w, 200, map[string]any{"nodes": snapshot})
	})
	mux.HandleFunc("POST /api/v1/{workspace}/stacks/{stack_id}/nodes/{task_id}/move", func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		requests++
		var body fleetdb.StackMoveReq
		_ = json.NewDecoder(r.Body).Decode(&body)
		taskID := r.PathValue("task_id")
		calls = append(calls, recordedMove{taskID, body.AfterTaskID, body.ExpectedRevision})
		if body.ExpectedRevision != nil && *body.ExpectedRevision != rev {
			writeFleetErr(w, 412, "precondition_failed", "stack changed since it was read")
			return
		}
		if staleArmed {
			staleArmed = false
			rev++ // peer writer advanced the document
			writeFleetErr(w, 412, "precondition_failed", "stack changed since it was read")
			return
		}
		moved, errStatus, errCode, errMsg := applyAtomicMove(live, taskID, body.AfterTaskID)
		if errStatus != 0 {
			writeFleetErr(w, errStatus, errCode, errMsg)
			return
		}
		if moved {
			rev++
		}
		var node fleetdb.StackNodeWire
		for _, n := range live {
			if n.TaskID == taskID {
				node = n
				break
			}
		}
		writeFleetJSON(w, 200, fleetdb.StackMoveResult{
			Revision: rev,
			Node:     node,
			Nodes:    append([]fleetdb.StackNodeWire(nil), live...),
		})
	})
	return newStubFleetDB(t, mux), &calls, &requests
}

// applyAtomicMove mutates live to match fleet-db MoveStackNode semantics.
// Returns (wrote, status, code, msg); status 0 means success.
func applyAtomicMove(live []fleetdb.StackNodeWire, taskID, afterTaskID string) (bool, int, string, string) {
	if taskID == afterTaskID {
		return false, 422, "validation_failed", "invalid stack lineage: stack lineage cycle detected"
	}
	var nodeIdx, afterIdx = -1, -1
	for i, n := range live {
		if n.TaskID == taskID {
			nodeIdx = i
		}
		if n.TaskID == afterTaskID {
			afterIdx = i
		}
	}
	if nodeIdx < 0 || afterIdx < 0 {
		return false, 404, "not_found", "stack node not found"
	}
	if live[nodeIdx].BaseTaskID == afterTaskID {
		return false, 0, "", "" // no-op, including when the node is already merged
	}
	if live[nodeIdx].State == "merged" {
		return false, 409, "invalid_transition", "stack node is merged and cannot be changed"
	}
	oldBase := live[nodeIdx].BaseTaskID
	for i := range live {
		if live[i].TaskID != taskID && live[i].BaseTaskID == taskID {
			live[i].BaseTaskID = oldBase
		}
	}
	for i := range live {
		if live[i].TaskID != taskID && live[i].BaseTaskID == afterTaskID {
			live[i].BaseTaskID = taskID
		}
	}
	live[nodeIdx].BaseTaskID = afterTaskID
	return true, 0, "", ""
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

func baseMap(nodes []sl.Node) map[string]string {
	m := make(map[string]string, len(nodes))
	for _, n := range nodes {
		m[n.TaskID] = n.BaseTaskID
	}
	return m
}

func TestFleetDBMoveNode_AtomicEndpoint(t *testing.T) {
	s, calls, _ := moveStub(t, wireChain(nil), 7, false)
	require.NoError(t, s.MoveNode(context.Background(), "WS", "epic:E1", "T2", "T4"))
	require.Len(t, *calls, 1)
	assert.Equal(t, "T2", (*calls)[0].taskID)
	assert.Equal(t, "T4", (*calls)[0].afterTaskID)
	require.NotNil(t, (*calls)[0].expectedRevision)
	assert.EqualValues(t, 7, *(*calls)[0].expectedRevision)

	nodes, err := s.ListNodes(context.Background(), "WS", "epic:E1")
	require.NoError(t, err)
	assert.Equal(t, map[string]string{"T1": "", "T3": "T1", "T4": "T3", "T2": "T4"}, baseMap(nodes))
}

func TestFleetDBMoveNode_RejectsSelfWithoutRoundTrip(t *testing.T) {
	s, calls, requests := moveStub(t, wireChain(nil), 1, false)
	assert.ErrorIs(t, s.MoveNode(context.Background(), "WS", "epic:E1", "T2", "T2"), sl.ErrCycle)
	assert.Empty(t, *calls)
	assert.Zero(t, *requests, "rejected without a round trip")
}

func TestFleetDBMoveNode_MergedRealMoveVsNoOp(t *testing.T) {
	t.Run("real move of merged node is terminal", func(t *testing.T) {
		s, calls, _ := moveStub(t, wireChain(map[string]string{"T2": "merged"}), 3, false)
		err := s.MoveNode(context.Background(), "WS", "epic:E1", "T2", "T4")
		assert.ErrorIs(t, err, ErrNodeTerminal)
		require.Len(t, *calls, 1)
		assert.Equal(t, "T2", (*calls)[0].taskID)
		assert.Equal(t, "T4", (*calls)[0].afterTaskID)
	})
	t.Run("already-positioned merged node is a no-op", func(t *testing.T) {
		s, calls, _ := moveStub(t, wireChain(map[string]string{"T3": "merged"}), 5, false)
		require.NoError(t, s.MoveNode(context.Background(), "WS", "epic:E1", "T3", "T2"))
		require.Len(t, *calls, 1)
		nodes, err := s.ListNodes(context.Background(), "WS", "epic:E1")
		require.NoError(t, err)
		assert.Equal(t, map[string]string{"T1": "", "T2": "T1", "T3": "T2", "T4": "T3"}, baseMap(nodes))
	})
	t.Run("already in place pending is a no-op", func(t *testing.T) {
		s, calls, _ := moveStub(t, wireChain(nil), 2, false)
		require.NoError(t, s.MoveNode(context.Background(), "WS", "epic:E1", "T3", "T2"))
		require.Len(t, *calls, 1)
	})
}

func TestFleetDBMoveNode_StaleRevisionRetries(t *testing.T) {
	s, calls, _ := moveStub(t, wireChain(nil), 10, true)
	require.NoError(t, s.MoveNode(context.Background(), "WS", "epic:E1", "T3", "T1"))
	require.Len(t, *calls, 2, "first move 412s; second succeeds with refreshed revision")
	require.NotNil(t, (*calls)[0].expectedRevision)
	require.NotNil(t, (*calls)[1].expectedRevision)
	assert.EqualValues(t, 10, *(*calls)[0].expectedRevision)
	assert.EqualValues(t, 11, *(*calls)[1].expectedRevision)

	nodes, err := s.ListNodes(context.Background(), "WS", "epic:E1")
	require.NoError(t, err)
	assert.Equal(t, map[string]string{"T1": "", "T3": "T1", "T2": "T3", "T4": "T2"}, baseMap(nodes))
}

func TestFleetDBMoveNode_MissingNode(t *testing.T) {
	s, _, _ := moveStub(t, wireChain(nil), 1, false)
	assert.ErrorIs(t, s.MoveNode(context.Background(), "WS", "epic:E1", "ghost", "T1"), ErrNodeNotFound)
	assert.ErrorIs(t, s.MoveNode(context.Background(), "WS", "epic:E1", "T1", "ghost"), ErrNodeNotFound)
}

// Fault injection: accepted Move + delayed projection / divergent follow-up -----

// moveOutcomeFaultStub serves Get + Move where the first Move returns 503
// stack_inconsistent (journal accepted). after503Get builds each subsequent
// Get response so tests can delay projection or diverge without a second Move.
// A 503 status replays stack_inconsistent, any other non-200 status answers
// with a generic internal_error, and getTransportFailure drops the connection.
func moveOutcomeFaultStub(t *testing.T, initial []fleetdb.StackNodeWire, revision int64, after503Get func(getN int, fencedRev int64) (nodes []fleetdb.StackNodeWire, rev int64, status int)) (*FleetDBStore, *[]recordedMove, func() int) {
	t.Helper()
	var mu sync.Mutex
	var calls []recordedMove
	getsAfter503 := 0
	moveDone := false
	fenced := revision
	live := append([]fleetdb.StackNodeWire(nil), initial...)
	rev := revision
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/{workspace}/stacks/{stack_id}", func(w http.ResponseWriter, _ *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		if !moveDone {
			writeFleetJSON(w, 200, fleetdb.StackWire{
				ID: "epic:E1", WorkspaceKey: "WS", Revision: rev,
				Nodes: append([]fleetdb.StackNodeWire(nil), live...),
			})
			return
		}
		getsAfter503++
		nodes, nextRev, status := after503Get(getsAfter503, fenced)
		switch {
		case status == getTransportFailure:
			hj, ok := w.(http.Hijacker)
			if !ok {
				t.Errorf("stub server cannot hijack connection")
				return
			}
			conn, _, err := hj.Hijack()
			if err != nil {
				t.Errorf("hijack: %v", err)
				return
			}
			_ = conn.Close()
			return
		case status == http.StatusServiceUnavailable:
			writeFleetErr(w, status, "stack_inconsistent", "stack change was recorded but not yet applied; re-read before retrying")
			return
		case status != 0 && status != 200:
			writeFleetErr(w, status, "internal_error", "follow-up read failed")
			return
		}
		writeFleetJSON(w, 200, fleetdb.StackWire{
			ID: "epic:E1", WorkspaceKey: "WS", Revision: nextRev,
			Nodes: append([]fleetdb.StackNodeWire(nil), nodes...),
		})
	})
	mux.HandleFunc("POST /api/v1/{workspace}/stacks/{stack_id}/nodes/{task_id}/move", func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		var body fleetdb.StackMoveReq
		_ = json.NewDecoder(r.Body).Decode(&body)
		taskID := r.PathValue("task_id")
		calls = append(calls, recordedMove{taskID, body.AfterTaskID, body.ExpectedRevision})
		if moveDone {
			t.Errorf("Move re-issued after stack_inconsistent; reconcile must be read-only")
			writeFleetErr(w, 500, "internal_error", "unexpected second move")
			return
		}
		if body.ExpectedRevision == nil || *body.ExpectedRevision != rev {
			writeFleetErr(w, 412, "precondition_failed", "stack changed since it was read")
			return
		}
		fenced = rev
		moveDone = true
		// Apply intended topology into the after503 callback's eventual state
		// by recording that the journal accepted the move; live stays stale
		// until after503Get projects it.
		writeFleetErr(w, 503, "stack_inconsistent", "stack change was recorded but not yet applied; re-read before retrying")
	})
	// Getter serializes with handler increments: a hijacked transport drop can
	// wake the client before the handler unlocks, so bare *int reads race.
	gets := func() int {
		mu.Lock()
		defer mu.Unlock()
		return getsAfter503
	}
	return newStubFleetDB(t, mux), &calls, gets
}

func TestFleetDBMoveNode_AcceptedWriteDelayedProjection(t *testing.T) {
	initial := wireChain(nil)
	projected := append([]fleetdb.StackNodeWire(nil), initial...)
	moved, status, code, msg := applyAtomicMove(projected, "T2", "T4")
	require.True(t, moved)
	require.Zero(t, status, "%s %s", code, msg)

	s, calls, gets := moveOutcomeFaultStub(t, initial, 7, func(getN int, fencedRev int64) ([]fleetdb.StackNodeWire, int64, int) {
		if getN < 3 {
			// Projection lag: still at the pre-move revision/topology.
			return append([]fleetdb.StackNodeWire(nil), initial...), fencedRev, 200
		}
		return append([]fleetdb.StackNodeWire(nil), projected...), fencedRev + 1, 200
	})
	require.NoError(t, s.MoveNode(context.Background(), "WS", "epic:E1", "T2", "T4"))
	require.Len(t, *calls, 1, "exactly one atomic Move; reconcile is Get-only")
	require.NotNil(t, (*calls)[0].expectedRevision)
	assert.EqualValues(t, 7, *(*calls)[0].expectedRevision)
	assert.GreaterOrEqual(t, gets(), 3)
}

func TestFleetDBMoveNode_AcceptedWriteDivergentFollowUp(t *testing.T) {
	initial := wireChain(nil)
	// Concurrent writer advanced the document to a different topology: T2 is
	// no longer after T4 (and not at the requested position).
	divergent := wireChain(nil)
	divergent[1].BaseTaskID = "T3" // T2 after T3, not T4

	s, calls, _ := moveOutcomeFaultStub(t, initial, 5, func(_ int, fencedRev int64) ([]fleetdb.StackNodeWire, int64, int) {
		return append([]fleetdb.StackNodeWire(nil), divergent...), fencedRev + 2, 200
	})
	err := s.MoveNode(context.Background(), "WS", "epic:E1", "T2", "T4")
	assert.ErrorIs(t, err, ErrUnknownWriteOutcome)
	require.Len(t, *calls, 1, "must not overwrite a concurrent writer with a second Move")
	assert.NotErrorIs(t, err, ErrConcurrentUpdate)
	assert.Contains(t, err.Error(), "re-read")
}

func TestFleetDBMoveNode_AcceptedWriteUnconfirmedProjection(t *testing.T) {
	initial := wireChain(nil)
	s, calls, gets := moveOutcomeFaultStub(t, initial, 3, func(_ int, fencedRev int64) ([]fleetdb.StackNodeWire, int64, int) {
		// Never catches up: always the fenced revision with old topology.
		return append([]fleetdb.StackNodeWire(nil), initial...), fencedRev, 200
	})
	err := s.MoveNode(context.Background(), "WS", "epic:E1", "T2", "T4")
	assert.ErrorIs(t, err, ErrUnknownWriteOutcome)
	require.Len(t, *calls, 1)
	assert.Equal(t, moveOutcomeReconcileAttempts, gets())
	assert.Contains(t, err.Error(), "could not confirm")
}

// getTransportFailure makes moveOutcomeFaultStub drop the follow-up Get's
// connection without a response.
const getTransportFailure = -1

func TestFleetDBMoveNode_AcceptedWriteFollowUpGetFails(t *testing.T) {
	t.Run("500", func(t *testing.T) {
		initial := wireChain(nil)
		s, calls, gets := moveOutcomeFaultStub(t, initial, 4, func(_ int, _ int64) ([]fleetdb.StackNodeWire, int64, int) {
			return nil, 0, http.StatusInternalServerError
		})
		err := s.MoveNode(context.Background(), "WS", "epic:E1", "T2", "T4")
		require.Error(t, err)
		assert.ErrorIs(t, err, ErrUnknownWriteOutcome, "Move may be journaled; a failed read must not hide that")
		require.Len(t, *calls, 1, "must not re-issue Move after stack_inconsistent")
		assert.Equal(t, 1, gets())
		var apiErr *fleetdb.StackAPIError
		require.ErrorAs(t, err, &apiErr, "underlying Get cause is preserved")
		assert.Equal(t, http.StatusInternalServerError, apiErr.Status)
		assert.Equal(t, "internal_error", apiErr.Code)
		assert.NotErrorIs(t, err, ErrConcurrentUpdate)
		assert.Contains(t, err.Error(), "re-read before retrying")
	})
	t.Run("transport failure", func(t *testing.T) {
		initial := wireChain(nil)
		s, calls, gets := moveOutcomeFaultStub(t, initial, 4, func(_ int, _ int64) ([]fleetdb.StackNodeWire, int64, int) {
			return nil, 0, getTransportFailure
		})
		err := s.MoveNode(context.Background(), "WS", "epic:E1", "T2", "T4")
		require.Error(t, err)
		assert.ErrorIs(t, err, ErrUnknownWriteOutcome, "Move may be journaled; a failed read must not hide that")
		require.Len(t, *calls, 1, "must not re-issue Move after stack_inconsistent")
		assert.GreaterOrEqual(t, gets(), 1)
		var urlErr *url.Error
		assert.ErrorAs(t, err, &urlErr, "underlying transport cause is preserved")
		var apiErr *fleetdb.StackAPIError
		assert.False(t, errors.As(err, &apiErr), "transport failure carries no API status")
		assert.NotErrorIs(t, err, ErrConcurrentUpdate)
		assert.Contains(t, err.Error(), "re-read before retrying")
	})
}

func TestStackClient_MoveNodeTypedContract(t *testing.T) {
	var gotMethod, gotPath string
	var gotBody fleetdb.StackMoveReq
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v1/{workspace}/stacks/{stack_id}/nodes/{task_id}/move", func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.EscapedPath()
		require.NoError(t, json.NewDecoder(r.Body).Decode(&gotBody))
		writeFleetJSON(w, 200, fleetdb.StackMoveResult{
			Revision: 42,
			Node:     fleetdb.StackNodeWire{TaskID: "T2", BaseTaskID: "T4", State: "pending"},
			Nodes: []fleetdb.StackNodeWire{
				{TaskID: "T1", State: "pending"},
				{TaskID: "T3", BaseTaskID: "T1", State: "pending"},
				{TaskID: "T4", BaseTaskID: "T3", State: "pending"},
				{TaskID: "T2", BaseTaskID: "T4", State: "pending"},
			},
		})
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	c, err := fleetdb.New(fleetdb.Config{BaseURL: srv.URL})
	require.NoError(t, err)

	rev := int64(41)
	out, err := c.Stacks().MoveNode(context.Background(), "WS", "epic:E1", "T2", fleetdb.StackMoveReq{
		AfterTaskID:      "T4",
		ExpectedRevision: &rev,
	})
	require.NoError(t, err)
	assert.Equal(t, http.MethodPost, gotMethod)
	assert.Equal(t, "/api/v1/WS/stacks/epic:E1/nodes/T2/move", gotPath)
	assert.Equal(t, "T4", gotBody.AfterTaskID)
	require.NotNil(t, gotBody.ExpectedRevision)
	assert.EqualValues(t, 41, *gotBody.ExpectedRevision)
	assert.EqualValues(t, 42, out.Revision)
	assert.Equal(t, "T2", out.Node.TaskID)
	assert.Equal(t, "T4", out.Node.BaseTaskID)
	require.Len(t, out.Nodes, 4)
}

func TestStackClient_MoveNodeStaleRevision(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v1/{workspace}/stacks/{stack_id}/nodes/{task_id}/move", func(w http.ResponseWriter, _ *http.Request) {
		writeFleetErr(w, 412, "precondition_failed", "stack changed since it was read")
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	c, err := fleetdb.New(fleetdb.Config{BaseURL: srv.URL})
	require.NoError(t, err)

	rev := int64(9)
	_, err = c.Stacks().MoveNode(context.Background(), "WS", "epic:E1", "T2", fleetdb.StackMoveReq{
		AfterTaskID:      "T4",
		ExpectedRevision: &rev,
	})
	require.Error(t, err)
	var apiErr *fleetdb.StackAPIError
	require.ErrorAs(t, err, &apiErr)
	assert.Equal(t, 412, apiErr.Status)
	assert.Equal(t, "precondition_failed", apiErr.Code)
	assert.ErrorIs(t, err, domain.ErrConflict)
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
	mux.HandleFunc("POST /api/v1/{workspace}/stacks/{stack_id}/nodes/{task_id}/move", func(w http.ResponseWriter, r *http.Request) {
		record(r)
		writeFleetJSON(w, 200, fleetdb.StackMoveResult{
			Revision: 1,
			Node:     fleetdb.StackNodeWire{TaskID: taskID},
			Nodes:    []fleetdb.StackNodeWire{{TaskID: taskID}},
		})
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
	require.NoError(t, s.MoveNode(ctx, "WS", stackID, taskID, "after"))
	require.NoError(t, s.RemoveNode(ctx, "WS", stackID, taskID))

	// GetStack, UpdateNode(list+patch), SetBase, MoveNode(get+move), RemoveNode
	require.Len(t, seen, 7, "every call routed to the stack_id pattern, none 404ed on an extra '/'")
	escStack := "/stacks/" + url.PathEscape(stackID)
	for _, p := range seen {
		assert.Contains(t, p, "/stacks/manual:repo%2Ffeature%20x", "'/' and ' ' in the stack ID are percent-encoded")
		assert.Contains(t, p, escStack)
	}
	taskPaths := []string{seen[2], seen[3], seen[5], seen[6]} // patch, base, move, delete
	for _, p := range taskPaths {
		assert.Contains(t, p, "/nodes/T%2F1:a")
	}
	assert.Contains(t, seen[5], "/move")
	for _, got := range gotStack {
		assert.Equal(t, stackID, got, "server decodes the original stack ID")
	}
	for _, got := range gotTask {
		assert.Equal(t, taskID, got, "server decodes the original task ID")
	}
}
