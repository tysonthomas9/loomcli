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

	"github.com/tysonthomas9/loomcli/internal/backend"
	"github.com/tysonthomas9/loomcli/internal/backend/api"
	"github.com/tysonthomas9/loomcli/internal/backend/api/gen"
)

func issueListServer(t *testing.T, payload []gen.Issue, captureQuery *string) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	registerAuthConfig(mux)
	mux.HandleFunc("/api/workspaces/default/issues", func(w http.ResponseWriter, r *http.Request) {
		if captureQuery != nil {
			*captureQuery = r.URL.RawQuery
		}
		writeEnvelope(t, w, payload)
	})
	return httptest.NewServer(mux)
}

func runList(t *testing.T, srvURL string, opts backend.ListOpts, format string) (string, error) {
	t.Helper()
	resetClient()
	serverURL = srvURL
	t.Setenv("LOOM_WORKSPACE", "default")
	outputFormat = format

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
	items, err := ab.List(ctx, opts)
	if err != nil {
		return "", err
	}
	var buf bytes.Buffer
	if err := printIssueList(&buf, items, format); err != nil {
		return "", err
	}
	return buf.String(), nil
}

func TestListAll(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	open := gen.IssueStatus("open")
	task := gen.IssueIssueType("task")
	payload := []gen.Issue{
		{Id: "a", Title: "alpha", Priority: 1, Status: &open, IssueType: &task, CreatedAt: now, UpdatedAt: now},
		{Id: "b", Title: "beta", Priority: 2, Status: &open, IssueType: &task, CreatedAt: now, UpdatedAt: now},
		{Id: "c", Title: "gamma", Priority: 3, Status: &open, IssueType: &task, CreatedAt: now, UpdatedAt: now},
	}
	srv := issueListServer(t, payload, nil)
	defer srv.Close()

	withDataClientState(t, func() {
		t.Setenv("LOOM_CONFIG_DIR", t.TempDir())
		out, err := runList(t, srv.URL, backend.ListOpts{}, "text")
		if err != nil {
			t.Fatalf("runList: %v", err)
		}
		for _, id := range []string{"a", "b", "c"} {
			if !strings.Contains(out, id) {
				t.Errorf("missing id %q in text output: %q", id, out)
			}
		}
	})
}

func TestListEmptyText(t *testing.T) {
	srv := issueListServer(t, []gen.Issue{}, nil)
	defer srv.Close()
	withDataClientState(t, func() {
		t.Setenv("LOOM_CONFIG_DIR", t.TempDir())
		out, err := runList(t, srv.URL, backend.ListOpts{}, "text")
		if err != nil {
			t.Fatalf("runList: %v", err)
		}
		if !strings.Contains(out, "(no issues)") {
			t.Errorf("text output missing '(no issues)' sentinel: %q", out)
		}
	})
}

func TestListEmptyJSON(t *testing.T) {
	srv := issueListServer(t, []gen.Issue{}, nil)
	defer srv.Close()
	withDataClientState(t, func() {
		t.Setenv("LOOM_CONFIG_DIR", t.TempDir())
		out, err := runList(t, srv.URL, backend.ListOpts{}, "json")
		if err != nil {
			t.Fatalf("runList: %v", err)
		}
		trimmed := strings.TrimSpace(out)
		// Should be "[]" — never "null".
		var decoded []interface{}
		if err := json.Unmarshal([]byte(trimmed), &decoded); err != nil {
			t.Fatalf("JSON decode: %v (out=%q)", err, trimmed)
		}
		if len(decoded) != 0 {
			t.Errorf("want empty slice, got %d items", len(decoded))
		}
	})
}

func TestListStatusFilter(t *testing.T) {
	var capturedQuery string
	srv := issueListServer(t, []gen.Issue{}, &capturedQuery)
	defer srv.Close()

	withDataClientState(t, func() {
		t.Setenv("LOOM_CONFIG_DIR", t.TempDir())
		_, err := runList(t, srv.URL, backend.ListOpts{Status: "open"}, "text")
		if err != nil {
			t.Fatalf("runList: %v", err)
		}
		if !strings.Contains(capturedQuery, "status=open") {
			t.Errorf("query = %q, want one containing 'status=open'", capturedQuery)
		}
	})
}
