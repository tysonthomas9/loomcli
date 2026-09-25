package prreview

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/ops"
	"github.com/tysonthomas9/loomcli/internal/prref"
	"github.com/tysonthomas9/loomcli/internal/store"
	"github.com/tysonthomas9/loomcli/internal/webui/server/middleware"
)

// fakeDeliveryGroups is an in-memory DeliveryGroupStore for HTTP contract tests.
type fakeDeliveryGroups struct {
	mu      sync.Mutex
	groups  map[string]*domain.DeliveryGroup
	byPR    map[string]string
	ops     map[string]*store.DeliveryGroupWriteResult
	listErr error
	// forceHasMoreNoCursor makes List report has_more with an empty next_cursor
	// so membership indexing returns incomplete (truncated / unverified).
	forceHasMoreNoCursor bool
	writes               []string
}

func newFakeDeliveryGroups() *fakeDeliveryGroups {
	return &fakeDeliveryGroups{
		groups: map[string]*domain.DeliveryGroup{},
		byPR:   map[string]string{},
		ops:    map[string]*store.DeliveryGroupWriteResult{},
	}
}

func (f *fakeDeliveryGroups) put(g *domain.DeliveryGroup) {
	f.mu.Lock()
	defer f.mu.Unlock()
	cp := *g
	cp.Members = append([]domain.DeliveryGroupMember(nil), g.Members...)
	f.groups[g.ID] = &cp
	if g.State == domain.DeliveryGroupActive {
		for _, m := range g.Members {
			f.byPR[m.PRKey] = g.ID
		}
	}
}

func (f *fakeDeliveryGroups) List(ctx context.Context, ws string, opts store.DeliveryGroupListOpts) (*store.DeliveryGroupPage, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.listErr != nil {
		return nil, f.listErr
	}
	out := make([]*domain.DeliveryGroup, 0, len(f.groups))
	for _, g := range f.groups {
		if opts.State == "" || opts.State == "active" {
			if g.State != domain.DeliveryGroupActive {
				continue
			}
		} else if opts.State == "archived" && g.State != domain.DeliveryGroupArchived {
			continue
		}
		cp := *g
		cp.Members = append([]domain.DeliveryGroupMember(nil), g.Members...)
		out = append(out, &cp)
	}
	limit := opts.Limit
	if limit <= 0 {
		limit = 50
	}
	hasMore := len(out) > limit
	if hasMore {
		out = out[:limit]
	}
	page := &store.DeliveryGroupPage{Groups: out, Count: len(out), HasMore: hasMore}
	if f.forceHasMoreNoCursor {
		page.HasMore = true
		page.NextCursor = ""
	}
	return page, nil
}

func (f *fakeDeliveryGroups) Get(ctx context.Context, ws, groupID string) (*domain.DeliveryGroup, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	g, ok := f.groups[groupID]
	if !ok {
		return nil, domain.ErrNotFound
	}
	cp := *g
	cp.Members = append([]domain.DeliveryGroupMember(nil), g.Members...)
	return &cp, nil
}

func (f *fakeDeliveryGroups) GetByPR(ctx context.Context, ws, prKey string) (*domain.DeliveryGroup, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	id, ok := f.byPR[prKey]
	if !ok {
		return nil, domain.ErrNotFound
	}
	return f.Get(ctx, ws, id)
}

func (f *fakeDeliveryGroups) Create(ctx context.Context, ws, key string, in domain.DeliveryGroupCreate) (*store.DeliveryGroupWriteResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.writes = append(f.writes, "create:"+key)
	if prev, ok := f.ops[key]; ok {
		return prev, nil
	}
	if _, exists := f.groups[in.ID]; exists {
		return nil, &domain.DeliveryGroupConflictError{Code: domain.DeliveryGroupConflictAlreadyExists}
	}
	members, err := f.resolveMembersLocked(in.Members, "")
	if err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	g := &domain.DeliveryGroup{
		WorkspaceKey: ws,
		ID:           in.ID,
		Title:        in.Title,
		EpicID:       in.EpicID,
		Owner:        in.Owner,
		State:        domain.DeliveryGroupActive,
		Revision:     1,
		Members:      members,
		LastOpID:     key,
		CreatedAt:    now,
		UpdatedAt:    now,
	}
	f.groups[g.ID] = g
	for _, m := range members {
		f.byPR[m.PRKey] = g.ID
	}
	res := &store.DeliveryGroupWriteResult{Group: g, Status: http.StatusCreated, ETag: `"1"`}
	f.ops[key] = res
	return res, nil
}

func (f *fakeDeliveryGroups) Update(ctx context.Context, ws, id, ifMatch, key string, in domain.DeliveryGroupUpdate) (*store.DeliveryGroupWriteResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.writes = append(f.writes, "update:"+key)
	if prev, ok := f.ops[key]; ok {
		cp := *prev
		cp.Replayed = true
		return &cp, nil
	}
	g, ok := f.groups[id]
	if !ok {
		return nil, domain.ErrNotFound
	}
	if err := f.checkRevisionLocked(g, ifMatch); err != nil {
		return nil, err
	}
	if in.Title != nil {
		g.Title = *in.Title
	}
	if in.EpicID != nil {
		g.EpicID = *in.EpicID
	}
	if in.Owner != nil {
		g.Owner = *in.Owner
	}
	g.Revision++
	g.LastOpID = key
	g.UpdatedAt = time.Now().UTC()
	res := &store.DeliveryGroupWriteResult{Group: cloneGroup(g), Status: http.StatusOK, ETag: etag(g.Revision)}
	f.ops[key] = res
	return res, nil
}

