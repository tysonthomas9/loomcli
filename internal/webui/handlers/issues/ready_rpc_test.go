package issues

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/rpc"
	"github.com/tysonthomas9/loomcli/internal/types"
	"github.com/tysonthomas9/loomcli/internal/webui/daemon"
)

type readyRPCClient struct {
	ready        func(*rpc.ReadyArgs) (*rpc.Response, error)
	list         func(*rpc.ListArgs) (*rpc.Response, error)
	getParentIDs func(*rpc.GetParentIDsArgs) (*rpc.GetParentIDsResponse, error)
}

func (c *readyRPCClient) Ready(args *rpc.ReadyArgs) (*rpc.Response, error) {
	if c.ready != nil {
		return c.ready(args)
	}
	return nil, errors.New("ready not implemented")
}

func (c *readyRPCClient) List(args *rpc.ListArgs) (*rpc.Response, error) {
	if c.list != nil {
		return c.list(args)
	}
	return nil, errors.New("list not implemented")
}

func (c *readyRPCClient) GetParentIDs(args *rpc.GetParentIDsArgs) (*rpc.GetParentIDsResponse, error) {
	if c.getParentIDs != nil {
		return c.getParentIDs(args)
	}
	return nil, errors.New("get parents not implemented")
}

type readyRPCPool struct {
	client     readyClient
	getErr     error
	put        int
	discard    int
	lastClient readyClient
}

func (p *readyRPCPool) Get(context.Context) (readyClient, error) {
	if p.getErr != nil {
		return nil, p.getErr
	}
	return p.client, nil
}

func (p *readyRPCPool) Put(client readyClient) {
	p.put++
	p.lastClient = client
}

func (p *readyRPCPool) Discard(client readyClient) {
	p.discard++
	p.lastClient = client
}

func TestExecuteReadyRPCFiltersUnclosedBlockers(t *testing.T) {
	issues := []*types.Issue{
		{ID: "READY", Title: "ready", Status: types.StatusOpen},
		{
			ID:     "BLOCKED",
			Title:  "blocked",
			Status: types.StatusOpen,
			Dependencies: []*types.Dependency{{
				IssueID:     "BLOCKED",
				DependsOnID: "OPEN-BLOCKER",
				Type:        types.DepBlocks,
			}},
		},
		{
			ID:     "CHILD",
			Title:  "child",
			Status: types.StatusOpen,
			Dependencies: []*types.Dependency{{
				IssueID:     "CHILD",
				DependsOnID: "OPEN-PARENT",
				Type:        types.DepParentChild,
			}},
		},
	}
	readyData, _ := json.Marshal(issues)
	blockersData, _ := json.Marshal([]*types.Issue{{ID: "OPEN-BLOCKER", Status: types.StatusOpen}})
	client := &readyRPCClient{
		ready: func(*rpc.ReadyArgs) (*rpc.Response, error) {
			return &rpc.Response{Success: true, Data: readyData}, nil
		},
		list: func(args *rpc.ListArgs) (*rpc.Response, error) {
			if len(args.IDs) != 2 {
				t.Fatalf("List IDs = %#v, want two blocker IDs", args.IDs)
			}
			return &rpc.Response{Success: true, Data: blockersData}, nil
		},
	}
	pool := &readyRPCPool{client: client}

	gotClient, got, status, err := executeReadyRPC(context.Background(), pool, &rpc.ReadyArgs{})
	if err != nil || status != 0 || gotClient != client {
		t.Fatalf("executeReadyRPC client=%v issues=%v status=%d err=%v", gotClient, got, status, err)
	}
	if len(got) != 2 || got[0].ID != "READY" || got[1].ID != "CHILD" {
		t.Fatalf("filtered issues = %+v", got)
	}
	if pool.put != 0 || pool.discard != 0 {
		t.Fatalf("pool should not be returned by executeReadyRPC success: put=%d discard=%d", pool.put, pool.discard)
	}
}

