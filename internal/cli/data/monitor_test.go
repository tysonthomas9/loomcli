package data

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/backend/api/gen"
)

// monitorStatusServer serves the workspace-scoped monitor route and records
// every path it was asked for, so tests can assert the CLI stopped using the
// unscoped /api/monitor/status (which reports zeros for everything).
func monitorStatusServer(t *testing.T, payload *gen.MonitorStatusResponse, status int) (*httptest.Server, *[]string) {
	t.Helper()
	var paths []string
	mux := http.NewServeMux()
	registerAuthConfig(mux)
	mux.HandleFunc("/api/", func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		if r.URL.Path != "/api/workspaces/"+testMonitorWorkspace+"/monitor/status" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		if status != 0 && status != http.StatusOK {
			w.WriteHeader(status)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(payload)
	})
	return httptest.NewServer(mux), &paths
}

// testMonitorWorkspace is the workspace the fixtures are scoped to.
const testMonitorWorkspace = "PUPPET"

// samplePayload is deliberately non-zero in every count: the bug this file
// guards produced an all-zero dashboard, so a fixture full of zeros could not
// have caught it. assertPayloadNonZero enforces that.
func samplePayload() *gen.MonitorStatusResponse {
	name := testMonitorWorkspace
	role := "plan"
	return &gen.MonitorStatusResponse{
		AgentTasks:     map[string]gen.MonitorTaskInfo{},
		InProgressList: []gen.MonitorTaskInfo{},
		Agents: []gen.MonitorAgentStatus{
			{Name: "falcon", Branch: "falcon", Status: "idle", Workspace: "test-ws", Role: &role},
		},
		Stats: gen.MonitorStats{
			Open: 5, InProgress: 1, Review: 0, Blocked: 2, Closed: 10, Total: 18, Completion: 55.5,
		},
		Tasks: gen.MonitorTaskSummary{
			ReadyToImplement: 3, NeedsPlanning: 1, InProgress: 1, NeedReview: 0, Backlog: 4, Epics: 2,
		},
		Sync:      gen.MonitorSyncInfo{DbSynced: true, DbLastSync: time.Now().UTC().Format(time.RFC3339)},
		Timestamp: time.Now().UTC(),
		Workspace: gen.MonitorWorkspaceInfo{Mode: "workspace", Name: &name, Resolved: true},
	}
}

// assertPayloadNonZero fails the test if the fixture would have been satisfied
// by the very bug under test — an all-zero, agent-less response.
func assertPayloadNonZero(t *testing.T, p *gen.MonitorStatusResponse) {
	t.Helper()
	if len(p.Agents) == 0 {
		t.Fatal("fixture guard: payload has no agents; it cannot distinguish a scoped response from the empty one")
	}
	if p.Tasks.ReadyToImplement == 0 || p.Tasks.InProgress == 0 || p.Stats.Total == 0 {
		t.Fatalf("fixture guard: payload counts must all be non-zero, got tasks=%+v stats=%+v", p.Tasks, p.Stats)
	}
}

func fetchAndRender(t *testing.T, srvURL, format string) (string, error) {
	t.Helper()
	resetClient()
	serverURL = srvURL
	outputFormat = format

	ctx := context.Background()
	cli, baseURL, err := getHTTPClient()
	if err != nil {
		return "", err
	}
	wsID, err := resolveWorkspaceID(ctx, cli, baseURL)
	if err != nil {
		return "", err
	}
	status, err := fetchMonitorStatus(ctx, cli, baseURL, wsID)
	if err != nil {
		return "", err
	}
	var buf bytes.Buffer
	if err := printMonitorStatus(&buf, status, format); err != nil {
		return "", err
	}
	return buf.String(), nil
}

func TestMonitorText(t *testing.T) {
	srv, _ := monitorStatusServer(t, samplePayload(), http.StatusOK)
	defer srv.Close()
	withDataClientState(t, func() {
		t.Setenv("LOOM_CONFIG_DIR", t.TempDir())
		t.Setenv("LOOM_WORKSPACE", testMonitorWorkspace)
		out, err := fetchAndRender(t, srv.URL, "text")
		if err != nil {
			t.Fatalf("fetchAndRender: %v", err)
		}
		for _, need := range []string{"AGENTS:", "TASKS:", "STATS:", "falcon"} {
			if !strings.Contains(out, need) {
				t.Errorf("output missing %q; got:\n%s", need, out)
			}
		}
	})
}

func TestMonitorJSON(t *testing.T) {
	srv, _ := monitorStatusServer(t, samplePayload(), http.StatusOK)
	defer srv.Close()
	withDataClientState(t, func() {
		t.Setenv("LOOM_CONFIG_DIR", t.TempDir())
		t.Setenv("LOOM_WORKSPACE", testMonitorWorkspace)
		out, err := fetchAndRender(t, srv.URL, "json")
		if err != nil {
			t.Fatalf("fetchAndRender: %v", err)
		}
		var decoded gen.MonitorStatusResponse
		if err := json.Unmarshal([]byte(out), &decoded); err != nil {
			t.Fatalf("round-trip decode: %v (out=%q)", err, out)
		}
		if len(decoded.Agents) != 1 || decoded.Agents[0].Name != "falcon" {
			t.Errorf("decoded agents shape unexpected: %+v", decoded.Agents)
		}
	})
}

func TestMonitor503(t *testing.T) {
	srv, _ := monitorStatusServer(t, nil, http.StatusServiceUnavailable)
	defer srv.Close()
	withDataClientState(t, func() {
		t.Setenv("LOOM_CONFIG_DIR", t.TempDir())
		t.Setenv("LOOM_WORKSPACE", testMonitorWorkspace)
		_, err := fetchAndRender(t, srv.URL, "text")
		if err == nil {
			t.Fatal("expected 503 to surface as error")
		}
		if !strings.Contains(err.Error(), "unavailable") {
			t.Errorf("error = %q, want one containing 'unavailable'", err.Error())
		}
	})
}

