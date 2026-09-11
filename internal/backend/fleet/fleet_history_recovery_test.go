package fleet

import (
	"context"
	"net/http"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/backend"
)

func TestHistoryRecoveryRejectsMissingSourcePayload(t *testing.T) {
	for _, body := range []string{`{"success":true}`, `{"success":true,"data":null}`, `{"success":true,"data":{}}`, `{"success":true,"data":{"history":null,"has_more":false}}`, `{"success":true,"data":{"history":[]}}`, `{"success":true,"data":{"history":[],"has_more":null}}`, `{"success":true,"data":{"history":[],"has_more":"false"}}`} {
		t.Run(body, func(t *testing.T) {
			fb, server := newTestServer(t, func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(body))
			})
			defer server.Close()
			if result, err := fb.ListEventHistory(context.Background(), "issue", backend.EventHistoryParams{Limit: 8}); err == nil {
				t.Fatalf("missing source acknowledged: %+v", result)
			}
		})
	}
}

func TestHistoryRecoveryLaterMissingPageCannotEraseEarlierRecords(t *testing.T) {
	calls := 0
	fb, server := newTestServer(t, func(w http.ResponseWriter, _ *http.Request) {
		calls++
		if calls == 1 {
			respondOK(w, map[string]any{"history": []map[string]any{{"id": "one", "action": "issue.create", "timestamp": "2026-09-05T00:00:00Z"}}, "cursor": "one", "has_more": true})
			return
		}
		respondOK(w, map[string]any{})
	})
	defer server.Close()
	result, err := fb.ListEventHistory(context.Background(), "issue", backend.EventHistoryParams{Limit: 8})
	if err == nil || result != nil || calls != 2 {
		t.Fatalf("result=%+v err=%v calls=%d", result, err, calls)
	}
}

func TestHistoryRecoveryAcceptsExplicitEmptyHistory(t *testing.T) {
	fb, server := newTestServer(t, func(w http.ResponseWriter, _ *http.Request) {
		respondOK(w, map[string]any{"history": []any{}, "cursor": "", "has_more": false, "total_events": 0})
	})
	defer server.Close()
	result, err := fb.ListEventHistory(context.Background(), "issue", backend.EventHistoryParams{Limit: 8})
	if err != nil || result == nil || result.Events == nil || len(result.Events) != 0 || result.HasMore {
		t.Fatalf("result=%+v err=%v", result, err)
	}
}