func (f *fakeDeliveryGroups) SetMembers(ctx context.Context, ws, id, ifMatch, key string, members []domain.DeliveryGroupMemberInput) (*store.DeliveryGroupWriteResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.writes = append(f.writes, "set:"+key)
	if prev, ok := f.ops[key]; ok {
		// Same key with different body → conflict (simplified: compare len + first key).
		prevKeys := memberKeys(prev.Group.Members)
		next, err := f.resolveMembersLocked(members, id)
		if err != nil {
			return nil, err
		}
		nextKeys := memberKeys(next)
		if prevKeys != nextKeys {
			return nil, &domain.DeliveryGroupConflictError{Code: domain.DeliveryGroupConflictIdempotencyReused}
		}
		cp := *prev
		cp.Replayed = true
		return &cp, nil
	}
	g, ok := f.groups[id]
	if !ok {
		return nil, domain.ErrNotFound
	}
	if err := f.checkRevisionLocked(g, ifMatch); err != nil {
		return nil, err
	}
	resolved, err := f.resolveMembersLocked(members, id)
	if err != nil {
		return nil, err
	}
	for _, m := range g.Members {
		delete(f.byPR, m.PRKey)
	}
	g.Members = resolved
	for _, m := range resolved {
		f.byPR[m.PRKey] = g.ID
	}
	g.Revision++
	g.LastOpID = key
	g.UpdatedAt = time.Now().UTC()
	res := &store.DeliveryGroupWriteResult{Group: cloneGroup(g), Status: http.StatusOK, ETag: etag(g.Revision)}
	f.ops[key] = res
	return res, nil
}

func (f *fakeDeliveryGroups) Archive(ctx context.Context, ws, id, ifMatch, key string) (*store.DeliveryGroupWriteResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.writes = append(f.writes, "archive:"+key)
	if prev, ok := f.ops[key]; ok {
		cp := *prev
		cp.Replayed = true
		return &cp, nil
	}
	g, ok := f.groups[id]
	if !ok {
		return nil, domain.ErrNotFound
	}
	if err := f.checkRevisionLocked(g, ifMatch); err != nil {
		return nil, err
	}
	for _, m := range g.Members {
		delete(f.byPR, m.PRKey)
	}
	g.State = domain.DeliveryGroupArchived
	g.Revision++
	g.LastOpID = key
	res := &store.DeliveryGroupWriteResult{Group: cloneGroup(g), Status: http.StatusOK, ETag: etag(g.Revision)}
	f.ops[key] = res
	return res, nil
}

func (f *fakeDeliveryGroups) resolveMembersLocked(in []domain.DeliveryGroupMemberInput, ignoreGroupID string) ([]domain.DeliveryGroupMember, error) {
	out := make([]domain.DeliveryGroupMember, 0, len(in))
	seen := map[string]bool{}
	now := time.Now().UTC()
	for _, m := range in {
		var key string
		var repo string
		var num int
		switch {
		case m.PRKey != "":
			ref, err := prref.Parse(m.PRKey)
			if err != nil {
				return nil, domain.ErrInvalid
			}
			key = ref.Key()
			repo = m.RepoName
			if repo == "" {
				repo = ref.Repo
			}
			num = ref.Number
		case m.RepoName != "" && m.PRNumber > 0:
			key = prref.Format("octocat", m.RepoName, m.PRNumber)
			repo = m.RepoName
			num = m.PRNumber
		default:
			return nil, domain.ErrInvalid
		}
		if seen[key] {
			return nil, domain.ErrInvalid
		}
		if other, ok := f.byPR[key]; ok && other != ignoreGroupID {
			return nil, &domain.DeliveryGroupConflictError{
				Code: domain.DeliveryGroupConflictPRInOtherGroup, PRKey: key, GroupID: other,
			}
		}
		seen[key] = true
		src := m.Source
		if src == "" {
			src = domain.DeliveryGroupMemberManual
		}
		out = append(out, domain.DeliveryGroupMember{
			PRKey: key, RepoName: repo, PRNumber: num, Source: src, AddedAt: now,
		})
	}
	return out, nil
}

func (f *fakeDeliveryGroups) checkRevisionLocked(g *domain.DeliveryGroup, ifMatch string) error {
	want := strings.Trim(strings.TrimSpace(ifMatch), `"`)
	got := strconv.FormatInt(g.Revision, 10)
	if want != got {
		expected, _ := strconv.ParseInt(want, 10, 64)
		return &domain.DeliveryGroupPreconditionError{
			Code:             domain.DeliveryGroupPreconditionFailedCode,
			ExpectedRevision: expected,
			StoredRevision:   g.Revision,
		}
	}
	return nil
}

func cloneGroup(g *domain.DeliveryGroup) *domain.DeliveryGroup {
	cp := *g
	cp.Members = append([]domain.DeliveryGroupMember(nil), g.Members...)
	return &cp
}

func memberKeys(members []domain.DeliveryGroupMember) string {
	parts := make([]string, 0, len(members))
	for _, m := range members {
		parts = append(parts, m.PRKey)
	}
	return strings.Join(parts, ",")
}

func etag(rev int64) string { return fmt.Sprintf(`"%d"`, rev) }

