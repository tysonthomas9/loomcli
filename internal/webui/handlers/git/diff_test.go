package git

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/modules/sourcecontrol"
)

func TestDiffCommitsMapsQueryAndResponse(t *testing.T) {
	var captured sourcecontrol.DiffCommitsQuery
	browse := &stubBrowse{diffCommits: func(_ context.Context, query sourcecontrol.DiffCommitsQuery) ([]sourcecontrol.DiffCommit, error) {
		captured = query
		return []sourcecontrol.DiffCommit{{Hash: "abc", ShortHash: "a", Subject: "change", Author: "A", Email: "a@example.test", Date: "now"}}, nil
	}}
	recorder := serveRoute(
		"GET /api/workspaces/{ws}/agents/{name}/diff/commits",
		HandleDiffCommits(browse),
		request(http.MethodGet, "/api/workspaces/test-ws/agents/coder/diff/commits?from=base&limit=7", ""),
	)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	want := sourcecontrol.DiffCommitsQuery{WorkspaceKey: "test-ws", AgentID: "coder", From: "base", Limit: 7}
	if captured != want || !strings.Contains(recorder.Body.String(), `"short_hash":"a"`) {
		t.Fatalf("query=%+v want=%+v body=%s", captured, want, recorder.Body.String())
	}
}

func TestDiffFilesMapsQueryAndResponse(t *testing.T) {
	var captured sourcecontrol.DiffFilesQuery
	browse := &stubBrowse{diffFiles: func(_ context.Context, query sourcecontrol.DiffFilesQuery) ([]sourcecontrol.DiffFile, error) {
		captured = query
		return []sourcecontrol.DiffFile{{Path: "new.go", OldPath: "old.go", Status: "R", Additions: 3, Deletions: 1}}, nil
	}}
	recorder := serveRoute(
		"GET /api/workspaces/{ws}/agents/{name}/diff/files",
		HandleDiffFiles(browse),
		request(http.MethodGet, "/api/workspaces/test-ws/agents/coder/diff/files?from=base&to=HEAD", ""),
	)
	want := sourcecontrol.DiffFilesQuery{WorkspaceKey: "test-ws", AgentID: "coder", From: "base", To: "HEAD"}
	if recorder.Code != http.StatusOK || captured != want || !strings.Contains(recorder.Body.String(), `"old_path":"old.go"`) {
		t.Fatalf("status=%d query=%+v body=%s", recorder.Code, captured, recorder.Body.String())
	}
}

func TestDiffFileMapsQueryAndBoundedFlags(t *testing.T) {
	var captured sourcecontrol.DiffFilePatchQuery
	browse := &stubBrowse{diffFilePatch: func(_ context.Context, query sourcecontrol.DiffFilePatchQuery) (*sourcecontrol.DiffFilePatch, error) {
		captured = query
		return &sourcecontrol.DiffFilePatch{Patch: "diff", IsBinary: true, IsTooLarge: true, Additions: 4, Deletions: 2}, nil
	}}
	recorder := serveRoute(
		"GET /api/workspaces/{ws}/agents/{name}/diff/file",
		HandleDiffFile(browse),
		request(http.MethodGet, "/api/workspaces/test-ws/agents/coder/diff/file?path=a.go&from=base&to=HEAD", ""),
	)
	want := sourcecontrol.DiffFilePatchQuery{WorkspaceKey: "test-ws", AgentID: "coder", Path: "a.go", From: "base", To: "HEAD"}
	if recorder.Code != http.StatusOK || captured != want || !strings.Contains(recorder.Body.String(), `"is_binary":true`) || !strings.Contains(recorder.Body.String(), `"is_too_large":true`) {
		t.Fatalf("status=%d query=%+v body=%s", recorder.Code, captured, recorder.Body.String())
	}
}

func TestDiffHandlersMapOwnerErrors(t *testing.T) {
	for _, test := range []struct {
		name       string
		pattern    string
		handler    http.HandlerFunc
		path       string
		wantStatus int
	}{
		{
			name: "invalid", pattern: "GET /api/workspaces/{ws}/agents/{name}/diff/files",
			handler: HandleDiffFiles(&stubBrowse{diffFiles: func(context.Context, sourcecontrol.DiffFilesQuery) ([]sourcecontrol.DiffFile, error) {
				return nil, sourcecontrol.ErrInvalid
			}}),
			path: "/api/workspaces/test-ws/agents/coder/diff/files", wantStatus: http.StatusBadRequest,
		},
		{
			name: "not found", pattern: "GET /api/workspaces/{ws}/agents/{name}/diff/commits",
			handler: HandleDiffCommits(&stubBrowse{diffCommits: func(context.Context, sourcecontrol.DiffCommitsQuery) ([]sourcecontrol.DiffCommit, error) {
				return nil, sourcecontrol.ErrNotFound
			}}),
			path: "/api/workspaces/test-ws/agents/coder/diff/commits", wantStatus: http.StatusNotFound,
		},
		{
			name: "unavailable", pattern: "GET /api/workspaces/{ws}/agents/{name}/diff/file",
			handler: HandleDiffFile(&stubBrowse{diffFilePatch: func(context.Context, sourcecontrol.DiffFilePatchQuery) (*sourcecontrol.DiffFilePatch, error) {
				return nil, sourcecontrol.ErrUnavailable
			}}),
			path: "/api/workspaces/test-ws/agents/coder/diff/file", wantStatus: http.StatusServiceUnavailable,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			recorder := serveRoute(test.pattern, test.handler, request(http.MethodGet, test.path, ""))
			if recorder.Code != test.wantStatus {
				t.Fatalf("status=%d want=%d body=%s", recorder.Code, test.wantStatus, recorder.Body.String())
			}
		})
	}
}

func TestDiffCommitsRejectsInvalidLimit(t *testing.T) {
	recorder := serveRoute(
		"GET /api/workspaces/{ws}/agents/{name}/diff/commits",
		HandleDiffCommits(&stubBrowse{}),
		request(http.MethodGet, "/api/workspaces/test-ws/agents/coder/diff/commits?limit=bad", ""),
	)
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}
