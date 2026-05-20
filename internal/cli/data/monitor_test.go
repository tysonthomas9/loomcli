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

func monitorStatusServer(t *testing.T, payload *gen.MonitorStatusResponse, status int) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	registerAuthConfig(mux)
	mux.HandleFunc("/api/monitor/status", func(w http.ResponseWriter, r *http.Request) {
		if status != 0 && status != http.StatusOK {
			w.WriteHeader(status)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(payload)
	})
	return httptest.NewServer(mux)
}

func samplePayload() *gen.MonitorStatusResponse {
	name := "test-ws"
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
		Workspace: gen.MonitorWorkspaceInfo{Mode: "single", Name: &name},
	}
}

func fetchAndRender(t *testing.T, srvURL, format string) (string, error) {
	t.Helper()
	resetClient()
	serverURL = srvURL
	outputFormat = format

	ctx := context.Background()
	cli, url, err := getHTTPClient()
	if err != nil {
		return "", err
	}
	status, err := fetchMonitorStatus(ctx, cli, url)
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
	srv := monitorStatusServer(t, samplePayload(), http.StatusOK)
	defer srv.Close()
	withDataClientState(t, func() {
		t.Setenv("LOOM_CONFIG_DIR", t.TempDir())
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
	srv := monitorStatusServer(t, samplePayload(), http.StatusOK)
	defer srv.Close()
	withDataClientState(t, func() {
		t.Setenv("LOOM_CONFIG_DIR", t.TempDir())
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
	srv := monitorStatusServer(t, nil, http.StatusServiceUnavailable)
	defer srv.Close()
	withDataClientState(t, func() {
		t.Setenv("LOOM_CONFIG_DIR", t.TempDir())
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
	t.Run("invalid url", func(t *testing.T) {
		if _, err := fetchMonitorStatus(context.Background(), http.DefaultClient, "://bad-url"); err == nil {
			t.Fatal("expected invalid URL error")
		}
	})

	t.Run("http error", func(t *testing.T) {
		srv := monitorStatusServer(t, nil, http.StatusInternalServerError)
		defer srv.Close()
		withDataClientState(t, func() {
			t.Setenv("LOOM_CONFIG_DIR", t.TempDir())
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
		mux.HandleFunc("/api/monitor/status", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte("{"))
		})
		srv := httptest.NewServer(mux)
		defer srv.Close()
		withDataClientState(t, func() {
			t.Setenv("LOOM_CONFIG_DIR", t.TempDir())
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
