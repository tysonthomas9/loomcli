package subscription

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/tysonthomas9/loomcli/internal/backend"
	"github.com/tysonthomas9/loomcli/internal/backend/fleet"
)

func TestBoundSourceRejectsChangedDurableIdentityOnSameBackend(t *testing.T) {
	for _, read := range []string{"page", "next head", "recovery"} {
		t.Run(read, func(t *testing.T) {
			identity := "s1.b2xk"
			sub := &recoverySubscriber{}
			sub.head = func(context.Context) (backend.MutationPage, error) {
				return backend.MutationPage{SourceIdentity: identity, Cursor: "c2.c2FtZQ"}, nil
			}
			sub.page = func(context.Context, string, string, int) (backend.MutationPage, error) {
				return backend.MutationPage{SourceIdentity: identity, Cursor: "c2.c2FtZQ", Events: []backend.MutationData{{Cursor: "c2.c2FtZQ"}}}, nil
			}
			sub.recover = func(context.Context) (backend.IssueRecoverySnapshot, error) {
				return backend.IssueRecoverySnapshot{SourceIdentity: identity, Workspace: "ws", Through: "c2.c2FtZQ", Document: []byte(`{}`)}, nil
			}
			manager := &MultiWorkspaceSubscriber{subscribers: map[string]*subscriberEntry{"ws": {sub: sub}}}
			source, err := manager.OpenMutationSource(t.Context(), "ws")
			require.NoError(t, err)
			_, err = source.ReadHead(t.Context())
			require.NoError(t, err)
			identity = "s1.bmV3" // same subscriber, workspace and cursor text, different database lineage
			switch read {
			case "page":
				page, err := source.ReadPage(t.Context(), "c2.c2FtZQ", "c2.c2FtZQ", 1)
				require.ErrorIs(t, err, backend.ErrMutationSourceChanged)
				require.Empty(t, page.Cursor)
				require.Empty(t, page.Events)
			case "next head":
				page, err := source.ReadHead(t.Context())
				require.ErrorIs(t, err, backend.ErrMutationSourceChanged)
				require.Empty(t, page.Cursor)
			case "recovery":
				snapshot, err := source.(backend.IssueRecoveryBackend).ReadIssueRecovery(t.Context())
				require.ErrorIs(t, err, backend.ErrMutationSourceChanged)
				require.Empty(t, snapshot.Document)
			}
			identity = "s1.b2xk"
			page, err := source.ReadHead(t.Context())
			require.ErrorIs(t, err, backend.ErrMutationSourceChanged)
			require.Empty(t, page.Cursor)

		})
	}
}

func TestBoundRecoveryHTTPSourceChangePermanentlyRetires(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		w.WriteHeader(http.StatusConflict)
		_, _ = w.Write([]byte(`{"error":{"code":"mutation_source_changed","message":"changed"}}`))
	}))
	defer server.Close()
	fb, err := fleet.New(fleet.Config{BaseURL: server.URL, WorkspaceID: "ws"})
	require.NoError(t, err)
	sub := &recoverySubscriber{recover: fb.ReadIssueRecovery}
	sub.head = func(context.Context) (backend.MutationPage, error) {
		return backend.MutationPage{SourceIdentity: "s1.b2xk", Cursor: "c2.aGVhZA"}, nil
	}
	manager := &MultiWorkspaceSubscriber{subscribers: map[string]*subscriberEntry{"ws": {sub: sub}}}
	source, err := manager.OpenMutationSource(t.Context(), "ws")
	require.NoError(t, err)
	_, err = source.ReadHead(t.Context())
	require.NoError(t, err)
	snapshot, err := source.(backend.IssueRecoveryBackend).ReadIssueRecovery(t.Context())
	require.ErrorIs(t, err, backend.ErrMutationSourceChanged)
	require.Empty(t, snapshot.Document)
	sub.recover = func(context.Context) (backend.IssueRecoverySnapshot, error) {
		t.Fatal("retired source reread original identity")
		return backend.IssueRecoverySnapshot{}, nil
	}
	_, err = source.ReadHead(t.Context())
	require.ErrorIs(t, err, backend.ErrMutationSourceChanged)
	_, err = source.(backend.IssueRecoveryBackend).ReadIssueRecovery(t.Context())
	require.ErrorIs(t, err, backend.ErrMutationSourceChanged)
	require.Equal(t, 1, calls)
}
