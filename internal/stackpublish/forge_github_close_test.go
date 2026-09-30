package stackpublish

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestClosePRRequiresCommentBeforeClosing(t *testing.T) {
	allowComment := false
	closes := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/issues/7/comments"):
			if !allowComment {
				w.WriteHeader(http.StatusForbidden)
				return
			}
			w.WriteHeader(http.StatusCreated)
		case r.Method == http.MethodPatch && strings.HasSuffix(r.URL.Path, "/pulls/7"):
			closes++
			w.WriteHeader(http.StatusOK)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	forge := NewGitHubForge("test", server.Client(), server.URL)
	if err := forge.ClosePR(context.Background(), "owner", "repo", 7, "Abandoned by Loom"); err == nil || closes != 0 {
		t.Fatalf("comment failure closed PR: err=%v closes=%d", err, closes)
	}
	allowComment = true
	if err := forge.ClosePR(context.Background(), "owner", "repo", 7, "Abandoned by Loom"); err != nil || closes != 1 {
		t.Fatalf("commented close failed: err=%v closes=%d", err, closes)
	}
}