func TestMonitorHTTPErrorAndDecodeError(t *testing.T) {
	t.Run("http error", func(t *testing.T) {
		srv, _ := monitorStatusServer(t, nil, http.StatusInternalServerError)
		defer srv.Close()
		withDataClientState(t, func() {
			t.Setenv("LOOM_CONFIG_DIR", t.TempDir())
			t.Setenv("LOOM_WORKSPACE", testMonitorWorkspace)
			_, err := fetchAndRender(t, srv.URL, "text")
			if err == nil {
				t.Fatal("expected HTTP error")
			}
			if !strings.Contains(err.Error(), "HTTP 500") {
				t.Fatalf("error = %q, want HTTP 500", err.Error())
			}
		})
	})

	t.Run("decode error", func(t *testing.T) {
		mux := http.NewServeMux()
		registerAuthConfig(mux)
		mux.HandleFunc("/api/workspaces/"+testMonitorWorkspace+"/monitor/status", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte("{"))
		})
		srv := httptest.NewServer(mux)
		defer srv.Close()
		withDataClientState(t, func() {
			t.Setenv("LOOM_CONFIG_DIR", t.TempDir())
			t.Setenv("LOOM_WORKSPACE", testMonitorWorkspace)
			_, err := fetchAndRender(t, srv.URL, "text")
			if err == nil {
				t.Fatal("expected decode error")
			}
			if !strings.Contains(err.Error(), "decode monitor response") {
				t.Fatalf("error = %q, want decode context", err.Error())
			}
		})
	})
}

// TestMonitorUsesScopedWorkspaceRoute is the regression test for PUPPET-258:
// the CLI used to GET the unscoped /api/monitor/status, which the server
// answers from a workspace-less collector — an all-zero dashboard while the
// fleet is busy.
func TestMonitorUsesScopedWorkspaceRoute(t *testing.T) {
	payload := samplePayload()
	assertPayloadNonZero(t, payload)
	srv, paths := monitorStatusServer(t, payload, http.StatusOK)
	defer srv.Close()
	withDataClientState(t, func() {
		t.Setenv("LOOM_CONFIG_DIR", t.TempDir())
		t.Setenv("LOOM_WORKSPACE", testMonitorWorkspace)
		out, err := fetchAndRender(t, srv.URL, "text")
		if err != nil {
			t.Fatalf("fetchAndRender: %v", err)
		}

		want := "/api/workspaces/" + testMonitorWorkspace + "/monitor/status"
		var got []string
		for _, p := range *paths {
			if p == "/api/auth/config" {
				continue
			}
			got = append(got, p)
		}
		if len(got) != 1 || got[0] != want {
			t.Fatalf("monitor requested %v, want exactly [%s]", got, want)
		}
		for _, p := range got {
			if p == "/api/monitor/status" {
				t.Errorf("monitor still uses the unscoped route %q", p)
			}
		}

		// The rendered numbers must be the payload's, not zeros.
		for _, need := range []string{
			"Workspace: " + testMonitorWorkspace,
			"ready_to_implement: 3",
			"in_progress:        1",
			"backlog:            4",
			"total:       18",
			"falcon",
		} {
			if !strings.Contains(out, need) {
				t.Errorf("output missing %q; got:\n%s", need, out)
			}
		}
		if strings.Contains(out, "warning: server did not resolve a workspace") {
			t.Errorf("unexpected unresolved warning for a scoped response:\n%s", out)
		}
	})
}

// TestMonitorRequiresWorkspace pins the deliberate behavior change: without a
// workspace the command errors instead of printing an empty dashboard.
func TestMonitorRequiresWorkspace(t *testing.T) {
	srv, paths := monitorStatusServer(t, samplePayload(), http.StatusOK)
	defer srv.Close()
	withDataClientState(t, func() {
		t.Setenv("LOOM_CONFIG_DIR", t.TempDir())
		t.Setenv("LOOM_WORKSPACE", "")
		out, err := fetchAndRender(t, srv.URL, "text")
		if err == nil {
			t.Fatalf("expected an error without LOOM_WORKSPACE; got output:\n%s", out)
		}
		if !strings.Contains(err.Error(), "workspace is required") {
			t.Errorf("error = %q, want the resolver's 'workspace is required' message", err.Error())
		}
		if out != "" {
			t.Errorf("expected nothing printed, got %q", out)
		}
		for _, p := range *paths {
			if strings.Contains(p, "monitor/status") {
				t.Errorf("monitor status was fetched despite an unresolved workspace: %q", p)
			}
		}
	})
}

// TestMonitor404NamesWorkspaceAndURL keeps version skew against an older
// server binary visible rather than reported as a bare "HTTP 404".
func TestMonitor404NamesWorkspaceAndURL(t *testing.T) {
	srv, _ := monitorStatusServer(t, samplePayload(), http.StatusOK)
	defer srv.Close()
	withDataClientState(t, func() {
		t.Setenv("LOOM_CONFIG_DIR", t.TempDir())
		t.Setenv("LOOM_WORKSPACE", "NOPE")
		_, err := fetchAndRender(t, srv.URL, "text")
		if err == nil {
			t.Fatal("expected a 404 for an unknown workspace")
		}
		for _, need := range []string{`workspace "NOPE" not found`, "/api/workspaces/NOPE/monitor/status"} {
			if !strings.Contains(err.Error(), need) {
				t.Errorf("error = %q, want it to contain %q", err.Error(), need)
			}
		}
	})
}
