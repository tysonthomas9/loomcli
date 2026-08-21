package modbuilder

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/backend"
	backendapi "github.com/tysonthomas9/loomcli/internal/backend/api"
	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/infra/memstore"
	"github.com/tysonthomas9/loomcli/internal/leadtoken"
	"github.com/tysonthomas9/loomcli/internal/rpc"
	"github.com/tysonthomas9/loomcli/internal/store"
	"github.com/tysonthomas9/loomcli/internal/webui/daemon"
	"github.com/tysonthomas9/loomcli/internal/webui/handlermux"
	"github.com/tysonthomas9/loomcli/internal/webui/server/middleware"
	"github.com/tysonthomas9/loomcli/internal/workflows"
)

type leadAPIRoundTripFunc func(*http.Request) (*http.Response, error)

func (f leadAPIRoundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }

type poisonLeadDataPool struct {
	gets atomic.Int64
}

func (p *poisonLeadDataPool) Get(context.Context) (*rpc.Client, error) {
	p.gets.Add(1)
	return nil, context.DeadlineExceeded
}
func (*poisonLeadDataPool) Put(*rpc.Client)           {}
func (*poisonLeadDataPool) PutAfterError(*rpc.Client) {}
func (*poisonLeadDataPool) Discard(*rpc.Client)       {}
func (*poisonLeadDataPool) Stats() daemon.PoolStats   { return daemon.PoolStats{} }
func (*poisonLeadDataPool) Close() error              { return nil }

func TestNewLeadAPIModule_UsesActorKeyedBackendAndNeverPool(t *testing.T) {
	var paths []string
	apiClient := &http.Client{Transport: leadAPIRoundTripFunc(func(req *http.Request) (*http.Response, error) {
		paths = append(paths, req.URL.Path)
		body := `{"success":true,"data":[]}`
		if strings.HasSuffix(req.URL.Path, "/stats") {
			body = `{"success":true,"data":{"total_issues":3}}`
		}
		return leadAPIHTTPResponse(http.StatusOK, body), nil
	})}
	apiBackend, err := backendapi.New(backendapi.Config{BaseURL: "http://loom.test", WorkspaceID: "WS", HTTPClient: apiClient})
	if err != nil {
		t.Fatalf("api.New: %v", err)
	}
	st, key, token := leadAPITestIdentity(t)
	var actors []string
	backendFn := func(ctx context.Context) backend.IssueBackend {
		actor, ok := middleware.ActorFromContext(ctx)
		if !ok {
			t.Error("IssueBackendFn context missing actor")
		} else {
			actors = append(actors, actor.BackendActor())
		}
		return apiBackend
	}
	pool := &poisonLeadDataPool{}
	mux := http.NewServeMux()
	handlermux.NewWorkspaceOpsModule(nil, pool, nil).WithIssueBackendFn(backendFn).Register(mux)
	NewLeadAPIModule(LeadAPIDeps{Store: st, TokenKey: key, IssueBackendFn: backendFn}).Register(mux)

	for _, path := range []string{
		"/api/workspaces/WS/lead/data/issues",
		"/api/workspaces/WS/lead/data/ready",
		"/api/workspaces/WS/lead/data/blocked",
		"/api/workspaces/WS/lead/data/stats",
	} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req.Header.Set("Authorization", "Bearer "+token)
		req = req.WithContext(middleware.WithWorkspace(req.Context(), "WS"))
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("GET %s status = %d, want 200; body = %s", path, rec.Code, rec.Body.String())
		}
	}
	if got := pool.gets.Load(); got != 0 {
		t.Fatalf("daemon pool Get calls = %d, want 0", got)
	}
	if len(paths) != 4 {
		t.Fatalf("backend paths = %v, want four calls", paths)
	}
	for i, actor := range actors {
		if actor != "lead-occupant:p1" {
			t.Fatalf("backend call %d actor = %q, want lead-occupant:p1", i, actor)
		}
	}
}

func TestNewLeadAPIModule_AbsentWithoutIssueBackendFn(t *testing.T) {
	st, key, token := leadAPITestIdentity(t)
	mux := http.NewServeMux()
	NewLeadAPIModule(LeadAPIDeps{Store: st, TokenKey: key}).Register(mux)
	req := httptest.NewRequest(http.MethodGet, "/api/workspaces/WS/lead/data/issues", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	req = req.WithContext(middleware.WithWorkspace(req.Context(), "WS"))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404; body = %s", rec.Code, rec.Body.String())
	}
}

