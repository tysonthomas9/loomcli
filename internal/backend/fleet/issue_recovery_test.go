package fleet

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/tysonthomas9/loomcli/internal/backend"
)

func recoveryTestIssue(id string) map[string]any {
	return map[string]any{"workspace": "WS", "id": id, "title": "title", "status": "custom-review", "priority": 2, "type": "task", "created_at": "2026-09-05T00:00:00Z", "created_by": "alice", "updated_at": "2026-09-05T00:00:00Z", "metadata": map[string]string{"owner": "retained"}, "estimated_minutes": 42, "labels": []string{}, "future_field": map[string]any{"value": true}}
}
func recoveryTestDocument() map[string]any {
	return map[string]any{"manifest": recoveryManifest, "workspace": "WS", "through": "c2.MTAtMA", "issues": []any{}, "total": 0, "ready": []any{}, "blocked": []any{}, "deferred": []any{}, "dependencies": []any{}, "comments": []any{}}
}
func recoveryTestBackend(t *testing.T, h http.HandlerFunc) *FleetBackend {
	t.Helper()
	server := httptest.NewServer(h)
	t.Cleanup(server.Close)
	b, err := New(Config{BaseURL: server.URL, WorkspaceID: "WS", AuthToken: "token", HTTPClient: server.Client()})
	require.NoError(t, err)
	return b
}
func TestReadIssueRecoveryNativeDocument(t *testing.T) {
	for _, full := range []bool{false, true} {
		t.Run(map[bool]string{false: "empty", true: "full"}[full], func(t *testing.T) {
			document := recoveryTestDocument()
			if full {
				issue := recoveryTestIssue("WS-1")
				blocker := recoveryTestIssue("WS-2")
				issue["repo"] = "org/repo"
				issue["source_repo"] = "org/repo"
				document["issues"] = []any{issue, blocker}
				document["total"] = 2
				document["ready"] = []any{blocker}
				document["blocked"] = []any{map[string]any{"issue": issue, "blockers": []any{map[string]any{"id": "WS-2", "title": "title", "status": "custom-review", "priority": 2, "reason": "direct", "dep_type": "blocks"}}}}
			}
			raw, err := json.Marshal(document)
			require.NoError(t, err)
			b := recoveryTestBackend(t, func(w http.ResponseWriter, r *http.Request) {
				require.Equal(t, http.MethodPost, r.Method)
				require.Equal(t, "/api/v2/WS/issues/recovery-snapshot", r.URL.Path)
				require.Empty(t, r.URL.RawQuery)
				require.Equal(t, "Bearer token", r.Header.Get("Authorization"))
				body, err := io.ReadAll(r.Body)
				require.NoError(t, err)
				require.Empty(t, body)
				w.Header().Set("Content-Type", "application/json; charset=utf-8")
				w.Header().Set("X-Fleet-Source-Identity", "s1.Zml4dHVyZQ")
				_, _ = w.Write(raw)
			})
			result, err := b.ReadIssueRecovery(context.Background())
			require.NoError(t, err)
			require.Equal(t, json.RawMessage(raw), result.Document)
			require.Equal(t, "WS", result.Workspace)
			require.Equal(t, "c2.MTAtMA", result.Through)
		})
	}
}
func TestReadIssueRecoveryRejectsInvalidDocument(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(map[string]any)
		suffix string
	}{
		{name: "legacy v3 manifest", mutate: func(d map[string]any) { d["manifest"] = "fleet.issue-workspace.v3" }},
		{name: "legacy v2 manifest", mutate: func(d map[string]any) { d["manifest"] = "fleet.issue-workspace.v2" }},
		{name: "legacy lower-bound v1 manifest", mutate: func(d map[string]any) { d["manifest"] = "fleet.issue-workspace.v1" }},
		{name: "case alias manifest", mutate: func(d map[string]any) { d["Manifest"] = d["manifest"]; delete(d, "manifest") }},

		{name: "missing manifest", mutate: func(d map[string]any) { delete(d, "manifest") }},
		{name: "foreign workspace", mutate: func(d map[string]any) { d["workspace"] = "OTHER" }},
		{name: "missing issues", mutate: func(d map[string]any) { delete(d, "issues") }},
		{name: "null ready", mutate: func(d map[string]any) { d["ready"] = nil }},
		{name: "wrong total", mutate: func(d map[string]any) { d["total"] = 1 }},
		{name: "zero cursor", mutate: func(d map[string]any) { d["through"] = "0" }},
		{name: "head cursor", mutate: func(d map[string]any) { d["through"] = "$" }},
		{name: "malformed cursor", mutate: func(d map[string]any) { d["through"] = "10-0" }},
		{name: "trailing JSON", suffix: "{}"},
		{name: "missing required issue field", mutate: func(d map[string]any) {
			i := recoveryTestIssue("WS-1")
			delete(i, "created_at")
			d["issues"] = []any{i}
			d["total"] = 1
		}},
		{name: "cross workspace issue", mutate: func(d map[string]any) {
			i := recoveryTestIssue("WS-1")
			i["workspace"] = "OTHER"
			d["issues"] = []any{i}
			d["total"] = 1
		}},
		{name: "duplicate issue", mutate: func(d map[string]any) { i := recoveryTestIssue("WS-1"); d["issues"] = []any{i, i}; d["total"] = 2 }},
		{name: "derived missing issue", mutate: func(d map[string]any) { d["ready"] = []any{recoveryTestIssue("WS-1")} }},
		{name: "derived changed metadata", mutate: func(d map[string]any) {
			i := recoveryTestIssue("WS-1")
			j := recoveryTestIssue("WS-1")
			j["metadata"] = map[string]string{"changed": "yes"}
			d["issues"] = []any{i}
			d["total"] = 1
			d["ready"] = []any{j}
		}},
		{name: "alias only repo", mutate: func(d map[string]any) {
			i := recoveryTestIssue("WS-1")
			i["source_repo"] = "repo"
			d["issues"] = []any{i}
			d["total"] = 1
		}},
		{name: "null metadata", mutate: func(d map[string]any) {
			i := recoveryTestIssue("WS-1")
			i["metadata"] = nil
			d["issues"] = []any{i}
			d["total"] = 1
		}},
		{name: "null labels", mutate: func(d map[string]any) {
			i := recoveryTestIssue("WS-1")
			i["labels"] = nil
			d["issues"] = []any{i}
			d["total"] = 1
		}},

		{name: "missing metadata", mutate: func(d map[string]any) {
			i := recoveryTestIssue("WS-1")
			delete(i, "metadata")
			d["issues"] = []any{i}
			d["total"] = 1
		}},
		{name: "missing labels", mutate: func(d map[string]any) {
			i := recoveryTestIssue("WS-1")
			delete(i, "labels")
			d["issues"] = []any{i}
			d["total"] = 1
		}},
		{name: "conflicting alias", mutate: func(d map[string]any) {
			i := recoveryTestIssue("WS-1")
			i["repo"] = "one"
			i["source_repo"] = "two"
			d["issues"] = []any{i}
			d["total"] = 1
		}},

		{name: "invalid metadata", mutate: func(d map[string]any) {
			i := recoveryTestIssue("WS-1")
			i["metadata"] = map[string]any{"bad": nil}
			d["issues"] = []any{i}
			d["total"] = 1
		}},
		{name: "invalid estimate", mutate: func(d map[string]any) {
			i := recoveryTestIssue("WS-1")
			i["estimated_minutes"] = "42"
			d["issues"] = []any{i}
			d["total"] = 1
		}},

		{name: "legacy envelope", mutate: func(d map[string]any) {
			for k := range d {
				delete(d, k)
			}
			d["success"] = true
			d["data"] = recoveryTestDocument()
		}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			d := recoveryTestDocument()
			if tc.mutate != nil {
				tc.mutate(d)
			}
			raw, err := json.Marshal(d)
			require.NoError(t, err)
			b := recoveryTestBackend(t, func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.Header().Set("X-Fleet-Source-Identity", "s1.Zml4dHVyZQ")
				_, _ = w.Write(append(raw, tc.suffix...))
			})
			result, err := b.ReadIssueRecovery(context.Background())
			require.Error(t, err)
			require.Empty(t, result.Document)
		})
	}
}
func TestReadIssueRecoveryHTTPFailures(t *testing.T) {
	for _, tc := range []struct {
		name              string
		status            int
		contentType, body string
	}{
		{"error", 503, "application/json", `{"error":{"code":"recovery_snapshot_unsupported"}}`},
		{"empty success", 204, "application/json", ""},
		{"wrong media", 200, "text/plain", `{}`},
		{"malformed", 200, "application/json", `{`},
		{"oversized", 200, "application/json", strings.Repeat(" ", recoveryBodyLimit+1)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b := recoveryTestBackend(t, func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", tc.contentType)
				w.WriteHeader(tc.status)
				_, _ = io.WriteString(w, tc.body)
			})
			result, err := b.ReadIssueRecovery(context.Background())
			require.Error(t, err)
			var typed *backend.BackendError
			require.ErrorAs(t, err, &typed)
			require.Empty(t, result.Document)
		})
	}
}
func TestReadIssueRecoveryRejectsRedirectAndCancellation(t *testing.T) {
	called := false
	destination := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { called = true }))
	defer destination.Close()
	b := recoveryTestBackend(t, func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, destination.URL, http.StatusTemporaryRedirect)
	})
	result, err := b.ReadIssueRecovery(context.Background())
	require.Error(t, err)
	require.Empty(t, result.Document)
	require.False(t, called)
	started := make(chan struct{})
	b = recoveryTestBackend(t, func(w http.ResponseWriter, r *http.Request) { close(started); <-r.Context().Done() })
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		result, err := b.ReadIssueRecovery(ctx)
		if len(result.Document) != 0 {
			t.Error("canceled request returned document")
		}
		done <- err
	}()
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("request did not start")
	}
	cancel()
	select {
	case err := <-done:
		require.Error(t, err)
	case <-time.After(3 * time.Second):
		t.Fatal("canceled request did not finish")
	}
}

func TestReadIssueRecoveryParentBlockerSentinel(t *testing.T) {
	for _, bad := range []bool{false, true} {
		t.Run(map[bool]string{false: "canonical", true: "malformed"}[bad], func(t *testing.T) {
			d := recoveryTestDocument()
			issue := recoveryTestIssue("WS-1")
			blocker := map[string]any{"id": "", "title": "", "status": "", "priority": 0, "reason": "parent-blocked", "dep_type": "parent-child"}
			if bad {
				blocker["id"] = "WS-1"
			}
			d["issues"] = []any{issue}
			d["total"] = 1
			d["blocked"] = []any{map[string]any{"issue": issue, "blockers": []any{blocker}}}
			raw, err := json.Marshal(d)
			require.NoError(t, err)
			b := recoveryTestBackend(t, func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.Header().Set("X-Fleet-Source-Identity", "s1.Zml4dHVyZQ")
				_, _ = w.Write(raw)
			})
			result, err := b.ReadIssueRecovery(context.Background())
			if bad {
				require.Error(t, err)
				require.Empty(t, result.Document)
			} else {
				require.NoError(t, err)
				require.Equal(t, json.RawMessage(raw), result.Document)
			}
		})
	}
}
