package stackpublish

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	sl "github.com/tysonthomas9/loomcli/internal/modules/sourcecontrol"
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
	ordered := []sl.StackNode{
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
