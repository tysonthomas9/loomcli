package providers

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestGitHubReadCommitStatusKeepsStatusIDs (OR8): commit_status keeps each
// status's id, which tells apart two same-state statuses of one context
// posted in the same second.
func TestGitHubReadCommitStatusKeepsStatusIDs(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"state":"failure","statuses":[{"id":11,"context":"lint","state":"failure","updated_at":"2026-10-10T12:00:00Z"}]}`))
	}))
	defer srv.Close()
	res, err := NewGitHub(nil, srv.URL).Call(context.Background(), CallSpec{Action: ActionGitHubRead, Credential: "tok",
		Args: map[string]any{"op": "commit_status", "owner": "o", "repo": "r", "ref": "sha1"}})
	if err != nil {
		t.Fatal(err)
	}
	item, _ := res.Body["item"].(map[string]any)
	statuses, _ := item["statuses"].([]any)
	if len(statuses) != 1 || statuses[0].(map[string]any)["id"] != float64(11) {
		t.Fatalf("commit_status item = %v; want the status id 11", item)
	}
}
