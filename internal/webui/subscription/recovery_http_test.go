package subscription

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/tysonthomas9/loomcli/internal/backend"
	"github.com/tysonthomas9/loomcli/internal/webui/server/middleware"
	"github.com/tysonthomas9/loomcli/internal/webui/server/realtime"
)

type httpRecoverySource func(context.Context) (backend.IssueRecoverySnapshot, error)

func (f httpRecoverySource) ReadIssueRecovery(ctx context.Context) (backend.IssueRecoverySnapshot, error) {
	return f(ctx)
}

func httpRecoveryRequest(principal, workspace, handle string) *http.Request {
	req := httptest.NewRequest(http.MethodPost, "/api/workspaces/"+workspace+"/events/recovery/issues", nil)
	ctx := middleware.WithWorkspace(req.Context(), workspace)
	if principal != "" {
		ctx = middleware.WithUserIdentity(ctx, middleware.UserIdentity{UserID: principal})
	}
	req = req.WithContext(ctx)
	req.Header.Set(recoveryHandleHeader, handle)
	return req
}

func TestRecoveryHTTPUsesCapturedSourceAndPreservesDocument(t *testing.T) {
	registry := realtime.NewRecoveryRegistry()
	defer registry.Close()
	raw := []byte(`{ "manifest":"fleet.issue-workspace.v1", "workspace":"WS", "through":"c1.MS0w", "issues":[], "total":0, "ready":[], "blocked":[], "deferred":[], "future":{"keep":"verbatim"} }`)
	oldCalls, newCalls := 0, 0
	var selected backend.IssueRecoveryBackend = httpRecoverySource(func(ctx context.Context) (backend.IssueRecoverySnapshot, error) {
		oldCalls++
		deadline, ok := ctx.Deadline()
		require.True(t, ok)
		require.LessOrEqual(t, time.Until(deadline), 15*time.Second)
		return backend.IssueRecoverySnapshot{Manifest: "fleet.issue-workspace.v1", Workspace: "WS", Through: "c1.MS0w", Document: raw}, nil
	})
	handle, err := registry.Register("user", "WS", nil, selected)
	require.NoError(t, err)
	selected = httpRecoverySource(func(context.Context) (backend.IssueRecoverySnapshot, error) {
		newCalls++
		return backend.IssueRecoverySnapshot{}, errors.New("replacement must not be selected")
	})
	require.NotNil(t, selected)
	recorder := httptest.NewRecorder()
	handleIssueRecovery(registry, middleware.WorkspaceFromContext)(recorder, httpRecoveryRequest("user", "WS", handle.Handle))
	require.Equal(t, http.StatusOK, recorder.Code)
	require.Equal(t, string(raw), recorder.Body.String())
	require.Equal(t, "application/json", recorder.Header().Get("Content-Type"))
	require.Equal(t, "no-store", recorder.Header().Get("Cache-Control"))
	require.Equal(t, handle.Handle, recorder.Header().Get(recoveryHandleHeader))
	require.Equal(t, 1, oldCalls)
	require.Zero(t, newCalls)
}

type unreadRecoveryBody struct{ reads int }

func (b *unreadRecoveryBody) Read([]byte) (int, error) { b.reads++; return 0, io.EOF }
func (*unreadRecoveryBody) Close() error               { return nil }

func TestRecoveryHTTPRejectsUnscopedAndMalformedRequestsWithoutReading(t *testing.T) {
	registry := realtime.NewRecoveryRegistry()
	defer registry.Close()
	calls := 0
	handle, err := registry.Register("user", "WS", nil, httpRecoverySource(func(context.Context) (backend.IssueRecoverySnapshot, error) {
		calls++
		return backend.IssueRecoverySnapshot{}, errors.New("must not read")
	}))
	require.NoError(t, err)
	tests := []struct {
		name   string
		status int
		mutate func(*http.Request)
	}{
		{"no JWT identity", 401, func(r *http.Request) {
			*r = *httpRecoveryRequest("", "WS", handle.Handle)
			r.Header.Set("X-Actor", "user")
		}},
		{"other principal", 410, func(r *http.Request) {
			*r = *r.WithContext(middleware.WithUserIdentity(r.Context(), middleware.UserIdentity{UserID: "other"}))
		}},
		{"other workspace", 410, func(r *http.Request) { *r = *r.WithContext(middleware.WithWorkspace(r.Context(), "OTHER")) }},
		{"missing handle", 400, func(r *http.Request) { r.Header.Del(recoveryHandleHeader) }},
		{"multiple handles", 400, func(r *http.Request) { r.Header.Add(recoveryHandleHeader, handle.Handle) }},
		{"case duplicate", 400, func(r *http.Request) { r.Header["x-loom-recovery-handle"] = []string{handle.Handle} }},
		{"noncanonical handle", 400, func(r *http.Request) { r.Header.Set(recoveryHandleHeader, " "+handle.Handle) }},
		{"query", 400, func(r *http.Request) { r.URL.RawQuery = "repo=a" }},
		{"bare query", 400, func(r *http.Request) { r.URL.ForceQuery = true }},
		{"scope header", 400, func(r *http.Request) { r.Header.Set("X-Fleet-Repo", "") }},
		{"body", 400, func(r *http.Request) { r.Body = &unreadRecoveryBody{} }},
		{"content length", 400, func(r *http.Request) { r.ContentLength = 1 }},
		{"chunked", 400, func(r *http.Request) { r.TransferEncoding = []string{"chunked"} }},
		{"method", 405, func(r *http.Request) { r.Method = http.MethodGet }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			req := httpRecoveryRequest("user", "WS", handle.Handle)
			test.mutate(req)
			recorder := httptest.NewRecorder()
			handleIssueRecovery(registry, middleware.WorkspaceFromContext)(recorder, req)
			require.Equal(t, test.status, recorder.Code)
			require.Equal(t, "no-store", recorder.Header().Get("Cache-Control"))
			require.Empty(t, recorder.Header().Get(recoveryHandleHeader))
			if body, ok := req.Body.(*unreadRecoveryBody); ok {
				require.Zero(t, body.reads)
			}
		})
	}
	require.Zero(t, calls)
}