func TestDeliveryGroupHTTPCrossRepoCreateAndStandaloneFilter(t *testing.T) {
	h := newPRReviewHarness(t, true)
	fake := newFakeDeliveryGroups()
	h.module.SetDeliveryGroups(fake)
	h.addRepo(t, "world", "https://github.com/octocat/world.git")
	h.github.setListPayload("octocat", "hello", []map[string]any{
		{"number": 7, "state": "open", "title": "A", "htmlUrl": "https://github.com/octocat/hello/pull/7",
			"head": map[string]any{"sha": "h1", "ref": "a"}, "base": map[string]any{"sha": "b1", "ref": "main"}},
		{"number": 8, "state": "open", "title": "Standalone", "htmlUrl": "https://github.com/octocat/hello/pull/8",
			"head": map[string]any{"sha": "h2", "ref": "b"}, "base": map[string]any{"sha": "b1", "ref": "main"}},
	})
	h.github.setListPayload("octocat", "world", []map[string]any{
		{"number": 3, "state": "open", "title": "B", "htmlUrl": "https://github.com/octocat/world/pull/3",
			"head": map[string]any{"sha": "h3", "ref": "c"}, "base": map[string]any{"sha": "b2", "ref": "main"}},
	})

	body := map[string]any{
		"id":    "dg_01JABCDEFGHJKMNPQRSTVWXYZ0",
		"title": "cross-repo",
		"members": []map[string]any{
			{"repo_name": "hello", "pr_number": 7},
			{"repo_name": "world", "pr_number": 3},
		},
	}
	status, raw := h.postJSON(t, "/api/workspaces/WS/delivery-groups", body, map[string]string{
		"X-Idempotency-Key": "intent-create-1",
	})
	if status != http.StatusCreated {
		t.Fatalf("create status=%d body=%s", status, raw)
	}

	status, raw = h.get(t, "/api/workspaces/WS/pull-requests?state=open")
	if status != http.StatusOK {
		t.Fatalf("list status=%d body=%s", status, raw)
	}
	data := decodePullRequestsResponse(t, raw)
	if len(data.DeliveryGroups) != 1 || len(data.DeliveryGroups[0].Members) != 2 {
		t.Fatalf("groups=%+v", data.DeliveryGroups)
	}
	if data.DeliveryGroups[0].Members[0].PRKey != "github:octocat/hello#7" ||
		data.DeliveryGroups[0].Members[1].PRKey != "github:octocat/world#3" {
		t.Fatalf("order=%+v", data.DeliveryGroups[0].Members)
	}
	if len(data.PullRequests) != 1 || data.PullRequests[0].Number != 8 {
		t.Fatalf("standalone=%+v, want only #8", data.PullRequests)
	}
}

func TestDeliveryGroupHTTPConflictsReplayAndStale(t *testing.T) {
	h := newPRReviewHarness(t, true)
	fake := newFakeDeliveryGroups()
	h.module.SetDeliveryGroups(fake)
	fake.put(&domain.DeliveryGroup{
		WorkspaceKey: "WS", ID: "dg_01JABCDEFGHJKMNPQRSTVWXYZ0", Title: "g",
		State: domain.DeliveryGroupActive, Revision: 2,
		Members: []domain.DeliveryGroupMember{{
			PRKey: "github:octocat/hello#7", RepoName: "hello", PRNumber: 7,
			Source: domain.DeliveryGroupMemberManual, AddedAt: time.Now().UTC(),
		}},
		LastOpID: "old", CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	})

	// Duplicate membership into a new group.
	status, raw := h.postJSON(t, "/api/workspaces/WS/delivery-groups", map[string]any{
		"id": "dg_01JABCDEFGHJKMNPQRSTVWXYZ1", "title": "other",
		"members": []map[string]any{{"pr_key": "github:octocat/hello#7", "repo_name": "hello", "pr_number": 7}},
	}, map[string]string{"X-Idempotency-Key": "dup-1"})
	if status != http.StatusConflict || !strings.Contains(string(raw), "pr_in_other_group") {
		t.Fatalf("dup status=%d body=%s", status, raw)
	}
	assertErrorDetails(t, raw, map[string]any{
		"pr_key":   "github:octocat/hello#7",
		"group_id": "dg_01JABCDEFGHJKMNPQRSTVWXYZ0",
	})

	// Stale revision on update.
	status, raw = h.patchJSON(t, "/api/workspaces/WS/delivery-groups/dg_01JABCDEFGHJKMNPQRSTVWXYZ0",
		map[string]any{"title": "x"},
		map[string]string{"X-Idempotency-Key": "stale-1", "If-Match": `"1"`})
	if status != http.StatusPreconditionFailed {
		t.Fatalf("stale status=%d body=%s", status, raw)
	}
	assertErrorDetails(t, raw, map[string]any{
		"expected_revision": float64(1),
		"stored_revision":   float64(2),
	})

	// Idempotent replay of set-members.
	members := map[string]any{"members": []map[string]any{
		{"repo_name": "hello", "pr_number": 7},
		{"repo_name": "hello", "pr_number": 9},
	}}
	status, raw = h.putJSON(t, "/api/workspaces/WS/delivery-groups/dg_01JABCDEFGHJKMNPQRSTVWXYZ0/members",
		members, map[string]string{"X-Idempotency-Key": "set-1", "If-Match": `"2"`})
	if status != http.StatusOK {
		t.Fatalf("set status=%d body=%s", status, raw)
	}
	status, raw = h.putJSON(t, "/api/workspaces/WS/delivery-groups/dg_01JABCDEFGHJKMNPQRSTVWXYZ0/members",
		members, map[string]string{"X-Idempotency-Key": "set-1", "If-Match": `"2"`})
	if status != http.StatusOK || !strings.Contains(string(raw), `"replayed":true`) {
		t.Fatalf("replay status=%d body=%s", status, raw)
	}

	// Changed body with same key.
	status, raw = h.putJSON(t, "/api/workspaces/WS/delivery-groups/dg_01JABCDEFGHJKMNPQRSTVWXYZ0/members",
		map[string]any{"members": []map[string]any{{"repo_name": "hello", "pr_number": 7}}},
		map[string]string{"X-Idempotency-Key": "set-1", "If-Match": `"3"`})
	if status != http.StatusConflict || !strings.Contains(string(raw), "idempotency_key_reused") {
		t.Fatalf("reuse status=%d body=%s", status, raw)
	}
}

