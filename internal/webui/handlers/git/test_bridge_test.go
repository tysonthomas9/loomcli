package git

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"

	"github.com/tysonthomas9/loomcli/internal/modules/sourcecontrol"
	"github.com/tysonthomas9/loomcli/internal/webui/readprojection"
	"github.com/tysonthomas9/loomcli/internal/webui/server/handler"
	"github.com/tysonthomas9/loomcli/internal/webui/server/middleware"
)

type module interface{ Register(*http.ServeMux) }

const MaxListLimit = handler.MaxListLimit

type stubIssueDiff struct {
	result readprojection.IssueDiffResult
	err    error
}

func (stub *stubIssueDiff) GetIssueDiff(
	context.Context,
	readprojection.IssueDiffQuery,
) (readprojection.IssueDiffResult, error) {
	return stub.result, stub.err
}

type stubBrowse struct {
	diffStat      func(context.Context, sourcecontrol.AgentQuery) (sourcecontrol.AgentDiffStat, error)
	diffCommits   func(context.Context, sourcecontrol.DiffCommitsQuery) ([]sourcecontrol.DiffCommit, error)
	diffFiles     func(context.Context, sourcecontrol.DiffFilesQuery) ([]sourcecontrol.DiffFile, error)
	diffFilePatch func(context.Context, sourcecontrol.DiffFilePatchQuery) (*sourcecontrol.DiffFilePatch, error)
}

func (stub *stubBrowse) DiffStat(ctx context.Context, query sourcecontrol.AgentQuery) (sourcecontrol.AgentDiffStat, error) {
	if stub != nil && stub.diffStat != nil {
		return stub.diffStat(ctx, query)
	}
	return sourcecontrol.AgentDiffStat{}, nil
}

func (stub *stubBrowse) DiffCommits(ctx context.Context, query sourcecontrol.DiffCommitsQuery) ([]sourcecontrol.DiffCommit, error) {
	if stub != nil && stub.diffCommits != nil {
		return stub.diffCommits(ctx, query)
	}
	return []sourcecontrol.DiffCommit{}, nil
}

func (stub *stubBrowse) DiffFiles(ctx context.Context, query sourcecontrol.DiffFilesQuery) ([]sourcecontrol.DiffFile, error) {
	if stub != nil && stub.diffFiles != nil {
		return stub.diffFiles(ctx, query)
	}
	return []sourcecontrol.DiffFile{}, nil
}

func (stub *stubBrowse) DiffFilePatch(ctx context.Context, query sourcecontrol.DiffFilePatchQuery) (*sourcecontrol.DiffFilePatch, error) {
	if stub != nil && stub.diffFilePatch != nil {
		return stub.diffFilePatch(ctx, query)
	}
	return &sourcecontrol.DiffFilePatch{}, nil
}

type stubCheckout struct {
	push              func(context.Context, sourcecontrol.PushCommand) (*sourcecontrol.PushResult, error)
	pushAll           func(context.Context, sourcecontrol.PushAllCommand) (*sourcecontrol.PushAllResult, error)
	pull              func(context.Context, sourcecontrol.PullCommand) (*sourcecontrol.PullResult, error)
	sync              func(context.Context, sourcecontrol.SyncCommand) (*sourcecontrol.SyncResult, error)
	createPullRequest func(context.Context, sourcecontrol.CreatePullRequestCommand) (*sourcecontrol.PullRequestCreation, error)
	reset             func(context.Context, sourcecontrol.ResetCommand) (*sourcecontrol.ResetResult, error)
	agentStatus       func(context.Context, sourcecontrol.AgentStatusQuery) (*sourcecontrol.AgentStatusResult, error)
	setTargetBranch   func(context.Context, sourcecontrol.SetTargetBranchCommand) error
}

func (*stubCheckout) Status(context.Context, sourcecontrol.LocationQuery) (sourcecontrol.FileGitStatusResult, error) {
	return sourcecontrol.FileGitStatusResult{}, nil
}
func (*stubCheckout) ListCheckouts(context.Context, sourcecontrol.WorkspaceQuery) (*sourcecontrol.FileCheckoutsResult, error) {
	return &sourcecontrol.FileCheckoutsResult{}, nil
}
func (*stubCheckout) Repair(context.Context, sourcecontrol.RepairCommand) (*sourcecontrol.RepairResult, error) {
	return &sourcecontrol.RepairResult{}, nil
}
func (stub *stubCheckout) Push(ctx context.Context, command sourcecontrol.PushCommand) (*sourcecontrol.PushResult, error) {
	if stub != nil && stub.push != nil {
		return stub.push(ctx, command)
	}
	return &sourcecontrol.PushResult{Success: true}, nil
}
func (stub *stubCheckout) PushAll(ctx context.Context, command sourcecontrol.PushAllCommand) (*sourcecontrol.PushAllResult, error) {
	if stub != nil && stub.pushAll != nil {
		return stub.pushAll(ctx, command)
	}
	return &sourcecontrol.PushAllResult{}, nil
}
func (stub *stubCheckout) Pull(ctx context.Context, command sourcecontrol.PullCommand) (*sourcecontrol.PullResult, error) {
	if stub != nil && stub.pull != nil {
		return stub.pull(ctx, command)
	}
	return &sourcecontrol.PullResult{Success: true}, nil
}
func (stub *stubCheckout) Sync(ctx context.Context, command sourcecontrol.SyncCommand) (*sourcecontrol.SyncResult, error) {
	if stub != nil && stub.sync != nil {
		return stub.sync(ctx, command)
	}
	return &sourcecontrol.SyncResult{}, nil
}
func (stub *stubCheckout) CreatePullRequest(ctx context.Context, command sourcecontrol.CreatePullRequestCommand) (*sourcecontrol.PullRequestCreation, error) {
	if stub != nil && stub.createPullRequest != nil {
		return stub.createPullRequest(ctx, command)
	}
	return &sourcecontrol.PullRequestCreation{}, nil
}
func (*stubCheckout) ListPullRequests(context.Context, sourcecontrol.ListPullRequestsQuery) (*sourcecontrol.PullRequestList, error) {
	return &sourcecontrol.PullRequestList{}, nil
}
func (stub *stubCheckout) Reset(ctx context.Context, command sourcecontrol.ResetCommand) (*sourcecontrol.ResetResult, error) {
	if stub != nil && stub.reset != nil {
		return stub.reset(ctx, command)
	}
	return &sourcecontrol.ResetResult{}, nil
}
func (stub *stubCheckout) AgentStatus(ctx context.Context, query sourcecontrol.AgentStatusQuery) (*sourcecontrol.AgentStatusResult, error) {
	if stub != nil && stub.agentStatus != nil {
		return stub.agentStatus(ctx, query)
	}
	return &sourcecontrol.AgentStatusResult{}, nil
}
func (stub *stubCheckout) SetTargetBranch(ctx context.Context, command sourcecontrol.SetTargetBranchCommand) error {
	if stub != nil && stub.setTargetBranch != nil {
		return stub.setTargetBranch(ctx, command)
	}
	return nil
}

func request(method, path, body string) *http.Request {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	return req.WithContext(middleware.WithWorkspace(req.Context(), "test-ws"))
}

func serveRoute(pattern string, handler http.HandlerFunc, req *http.Request) *httptest.ResponseRecorder {
	mux := http.NewServeMux()
	mux.HandleFunc(pattern, handler)
	recorder := httptest.NewRecorder()
	mux.ServeHTTP(recorder, req)
	return recorder
}

func decodeJSON(body string, destination any) error {
	return json.Unmarshal([]byte(body), destination)
}
