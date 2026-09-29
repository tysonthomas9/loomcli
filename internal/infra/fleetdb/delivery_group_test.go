package fleetdb

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/store"
)

type fakeDeliveryGroupServer struct {
	mu      sync.Mutex
	calls   []fakeDGCall
	handler http.HandlerFunc
	server  *httptest.Server
}

type fakeDGCall struct {
	Method  string
	Path    string
	Query   string
	Headers http.Header
	Body    map[string]any
}

func newFakeDeliveryGroupServer(t *testing.T, h http.HandlerFunc) *fakeDeliveryGroupServer {
	t.Helper()
	f := &fakeDeliveryGroupServer{handler: h}
	f.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		bodyBytes, _ := io.ReadAll(r.Body)
		var body map[string]any
		if len(bodyBytes) > 0 {
			_ = json.Unmarshal(bodyBytes, &body)
		}
		f.mu.Lock()
		f.calls = append(f.calls, fakeDGCall{
			Method:  r.Method,
			Path:    r.URL.Path,
			Query:   r.URL.RawQuery,
			Headers: r.Header.Clone(),
			Body:    body,
		})
		handler := f.handler
		f.mu.Unlock()
		handler(w, r)
	}))
	t.Cleanup(f.server.Close)
	return f
}

func (f *fakeDeliveryGroupServer) client(t *testing.T) *Client {
	t.Helper()
	c, err := New(Config{BaseURL: f.server.URL, Actor: "test"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { _ = c.Close() })
	return c
}

func (f *fakeDeliveryGroupServer) lastCall(t *testing.T) fakeDGCall {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.calls) == 0 {
		t.Fatal("expected at least one call")
	}
	return f.calls[len(f.calls)-1]
}

func writeDGJSON(w http.ResponseWriter, status int, headers map[string]string, v any) {
	for k, val := range headers {
		w.Header().Set(k, val)
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeDGError(w http.ResponseWriter, status int, code string, meta map[string]string) {
	writeDGJSON(w, status, nil, map[string]any{
		"error": map[string]any{
			"code":    code,
			"message": code,
			"meta":    meta,
		},
	})
}

func sampleGroupWire(id string, revision int64, members ...map[string]any) map[string]any {
	if members == nil {
		members = []map[string]any{}
	}
	now := time.Date(2026, 9, 24, 12, 0, 0, 0, time.UTC).Format(time.RFC3339)
	return map[string]any{
		"workspace_key": "WS",
		"id":            id,
		"title":         "ship it",
		"state":         "active",
		"revision":      revision,
		"members":       members,
		"last_op_id":    "op-1",
		"created_at":    now,
		"updated_at":    now,
	}
}

func TestDeliveryGroupListPagesHasMore(t *testing.T) {
	f := newFakeDeliveryGroupServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || !strings.HasSuffix(r.URL.Path, "/delivery-groups") {
			writeDGError(w, http.StatusNotFound, "not_found", nil)
			return
		}
		if r.URL.Query().Get("cursor") != "c1.next" {
			t.Errorf("cursor = %q, want c1.next", r.URL.Query().Get("cursor"))
		}
		if r.URL.Query().Get("limit") != "2" {
			t.Errorf("limit = %q, want 2", r.URL.Query().Get("limit"))
		}
		writeDGJSON(w, http.StatusOK, nil, map[string]any{
			"delivery_groups": []any{
				sampleGroupWire("dg_01JABCDEFGHJKMNPQRSTVWXYZ0", 1),
				sampleGroupWire("dg_01JABCDEFGHJKMNPQRSTVWXYZ1", 2),
			},
			"count":       2,
			"has_more":    true,
			"next_cursor": "c1.more",
		})
	})
	page, err := f.client(t).DeliveryGroups().List(context.Background(), "WS", store.DeliveryGroupListOpts{
		Limit:  2,
		Cursor: "c1.next",
		State:  "active",
	})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if !page.HasMore || page.NextCursor != "c1.more" || page.Count != 2 || len(page.Groups) != 2 {
		t.Fatalf("page = %+v", page)
	}
}

