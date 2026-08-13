package git

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/modules/sourcecontrol"
	"github.com/tysonthomas9/loomcli/internal/webui/readprojection"
)

func TestHandleAgentDiffStatUsesSourceControlBrowse(t *testing.T) {
	var captured sourcecontrol.AgentQuery
	browse := &stubBrowse{diffStat: func(_ context.Context, query sourcecontrol.AgentQuery) (sourcecontrol.AgentDiffStat, error) {
		captured = query
		return sourcecontrol.AgentDiffStat{Branch: "feature", LinesAdded: 12, LinesRemoved: 3}, nil
	}}
	recorder := serveRoute(
		"GET /api/workspaces/{ws}/agents/{name}/git/diff-stat",
		HandleAgentDiffStat(browse),
		request(http.MethodGet, "/api/workspaces/test-ws/agents/coder/git/diff-stat", ""),
	)
	if recorder.Code != http.StatusOK || captured != (sourcecontrol.AgentQuery{WorkspaceKey: "test-ws", AgentID: "coder"}) {
		t.Fatalf("status=%d query=%+v body=%s", recorder.Code, captured, recorder.Body.String())
	}
	if !strings.Contains(recorder.Body.String(), `"added":12`) || !strings.Contains(recorder.Body.String(), `"removed":3`) {
		t.Fatalf("response=%s", recorder.Body.String())
	}
}

func TestHandleAgentDiffStatMapsOwnerError(t *testing.T) {
	browse := &stubBrowse{diffStat: func(context.Context, sourcecontrol.AgentQuery) (sourcecontrol.AgentDiffStat, error) {
		return sourcecontrol.AgentDiffStat{}, sourcecontrol.ErrNotFound
	}}
	recorder := serveRoute(
		"GET /api/workspaces/{ws}/agents/{name}/git/diff-stat",
		HandleAgentDiffStat(browse),
		request(http.MethodGet, "/api/workspaces/test-ws/agents/missing/git/diff-stat", ""),
	)
	if recorder.Code != http.StatusNotFound {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestHandleIssueDiffStatUsesNamedProjection(t *testing.T) {
	projection := &stubIssueDiff{result: readprojection.IssueDiffResult{Branch: "feature", Added: 7, Removed: 2}}
	recorder := serveRoute(
		"GET /api/workspaces/{ws}/issues/{id}/git/diff-stat",
		HandleGetIssueDiffStat(projection),
		request(http.MethodGet, "/api/workspaces/test-ws/issues/TASK-1/git/diff-stat", ""),
	)
	if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), `"added":7`) {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestHandleIssueDiffStatMapsProjectionError(t *testing.T) {
	projection := &stubIssueDiff{err: readprojection.ErrIssueDiffUnavailable}
	recorder := serveRoute(
		"GET /api/workspaces/{ws}/issues/{id}/git/diff-stat",
		HandleGetIssueDiffStat(projection),
		request(http.MethodGet, "/api/workspaces/test-ws/issues/TASK-1/git/diff-stat", ""),
	)
	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}
