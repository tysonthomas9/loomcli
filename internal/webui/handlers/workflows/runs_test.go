package workflows

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/infra/memstore"
	"github.com/tysonthomas9/loomcli/internal/modules/execution"
)

// seedRun creates a queued driver run under the "demo" driver/"version-1"
// version seeded by seededWorkflowStore.
func seedRun(t *testing.T, ctx context.Context, st *memstore.Store, runID string) *execution.DriverRunRecord {
	t.Helper()
	run, err := st.DriverRuns().Create(ctx, execution.DriverRunCreate{
		WorkspaceKey:    "TEST",
		RunID:           runID,
		DriverID:        "demo",
		DriverVersionID: "version-1",
	})
	if err != nil {
		t.Fatalf("create run %s: %v", runID, err)
	}
	return run
}

// startRun claims a queued run so it becomes running with a StartedAt stamp;
// sequential claims produce strictly increasing StartedAt values.
func startRun(t *testing.T, ctx context.Context, st *memstore.Store, runID string) *execution.DriverRunRecord {
	t.Helper()
	run, err := st.DriverRuns().Claim(ctx, "TEST", runID, "node-1", "lease-"+runID)
	if err != nil {
		t.Fatalf("claim run %s: %v", runID, err)
	}
	return run
}

// finishRun terminalizes a running run with the given status (StartedAt is
// preserved by Finish).
func finishRun(t *testing.T, ctx context.Context, st *memstore.Store, run *execution.DriverRunRecord, status execution.DriverRunStatus) {
	t.Helper()
	if _, err := st.DriverRuns().Finish(ctx, "TEST", run.RunID, execution.DriverRunFinish{
		NodeID:       run.NodeID,
		LeaseID:      run.LeaseID,
		FencingToken: run.FencingToken,
		Status:       status,
	}); err != nil {
		t.Fatalf("finish run %s: %v", run.RunID, err)
	}
}

type runsResponse struct {
	DriverID        string                      `json:"driver_id"`
	ActiveVersionID string                      `json:"active_version_id"`
	Runs            []execution.DriverRunRecord `json:"runs"`
}

func listRuns(t *testing.T, mux *http.ServeMux, workflow, query string) (*httptest.ResponseRecorder, runsResponse) {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/workspaces/TEST/workflows/"+workflow+"/runs"+query, nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	var out runsResponse
	if rec.Code == http.StatusOK {
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
			t.Fatalf("decode runs: %v; body=%s", err, rec.Body.String())
		}
	}
	return rec, out
}

func TestListWorkflowRunsOrdersNewestFirst(t *testing.T) {
	ctx := context.Background()
	st := seededWorkflowStore(t, ctx)

	// r1 stays queued (zero StartedAt → sorts last). r2/r3/r4 are claimed in
	// order, newest last. The sleeps force distinct StartedAt stamps: memstore
	// records time.Now().UTC(), which drops the monotonic reading, so back-to-back
	// claims can otherwise tie at wall-clock resolution and leave the order (which
	// the handler cannot define for identical instants) ambiguous.
	seedRun(t, ctx, st, "r1")
	seedRun(t, ctx, st, "r2")
	seedRun(t, ctx, st, "r3")
	seedRun(t, ctx, st, "r4")
	startRun(t, ctx, st, "r2")
	time.Sleep(2 * time.Millisecond)
	c3 := startRun(t, ctx, st, "r3")
	time.Sleep(2 * time.Millisecond)
	c4 := startRun(t, ctx, st, "r4")
	finishRun(t, ctx, st, c3, execution.DriverRunCompleted)
	finishRun(t, ctx, st, c4, execution.DriverRunFailed)

	mux := http.NewServeMux()
	newWorkflowTestModule(st).Register(mux)

	rec, resp := listRuns(t, mux, "demo", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if resp.DriverID != "demo" || resp.ActiveVersionID != "version-1" {
		t.Fatalf("resp meta = driver=%q active=%q, want demo/version-1", resp.DriverID, resp.ActiveVersionID)
	}
	got := make([]string, len(resp.Runs))
	for i, r := range resp.Runs {
		got[i] = r.RunID
	}
	want := []string{"r4", "r3", "r2", "r1"}
	if !slices.Equal(got, want) {
		t.Fatalf("run order = %v, want %v (StartedAt desc, queued last)", got, want)
	}
}

func TestListWorkflowRunsFiltersByStatus(t *testing.T) {
	ctx := context.Background()
	st := seededWorkflowStore(t, ctx)
	seedRun(t, ctx, st, "queued-1")
	seedRun(t, ctx, st, "running-1")
	startRun(t, ctx, st, "running-1")

	mux := http.NewServeMux()
	newWorkflowTestModule(st).Register(mux)

	rec, resp := listRuns(t, mux, "demo", "?status=running")
	if rec.Code != http.StatusOK {
		t.Fatalf("status=running code = %d; body=%s", rec.Code, rec.Body.String())
	}
	if len(resp.Runs) != 1 || resp.Runs[0].RunID != "running-1" {
		t.Fatalf("status=running returned %+v, want only running-1", resp.Runs)
	}

	_, resp = listRuns(t, mux, "demo", "?status=queued")
	if len(resp.Runs) != 1 || resp.Runs[0].RunID != "queued-1" {
		t.Fatalf("status=queued returned %+v, want only queued-1", resp.Runs)
	}
}

func TestListWorkflowRunsLimit(t *testing.T) {
	ctx := context.Background()
	st := seededWorkflowStore(t, ctx)
	for i := 0; i < 205; i++ {
		seedRun(t, ctx, st, fmt.Sprintf("run-%03d", i))
	}
	mux := http.NewServeMux()
	newWorkflowTestModule(st).Register(mux)

	// Above-cap limit is clamped to 200, not rejected.
	if rec, resp := listRuns(t, mux, "demo", "?limit=1000"); rec.Code != http.StatusOK || len(resp.Runs) != maxRunsLimit {
		t.Fatalf("limit=1000 -> code=%d runs=%d, want 200/200", rec.Code, len(resp.Runs))
	}
	// Explicit small limit is honored.
	if _, resp := listRuns(t, mux, "demo", "?limit=5"); len(resp.Runs) != 5 {
		t.Fatalf("limit=5 runs = %d, want 5", len(resp.Runs))
	}
	// Default (no limit) caps at 50.
	if _, resp := listRuns(t, mux, "demo", ""); len(resp.Runs) != defaultRunsLimit {
		t.Fatalf("default runs = %d, want 50", len(resp.Runs))
	}
}

func TestListWorkflowRunsValidation(t *testing.T) {
	ctx := context.Background()
	st := seededWorkflowStore(t, ctx)
	mux := http.NewServeMux()
	newWorkflowTestModule(st).Register(mux)

	cases := []struct {
		name     string
		workflow string
		query    string
		want     int
	}{
		{name: "bad status", workflow: "demo", query: "?status=bogus", want: http.StatusBadRequest},
		{name: "non-numeric limit", workflow: "demo", query: "?limit=abc", want: http.StatusBadRequest},
		{name: "zero limit", workflow: "demo", query: "?limit=0", want: http.StatusBadRequest},
		{name: "negative limit", workflow: "demo", query: "?limit=-3", want: http.StatusBadRequest},
		{name: "unknown workflow", workflow: "not-a-workflow", query: "", want: http.StatusNotFound},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec, _ := listRuns(t, mux, tc.workflow, tc.query)
			if rec.Code != tc.want {
				t.Fatalf("status = %d, want %d; body=%s", rec.Code, tc.want, rec.Body.String())
			}
		})
	}
}
