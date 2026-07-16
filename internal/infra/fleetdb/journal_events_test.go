package fleetdb

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/infra/memstore"
	"github.com/tysonthomas9/loomcli/internal/store"
)

// issueJournalReader narrows the fleet-db client to the optional capability
// under test, failing the test when the type assertion does not hold.
func issueJournalReader(t *testing.T, c *Client) store.IssueJournalReader {
	t.Helper()
	r, ok := c.TriggerEvents().(store.IssueJournalReader)
	if !ok {
		t.Fatalf("fleetdb TriggerEvents %T does not implement store.IssueJournalReader", c.TriggerEvents())
	}
	return r
}

func TestListIssueEvents(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name        string
		afterCursor string
		limit       int
		// handler responds and asserts the request; on the second call (paging
		// case) it advances by reading the since param.
		handler func(t *testing.T, w http.ResponseWriter, r *http.Request)

		wantErr     error
		wantLen     int
		wantCursor  string
		wantHasMore bool
		// assertEvents runs extra per-event assertions when set.
		assertEvents func(t *testing.T, events []store.JournalEvent)
	}{
		{
			name:        "filter params, since/limit echo and after unwrap",
			afterCursor: "1707001234560-0",
			limit:       50,
			handler: func(t *testing.T, w http.ResponseWriter, r *http.Request) {
				q := r.URL.Query()
				if got := q.Get("entity_type"); got != "issue" {
					t.Errorf("entity_type = %q, want issue", got)
				}
				if got := q.Get("since"); got != "1707001234560-0" {
					t.Errorf("since = %q, want opaque cursor pass-through", got)
				}
				if got := q.Get("limit"); got != "50" {
					t.Errorf("limit = %q, want 50", got)
				}
				writeJSON(t, w, map[string]any{
					"events": []map[string]any{{
						"id":          "1707001234561-0",
						"timestamp":   "2026-06-11T10:00:00Z",
						"actor":       "octocat",
						"action":      "issue.opened",
						"entity_type": "issue",
						"entity_id":   "issue-42",
						// fleet-db serializes the snapshot as a JSON STRING.
						"after":    `{"number":42,"title":"bug"}`,
						"metadata": map[string]string{"repo": "owner/repo"},
					}},
					"cursor":   "1707001234561-0",
					"has_more": false,
				})
			},
			wantLen:     1,
			wantCursor:  "1707001234561-0",
			wantHasMore: false,
			assertEvents: func(t *testing.T, events []store.JournalEvent) {
				e := events[0]
				if e.ID != "1707001234561-0" || e.Action != "issue.opened" || e.Actor != "octocat" || e.EntityID != "issue-42" {
					t.Errorf("event projection = %+v", e)
				}
				if string(e.After) != `{"number":42,"title":"bug"}` {
					t.Errorf("after unwrap = %s, want unwrapped JSON object", e.After)
				}
				if e.Metadata["repo"] != "owner/repo" {
					t.Errorf("metadata = %v", e.Metadata)
				}
			},
		},
		{
			name:        "empty cursor maps to beginning-of-stream sentinel",
			afterCursor: "",
			limit:       10,
			handler: func(t *testing.T, w http.ResponseWriter, r *http.Request) {
				if got := r.URL.Query().Get("since"); got != "0" {
					t.Errorf("since = %q, want \"0\" for empty cursor", got)
				}
				writeJSON(t, w, map[string]any{
					"events":   []map[string]any{},
					"cursor":   "0",
					"has_more": false,
				})
			},
			wantLen:     0,
			wantCursor:  "0",
			wantHasMore: false,
		},
		{
			name:        "empty batch echoes afterCursor when server cursor blank",
			afterCursor: "1707001234999-0",
			limit:       10,
			handler: func(t *testing.T, w http.ResponseWriter, r *http.Request) {
				writeJSON(t, w, map[string]any{
					"events":   []map[string]any{},
					"cursor":   "",
					"has_more": false,
				})
			},
			wantLen:     0,
			wantCursor:  "1707001234999-0",
			wantHasMore: false,
		},
		{
			name:        "full page reports has_more for paging",
			afterCursor: "1707001234560-0",
			limit:       2,
			handler: func(t *testing.T, w http.ResponseWriter, r *http.Request) {
				writeJSON(t, w, map[string]any{
					"events": []map[string]any{
						{
							"id":          "1707001234561-0",
							"timestamp":   "2026-06-11T10:00:01Z",
							"action":      "issue.opened",
							"entity_type": "issue",
							"entity_id":   "issue-1",
							"after":       `{"number":1}`,
						},
						{
							"id":          "1707001234562-0",
							"timestamp":   "2026-06-11T10:00:02Z",
							"action":      "issue.edited",
							"entity_type": "issue",
							"entity_id":   "issue-2",
							"after":       `{"number":2}`,
						},
					},
					"cursor":   "1707001234562-0",
					"has_more": true,
				})
			},
			wantLen:     2,
			wantCursor:  "1707001234562-0",
			wantHasMore: true,
		},
		{
			name:        "malformed after JSON skipped, event retained",
			afterCursor: "0",
			limit:       10,
			handler: func(t *testing.T, w http.ResponseWriter, r *http.Request) {
				writeJSON(t, w, map[string]any{
					"events": []map[string]any{
						{
							"id":          "1707001234561-0",
							"timestamp":   "2026-06-11T10:00:01Z",
							"action":      "issue.opened",
							"entity_type": "issue",
							"entity_id":   "issue-1",
							"after":       `{not json`,
						},
						{
							"id":          "1707001234562-0",
							"timestamp":   "2026-06-11T10:00:02Z",
							"action":      "issue.closed",
							"entity_type": "issue",
							"entity_id":   "issue-2",
							// no after at all
						},
					},
					"cursor":   "1707001234562-0",
					"has_more": false,
				})
			},
			wantLen:     2,
			wantCursor:  "1707001234562-0",
			wantHasMore: false,
			assertEvents: func(t *testing.T, events []store.JournalEvent) {
				if events[0].After != nil {
					t.Errorf("malformed after should be nil, got %s", events[0].After)
				}
				if events[1].After != nil {
					t.Errorf("absent after should be nil, got %s", events[1].After)
				}
				if events[1].Action != "issue.closed" {
					t.Errorf("second event action = %q", events[1].Action)
				}
			},
		},
		{
			name:        "non-2xx classified to domain error",
			afterCursor: "0",
			limit:       10,
			handler: func(t *testing.T, w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusBadRequest)
				writeJSON(t, w, map[string]any{
					"error": map[string]any{"code": "invalid_parameter", "message": "bad since"},
				})
			},
			wantErr: domain.ErrInvalid,
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodGet || r.URL.Path != "/api/v1/WS/events/mutations" {
					t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
					w.WriteHeader(http.StatusNotFound)
					return
				}
				tc.handler(t, w, r)
			}))
			defer ts.Close()

			client, err := New(Config{BaseURL: ts.URL, Actor: "tester"})
			if err != nil {
				t.Fatalf("New: %v", err)
			}
			reader := issueJournalReader(t, client)

			events, cursor, hasMore, err := reader.ListIssueEvents(context.Background(), "WS", tc.afterCursor, tc.limit)
			if tc.wantErr != nil {
				if !errors.Is(err, tc.wantErr) {
					t.Fatalf("err = %v, want errors.Is %v", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("ListIssueEvents: %v", err)
			}
			if len(events) != tc.wantLen {
				t.Fatalf("len(events) = %d, want %d", len(events), tc.wantLen)
			}
			if cursor != tc.wantCursor {
				t.Errorf("nextCursor = %q, want %q", cursor, tc.wantCursor)
			}
			if hasMore != tc.wantHasMore {
				t.Errorf("hasMore = %v, want %v", hasMore, tc.wantHasMore)
			}
			if tc.assertEvents != nil {
				tc.assertEvents(t, events)
			}
		})
	}
}

// TestMemstoreDoesNotImplementIssueJournalReader pins the capability gate: the
// bridge is enabled only when the store satisfies store.IssueJournalReader,
// and memstore deliberately does not (same posture as the run.finished lane,
// which gates on TriggerEventAppender). If memstore ever grows this method by
// accident the bridge would silently activate against a non-journaling store.
func TestMemstoreDoesNotImplementIssueJournalReader(t *testing.T) {
	t.Parallel()
	mem := memstore.New()
	if _, ok := mem.TriggerEvents().(store.IssueJournalReader); ok {
		t.Fatal("memstore TriggerEvents must NOT implement store.IssueJournalReader (bridge is capability-gated)")
	}
}
