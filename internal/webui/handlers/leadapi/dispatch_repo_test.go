package leadapi

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/backend"
	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/infra/memstore"
	"github.com/tysonthomas9/loomcli/internal/store"
)

func TestEpicRepoHint(t *testing.T) {
	tests := []struct {
		name string
		epic *backend.IssueDetailData
		want string
	}{
		{"source repo wins", &backend.IssueDetailData{IssueData: backend.IssueData{SourceRepo: "source", Labels: []string{"repo:label"}}}, "source"},
		{"repo label", &backend.IssueDetailData{IssueData: backend.IssueData{Labels: []string{"other", "repo:label"}}}, "label"},
		{"empty", &backend.IssueDetailData{}, ""},
		{"nil", nil, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := epicRepoHint(tt.epic); got != tt.want {
				t.Fatalf("hint = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestSelectDispatchRepo(t *testing.T) {
	frontend := &domain.Repo{Name: "frontend", SourceRepoID: "repo-front"}
	backendRepo := &domain.Repo{Name: "backend", SourceRepoID: "repo-back"}
	tests := []struct {
		name  string
		repos []*domain.Repo
		hint  string
		want  *domain.Repo
	}{
		{"name", []*domain.Repo{frontend, backendRepo}, "frontend", frontend},
		{"source repo id", []*domain.Repo{frontend, backendRepo}, "repo-back", backendRepo},
		{"single fallback", []*domain.Repo{frontend}, "unknown", frontend},
		{"multi without hint", []*domain.Repo{frontend, backendRepo}, "", nil},
		{"multi unknown hint", []*domain.Repo{frontend, backendRepo}, "unknown", nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := selectDispatchRepo(tt.repos, tt.hint); got != tt.want {
				t.Fatalf("repo = %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestResolveEpicRepo(t *testing.T) {
	tests := []struct {
		name       string
		remote     string
		branch     string
		wantURL    string
		wantBranch string
		wantCode   string
	}{
		{"ssh normalized", "git@github.com:octocat/hello.git", "develop", "https://github.com/octocat/hello", "develop", ""},
		{"empty remote", "", "main", "", "", "repo_unresolved"},
		{"default branch", "https://github.com/octocat/hello", "", "https://github.com/octocat/hello", "main", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			st := memstore.New()
			if _, err := st.Repos().Create(context.Background(), store.RepoCreate{
				WorkspaceKey: "WS", Name: "repo-1", RemoteURL: tt.remote, DefaultBranch: tt.branch,
			}); err != nil {
				t.Fatal(err)
			}
			module := &Module{store: st}
			url, branch, err := module.resolveEpicRepo(context.Background(), "WS",
				dispatchEpic("epic-1", "repo-1"))
			if tt.wantCode != "" {
				var statusErr *opStatusError
				if !errors.As(err, &statusErr) || statusErr.status != http.StatusBadRequest || statusErr.code != tt.wantCode {
					t.Fatalf("error = %v, want %s", err, tt.wantCode)
				}
				return
			}
			if err != nil || url != tt.wantURL || branch != tt.wantBranch {
				t.Fatalf("resolve = %q %q, err = %v; want %q %q", url, branch, err, tt.wantURL, tt.wantBranch)
			}
		})
	}
}

func TestRequireGitHubRemoteMatchesDaytonaParser(t *testing.T) {
	tests := []struct {
		remote string
		wantOK bool
	}{
		{"https://github.com/octocat/hello", true},
		{"https://github.com/octocat/hello.git", true},
		{"https://gitlab.com/octocat/hello", false},
		{"https://GitHub.com/octocat/hello", false},
		{"https://github.com/octocat/hello/", false},
		{"https://github.com/group/subgroup/repo", false},
	}
	for _, tt := range tests {
		t.Run(tt.remote, func(t *testing.T) {
			if got := requireGitHubRemote(tt.remote) == nil; got != tt.wantOK {
				t.Fatalf("accepted = %t, want %t", got, tt.wantOK)
			}
		})
	}
}
