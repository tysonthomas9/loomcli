package data

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
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

// resetListLabelFlagVars clears listLabels before and after a test so the shared
// package-level listCmd stays order-independent.
//
// It is also what keeps repeated real parsing safe: pflag's stringArrayValue
// tracks its own "changed" bool that no test helper can reach, so a second
// ParseFlags of --label in one test binary APPENDS instead of replacing.
// Appending to a nil slice yields the expected result anyway.
func resetListLabelFlagVars(t *testing.T) {
	t.Helper()
	listLabels = nil
	t.Cleanup(func() { listLabels = nil })
}

// TestListLabelFlag_ParseSemantics drives real pflag parsing, which runList
// deliberately bypasses (it calls the backend with a ListOpts directly and never
// executes listCmd). It pins the flag to StringArray: StringSlice would
// comma-split "x,y" into two labels and a plain StringVar would keep only the
// last occurrence.
func TestListLabelFlag_ParseSemantics(t *testing.T) {
	resetListLabelFlagVars(t)

	if err := listCmd.ParseFlags([]string{
		"--label", "a",
		"--label", "b",
		"--label", "x,y",
	}); err != nil {
		t.Fatalf("ParseFlags: %v", err)
	}

	if want := []string{"a", "b", "x,y"}; !reflect.DeepEqual(listLabels, want) {
		t.Fatalf("listLabels = %#v, want %#v (StringArray: no comma-split, every occurrence kept)", listLabels, want)
	}
	if !listCmd.Flags().Changed("label") {
		t.Fatal("parsing --label must mark the flag Changed")
	}
}

func TestListLabelFlag_DefaultNil(t *testing.T) {
	resetListLabelFlagVars(t)

	if err := listCmd.ParseFlags([]string{"--status", "open"}); err != nil {
		t.Fatalf("ParseFlags: %v", err)
	}
	if listLabels != nil {
		t.Fatalf("listLabels = %#v, want nil when --label is absent (so ListOpts.Labels is omitted)", listLabels)
	}
}

func TestListLabelFilter(t *testing.T) {
	var capturedQuery string
	srv := issueListServer(t, []gen.Issue{}, &capturedQuery)
	defer srv.Close()

	withDataClientState(t, func() {
		t.Setenv("LOOM_CONFIG_DIR", t.TempDir())
		_, err := runList(t, srv.URL, backend.ListOpts{Labels: []string{"a", "b"}}, "text")
		if err != nil {
			t.Fatalf("runList: %v", err)
		}
		if !strings.Contains(capturedQuery, "labels=a%2Cb") {
			t.Errorf("query = %q, want one containing 'labels=a%%2Cb'", capturedQuery)
		}
	})
}
