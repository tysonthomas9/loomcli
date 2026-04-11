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

	"github.com/tysonthomas9/loomcli/internal/backend/api"
	"github.com/tysonthomas9/loomcli/internal/backend/api/gen"
)

// canned envelope mirroring api.apiResponse — re-declared here because it is
// unexported in internal/backend/api.
type testEnvelope struct {
	Success bool            `json:"success"`
	Data    json.RawMessage `json:"data,omitempty"`
	Error   string          `json:"error,omitempty"`
}

func writeEnvelope(t *testing.T, w http.ResponseWriter, data interface{}) {
	t.Helper()
	raw, err := json.Marshal(data)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(testEnvelope{Success: true, Data: raw})
}

// registerAuthConfig adds /api/config to mux so internal/httpclient.New()
// can discover auth mode without hitting a real loom server.
func registerAuthConfig(mux *http.ServeMux) {
	mux.HandleFunc("/api/config", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"mode":"open"}`))
	})
}

func cannedIssueServer(t *testing.T) *httptest.Server {
	t.Helper()
	now := time.Now().UTC().Truncate(time.Second)
	mux := http.NewServeMux()
	registerAuthConfig(mux)
	mux.HandleFunc("/api/workspaces/default/issues/loom-1", func(w http.ResponseWriter, r *http.Request) {
		writeEnvelope(t, w, gen.IssueResponse{
			Id:           "loom-1",
			Title:        "Example issue",
			Status:       gen.IssueResponseStatus("open"),
			Priority:     1,
			IssueType:    gen.IssueResponseIssueType("task"),
			CreatedAt:    now,
			UpdatedAt:    now,
			Labels:       []string{"v2"},
			Comments:     []gen.CommentResponse{},
			Dependencies: []gen.DependencyRef{},
			Dependents:   []gen.DependencyRef{},
		})
	})
	mux.HandleFunc("/api/workspaces/default/issues/missing", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_ = json.NewEncoder(w).Encode(testEnvelope{Success: false, Error: "not found"})
	})
	return httptest.NewServer(mux)
}

func runShow(t *testing.T, srvURL, id, format string) (string, error) {
	t.Helper()
	resetClient()
	serverURL = srvURL
	workspaceID = "default"
	outputFormat = format

	// Run showCmd's RunE directly to capture stdout via a pipe. We swap
	// os.Stdout for simplicity because showCmd writes to os.Stdout.
	ctx := context.Background()
	cli, url, err := getHTTPClient()
	if err != nil {
		return "", err
	}
	wsID, err := resolveWorkspaceID(ctx, cli, url)
	if err != nil {
		return "", err
	}
	ab, err := api.New(api.Config{BaseURL: url, WorkspaceID: wsID, HTTPClient: cli})
	if err != nil {
		return "", err
	}
	detail, err := ab.Get(ctx, id)
	if err != nil {
		return "", err
	}
	var buf bytes.Buffer
	if err := printIssueDetail(&buf, detail, format); err != nil {
		return "", err
	}
	return buf.String(), nil
}

func TestShowText(t *testing.T) {
	srv := cannedIssueServer(t)
	defer srv.Close()
	withDataClientState(t, func() {
		t.Setenv("LOOM_CONFIG_DIR", t.TempDir())
		out, err := runShow(t, srv.URL, "loom-1", "text")
		if err != nil {
			t.Fatalf("runShow: %v", err)
		}
		if !strings.Contains(out, "loom-1") {
			t.Errorf("text output missing id; got %q", out)
		}
		if !strings.Contains(out, "Example issue") {
			t.Errorf("text output missing title; got %q", out)
		}
	})
}

func TestShowJSON(t *testing.T) {
	srv := cannedIssueServer(t)
	defer srv.Close()
	withDataClientState(t, func() {
		t.Setenv("LOOM_CONFIG_DIR", t.TempDir())
		out, err := runShow(t, srv.URL, "loom-1", "json")
		if err != nil {
			t.Fatalf("runShow: %v", err)
		}
		var decoded map[string]interface{}
		if err := json.Unmarshal([]byte(out), &decoded); err != nil {
			t.Fatalf("JSON decode: %v (out=%q)", err, out)
		}
		if decoded["id"] != "loom-1" {
			t.Errorf("decoded id = %v, want loom-1", decoded["id"])
		}
	})
}

func TestShowNotFound(t *testing.T) {
	srv := cannedIssueServer(t)
	defer srv.Close()
	withDataClientState(t, func() {
		t.Setenv("LOOM_CONFIG_DIR", t.TempDir())
		_, err := runShow(t, srv.URL, "missing", "text")
		if err == nil {
			t.Fatal("expected error for missing issue")
		}
		if !strings.Contains(strings.ToLower(err.Error()), "not found") {
			t.Errorf("err = %q, want one containing 'not found'", err.Error())
		}
	})
}