func TestDeliveryGroupCreateCrossRepoOrder(t *testing.T) {
	f := newFakeDeliveryGroupServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeDGError(w, http.StatusMethodNotAllowed, "method", nil)
			return
		}
		writeDGJSON(w, http.StatusCreated, map[string]string{"ETag": `"1"`}, sampleGroupWire(
			"dg_01JABCDEFGHJKMNPQRSTVWXYZ0", 1,
			map[string]any{
				"pr_key": "github:acme/web#12", "repo_name": "web", "pr_number": 12,
				"source": "manual", "added_at": time.Now().UTC().Format(time.RFC3339),
			},
			map[string]any{
				"pr_key": "github:acme/api#3", "repo_name": "api", "pr_number": 3,
				"source": "manual", "added_at": time.Now().UTC().Format(time.RFC3339),
			},
		))
	})
	res, err := f.client(t).DeliveryGroups().Create(context.Background(), "WS", "intent-create-1", domain.DeliveryGroupCreate{
		ID:    "dg_01JABCDEFGHJKMNPQRSTVWXYZ0",
		Title: "ship it",
		Members: []domain.DeliveryGroupMemberInput{
			{RepoName: "web", PRNumber: 12},
			{RepoName: "api", PRNumber: 3},
		},
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	call := f.lastCall(t)
	if call.Headers.Get("X-Idempotency-Key") != "intent-create-1" {
		t.Fatalf("idempotency key = %q", call.Headers.Get("X-Idempotency-Key"))
	}
	if res.Status != http.StatusCreated || res.ETag != `"1"` || len(res.Group.Members) != 2 {
		t.Fatalf("result = %+v group=%+v", res, res.Group)
	}
	if res.Group.Members[0].PRKey != "github:acme/web#12" || res.Group.Members[1].PRKey != "github:acme/api#3" {
		t.Fatalf("order = %+v", res.Group.Members)
	}
}

func TestDeliveryGroupSetMembersReplayAndConflicts(t *testing.T) {
	t.Run("verified replay", func(t *testing.T) {
		f := newFakeDeliveryGroupServer(t, func(w http.ResponseWriter, r *http.Request) {
			writeDGJSON(w, http.StatusOK, map[string]string{
				"ETag":                   `"3"`,
				"X-Idempotency-Replayed": "true",
			}, sampleGroupWire("dg_01JABCDEFGHJKMNPQRSTVWXYZ0", 3))
		})
		res, err := f.client(t).DeliveryGroups().SetMembers(
			context.Background(), "WS", "dg_01JABCDEFGHJKMNPQRSTVWXYZ0", `"2"`, "intent-same",
			[]domain.DeliveryGroupMemberInput{{RepoName: "web", PRNumber: 1}},
		)
		if err != nil {
			t.Fatalf("SetMembers: %v", err)
		}
		if !res.Replayed || res.Group.Revision != 3 {
			t.Fatalf("result = %+v", res)
		}
		call := f.lastCall(t)
		if call.Headers.Get("If-Match") != `"2"` {
			t.Fatalf("If-Match = %q", call.Headers.Get("If-Match"))
		}
	})

	t.Run("duplicate membership", func(t *testing.T) {
		f := newFakeDeliveryGroupServer(t, func(w http.ResponseWriter, r *http.Request) {
			writeDGError(w, http.StatusConflict, domain.DeliveryGroupConflictPRInOtherGroup, map[string]string{
				"pr_key":   "github:acme/web#12",
				"group_id": "dg_other",
			})
		})
		_, err := f.client(t).DeliveryGroups().SetMembers(
			context.Background(), "WS", "dg_01JABCDEFGHJKMNPQRSTVWXYZ0", `"1"`, "intent-dup",
			[]domain.DeliveryGroupMemberInput{{PRKey: "github:acme/web#12", RepoName: "web", PRNumber: 12}},
		)
		var conflict *domain.DeliveryGroupConflictError
		if !errors.As(err, &conflict) {
			t.Fatalf("err = %v, want DeliveryGroupConflictError", err)
		}
		if conflict.Code != domain.DeliveryGroupConflictPRInOtherGroup || conflict.PRKey != "github:acme/web#12" || conflict.GroupID != "dg_other" {
			t.Fatalf("conflict = %+v", conflict)
		}
	})

	t.Run("stale revision", func(t *testing.T) {
		f := newFakeDeliveryGroupServer(t, func(w http.ResponseWriter, r *http.Request) {
			writeDGError(w, http.StatusPreconditionFailed, domain.DeliveryGroupPreconditionFailedCode, map[string]string{
				"expected_revision": "2",
				"stored_revision":   "5",
			})
		})
		_, err := f.client(t).DeliveryGroups().Update(
			context.Background(), "WS", "dg_01JABCDEFGHJKMNPQRSTVWXYZ0", `"2"`, "intent-stale",
			domain.DeliveryGroupUpdate{Title: strPtr("new")},
		)
		var pre *domain.DeliveryGroupPreconditionError
		if !errors.As(err, &pre) {
			t.Fatalf("err = %v, want DeliveryGroupPreconditionError", err)
		}
		if pre.ExpectedRevision != 2 || pre.StoredRevision != 5 {
			t.Fatalf("pre = %+v", pre)
		}
	})

	t.Run("reused key with changed body", func(t *testing.T) {
		f := newFakeDeliveryGroupServer(t, func(w http.ResponseWriter, r *http.Request) {
			writeDGError(w, http.StatusConflict, domain.DeliveryGroupConflictIdempotencyReused, nil)
		})
		_, err := f.client(t).DeliveryGroups().Create(context.Background(), "WS", "intent-reuse", domain.DeliveryGroupCreate{
			ID: "dg_01JABCDEFGHJKMNPQRSTVWXYZ0", Title: "other",
		})
		var conflict *domain.DeliveryGroupConflictError
		if !errors.As(err, &conflict) || conflict.Code != domain.DeliveryGroupConflictIdempotencyReused {
			t.Fatalf("err = %v", err)
		}
	})

	t.Run("inconsistent group", func(t *testing.T) {
		f := newFakeDeliveryGroupServer(t, func(w http.ResponseWriter, r *http.Request) {
			writeDGError(w, http.StatusServiceUnavailable, domain.DeliveryGroupInconsistentCode, nil)
		})
		_, err := f.client(t).DeliveryGroups().Get(context.Background(), "WS", "dg_01JABCDEFGHJKMNPQRSTVWXYZ0")
		if !errors.Is(err, domain.ErrDeliveryGroupInconsistent) {
			t.Fatalf("err = %v", err)
		}
	})
}

func TestDeliveryGroupWritesNeverCallGitHub(t *testing.T) {
	// Adapter only dials the FleetDB fake; any github.com path would be a bug.
	f := newFakeDeliveryGroupServer(t, func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.Host, "github") || strings.Contains(r.URL.Path, "github") {
			t.Fatalf("unexpected github path %s", r.URL.Path)
		}
		writeDGJSON(w, http.StatusOK, map[string]string{"ETag": `"4"`}, sampleGroupWire("dg_01JABCDEFGHJKMNPQRSTVWXYZ0", 4))
	})
	dg := f.client(t).DeliveryGroups()
	if _, err := dg.Archive(context.Background(), "WS", "dg_01JABCDEFGHJKMNPQRSTVWXYZ0", `"3"`, "intent-arch"); err != nil {
		t.Fatalf("Archive: %v", err)
	}
	call := f.lastCall(t)
	if call.Method != http.MethodPost || !strings.HasSuffix(call.Path, "/archive") {
		t.Fatalf("call = %+v", call)
	}
}

func strPtr(s string) *string { return &s }
