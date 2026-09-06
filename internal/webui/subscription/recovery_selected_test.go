package subscription

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/tysonthomas9/loomcli/internal/backend"
	"github.com/tysonthomas9/loomcli/internal/backend/fleet"
	"github.com/tysonthomas9/loomcli/internal/webui/server/middleware"
	"github.com/tysonthomas9/loomcli/internal/webui/server/realtime"
)

func TestSelectedRecoveryCapturedHTTPChain(t *testing.T) {
	calls := 0
	sourceIdentity := "s1.Zml4dHVyZQ"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodGet {
			_, _ = w.Write([]byte(`{"success":true,"data":{"events":[],"cursor":"c2.MS0w","has_more":false,"source_identity":"s1.Zml4dHVyZQ"}}`))
			return
		}
		calls++
		w.Header().Set("X-Fleet-Source-Identity", sourceIdentity)
		id := r.URL.Query().Get("issue_id")
		require.NotEmpty(t, id)
		document := map[string]any{"manifest": "fleet.issue-workspace.v6", "workspace": "WS", "through": "c2.MS0w", "issues": []any{}, "total": 0, "ready": []any{}, "blocked": []any{}, "deferred": []any{}, "dependencies": []any{}, "comments": []any{}, "history": map[string]any{"issue_id": id, "present": false, "events": []any{}, "has_older": false, "timeline": []any{}}}
		require.NoError(t, json.NewEncoder(w).Encode(document))
	}))
	defer server.Close()
	fb, err := fleet.New(fleet.Config{BaseURL: server.URL, WorkspaceID: "WS", HTTPClient: server.Client()})
	require.NoError(t, err)
	lifetime, cancel := context.WithCancel(t.Context())
	defer cancel()
	sub := &BackendMutationSubscriber{backend: fb, ctx: lifetime}
	manager := &MultiWorkspaceSubscriber{subscribers: map[string]*subscriberEntry{"WS": {sub: sub}}}
	source, err := manager.OpenMutationSource(t.Context(), "WS")
	require.NoError(t, err)
	_, err = source.ReadHead(t.Context())
	require.NoError(t, err)
	registry := realtime.NewRecoveryRegistry()
	defer registry.Close()
	handle, err := registry.Register("user", "WS", nil, source.(backend.IssueRecoveryBackend), sourceIdentity)
	require.NoError(t, err)
	handler := handleIssueRecovery(registry, middleware.WorkspaceFromContext)
	for _, id := range []string{"A & query", "B", "A & query"} {
		request := httpRecoveryRequest("user", "WS", handle.Handle)
		request.URL.RawQuery = url.Values{"issue_id": []string{id}}.Encode()
		recorder := httptest.NewRecorder()
		handler(recorder, request)
		require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
		var result struct {
			History struct {
				IssueID string `json:"issue_id"`
			} `json:"history"`
		}
		require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &result))
		require.Equal(t, id, result.History.IssueID)
	}
	require.Equal(t, 3, calls)
	request := httpRecoveryRequest("foreign", "WS", handle.Handle)
	request.URL.RawQuery = "issue_id=A"
	recorder := httptest.NewRecorder()
	handler(recorder, request)
	require.Equal(t, http.StatusGone, recorder.Code)
	require.Equal(t, 3, calls)
	sourceIdentity = "s1.b3RoZXI"
	request = httpRecoveryRequest("user", "WS", handle.Handle)
	request.URL.RawQuery = "issue_id=A"
	recorder = httptest.NewRecorder()
	handler(recorder, request)
	require.NotEqual(t, http.StatusOK, recorder.Code)
	sourceIdentity = "s1.Zml4dHVyZQ"
	recorder = httptest.NewRecorder()
	handler(recorder, request)
	require.NotEqual(t, http.StatusOK, recorder.Code)
	require.Equal(t, 4, calls, "identity mismatch permanently retires captured source")
}

func TestSelectedRecoveryHTTPRejectsMalformedSelection(t *testing.T) {
	for _, query := range []string{"issue_id=", "issue_id=+", "issue_id=A&issue_id=B", "issue_id=A&other=x", "issue_id=%ff", "issue_id=%zz", "issue_id=A;other=B"} {
		request := httpRecoveryRequest("user", "WS", "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA")
		request.URL.RawQuery = query
		recorder := httptest.NewRecorder()
		handleIssueRecovery(nil, middleware.WorkspaceFromContext)(recorder, request)
		require.Equal(t, http.StatusBadRequest, recorder.Code, query)
	}
}