func assertErrorDetails(t *testing.T, raw []byte, want map[string]any) {
	t.Helper()
	var envelope map[string]any
	if err := json.Unmarshal(raw, &envelope); err != nil {
		t.Fatalf("decode error envelope: %v (body %s)", err, raw)
	}
	if _, hasMeta := envelope["meta"]; hasMeta {
		t.Fatalf("error envelope must not emit meta; body=%s", raw)
	}
	details, ok := envelope["details"].(map[string]any)
	if !ok {
		t.Fatalf("details missing or not object: %s", raw)
	}
	for k, wantVal := range want {
		got, exists := details[k]
		if !exists {
			t.Fatalf("details[%q] missing in %s", k, raw)
		}
		if got != wantVal {
			t.Fatalf("details[%q]=%v (%T), want %v (%T)", k, got, got, wantVal, wantVal)
		}
	}
}

func TestGhListFallbackPreservesDeliveryGroupsAndFiltersMembership(t *testing.T) {
	fallback := &fallbackAgentService{
		result: &ops.GitPullRequestList{
			PullRequests: []ops.GitPullRequest{
				{
					Number: 7, PRKey: "github:octocat/hello#7", Title: "Grouped",
					State: "OPEN", RepoName: "octocat/hello",
					URL: "https://github.com/octocat/hello/pull/7",
				},
				{
					Number: 8, PRKey: "github:octocat/hello#8", Title: "Standalone",
					State: "OPEN", RepoName: "octocat/hello",
					URL: "https://github.com/octocat/hello/pull/8",
				},
			},
		},
	}
	h := newPRReviewHarnessWithAgent(t, false, fallback)
	fake := newFakeDeliveryGroups()
	h.module.SetDeliveryGroups(fake)
	fake.put(&domain.DeliveryGroup{
		WorkspaceKey: "WS", ID: "dg_01JABCDEFGHJKMNPQRSTVWXYZ0", Title: "g",
		State: domain.DeliveryGroupActive, Revision: 1,
		Members: []domain.DeliveryGroupMember{{
			PRKey: "github:octocat/hello#7", RepoName: "hello", PRNumber: 7,
			Source: domain.DeliveryGroupMemberManual, AddedAt: time.Now().UTC(),
		}},
		LastOpID: "old", CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	})

	status, raw := h.get(t, "/api/workspaces/WS/pull-requests?state=open")
	if status != http.StatusOK {
		t.Fatalf("status=%d body=%s", status, raw)
	}
	if !fallback.called {
		t.Fatal("expected gh fallback")
	}
	data := decodePullRequestsResponse(t, raw)
	if len(data.DeliveryGroups) != 1 || data.DeliveryGroups[0].ID != "dg_01JABCDEFGHJKMNPQRSTVWXYZ0" {
		t.Fatalf("delivery_groups=%+v, want preserved group", data.DeliveryGroups)
	}
	if len(data.PullRequests) != 1 || data.PullRequests[0].Number != 8 {
		t.Fatalf("standalone=%+v, want only #8 (grouped #7 filtered)", data.PullRequests)
	}
}

func TestGhListFallbackConnectorUnavailablePreservesGroups(t *testing.T) {
	fallback := &fallbackAgentService{
		result: &ops.GitPullRequestList{
			PullRequests: []ops.GitPullRequest{{
				Number: 8, PRKey: "github:octocat/hello#8", State: "OPEN",
				RepoName: "octocat/hello", URL: "https://github.com/octocat/hello/pull/8",
			}},
		},
	}
	h := newPRReviewHarnessWithCredential(t, true, fallback, testCredentialNone, "")
	fake := newFakeDeliveryGroups()
	h.module.SetDeliveryGroups(fake)
	fake.put(&domain.DeliveryGroup{
		WorkspaceKey: "WS", ID: "dg_01JABCDEFGHJKMNPQRSTVWXYZ0", Title: "g",
		State: domain.DeliveryGroupActive, Revision: 1,
		Members: []domain.DeliveryGroupMember{{
			PRKey: "github:octocat/hello#7", RepoName: "hello", PRNumber: 7,
			Source: domain.DeliveryGroupMemberManual, AddedAt: time.Now().UTC(),
		}},
		LastOpID: "old", CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	})

	status, raw := h.get(t, "/api/workspaces/WS/pull-requests?state=open")
	if status != http.StatusOK {
		t.Fatalf("status=%d body=%s", status, raw)
	}
	data := decodePullRequestsResponse(t, raw)
	if len(data.DeliveryGroups) != 1 {
		t.Fatalf("groups=%+v warnings=%v", data.DeliveryGroups, data.Warnings)
	}
	if !slicesContains(data.Warnings, connectorUnavailableWarning) {
		t.Fatalf("warnings=%v, want connector unavailable", data.Warnings)
	}
}

func TestGhListFallbackMergedPreservesGroups(t *testing.T) {
	fallback := &fallbackAgentService{
		result: &ops.GitPullRequestList{
			PullRequests: []ops.GitPullRequest{{
				Number: 9, PRKey: "github:octocat/hello#9", Title: "Merged PR",
				State: "MERGED", RepoName: "octocat/hello",
			}},
		},
	}
	h := newPRReviewHarnessWithAgent(t, true, fallback)
	fake := newFakeDeliveryGroups()
	h.module.SetDeliveryGroups(fake)
	fake.put(&domain.DeliveryGroup{
		WorkspaceKey: "WS", ID: "dg_01JABCDEFGHJKMNPQRSTVWXYZ0", Title: "g",
		State: domain.DeliveryGroupActive, Revision: 1,
		Members: []domain.DeliveryGroupMember{{
			PRKey: "github:octocat/hello#7", RepoName: "hello", PRNumber: 7,
			Source: domain.DeliveryGroupMemberManual, AddedAt: time.Now().UTC(),
		}},
		LastOpID: "old", CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	})

	status, raw := h.get(t, "/api/workspaces/WS/pull-requests?state=merged")
	if status != http.StatusOK {
		t.Fatalf("status=%d body=%s", status, raw)
	}
	data := decodePullRequestsResponse(t, raw)
	if len(data.DeliveryGroups) != 1 {
		t.Fatalf("groups=%+v", data.DeliveryGroups)
	}
	if !slicesContains(data.Warnings, mergedStateGhFallbackWarning) {
		t.Fatalf("warnings=%v, want merged fallback notice", data.Warnings)
	}
	if !fallback.called || fallback.state != "merged" {
		t.Fatalf("fallback called=%v state=%q", fallback.called, fallback.state)
	}
}

