package subscription

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/tysonthomas9/loomcli/internal/backend"
	"github.com/tysonthomas9/loomcli/internal/webui/server/realtime"
)

type bindingHandlerSubscriber struct {
	trackingWorkspaceSubscriber
	headReads, pageReads int
	page                 backend.MutationPage
}

func (s *bindingHandlerSubscriber) GetMutationHead(context.Context) (backend.MutationPage, error) {
	s.headReads++
	return backend.MutationPage{SourceIdentity: "s1.Zml4dHVyZQ", Cursor: "c2.ZmVuY2U"}, nil
}

func (s *bindingHandlerSubscriber) GetMutationPageThrough(context.Context, string, string, int) (backend.MutationPage, error) {
	s.pageReads++
	return s.page, nil
}

type sourceSwapWriter struct {
	*httptest.ResponseRecorder
	onFrame func(string)
}

func (w *sourceSwapWriter) Write(data []byte) (int, error) {
	n, err := w.ResponseRecorder.Write(data)
	if err == nil {
		w.onFrame(string(data))
	}
	return n, err
}

// Uses the production handler and source registry, with deterministic source
// and writer fixtures. The swap happens after the registry post-read check and
// before the handler starts its next page/pass, reproducing the original gap.
func TestBoundSourceHandlerNeverAdoptsReplacementWithEqualCursors(t *testing.T) {
	for _, boundary := range []string{"between pages", "between passes"} {
		t.Run(boundary, func(t *testing.T) {
			hub := realtime.NewHub()
			go hub.Run()
			t.Cleanup(hub.Stop)
			cursor := "c2.Zmlyc3Q"
			more := true
			if boundary == "between passes" {
				cursor, more = "c2.ZmVuY2U", false
			}
			old := &bindingHandlerSubscriber{page: backend.MutationPage{SourceIdentity: "s1.Zml4dHVyZQ", Cursor: cursor, HasMore: more, Events: []backend.MutationData{{Cursor: cursor, Type: backend.MutationUpdate, IssueID: "old-issue"}}}}
			// Both identities advertise the same fence; cursor equality cannot
			// distinguish them. The replacement's payload must never be fetched.
			replacement := &bindingHandlerSubscriber{page: backend.MutationPage{SourceIdentity: "s1.Zml4dHVyZQ", Cursor: "c2.ZmVuY2U", Events: []backend.MutationData{{Cursor: "c2.ZmVuY2U", Type: backend.MutationUpdate, IssueID: "replacement-issue"}}}}
			multi := &MultiWorkspaceSubscriber{subscribers: map[string]*subscriberEntry{"ws": {sub: old}}}
			handler := realtime.NewHandler(realtime.HandlerConfig{Hub: hub, WorkspaceFromCtx: func(context.Context) string { return "ws" }, OpenMutationSource: multi.OpenMutationSource})
			swapped := false
			writer := &sourceSwapWriter{ResponseRecorder: httptest.NewRecorder(), onFrame: func(frame string) {
				trigger := strings.Contains(frame, "id: c2.Zmlyc3Q\n")
				if boundary == "between passes" {
					trigger = strings.Contains(frame, "event: connected\n")
				}
				if !trigger || swapped {
					return
				}
				swapped = true
				multi.mu.Lock()
				multi.subscribers["ws"] = &subscriberEntry{sub: replacement}
				multi.mu.Unlock()
				hub.Broadcast(&realtime.MutationPayload{WorkspaceID: "ws", Cursor: "c2.ZmVuY2U", Type: backend.MutationUpdate, IssueID: "replacement-issue"})
			}}
			ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
			defer cancel()
			req := httptest.NewRequest(http.MethodGet, "/events", nil).WithContext(ctx)
			req.Header.Set("Last-Event-ID", "c2.c3RhcnQ")
			handler.ServeHTTP(writer, req)
			require.NoError(t, ctx.Err(), "source retirement must end the request before its timeout")
			require.True(t, swapped)
			require.Equal(t, 1, old.headReads)
			require.Equal(t, 1, old.pageReads)
			require.Zero(t, replacement.headReads)
			require.Zero(t, replacement.pageReads)
			body := writer.Body.String()
			require.Contains(t, body, "old-issue")
			require.Contains(t, body, "event: resync\n")
			require.NotContains(t, body, "replacement-issue")
			require.Equal(t, 1, strings.Count(body, "id: "), "retirement cannot advance the checkpoint")
			if boundary == "between pages" {
				require.NotContains(t, body, "event: connected\n")
			}
		})
	}
}
