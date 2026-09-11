package stackpublish

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	sl "github.com/tysonthomas9/loomcli/internal/stacklineage"
)

func TestWithStackSection(t *testing.T) {
	listing := "📚 stack\n- #1 a"

	// Fresh body: preamble preserved, section appended.
	got := withStackSection("Original description.", listing)
	assert.Contains(t, got, "Original description.")
	assert.Contains(t, got, stackMarkStart)
	assert.Contains(t, got, listing)

	// Idempotent: same listing yields an identical body.
	assert.Equal(t, got, withStackSection(got, listing))

	// Replacing with a new listing swaps the section but keeps the preamble.
	got3 := withStackSection(got, "NEW LISTING")
	assert.Contains(t, got3, "Original description.")
	assert.Contains(t, got3, "NEW LISTING")
	assert.NotContains(t, got3, "- #1 a")

	// Empty body → just the section.
	assert.Equal(t, stackMarkStart+"\nX\n"+stackMarkEnd, withStackSection("", "X"))
}

func TestRenderStackListing(t *testing.T) {
	id := sl.StackID("epic:E")
	ordered := []sl.Node{
		{TaskID: "T1", OutputBranch: sl.OutputBranchName(id, "T1")},
		{TaskID: "T2", OutputBranch: sl.OutputBranchName(id, "T2")},
	}
	live := map[string]PR{"T1": {Number: 1}, "T2": {Number: 2}} // keyed by task ID
	out := renderStackListing(ordered, live, "T2", id)
	assert.Contains(t, out, "#1")
	assert.Contains(t, out, "#2")
	for _, line := range strings.Split(out, "\n") {
		if strings.Contains(line, "#2") {
			assert.Contains(t, line, "👉", "current unit is marked")
		}
		if strings.Contains(line, "#1") {
			assert.NotContains(t, line, "👉", "non-current unit has no marker")
		}
	}
}

func TestPRStatuses_GraphQLParse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"data":{"repository":{"pullRequests":{"nodes":[`+
			`{"number":1,"headRefName":"loom/stack/epic-E/T1","mergeable":"MERGEABLE","reviewDecision":"APPROVED","mergeQueueEntry":null,"commits":{"nodes":[{"commit":{"statusCheckRollup":{"state":"SUCCESS"}}}]}},`+
			`{"number":2,"headRefName":"loom/stack/epic-E/T2","mergeable":"CONFLICTING","reviewDecision":"CHANGES_REQUESTED","mergeQueueEntry":null,"commits":{"nodes":[{"commit":{"statusCheckRollup":{"state":"FAILURE"}}}]}},`+
			`{"number":9,"headRefName":"other/branch","mergeable":"MERGEABLE","reviewDecision":null,"mergeQueueEntry":null,"commits":{"nodes":[]}}`+
			`],"pageInfo":{"hasNextPage":false,"endCursor":""}}}}}`)
	}))
	defer srv.Close()

	f := NewGitHubForge("tok", srv.Client(), srv.URL)
	got, err := f.PRStatuses(context.Background(), "o", "r", "loom/stack/epic-E/")
	require.NoError(t, err)
	require.Len(t, got, 2, "only PRs under the prefix are returned")
	assert.Equal(t, PRStatus{Number: 1, Checks: "passing", Review: "approved", Mergeable: "mergeable"}, got["loom/stack/epic-E/T1"])
	assert.Equal(t, PRStatus{Number: 2, Checks: "failing", Review: "changes_requested", Mergeable: "conflicting"}, got["loom/stack/epic-E/T2"])
}

func TestCreatePRMetadataPayload(t *testing.T) {
	var payload map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/repos/o/r/pulls", r.URL.Path)
		require.NoError(t, json.NewDecoder(r.Body).Decode(&payload))
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = io.WriteString(w, `{"number":5,"node_id":"PR_node","state":"open","title":"custom","body":"body","html_url":"https://github.com/o/r/pull/5","draft":true,"head":{"ref":"h"},"base":{"ref":"b"}}`)
	}))
	defer srv.Close()

	f := NewGitHubForge("tok", srv.Client(), srv.URL)
	pr, err := f.CreatePR(context.Background(), "o", "r", "h", "b", PullRequestOptions{
		Title: "custom", Body: "body", Draft: true,
		MaintainerCanModify: false, MaintainerCanModifySet: true,
	})
	require.NoError(t, err)
	assert.Equal(t, 5, pr.Number)
	assert.Equal(t, "PR_node", pr.NodeID)
	assert.True(t, pr.Draft)
	assert.Equal(t, "custom", payload["title"])
	assert.Equal(t, "h", payload["head"])
	assert.Equal(t, "b", payload["base"])
	assert.Equal(t, "body", payload["body"])
	assert.Equal(t, true, payload["draft"])
	assert.Equal(t, false, payload["maintainer_can_modify"])
}

func TestSetPRDraftUsesGraphQLMutation(t *testing.T) {
	var payload map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/graphql", r.URL.Path)
		require.NoError(t, json.NewDecoder(r.Body).Decode(&payload))
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"data":{"convertPullRequestToDraft":{"pullRequest":{"id":"PR_node","isDraft":true}}}}`)
	}))
	defer srv.Close()

	f := NewGitHubForge("tok", srv.Client(), srv.URL)
	err := f.SetPRDraft(context.Background(), "o", "r", PR{Number: 5, NodeID: "PR_node"}, true)
	require.NoError(t, err)
	assert.Contains(t, payload["query"], "convertPullRequestToDraft")
	assert.Equal(t, map[string]any{"id": "PR_node"}, payload["variables"])
}
