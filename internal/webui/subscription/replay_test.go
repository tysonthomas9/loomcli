package subscription_test

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/backend/fleet"
	"github.com/tysonthomas9/loomcli/internal/rpc"
	"github.com/tysonthomas9/loomcli/internal/webui/server/realtime"
	"github.com/tysonthomas9/loomcli/internal/webui/subscription"
)

type replayTransport func(*http.Request) (*http.Response, error)

func (f replayTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

type handshakeRecorder struct {
	*httptest.ResponseRecorder
	cancel      context.CancelFunc
	onConnected func()
	stopID      string
}

func (w *handshakeRecorder) Flush() {
	w.ResponseRecorder.Flush()
	if strings.Contains(w.Body.String(), "event: connected\n") {
		if w.onConnected != nil {
			f := w.onConnected
			w.onConnected = nil
			f()
		}
		if w.stopID == "" || strings.Contains(w.Body.String(), "id: "+w.stopID+"\n") {
			w.cancel()
		}
	}
}

// Exercise the actual Fleet HTTP adapter, workspace subscriber, and SSE handler.
// The external Fleet API is controlled to expose pagination deterministically.
func TestReplayCompletesBeforeConnected(t *testing.T) {
	for _, total := range []int{101, 201, 1001} {
		t.Run(fmt.Sprint(total), func(t *testing.T) {
			cursor := func(n int) string {
				return "c1." + base64.RawURLEncoding.EncodeToString([]byte(fmt.Sprintf("100-%d", n)))
			}
			requests := 0
			hub := realtime.NewHub()
			go hub.Run()
			defer hub.Stop()
			b, err := fleet.New(fleet.Config{BaseURL: "http://fleet.test", WorkspaceID: "replay", HTTPClient: &http.Client{Transport: replayTransport(func(r *http.Request) (*http.Response, error) {
				requests++
				raw, e := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(r.URL.Query().Get("since"), "c1."))
				if e != nil {
					return nil, e
				}
				var start int
				if _, e = fmt.Sscanf(string(raw), "100-%d", &start); e != nil {
					return nil, e
				}
				end := min(start+100, total)
				// Publish the last replay record concurrently through the live path.
				if end == total {
					hub.Broadcast(&realtime.MutationPayload{Cursor: cursor(total), Type: "update", WorkspaceID: "replay"})
				}
				events := []map[string]any{}
				for n := start + 1; n <= end; n++ {
					events = append(events, map[string]any{"id": cursor(n), "action": "issue.update", "entity_type": "issue", "entity_id": fmt.Sprintf("issue-%d", n), "timestamp": "2026-09-04T00:00:00Z"})
				}
				rec := httptest.NewRecorder()
				e = json.NewEncoder(rec).Encode(map[string]any{"success": true, "data": map[string]any{"events": events, "cursor": cursor(end), "has_more": end < total}})
				return rec.Result(), e
			})}})
			if err != nil {
				t.Fatal(err)
			}
			sub := subscription.NewBackendMutationSubscriber(b, hub, "replay")
			defer sub.Stop()
			h := realtime.NewHandler(realtime.HandlerConfig{Hub: hub, WorkspaceFromCtx: func(context.Context) string { return "replay" }, GetMutationsSince: func(_, since string) ([]rpc.MutationEvent, error) {
				data, err := sub.GetMutationDataSince(since)
				if err != nil {
					return nil, err
				}
				out := make([]rpc.MutationEvent, len(data))
				for i, m := range data {
					out[i] = realtime.BackendMutationToRPCEvent(m)
				}
				return out, nil
			}})
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			rec := &handshakeRecorder{ResponseRecorder: httptest.NewRecorder(), cancel: cancel, stopID: cursor(total + 1), onConnected: func() {
				hub.Broadcast(&realtime.MutationPayload{Cursor: cursor(total + 1), Type: "update", WorkspaceID: "replay"})
			}}
			h.ServeHTTP(rec, httptest.NewRequest("GET", "/events?since="+cursor(0), nil).WithContext(ctx))
			wire := rec.Body.String()
			expected := 1
			for _, line := range strings.Split(wire, "\n") {
				if line == "event: connected" && expected != total+1 {
					t.Fatalf("completion before replay drained: %d/%d", expected-1, total)
				}
				if strings.HasPrefix(line, "id: ") {
					if line != "id: "+cursor(expected) {
						t.Fatalf("checkpoint at event %d: %s", expected, line)
					}
					expected++
				}
			}
			if expected != total+2 {
				t.Fatalf("connected after %d/%d mutations (%d API requests)", expected-1, total, requests)
			}
			if !strings.Contains(wire, "event: connected\n") {
				t.Fatal("missing completion barrier")
			}
		})
	}
}

func TestReplayFailureDoesNotReportConnected(t *testing.T) {
	for _, scope := range []string{"", "included"} {
		for _, partial := range []bool{false, true} {
			t.Run(fmt.Sprintf("partial=%v/scope=%s", partial, scope), func(t *testing.T) {
				calls := 0
				b, err := fleet.New(fleet.Config{BaseURL: "http://fleet.test", WorkspaceID: "replay", HTTPClient: &http.Client{Transport: replayTransport(func(r *http.Request) (*http.Response, error) {
					calls++
					rec := httptest.NewRecorder()
					if partial && calls == 1 {
						_ = json.NewEncoder(rec).Encode(map[string]any{"success": true, "data": map[string]any{"events": []map[string]any{{"id": "100-1", "action": "issue.update", "entity_type": "issue", "entity_id": "first", "after": `{"repo":"excluded"}`, "timestamp": "2026-09-04T00:00:00Z"}}, "cursor": "c1.MTAwLTE", "has_more": true}})
					} else {
						rec.WriteHeader(http.StatusBadRequest)
						_, _ = rec.Write([]byte(`{"success":false,"error":"replay unavailable"}`))
					}
					return rec.Result(), nil
				})}})
				if err != nil {
					t.Fatal(err)
				}
				hub := realtime.NewHub()
				go hub.Run()
				defer hub.Stop()
				sub := subscription.NewBackendMutationSubscriber(b, hub, "replay")
				defer sub.Stop()
				h := realtime.NewHandler(realtime.HandlerConfig{Hub: hub, WorkspaceFromCtx: func(context.Context) string { return "replay" }, GetMutationsSince: func(_, since string) ([]rpc.MutationEvent, error) {
					data, err := sub.GetMutationDataSince(since)
					out := make([]rpc.MutationEvent, len(data))
					for i, m := range data {
						out[i] = realtime.BackendMutationToRPCEvent(m)
					}
					return out, err
				}})
				ctx, cancel := context.WithTimeout(context.Background(), time.Second)
				defer cancel()
				rec := &handshakeRecorder{ResponseRecorder: httptest.NewRecorder(), cancel: cancel}
				h.ServeHTTP(rec, httptest.NewRequest("GET", "/events?since=100-0&source_repos="+scope, nil).WithContext(ctx))
				if strings.Contains(rec.Body.String(), "event: connected") {
					t.Fatal("failed replay reported successful synchronization")
				}
				if partial && (!strings.Contains(rec.Body.String(), "id: 100-1\n") || (scope != "" && !strings.Contains(rec.Body.String(), "event: checkpoint\n"))) {
					t.Fatal("later-page failure discarded the successful prefix checkpoint")
				}
			})
		}
	}
}
