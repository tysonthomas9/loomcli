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

func TestQueuedPRNumbers_GraphQLParse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/graphql", r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"data":{"repository":{"pullRequests":{"nodes":[`+
			`{"number":1,"mergeQueueEntry":{"id":"MQ_1"}},`+
			`{"number":2,"mergeQueueEntry":null},`+
			`{"number":3,"mergeQueueEntry":{"id":"MQ_3"}}`+
			`],"pageInfo":{"hasNextPage":false,"endCursor":""}}}}}`)
	}))
	defer srv.Close()

	f := NewGitHubForge("tok", srv.Client(), srv.URL)
	got, err := f.QueuedPRNumbers(context.Background(), "o", "r")
	require.NoError(t, err)
	assert.Equal(t, map[int]bool{1: true, 3: true}, got)
}
