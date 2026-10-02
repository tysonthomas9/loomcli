package stackpublish

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPRStatuses_GraphQLParse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"data":{"repository":{"pullRequests":{"nodes":[`+
			`{"number":1,"headRefName":"loom/stack/epic-E/T1","mergeable":"MERGEABLE","mergeStateStatus":"UNSTABLE","reviewDecision":"APPROVED","mergeQueueEntry":null,"commits":{"nodes":[{"commit":{"statusCheckRollup":{"state":"SUCCESS"}}}]}},`+
			`{"number":2,"headRefName":"loom/stack/epic-E/T2","mergeable":"CONFLICTING","reviewDecision":"CHANGES_REQUESTED","mergeQueueEntry":null,"commits":{"nodes":[{"commit":{"statusCheckRollup":{"state":"FAILURE"}}}]}},`+
			`{"number":9,"headRefName":"other/branch","mergeable":"MERGEABLE","reviewDecision":null,"mergeQueueEntry":null,"commits":{"nodes":[]}}`+
			`],"pageInfo":{"hasNextPage":false,"endCursor":""}}}}}`)
	}))
	defer srv.Close()

	f := NewGitHubForge("tok", srv.Client(), srv.URL)
	got, err := f.PRStatuses(context.Background(), "o", "r", "loom/stack/epic-E/")
	require.NoError(t, err)
	require.Len(t, got, 2, "only PRs under the prefix are returned")
	assert.Equal(t, PRStatus{Number: 1, Checks: "passing", Review: "approved", Mergeable: "mergeable", MergeState: "unstable"}, got["loom/stack/epic-E/T1"])
	assert.Equal(t, PRStatus{Number: 2, Checks: "failing", Review: "changes_requested", Mergeable: "conflicting"}, got["loom/stack/epic-E/T2"])
}