func TestRecoveryHTTPErrorsNeverExposeSourceData(t *testing.T) {
	for _, test := range []struct {
		name   string
		err    error
		status int
	}{{"source", errors.New("secret upstream response"), 503}, {"canceled", context.Canceled, 504}, {"deadline", context.DeadlineExceeded, 504}} {
		t.Run(test.name, func(t *testing.T) {
			registry := realtime.NewRecoveryRegistry()
			defer registry.Close()
			handle, err := registry.Register("user", "WS", nil, httpRecoverySource(func(context.Context) (backend.IssueRecoverySnapshot, error) {
				return backend.IssueRecoverySnapshot{Document: []byte("secret")}, test.err
			}))
			require.NoError(t, err)
			rec := httptest.NewRecorder()
			handleIssueRecovery(registry, nil)(rec, httpRecoveryRequest("user", "WS", handle.Handle))
			require.Equal(t, test.status, rec.Code)
			require.NotContains(t, rec.Body.String(), "secret")
			require.Empty(t, rec.Header().Get(recoveryHandleHeader))
		})
	}
}

func TestRecoveryHTTPBusyAndClosedRegistry(t *testing.T) {
	registry := realtime.NewRecoveryRegistry()
	defer registry.Close()
	entered := make(chan struct{})
	release := make(chan struct{})
	handle, err := registry.Register("user", "WS", nil, httpRecoverySource(func(ctx context.Context) (backend.IssueRecoverySnapshot, error) {
		close(entered)
		select {
		case <-release:
		case <-ctx.Done():
		}
		return backend.IssueRecoverySnapshot{}, errors.New("released")
	}))
	require.NoError(t, err)
	done := make(chan struct{})
	go func() { defer close(done); _, _ = registry.Read(t.Context(), "user", "WS", handle.Handle) }()
	<-entered
	rec := httptest.NewRecorder()
	handleIssueRecovery(registry, nil)(rec, httpRecoveryRequest("user", "WS", handle.Handle))
	require.Equal(t, 409, rec.Code)
	close(release)
	<-done
	registry.Close()
	rec = httptest.NewRecorder()
	handleIssueRecovery(registry, nil)(rec, httpRecoveryRequest("user", "WS", handle.Handle))
	require.Equal(t, 410, rec.Code)
	require.False(t, strings.Contains(rec.Body.String(), handle.Handle))
}

func TestRecoveryHTTPCanceledReadCannotPublishSuccess(t *testing.T) {
	registry := realtime.NewRecoveryRegistry()
	defer registry.Close()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	handle, err := registry.Register("user", "WS", nil, httpRecoverySource(func(context.Context) (backend.IssueRecoverySnapshot, error) {
		cancel()
		return backend.IssueRecoverySnapshot{Manifest: "fleet.issue-workspace.v1", Workspace: "WS", Through: "c1.MS0w", Document: []byte(`{"secret":"late success"}`)}, nil
	}))
	require.NoError(t, err)
	req := httpRecoveryRequest("user", "WS", handle.Handle)
	req = req.WithContext(middleware.WithUserIdentity(middleware.WithWorkspace(ctx, "WS"), middleware.UserIdentity{UserID: "user"}))
	rec := httptest.NewRecorder()
	handleIssueRecovery(registry, nil)(rec, req)
	require.Equal(t, http.StatusGatewayTimeout, rec.Code)
	require.NotContains(t, rec.Body.String(), "secret")
	require.Empty(t, rec.Header().Get(recoveryHandleHeader))
}