func TestGhListFallbackLocalFailureKeepsGroupsNot502(t *testing.T) {
	fallback := &fallbackAgentService{err: fmt.Errorf("gh: authentication failed")}
	h := newPRReviewHarnessWithAgent(t, false, fallback)
	fake := newFakeDeliveryGroups()
	h.module.SetDeliveryGroups(fake)
	fake.put(&domain.DeliveryGroup{
		WorkspaceKey: "WS", ID: "dg_01JABCDEFGHJKMNPQRSTVWXYZ0", Title: "g",
		State: domain.DeliveryGroupActive, Revision: 1,
		Members: []domain.DeliveryGroupMember{{
			PRKey: "github:octocat/hello#7", RepoName: "hello", PRNumber: 7,
			Source: domain.DeliveryGroupMemberManual, AddedAt: time.Now().UTC(),
		}},
		LastOpID: "old", CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	})

	status, raw := h.get(t, "/api/workspaces/WS/pull-requests?state=open")
	if status != http.StatusOK {
		t.Fatalf("status=%d body=%s (want 200 with groups, not 502)", status, raw)
	}
	data := decodePullRequestsResponse(t, raw)
	if len(data.DeliveryGroups) != 1 {
		t.Fatalf("groups=%+v", data.DeliveryGroups)
	}
	if len(data.PullRequests) != 0 {
		t.Fatalf("pull_requests=%+v, want empty when gh failed", data.PullRequests)
	}
	found := false
	for _, w := range data.Warnings {
		if strings.Contains(w, localGhUnavailableWarning) {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("warnings=%v, want local gh unavailable", data.Warnings)
	}
	if data.StandaloneContinuation == nil || data.StandaloneContinuation.Complete {
		t.Fatalf("continuation=%+v, want incomplete when gh failed", data.StandaloneContinuation)
	}
}

func TestGhListFallbackLocalFailureWithoutGroupsStill502(t *testing.T) {
	fallback := &fallbackAgentService{err: fmt.Errorf("gh: authentication failed")}
	h := newPRReviewHarnessWithAgent(t, false, fallback)
	status, raw := h.get(t, "/api/workspaces/WS/pull-requests?state=open")
	if status != http.StatusBadGateway {
		t.Fatalf("status=%d body=%s, want 502 without delivery groups", status, raw)
	}
	if code := decodeErrorCode(t, raw); code != "upstream_error" {
		t.Fatalf("code=%q, want upstream_error", code)
	}
}

func slicesContains(ss []string, want string) bool {
	for _, s := range ss {
		if s == want {
			return true
		}
	}
	return false
}

func TestDeliveryGroupHTTPNoGitHubWriteOnEdits(t *testing.T) {
	h := newPRReviewHarness(t, true)
	fake := newFakeDeliveryGroups()
	h.module.SetDeliveryGroups(fake)
	fake.put(&domain.DeliveryGroup{
		WorkspaceKey: "WS", ID: "dg_01JABCDEFGHJKMNPQRSTVWXYZ0", Title: "g",
		State: domain.DeliveryGroupActive, Revision: 1, Members: nil,
		LastOpID: "old", CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	})
	before := len(h.github.snapshot())
	status, _ := h.patchJSON(t, "/api/workspaces/WS/delivery-groups/dg_01JABCDEFGHJKMNPQRSTVWXYZ0",
		map[string]any{"title": "renamed"},
		map[string]string{"X-Idempotency-Key": "u1", "If-Match": `"1"`})
	if status != http.StatusOK {
		t.Fatalf("status=%d", status)
	}
	status, _ = h.postJSON(t, "/api/workspaces/WS/delivery-groups/dg_01JABCDEFGHJKMNPQRSTVWXYZ0/archive", nil,
		map[string]string{"X-Idempotency-Key": "a1", "If-Match": `"2"`})
	if status != http.StatusOK {
		t.Fatalf("archive status=%d", status)
	}
	if got := len(h.github.snapshot()); got != before {
		t.Fatalf("github calls grew from %d to %d on group edits", before, got)
	}
}

func TestDeliveryGroupHTTPRemovedRepoRetainsMember(t *testing.T) {
	h := newPRReviewHarness(t, true)
	fake := newFakeDeliveryGroups()
	h.module.SetDeliveryGroups(fake)
	fake.put(&domain.DeliveryGroup{
		WorkspaceKey: "WS", ID: "dg_01JABCDEFGHJKMNPQRSTVWXYZ0", Title: "g",
		State: domain.DeliveryGroupActive, Revision: 1,
		Members: []domain.DeliveryGroupMember{{
			PRKey: "github:someone/else#1", RepoName: "else", PRNumber: 1,
			Source: domain.DeliveryGroupMemberManual, AddedAt: time.Now().UTC(),
		}},
		LastOpID: "old", CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	})
	status, raw := h.get(t, "/api/workspaces/WS/delivery-groups/dg_01JABCDEFGHJKMNPQRSTVWXYZ0")
	if status != http.StatusOK {
		t.Fatalf("status=%d body=%s", status, raw)
	}
	if !strings.Contains(string(raw), "no longer registered") {
		t.Fatalf("expected removed-repo warning in %s", raw)
	}
	if !strings.Contains(string(raw), "github:someone/else#1") {
		t.Fatalf("member dropped: %s", raw)
	}
}

func TestDeliveryGroupHTTPPartialGitHubOutageStillListsGroups(t *testing.T) {
	h := newPRReviewHarness(t, true)
	fake := newFakeDeliveryGroups()
	h.module.SetDeliveryGroups(fake)
	fake.put(&domain.DeliveryGroup{
		WorkspaceKey: "WS", ID: "dg_01JABCDEFGHJKMNPQRSTVWXYZ0", Title: "g",
		State: domain.DeliveryGroupActive, Revision: 1,
		Members: []domain.DeliveryGroupMember{{
			PRKey: "github:octocat/hello#7", RepoName: "hello", PRNumber: 7,
			Source: domain.DeliveryGroupMemberManual, AddedAt: time.Now().UTC(),
		}},
		LastOpID: "old", CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	})
	h.github.setListStatus("octocat", "hello", http.StatusInternalServerError)
	status, raw := h.get(t, "/api/workspaces/WS/pull-requests?state=open")
	if status != http.StatusOK {
		t.Fatalf("status=%d body=%s", status, raw)
	}
	data := decodePullRequestsResponse(t, raw)
	if len(data.DeliveryGroups) != 1 {
		t.Fatalf("expected group visible during github outage, got %+v warnings=%v", data.DeliveryGroups, data.Warnings)
	}
}

func TestDeliveryGroupHTTPStandaloneContinuation(t *testing.T) {
	h := newPRReviewHarness(t, true)
	for page := 1; page <= maxPullsListPages; page++ {
		h.github.setListPage("octocat", "hello", page, fakePullRequestPage((page-1)*pullsListPerPage+1, pullsListPerPage))
	}
	status, raw := h.get(t, "/api/workspaces/WS/pull-requests?state=all")
	if status != http.StatusOK {
		t.Fatalf("status=%d", status)
	}
	data := decodePullRequestsResponse(t, raw)
	if data.StandaloneContinuation == nil || !data.StandaloneContinuation.HasMore || data.StandaloneContinuation.Complete {
		t.Fatalf("continuation=%+v", data.StandaloneContinuation)
	}
	if data.StandaloneContinuation.Repos[0].NextPage != maxPullsListPages+1 {
		t.Fatalf("next_page=%d", data.StandaloneContinuation.Repos[0].NextPage)
	}
	h.github.setListPage("octocat", "hello", maxPullsListPages+1, fakePullRequestPage(501, 2))
	status, raw = h.get(t, "/api/workspaces/WS/pull-requests?state=all&standalone_repo=octocat/hello&standalone_page=6")
	if status != http.StatusOK {
		t.Fatalf("continue status=%d body=%s", status, raw)
	}
	data = decodePullRequestsResponse(t, raw)
	if len(data.PullRequests) != 2 || data.PullRequests[0].Number != 501 {
		t.Fatalf("continued=%+v", data.PullRequests)
	}
}

func TestConnectorListMembershipTruncatedMarksContinuationIncomplete(t *testing.T) {
	h := newPRReviewHarness(t, true)
	fake := newFakeDeliveryGroups()
	fake.forceHasMoreNoCursor = true
	h.module.SetDeliveryGroups(fake)
	fake.put(&domain.DeliveryGroup{
		WorkspaceKey: "WS", ID: "dg_01JABCDEFGHJKMNPQRSTVWXYZ0", Title: "g",
		State: domain.DeliveryGroupActive, Revision: 1,
		Members: []domain.DeliveryGroupMember{{
			PRKey: "github:octocat/hello#7", RepoName: "hello", PRNumber: 7,
			Source: domain.DeliveryGroupMemberManual, AddedAt: time.Now().UTC(),
		}},
		LastOpID: "old", CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	})
	h.github.setListPayload("octocat", "hello", []map[string]any{
		{"number": 7, "state": "open", "title": "Grouped", "htmlUrl": "https://github.com/octocat/hello/pull/7",
			"head": map[string]any{"sha": "h1", "ref": "a"}, "base": map[string]any{"sha": "b1", "ref": "main"}},
		{"number": 8, "state": "open", "title": "Maybe standalone", "htmlUrl": "https://github.com/octocat/hello/pull/8",
			"head": map[string]any{"sha": "h2", "ref": "b"}, "base": map[string]any{"sha": "b1", "ref": "main"}},
	})

	status, raw := h.get(t, "/api/workspaces/WS/pull-requests?state=open")
	if status != http.StatusOK {
		t.Fatalf("status=%d body=%s", status, raw)
	}
	data := decodePullRequestsResponse(t, raw)
	if data.StandaloneContinuation == nil || data.StandaloneContinuation.Complete {
		t.Fatalf("standalone_continuation=%+v, want complete=false when membership index is truncated", data.StandaloneContinuation)
	}
	foundWarn := false
	for _, w := range data.Warnings {
		if strings.Contains(w, "has_more without next_cursor") || strings.Contains(w, "membership index truncated") {
			foundWarn = true
			break
		}
	}
	if !foundWarn {
		t.Fatalf("warnings=%v, want truncated/incomplete membership notice", data.Warnings)
	}
	if len(data.PullRequests) != 1 || data.PullRequests[0].Number != 8 {
		t.Fatalf("standalone=%+v, want filtered #8 with partial membership preserved", data.PullRequests)
	}
	if len(data.DeliveryGroups) != 1 {
		t.Fatalf("delivery_groups=%+v, want durable group preserved", data.DeliveryGroups)
	}
}

func TestConnectorListMembershipUnavailableMarksContinuationIncomplete(t *testing.T) {
	h := newPRReviewHarness(t, true)
	fake := newFakeDeliveryGroups()
	fake.listErr = fmt.Errorf("fleet-db membership query failed")
	h.module.SetDeliveryGroups(fake)
	h.github.setListPayload("octocat", "hello", []map[string]any{
		{"number": 7, "state": "open", "title": "Unknown membership", "htmlUrl": "https://github.com/octocat/hello/pull/7",
			"head": map[string]any{"sha": "h1", "ref": "a"}, "base": map[string]any{"sha": "b1", "ref": "main"}},
	})

	status, raw := h.get(t, "/api/workspaces/WS/pull-requests?state=open")
	if status != http.StatusOK {
		t.Fatalf("status=%d body=%s", status, raw)
	}
	data := decodePullRequestsResponse(t, raw)
	if data.StandaloneContinuation == nil || data.StandaloneContinuation.Complete {
		t.Fatalf("standalone_continuation=%+v, want complete=false when membership is unverified", data.StandaloneContinuation)
	}
	foundWarn := false
	for _, w := range data.Warnings {
		if strings.Contains(w, "delivery groups unavailable") {
			foundWarn = true
			break
		}
	}
	if !foundWarn {
		t.Fatalf("warnings=%v, want delivery groups unavailable", data.Warnings)
	}
	if len(data.PullRequests) != 1 || data.PullRequests[0].Number != 7 {
		t.Fatalf("pull_requests=%+v, want unknown-membership rows still returned", data.PullRequests)
	}
}

func TestConnectorListInconsistentMembershipMarksContinuationIncomplete(t *testing.T) {
	h := newPRReviewHarness(t, true)
	fake := newFakeDeliveryGroups()
	h.module.SetDeliveryGroups(fake)
	fake.put(&domain.DeliveryGroup{
		WorkspaceKey: "WS", ID: "dg_01JABCDEFGHJKMNPQRSTVWXYZ0", Title: "g",
		State: domain.DeliveryGroupActive, Revision: 1, Inconsistent: true,
		Members: []domain.DeliveryGroupMember{{
			PRKey: "github:octocat/hello#7", RepoName: "hello", PRNumber: 7,
			Source: domain.DeliveryGroupMemberManual, AddedAt: time.Now().UTC(),
		}},
		LastOpID: "old", CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	})
	fake.put(&domain.DeliveryGroup{
		WorkspaceKey: "WS", ID: "dg_01JABCDEFGHJKMNPQRSTVWXYZ1", Title: "ok",
		State: domain.DeliveryGroupActive, Revision: 1,
		Members: []domain.DeliveryGroupMember{{
			PRKey: "github:octocat/hello#9", RepoName: "hello", PRNumber: 9,
			Source: domain.DeliveryGroupMemberManual, AddedAt: time.Now().UTC(),
		}},
		LastOpID: "old", CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	})
	h.github.setListPayload("octocat", "hello", []map[string]any{
		{"number": 7, "state": "open", "title": "Last-committed member", "htmlUrl": "https://github.com/octocat/hello/pull/7",
			"head": map[string]any{"sha": "h1", "ref": "a"}, "base": map[string]any{"sha": "b1", "ref": "main"}},
		{"number": 8, "state": "open", "title": "Maybe standalone", "htmlUrl": "https://github.com/octocat/hello/pull/8",
			"head": map[string]any{"sha": "h2", "ref": "b"}, "base": map[string]any{"sha": "b1", "ref": "main"}},
		{"number": 9, "state": "open", "title": "Consistent member", "htmlUrl": "https://github.com/octocat/hello/pull/9",
			"head": map[string]any{"sha": "h3", "ref": "c"}, "base": map[string]any{"sha": "b1", "ref": "main"}},
	})

	status, raw := h.get(t, "/api/workspaces/WS/pull-requests?state=open")
	if status != http.StatusOK {
		t.Fatalf("status=%d body=%s", status, raw)
	}
	data := decodePullRequestsResponse(t, raw)
	if data.StandaloneContinuation == nil || data.StandaloneContinuation.Complete {
		t.Fatalf("standalone_continuation=%+v, want complete=false when an inconsistent active group exists", data.StandaloneContinuation)
	}
	foundWarn := false
	for _, w := range data.Warnings {
		if strings.Contains(w, "inconsistent") {
			foundWarn = true
			break
		}
	}
	if !foundWarn {
		t.Fatalf("warnings=%v, want inconsistent membership notice", data.Warnings)
	}
	if len(data.PullRequests) != 1 || data.PullRequests[0].Number != 8 {
		t.Fatalf("standalone=%+v, want #8 only (best-effort filter of last-committed + consistent members)", data.PullRequests)
	}
	if len(data.DeliveryGroups) != 2 {
		t.Fatalf("delivery_groups=%+v, want durable inconsistent + consistent rows preserved", data.DeliveryGroups)
	}
	var sawInconsistent bool
	for _, g := range data.DeliveryGroups {
		if g.Inconsistent {
			sawInconsistent = true
			break
		}
	}
	if !sawInconsistent {
		t.Fatalf("delivery_groups=%+v, want inconsistent flag preserved on durable row", data.DeliveryGroups)
	}
}

func TestConnectorListInconsistentIdentityOnlyMarksContinuationIncomplete(t *testing.T) {
	h := newPRReviewHarness(t, true)
	fake := newFakeDeliveryGroups()
	h.module.SetDeliveryGroups(fake)
	fake.put(&domain.DeliveryGroup{
		WorkspaceKey: "WS", ID: "dg_01JABCDEFGHJKMNPQRSTVWXYZ0", Title: "g",
		State: domain.DeliveryGroupActive, Revision: 1, Inconsistent: true,
		Members:  nil, // identity-only: last committed document unreadable
		LastOpID: "old", CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	})
	h.github.setListPayload("octocat", "hello", []map[string]any{
		{"number": 7, "state": "open", "title": "Uncertain membership", "htmlUrl": "https://github.com/octocat/hello/pull/7",
			"head": map[string]any{"sha": "h1", "ref": "a"}, "base": map[string]any{"sha": "b1", "ref": "main"}},
	})

	status, raw := h.get(t, "/api/workspaces/WS/pull-requests?state=open")
	if status != http.StatusOK {
		t.Fatalf("status=%d body=%s", status, raw)
	}
	data := decodePullRequestsResponse(t, raw)
	if data.StandaloneContinuation == nil || data.StandaloneContinuation.Complete {
		t.Fatalf("standalone_continuation=%+v, want complete=false for identity-only inconsistent row", data.StandaloneContinuation)
	}
	foundWarn := false
	for _, w := range data.Warnings {
		if strings.Contains(w, "inconsistent") {
			foundWarn = true
			break
		}
	}
	if !foundWarn {
		t.Fatalf("warnings=%v, want inconsistent membership notice", data.Warnings)
	}
	// No members to filter: row may still appear, but never as confirmed standalone.
	if len(data.PullRequests) != 1 || data.PullRequests[0].Number != 7 {
		t.Fatalf("pull_requests=%+v, want discovered PR retained without confirmed-standalone claim", data.PullRequests)
	}
	if len(data.DeliveryGroups) != 1 || !data.DeliveryGroups[0].Inconsistent {
		t.Fatalf("delivery_groups=%+v, want identity-only inconsistent durable row", data.DeliveryGroups)
	}
}

func TestDeliveryGroupPreviewReadOnly(t *testing.T) {
	h, f, _ := newReadinessHarness(t)
	fake := newFakeDeliveryGroups()
	h.module.SetDeliveryGroups(fake)
	fake.put(&domain.DeliveryGroup{
		WorkspaceKey: "WS", ID: "dg_01JABCDEFGHJKMNPQRSTVWXYZ0", Title: "g",
		State: domain.DeliveryGroupActive, Revision: 1,
		Members: []domain.DeliveryGroupMember{
			{PRKey: keyHello7, RepoName: "hello", PRNumber: 7, Source: domain.DeliveryGroupMemberManual, AddedAt: time.Now().UTC()},
		},
		LastOpID: "old", CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	})
	f.setNode("octocat/hello", map[string]any{
		"number": 7, "isDraft": false, "mergeable": "MERGEABLE", "mergeStateStatus": "CLEAN",
		"reviewDecision": "APPROVED", "state": "OPEN",
		"headRefOid": "head", "baseRefOid": "base", "baseRefName": "main", "headRefName": "feat",
		"statusCheckRollup": map[string]any{"state": "SUCCESS", "contexts": map[string]any{"nodes": []any{}}},
	})
	before := f.queryCount("octocat/hello")
	status, raw := h.get(t, "/api/workspaces/WS/delivery-groups/dg_01JABCDEFGHJKMNPQRSTVWXYZ0/preview")
	if status != http.StatusOK {
		t.Fatalf("status=%d body=%s", status, raw)
	}
	if !strings.Contains(string(raw), `"preview"`) {
		t.Fatalf("body=%s", raw)
	}
	if f.queryCount("octocat/hello") <= before {
		t.Fatalf("expected readiness re-read for preview")
	}
	// No GitHub write verbs.
	for _, c := range h.github.snapshot() {
		if c.method != http.MethodGet && c.method != http.MethodPost {
			t.Fatalf("unexpected method %s", c.method)
		}
		if c.method == http.MethodPost && c.path != "/graphql" {
			t.Fatalf("unexpected write path %s", c.path)
		}
	}
}

func TestDeliveryGroupUnregisteredMemberRejected(t *testing.T) {
	h := newPRReviewHarness(t, true)
	h.module.SetDeliveryGroups(newFakeDeliveryGroups())
	status, raw := h.postJSON(t, "/api/workspaces/WS/delivery-groups", map[string]any{
		"id": "dg_01JABCDEFGHJKMNPQRSTVWXYZ0", "title": "g",
		"members": []map[string]any{{"pr_key": "github:someone/else#1"}},
	}, map[string]string{"X-Idempotency-Key": "bad"})
	if status != http.StatusUnprocessableEntity {
		t.Fatalf("status=%d body=%s", status, raw)
	}
}

// helpers ---------------------------------------------------------------

func (h *prReviewHarness) postJSON(t *testing.T, path string, body any, headers map[string]string) (int, []byte) {
	t.Helper()
	return h.doJSON(t, http.MethodPost, path, body, headers)
}
func (h *prReviewHarness) patchJSON(t *testing.T, path string, body any, headers map[string]string) (int, []byte) {
	t.Helper()
	return h.doJSON(t, http.MethodPatch, path, body, headers)
}
func (h *prReviewHarness) putJSON(t *testing.T, path string, body any, headers map[string]string) (int, []byte) {
	t.Helper()
	return h.doJSON(t, http.MethodPut, path, body, headers)
}

func (h *prReviewHarness) doJSON(t *testing.T, method, path string, body any, headers map[string]string) (int, []byte) {
	t.Helper()
	var payload []byte
	if body != nil {
		var err error
		payload, err = json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
	}
	req := httptest.NewRequest(method, path, bytes.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	req = req.WithContext(middleware.WithUserIdentity(req.Context(), middleware.UserIdentity{UserID: "user-1"}))
	rr := httptest.NewRecorder()
	h.mux.ServeHTTP(rr, req)
	return rr.Code, rr.Body.Bytes()
}
