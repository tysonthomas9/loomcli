package fleet

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/tysonthomas9/loomcli/internal/backend"
)

func TestGetMutationsThroughPreservesFilteredFence(t *testing.T) {
	const before, fence = "c2.MTAwLTA", "c2.MjAwLTA"
	fb, server := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, before, r.URL.Query().Get("since"))
		require.Equal(t, fence, r.URL.Query().Get("through"))
		require.Equal(t, "0", r.URL.Query().Get("timeout"))
		require.Equal(t, "10", r.URL.Query().Get("limit"))
		respondOK(w, fleetMutationsResponse{SourceIdentity: "s1.Zml4dHVyZQ", Events: []fleetMutationEvent{}, Cursor: fence})
	})
	defer server.Close()
	page, err := fb.GetMutationsThrough(t.Context(), before, fence, 10)
	require.NoError(t, err)
	require.Empty(t, page.Events)
	require.Equal(t, fence, page.Cursor)
	require.False(t, page.HasMore)
}

func TestGetMutationsThroughRejectsIncompleteOrUnboundedResponse(t *testing.T) {
	for _, body := range []string{
		`null`, `{}`, `{"events":[],"cursor":"c2.MjAwLTA"}`,
		`{"events":null,"cursor":"c2.MjAwLTA","has_more":false}`,
		`{"events":[],"cursor":"","has_more":false}`,
		`{"events":[],"cursor":"c2.MzAwLTA","has_more":false}`,
		`{"events":[],"cursor":"c2.MjAwLTA","has_more":true}`,
		`{"events":[],"cursor":"c2.MTAwLTA","has_more":true}`,
		`{"events":[{"id":"200-0"}],"cursor":"c2.MjAwLTA","has_more":false}`,
		`{"events":[{"id":"c2.MjAwLTA"},{"id":"c2.MzAwLTA"}],"cursor":"c2.MjAwLTA","has_more":false}`,
	} {
		t.Run(body, func(t *testing.T) {
			fb, server := newTestServer(t, func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(body))
			})
			defer server.Close()
			page, err := fb.GetMutationsThrough(t.Context(), "c2.MTAwLTA", "c2.MjAwLTA", 10)
			require.Error(t, err)
			require.Empty(t, page.Cursor)
			require.Empty(t, page.Events)
		})
	}
}

func TestGetMutationsThroughExpiredWithoutFloor(t *testing.T) {
	fb, server := newTestServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusGone)
		_, _ = w.Write([]byte(`{"error":{"code":"cursor_expired","message":"cursor expired"}}`))
	})
	defer server.Close()
	_, err := fb.GetMutationsThrough(t.Context(), "c2.MTAwLTA", "c2.MjAwLTA", 10)
	require.ErrorIs(t, err, backend.ErrMutationCursorExpired)
}

func TestGetMutationsThroughRejectsCursorCoercionAndCancels(t *testing.T) {
	fb, server := newTestServer(t, func(w http.ResponseWriter, _ *http.Request) { t.Error("invalid request reached HTTP") })
	defer server.Close()
	for _, id := range []string{"", "$", "100", "100-0", "c1.JA", "c2.%%%", "c2.MTAwLTB"} {
		_, err := fb.GetMutationsThrough(t.Context(), id, "c2.MjAwLTA", 10)
		require.Error(t, err, id)
		_, err = fb.GetMutationsThrough(t.Context(), "0", id, 10)
		require.Error(t, err, id)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err := fb.GetMutationsThrough(ctx, "0", "0", 1)
	require.True(t, errors.Is(err, context.Canceled), "%v", err)
}

func TestMutationHeadUsesV2OpaqueSentinel(t *testing.T) {
	fb, server := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		// Initial head lookup uses the explicit selector; returned positions are c2.
		if r.URL.Query().Get("since") != "$" {
			http.Error(w, "expected opaque cursor", http.StatusBadRequest)
			return
		}
		respondOK(w, fleetMutationsResponse{SourceIdentity: "s1.Zml4dHVyZQ", Events: []fleetMutationEvent{}, Cursor: "c2.MjAwLTA"})
	})
	defer server.Close()
	head, supported, err := fb.ProbeHead(t.Context())
	require.NoError(t, err)
	require.True(t, supported)
	require.Equal(t, "c2.MjAwLTA", head)
	page, err := fb.GetMutationsAfter(t.Context(), "$", 1)
	require.NoError(t, err)
	require.Equal(t, head, page.Cursor)
}
