package workflowruns

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/workflows/platform"
)

const ws = "test-ws"

func newServer(t *testing.T) (*platform.MemStore, *httptest.Server) {
	t.Helper()
	m := platform.NewMemStore()
	mux := http.NewServeMux()
	NewModule(m).Register(mux)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return m, srv
}

func seed(t *testing.T, m *platform.MemStore) string {
	t.Helper()
	ctx := context.Background()
	if _, err := m.Drivers().Create(ctx, ws, platform.Driver{DriverID: "epic-runner", Name: "epic-runner"}); err != nil {
		t.Fatal(err)
	}
	v, err := m.Drivers().CreateVersion(ctx, ws, "epic-runner", platform.DriverVersion{
		VersionID: "ver-1", Version: 1, SourceDigest: "sha256:dev", BundleDigest: "sha256:dev",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.Drivers().Activate(ctx, ws, "epic-runner", v.VersionID); err != nil {
		t.Fatal(err)
	}
	return v.VersionID
}

func TestListAndGetRuns(t *testing.T) {
	t.Parallel()
	m, srv := newServer(t)
	ver := seed(t, m)
	if _, err := m.DriverRuns().Create(context.Background(), ws, platform.DriverRunCreate{
		RunID: "run-1", DriverID: "epic-runner", DriverVersionID: ver, EpicID: "EPIC-1",
	}); err != nil {
		t.Fatal(err)
	}

	resp, err := http.Get(srv.URL + "/api/workspaces/" + ws + "/workflows/runs?epic=EPIC-1")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var list struct {
		Runs  []platform.DriverRun `json:"runs"`
		Count int                  `json:"count"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&list); err != nil {
		t.Fatal(err)
	}
	if list.Count != 1 || list.Runs[0].RunID != "run-1" {
		t.Fatalf("list: %+v", list)
	}

	resp2, err := http.Get(srv.URL + "/api/workspaces/" + ws + "/workflows/runs/run-1")
	if err != nil {
		t.Fatal(err)
	}
	defer resp2.Body.Close()
	if resp2.StatusCode != http.StatusOK {
		t.Fatalf("get status: %d", resp2.StatusCode)
	}

	resp3, err := http.Get(srv.URL + "/api/workspaces/" + ws + "/workflows/runs/nope")
	if err != nil {
		t.Fatal(err)
	}
	defer resp3.Body.Close()
	if resp3.StatusCode != http.StatusNotFound {
		t.Fatalf("missing run status: %d", resp3.StatusCode)
	}
}

func TestRunEpicAdmission(t *testing.T) {
	t.Parallel()
	m, srv := newServer(t)
	seed(t, m)
	url := srv.URL + "/api/workspaces/" + ws + "/workflows/epics/EPIC-9/run"

	resp, err := http.Post(url, "application/json", strings.NewReader(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("first admission: %d", resp.StatusCode)
	}
	var created platform.DriverRun
	if err := json.NewDecoder(resp.Body).Decode(&created); err != nil {
		t.Fatal(err)
	}
	if created.EpicID != "EPIC-9" || created.Status != platform.DriverRunQueued {
		t.Fatalf("created run: %+v", created)
	}

	// Second admission while the first is active → 409 with the run.
	resp2, err := http.Post(url, "application/json", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer resp2.Body.Close()
	if resp2.StatusCode != http.StatusConflict {
		t.Fatalf("second admission: %d", resp2.StatusCode)
	}
	var conflict struct {
		ActiveRun platform.DriverRun `json:"active_run"`
	}
	if err := json.NewDecoder(resp2.Body).Decode(&conflict); err != nil {
		t.Fatal(err)
	}
	if conflict.ActiveRun.RunID != created.RunID {
		t.Fatalf("conflict body: %+v", conflict)
	}
}

func TestRunEpicWithoutDriver(t *testing.T) {
	t.Parallel()
	_, srv := newServer(t) // no driver seeded
	resp, err := http.Post(srv.URL+"/api/workspaces/"+ws+"/workflows/epics/E/run", "application/json", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("status: %d, want 503 no execution plane", resp.StatusCode)
	}
}