func TestExecuteReadyRPCErrorsReturnOrDiscard(t *testing.T) {
	tests := []struct {
		name        string
		pool        *readyRPCPool
		wantStatus  int
		wantPut     int
		wantDiscard int
	}{
		{
			name:       "daemon starting",
			pool:       &readyRPCPool{getErr: daemon.ErrDaemonStarting},
			wantStatus: http.StatusServiceUnavailable,
		},
		{
			name:        "ready rpc error discards",
			pool:        &readyRPCPool{client: &readyRPCClient{ready: func(*rpc.ReadyArgs) (*rpc.Response, error) { return nil, errors.New("boom") }}},
			wantStatus:  http.StatusInternalServerError,
			wantDiscard: 1,
		},
		{
			name:       "unsuccessful ready response returns connection",
			pool:       &readyRPCPool{client: &readyRPCClient{ready: func(*rpc.ReadyArgs) (*rpc.Response, error) { return &rpc.Response{Success: false, Error: "nope"}, nil }}},
			wantStatus: http.StatusInternalServerError,
			wantPut:    1,
		},
		{
			name: "invalid ready json returns connection",
			pool: &readyRPCPool{client: &readyRPCClient{ready: func(*rpc.ReadyArgs) (*rpc.Response, error) {
				return &rpc.Response{Success: true, Data: json.RawMessage("{bad")}, nil
			}}},
			wantStatus: http.StatusInternalServerError,
			wantPut:    1,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, _, status, err := executeReadyRPC(context.Background(), tt.pool, &rpc.ReadyArgs{})
			if err == nil {
				t.Fatal("error = nil")
			}
			if status != tt.wantStatus || tt.pool.put != tt.wantPut || tt.pool.discard != tt.wantDiscard {
				t.Fatalf("status=%d put=%d discard=%d", status, tt.pool.put, tt.pool.discard)
			}
		})
	}
}

func TestBuildReadyResponseParentFallbacks(t *testing.T) {
	issues := []*types.Issue{
		{ID: "CHILD", Title: "child", SourceRepo: "api"},
		{ID: "ROOT", Title: "root"},
	}
	client := &readyRPCClient{
		getParentIDs: func(args *rpc.GetParentIDsArgs) (*rpc.GetParentIDsResponse, error) {
			if len(args.IssueIDs) != 2 || args.IssueIDs[0] != "CHILD" {
				t.Fatalf("parent issue IDs = %#v", args.IssueIDs)
			}
			return &rpc.GetParentIDsResponse{Parents: map[string]*rpc.ParentInfo{
				"CHILD": {ParentID: "EPIC-1", ParentTitle: "Epic"},
			}}, nil
		},
	}

	got := buildReadyResponse(client, issues)
	if got[0].Parent == nil || *got[0].Parent != "EPIC-1" || got[0].ParentTitle == nil || *got[0].ParentTitle != "Epic" {
		t.Fatalf("child parent fields = %+v", got[0])
	}
	if got[0].Repo == nil || *got[0].Repo != "api" {
		t.Fatalf("child repo = %+v", got[0].Repo)
	}
	if got[1].Parent != nil || got[1].ParentTitle != nil {
		t.Fatalf("root parent fields = %+v", got[1])
	}

	errClient := &readyRPCClient{getParentIDs: func(*rpc.GetParentIDsArgs) (*rpc.GetParentIDsResponse, error) {
		return nil, errors.New("parents failed")
	}}
	got = buildReadyResponse(errClient, issues[:1])
	if got[0].Parent != nil || got[0].ParentTitle != nil {
		t.Fatalf("parent error response = %+v", got[0])
	}
}

func TestHandleReadyWithPoolSuccessPaths(t *testing.T) {
	readyData, _ := json.Marshal([]*types.Issue{{ID: "READY", Title: "ready", Status: types.StatusOpen}})
	client := &readyRPCClient{
		ready: func(*rpc.ReadyArgs) (*rpc.Response, error) {
			return &rpc.Response{Success: true, Data: readyData}, nil
		},
		list: func(*rpc.ListArgs) (*rpc.Response, error) {
			return &rpc.Response{Success: true, Data: json.RawMessage("[]")}, nil
		},
		getParentIDs: func(*rpc.GetParentIDsArgs) (*rpc.GetParentIDsResponse, error) {
			return &rpc.GetParentIDsResponse{Parents: map[string]*rpc.ParentInfo{}}, nil
		},
	}
	pool := &readyRPCPool{client: client}
	rr := httptest.NewRecorder()
	handleReadyWithPool(pool).ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/ready?limit=1", nil))
	if rr.Code != http.StatusOK || pool.put != 1 || pool.discard != 0 {
		t.Fatalf("code=%d put=%d discard=%d body=%s", rr.Code, pool.put, pool.discard, rr.Body.String())
	}
	var resp ReadyResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil || !resp.Success || len(resp.Data) != 1 {
		t.Fatalf("response = %+v err=%v", resp, err)
	}

	emptyData, _ := json.Marshal([]*types.Issue{})
	emptyPool := &readyRPCPool{client: &readyRPCClient{
		ready: func(*rpc.ReadyArgs) (*rpc.Response, error) {
			return &rpc.Response{Success: true, Data: emptyData}, nil
		},
	}}
	rr = httptest.NewRecorder()
	handleReadyWithPool(emptyPool).ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/ready", nil))
	if rr.Code != http.StatusOK || emptyPool.put != 1 {
		t.Fatalf("empty code=%d put=%d body=%s", rr.Code, emptyPool.put, rr.Body.String())
	}
}
