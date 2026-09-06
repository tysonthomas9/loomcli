package subscription

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/tysonthomas9/loomcli/internal/backend"
	"github.com/tysonthomas9/loomcli/internal/webui/server/middleware"
	"github.com/tysonthomas9/loomcli/internal/webui/server/realtime"
)

// The actual SSE handler, token validator, module HTTP route and captured
// subscriber run together. Only backend effects and REST identity are fixtures.
func TestRecoveryBridgeSurvivesExpiredStreamAndRejectsReplacement(t *testing.T) {
	hub := realtime.NewHub()
	go hub.Run()
	t.Cleanup(hub.Stop)
	tokens, err := realtime.NewTokenStore()
	require.NoError(t, err)
	t.Cleanup(tokens.Stop)
	calls := 0
	sub := &recoverySubscriber{
		sourceBindingSubscriber: sourceBindingSubscriber{
			head: func(context.Context) (backend.MutationPage, error) {
				return backend.MutationPage{Cursor: "c1.MTAtMA"}, nil
			},
			page: func(context.Context, string, string, int) (backend.MutationPage, error) {
				return backend.MutationPage{}, backend.ErrMutationCursorExpired
			},
		},
		recover: func(context.Context) (backend.IssueRecoverySnapshot, error) {
			calls++
			return backend.IssueRecoverySnapshot{Manifest: "fleet.issue-workspace.v2", Workspace: "ws", Through: "c1.MTAtMA", Document: []byte(`{"manifest":"fleet.issue-workspace.v2","workspace":"ws","through":"c1.MTAtMA","issues":[],"total":0,"ready":[],"blocked":[],"deferred":[]}`)}, nil
		},
	}
	multi := &MultiWorkspaceSubscriber{subscribers: map[string]*subscriberEntry{"ws": {sub: sub}}}
	mux := http.NewServeMux()
	NewModule(hub, multi.OpenMutationSource, middleware.WorkspaceFromContext, nil, tokens).Register(mux)
	token, err := tokens.Generate("alice", "ws")
	require.NoError(t, err)
	ctx, cancel := context.WithTimeout(middleware.WithWorkspace(t.Context(), "ws"), time.Second)
	defer cancel()
	request := httptest.NewRequest(http.MethodGet, "/api/workspaces/ws/events?source_repos=repo-a&token="+url.QueryEscape(token), nil).WithContext(ctx)
	request.Header.Set("Last-Event-ID", "c1.MS0w")
	stream := httptest.NewRecorder()
	protected := middleware.Auth(middleware.AuthConfig{JWKSCache: &middleware.JWKSCache{}})(mux)
	protected.ServeHTTP(stream, request)
	require.NoError(t, ctx.Err(), "expired replay should return before deadline")
	require.NotContains(t, stream.Body.String(), "id:")
	require.NotContains(t, stream.Body.String(), "event: connected")
	var frame struct {
		Reason   string                  `json:"reason"`
		Recovery realtime.RecoveryHandle `json:"recovery"`
	}
	for _, line := range strings.Split(stream.Body.String(), "\n") {
		if strings.HasPrefix(line, "data: ") {
			require.NoError(t, json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &frame))
		}
	}
	require.Equal(t, "expired", frame.Reason)
	require.True(t, realtime.ValidRecoveryHandle(frame.Recovery.Handle))
	require.Equal(t, []string{"repo-a"}, frame.Recovery.SourceRepos)
	cancel() // HTTP recovery has its own lifetime after SSE closes.
	unauthenticated := httptest.NewRequest(http.MethodPost, "/api/workspaces/ws/events/recovery/issues", nil)
	unauthenticated.Header.Set("X-Loom-Recovery-Handle", frame.Recovery.Handle)
	denied := httptest.NewRecorder()
	protected.ServeHTTP(denied, unauthenticated)
	require.Equal(t, http.StatusUnauthorized, denied.Code, "a handle cannot bypass normal JWT middleware")
	require.Zero(t, calls)
	read := func(user, ws string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/api/workspaces/"+ws+"/events/recovery/issues", nil)
		req = req.WithContext(middleware.WithUserIdentity(middleware.WithWorkspace(t.Context(), ws), middleware.UserIdentity{UserID: user}))
		req.Header.Set("X-Loom-Recovery-Handle", frame.Recovery.Handle)
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		return rec
	}
	require.Equal(t, http.StatusGone, read("bob", "ws").Code)
	require.Zero(t, calls)
	require.Equal(t, http.StatusGone, read("alice", "other").Code)
	require.Zero(t, calls)
	valid := read("alice", "ws")
	require.Equal(t, http.StatusOK, valid.Code, valid.Body.String())
	require.Equal(t, 1, calls)
	require.Equal(t, frame.Recovery.Handle, valid.Header().Get("X-Loom-Recovery-Handle"))
	multi.mu.Lock()
	multi.subscribers["ws"] = &subscriberEntry{sub: sub}
	multi.mu.Unlock()
	retired := read("alice", "ws")
	require.NotEqual(t, http.StatusOK, retired.Code)
	require.Equal(t, 1, calls, "must not reselect equal backend object through new registration")
	require.NotContains(t, retired.Body.String(), "through")
}