func TestNewLeadAPIModule_DispatchUsesRealWiringAndNeverPool(t *testing.T) {
	apiClient := &http.Client{Transport: leadAPIRoundTripFunc(func(req *http.Request) (*http.Response, error) {
		if req.URL.Path != "/api/workspaces/WS/issues/epic-1" {
			return leadAPIHTTPResponse(http.StatusNotFound, `{"success":false,"error":"not found","code":"not_found"}`), nil
		}
		return leadAPIHTTPResponse(http.StatusOK,
			`{"success":true,"data":{"id":"epic-1","title":"Epic","issue_type":"epic","source_repo":"repo-1"}}`), nil
	})}
	apiBackend, err := backendapi.New(backendapi.Config{
		BaseURL: "http://loom.test", WorkspaceID: "WS", HTTPClient: apiClient,
	})
	if err != nil {
		t.Fatal(err)
	}
	st, key, _ := leadAPITestIdentity(t)
	seedLeadAPIDispatchStore(t, st)
	backendFn := func(context.Context) backend.IssueBackend { return apiBackend }
	pool := &poisonLeadDataPool{}
	mux := http.NewServeMux()
	handlermux.NewWorkspaceOpsModule(nil, pool, nil).WithIssueBackendFn(backendFn).Register(mux)
	NewLeadAPIModule(LeadAPIDeps{Store: st, TokenKey: key, IssueBackendFn: backendFn}).Register(mux)
	token, err := leadtoken.MintOccupantToken(leadtoken.OccupantClaims{
		WorkspaceKey: "WS", PlacementID: "p1", Generation: 7,
		Caps: []string{leadtoken.CapLeadDispatch},
	}, key, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/workspaces/WS/lead/dispatch/epic-run",
		strings.NewReader(`{"epicId":"epic-1"}`))
	req.Header.Set("Authorization", "Bearer "+token)
	req = req.WithContext(middleware.WithWorkspace(req.Context(), "WS"))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202; body = %s", rec.Code, rec.Body.String())
	}
	if got := pool.gets.Load(); got != 0 {
		t.Fatalf("daemon pool Get calls = %d, want 0", got)
	}
}

func seedLeadAPIDispatchStore(t *testing.T, st store.Store) {
	t.Helper()
	ctx := context.Background()
	if _, err := st.AgentSessions().Create(ctx, store.AgentSessionCreate{
		WorkspaceKey: "WS", SessionID: "lead-session", AgentID: "lead", NodeID: "p1",
		Kind: domain.AgentSessionKindOrchestration, Status: domain.AgentSessionRunning,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Repos().Create(ctx, store.RepoCreate{
		WorkspaceKey: "WS", Name: "repo-1", RemoteURL: "https://github.com/octocat/hello", DefaultBranch: "main",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Drivers().Create(ctx, store.DriverCreate{
		WorkspaceKey: "WS", DriverID: workflows.BuiltinEpicRunnerWorkflowName,
		Name: workflows.BuiltinEpicRunnerWorkflowName, OwnerType: domain.DriverOwnerSystem,
		ActiveVersionID: "epic-version", Status: domain.DriverStatusActive, TrustLevel: domain.DriverTrustTrusted,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.DriverVersions().Create(ctx, store.DriverVersionCreate{
		WorkspaceKey: "WS", VersionID: "epic-version", DriverID: workflows.BuiltinEpicRunnerWorkflowName,
		Version: 1, SourceDigest: "sha256:source", BundleDigest: "sha256:bundle",
		ValidationStatus: domain.DriverVersionValidationPassed,
	}); err != nil {
		t.Fatal(err)
	}
}

func TestNewLeadAPIModule_OccupantMutationBodyCaps(t *testing.T) {
	st, key, token := leadAPITestIdentity(t)
	backendCalls := atomic.Int64{}
	backendFn := func(context.Context) backend.IssueBackend {
		backendCalls.Add(1)
		return nil
	}
	mux := http.NewServeMux()
	NewLeadAPIModule(LeadAPIDeps{Store: st, TokenKey: key, IssueBackendFn: backendFn}).Register(mux)
	large := strings.Repeat("x", (1<<20)+1)
	tests := []struct {
		name string
		path string
		body string
	}{
		{"create", "/api/workspaces/WS/lead/data/issues", `{"title":"` + large + `"}`},
		{"patch", "/api/workspaces/WS/lead/data/issues/i1", `{"description":"` + large + `"}`},
		{"comment", "/api/workspaces/WS/lead/data/issues/i1/comments", `{"text":"` + large + `"}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			method := http.MethodPost
			if tt.name == "patch" {
				method = http.MethodPatch
			}
			req := httptest.NewRequest(method, tt.path, strings.NewReader(tt.body))
			req.Header.Set("Authorization", "Bearer "+token)
			req = req.WithContext(middleware.WithWorkspace(req.Context(), "WS"))
			rec := httptest.NewRecorder()
			mux.ServeHTTP(rec, req)
			if rec.Code != http.StatusRequestEntityTooLarge {
				t.Fatalf("status = %d, want 413; body = %s", rec.Code, rec.Body.String())
			}
		})
	}
	if got := backendCalls.Load(); got != 0 {
		t.Fatalf("backend calls = %d, want zero", got)
	}
}

func leadAPITestIdentity(t *testing.T) (store.Store, []byte, string) {
	t.Helper()
	st := memstore.New()
	_, err := st.Nodes().Create(context.Background(), store.NodeCreate{
		WorkspaceKey: "WS",
		NodeID:       "p1",
		OwnerActor:   "agent:lead",
		Labels:       []string{"loom-agent=lead"},
		Placement: &domain.NodePlacement{
			SandboxID:  "sandbox-p1",
			Generation: 7,
			State:      domain.PlacementStateActive,
		},
	})
	if err != nil {
		t.Fatalf("create placement: %v", err)
	}
	key := bytes.Repeat([]byte{0x42}, 32)
	token, err := leadtoken.MintOccupantToken(leadtoken.OccupantClaims{
		WorkspaceKey: "WS",
		PlacementID:  "p1",
		Generation:   7,
		Caps:         []string{leadtoken.CapLeadData},
	}, key, time.Hour)
	if err != nil {
		t.Fatalf("MintOccupantToken: %v", err)
	}
	return st, key, token
}

func leadAPIHTTPResponse(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}
