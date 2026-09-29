package git

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/loomgit"
	"github.com/tysonthomas9/loomcli/internal/loomgit/gitread"
)

type revisionRows map[int]loomgit.Revision

func (r revisionRows) ReserveRevision(context.Context, loomgit.Revision) (loomgit.Revision, error) {
	return loomgit.Revision{}, errors.New("read only")
}
func (r revisionRows) FinishRevision(context.Context, loomgit.Revision) error {
	return errors.New("read only")
}
func (r revisionRows) GetRevision(_ context.Context, _, _ string, n int) (loomgit.Revision, error) {
	if row, ok := r[n]; ok {
		return row, nil
	}
	return loomgit.Revision{}, errors.New("missing")
}

type revisionRepos []loomgit.WorkspaceRepo

func (r revisionRepos) WorkspaceRepos(context.Context, string) ([]loomgit.WorkspaceRepo, error) {
	return r, nil
}

type revisionRepo struct{ missingBase bool }

func (r *revisionRepo) Path() string { return "" }
func (r *revisionRepo) RunWithEnv(context.Context, map[string]string, ...string) ([]byte, error) {
	return nil, errors.New("read only")
}
func (r *revisionRepo) UpdateRef(context.Context, string, string, string) error {
	return errors.New("read only")
}
func (r *revisionRepo) Run(_ context.Context, args ...string) ([]byte, error) {
	switch args[0] {
	case "rev-parse":
		ref := args[len(args)-1]
		if r.missingBase && strings.Contains(ref, "/1/base") {
			return nil, errors.New("missing")
		}
		if strings.Contains(ref, "/2/head") {
			return []byte("bbbb\n"), nil
		}
		if strings.Contains(ref, "/1/head") {
			return []byte("aaaa\n"), nil
		}
		return []byte("base\n"), nil
	case "diff":
		for _, arg := range args {
			if arg == "--name-only" {
				return []byte("file.txt\x00"), nil
			}
		}
		return []byte("diff --git a/file.txt b/file.txt\n@@ -1 +1 @@\n-old\n+new\n"), nil
	}
	return nil, errors.New("unexpected git command")
}

func TestRevisionDiffRoutesReturnHunksAndCodes(t *testing.T) {
	repo := &revisionRepo{}
	reader := &gitread.Reader{Revisions: revisionRows{
		1: {Workspace: "W", Change: "C", Number: 1, BaseSHA: "base", HeadSHA: "aaaa", Ready: true},
		2: {Workspace: "W", Change: "C", Number: 2, BaseSHA: "base", HeadSHA: "bbbb", Ready: true},
	}, Workspaces: revisionRepos{{Workspace: "W", Repo: "repo"}},
		OpenRepo: func(_, _ string) (loomgit.RepoStore, error) { return repo, nil }}
	open := func() (*gitread.Reader, func() error, error) { return reader, func() error { return nil }, nil }
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/workspaces/{ws}/changes/{change}/revisions/{r}/diff", handleRevisionDiff(false, open))
	mux.HandleFunc("GET /api/workspaces/{ws}/changes/{change}/revisions/{r}/interdiff", handleRevisionDiff(true, open))
	for _, path := range []string{
		"/api/workspaces/W/changes/C/revisions/2/diff?repo=repo",
		"/api/workspaces/W/changes/C/revisions/2/interdiff?repo=repo&against=1",
	} {
		response := httptest.NewRecorder()
		mux.ServeHTTP(response, httptest.NewRequest("GET", path, nil))
		if response.Code != http.StatusOK {
			t.Fatalf("%s: %d %s", path, response.Code, response.Body)
		}
		var payload struct {
			Success bool         `json:"success"`
			Data    gitread.Diff `json:"data"`
		}
		if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
			t.Fatal(err)
		}
		if !payload.Success || len(payload.Data.Files) != 1 || len(payload.Data.Files[0].Hunks) != 1 {
			t.Fatalf("%s: %+v", path, payload)
		}
	}
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, httptest.NewRequest("GET", "/api/workspaces/W/changes/C/revisions/1/diff?repo=foreign", nil))
	if response.Code != http.StatusBadRequest || !strings.Contains(response.Body.String(), "repo_selection_required") {
		t.Fatalf("foreign repo: %d %s", response.Code, response.Body)
	}
	repo.missingBase = true
	response = httptest.NewRecorder()
	mux.ServeHTTP(response, httptest.NewRequest("GET", "/api/workspaces/W/changes/C/revisions/1/diff?repo=repo", nil))
	if response.Code != http.StatusConflict || !strings.Contains(response.Body.String(), "base_ref_unresolvable") {
		t.Fatalf("missing base: %d %s", response.Code, response.Body)
	}
}
