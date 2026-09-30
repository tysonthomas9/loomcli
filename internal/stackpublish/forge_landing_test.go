package stackpublish

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestGitHubForgeLandingQueriesOwnedPRAndAssociation(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Header.Get("Authorization") != "Bearer token" {
			t.Errorf("missing host authorization")
		}
		switch request.URL.Path {
		case "/repos/owner/repo/pulls/42":
			_, _ = fmt.Fprint(writer, `{"number":42,"state":"closed","merged_at":"2026-09-30T00:00:00Z","merge_commit_sha":"abc123","head":{"ref":"loom/ws/W/change/A"}}`)
		case "/repos/owner/repo/commits/abc123/pulls":
			_, _ = fmt.Fprint(writer, `[{"number":42,"state":"closed","merged_at":"2026-09-30T00:00:00Z"}]`)
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()
	forge := NewGitHubForge("token", server.Client(), server.URL)
	pull, err := forge.PullByNumber(context.Background(), "owner", "repo", 42)
	if err != nil || !pull.Merged || pull.MergeCommitSHA != "abc123" || pull.Head != "loom/ws/W/change/A" {
		t.Fatalf("owned PR = %+v, %v", pull, err)
	}
	associated, err := forge.PullsForCommit(context.Background(), "owner", "repo", "abc123")
	if err != nil || len(associated) != 1 || associated[0].Number != 42 {
		t.Fatalf("associated PRs = %+v, %v", associated, err)
	}
}
