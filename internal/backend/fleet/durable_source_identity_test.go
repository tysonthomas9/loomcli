package fleet

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/tysonthomas9/loomcli/internal/backend"
)

func TestMutationPageRequiresDurableSourceIdentity(t *testing.T) {
	for _, identity := range []any{nil, "", "s1.%%%", "s1.Zg=", "c2.Zg"} {
		body := map[string]any{"events": []any{}, "cursor": "c2.aGVhZA", "has_more": false, "source_identity": identity}
		data, err := json.Marshal(body)
		require.NoError(t, err)
		page, err := decodeMutationPage(data, 1, "test-ws")
		require.Error(t, err)
		require.Empty(t, page.Cursor)
	}
	body := []byte(`{"events":[],"cursor":"c2.aGVhZA","has_more":false,"source_identity":"s1.Zml4dHVyZQ"}`)
	page, err := decodeMutationPage(body, 1, "test-ws")
	require.NoError(t, err)
	require.Equal(t, "s1.Zml4dHVyZQ", page.SourceIdentity)
}
func TestMutationSourceChangeHTTPIsTerminalTypedError(t *testing.T) {
	fb, server := newTestServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusConflict)
		_, _ = w.Write([]byte(`{"error":{"code":"mutation_source_changed","message":"source changed"}}`))
	})
	defer server.Close()
	page, err := fb.GetMutationsThrough(t.Context(), "c2.b2xk", "c2.bmV3", 1)
	require.ErrorIs(t, err, backend.ErrMutationSourceChanged)
	require.Empty(t, page.Cursor)
	require.Empty(t, page.Events)
}
func TestRecoveryRequiresSourceHeader(t *testing.T) {
	for _, identity := range []string{"", "s1.%%%", "c1.b2xk"} {
		b := recoveryTestBackend(t, func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			if identity != "" {
				w.Header().Set("X-Fleet-Source-Identity", identity)
			}
			_ = json.NewEncoder(w).Encode(recoveryTestDocument())
		})
		result, err := b.ReadIssueRecovery(context.Background())
		require.Error(t, err)
		require.Empty(t, result.Document)
	}
}

func TestMutationPageRejectsMissingOrForeignWorkspace(t *testing.T) {
	for _, workspace := range []string{"", "other"} {
		data, err := json.Marshal(map[string]any{"source_identity": "s1.Zml4dHVyZQ", "cursor": "c2.aGVhZA", "has_more": false, "events": []map[string]any{{"id": "c2.aGVhZA", "workspace_id": workspace}}})
		require.NoError(t, err)
		page, err := decodeMutationPage(data, 1, "test-ws")
		require.Error(t, err)
		require.Empty(t, page.Events)
		require.Empty(t, page.Cursor)
	}
}

func TestRecoverySourceChangeHTTPIsTerminalTypedError(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		changed    bool
	}{
		{"source changed", `{"error":{"code":"mutation_source_changed","message":"changed"}}`, true},
		{"other conflict", `{"error":{"code":"conflict","message":"mutation_source_changed"}}`, false},
		{"malformed", `{"error":`, false},
		{"oversize", strings.Repeat(" ", 64<<10) + `{"error":{"code":"mutation_source_changed"}}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b := recoveryTestBackend(t, func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusConflict)
				_, _ = w.Write([]byte(tc.body))
			})
			result, err := b.ReadIssueRecovery(t.Context())
			require.Error(t, err)
			require.Equal(t, tc.changed, errors.Is(err, backend.ErrMutationSourceChanged))
			require.Empty(t, result.Document)
		})
	}
}
