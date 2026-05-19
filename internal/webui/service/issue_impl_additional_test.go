package service

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/backend"
	"github.com/tysonthomas9/loomcli/internal/rpc"
	webuidaemon "github.com/tysonthomas9/loomcli/internal/webui/daemon"
)

func TestIssueServiceBackendResolutionAndPoolBranches(t *testing.T) {
	ctx := context.Background()

	noProvider := NewIssueServiceWithBackend(nil, nil, nil, nil).(*issueServiceImpl)
	if _, err := noProvider.resolveBackend(ctx); !serviceErrorKind(err, KindUnavailable) {
		t.Fatalf("resolveBackend nil provider err = %v", err)
	}

	nilBackend := NewIssueServiceWithBackend(nil, nil, nil, func(context.Context) backend.IssueBackend { return nil }).(*issueServiceImpl)
	if _, err := nilBackend.resolveBackend(ctx); !serviceErrorKind(err, KindUnavailable) {
		t.Fatalf("resolveBackend nil backend err = %v", err)
	}

	svc := &issueServiceImpl{}
	if _, err := svc.acquireClient(ctx); !serviceErrorKind(err, KindUnavailable) {
		t.Fatalf("acquireClient nil pool err = %v", err)
	}

	for _, tc := range []struct {
		name string
		err  error
		kind ErrorKind
	}{
		{name: "starting", err: webuidaemon.ErrDaemonStarting, kind: KindStarting},
		{name: "deadline", err: context.DeadlineExceeded, kind: KindTimeout},
		{name: "generic", err: errors.New("dial failed"), kind: KindUnavailable},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc := &issueServiceImpl{pool: &fakeIssuePool{getErr: tc.err}}
			if _, err := svc.acquireClient(ctx); !serviceErrorKind(err, tc.kind) {
				t.Fatalf("acquireClient err = %v, want %s", err, tc.kind)
			}
		})
	}

	client := &rpc.Client{}
	pool := &fakeIssuePool{client: client}
	svc = &issueServiceImpl{pool: pool}
	got, err := svc.acquireClient(ctx)
	if err != nil || got != client {
		t.Fatalf("acquireClient success got=%p err=%v, want %p", got, err, client)
	}
	ok := true
	svc.releaseClient(nil, &ok)
	ok = false
	svc.releaseClient(nil, &ok)
	if pool.putCalls != 1 || pool.discardCalls != 1 {
		t.Fatalf("releaseClient put=%d discard=%d, want 1/1", pool.putCalls, pool.discardCalls)
	}
}

func TestTranslateBackendErrorAllBackendKinds(t *testing.T) {
	tests := []struct {
		name string
		err  error
		kind ErrorKind
	}{
		{name: "validation", err: backend.ErrValidation("op", "bad input"), kind: KindValidation},
		{name: "unavailable", err: backend.ErrUnavailable("op", "offline", errors.New("dial")), kind: KindUnavailable},
		{name: "timeout", err: backend.ErrTimeout("op", "slow", context.DeadlineExceeded), kind: KindTimeout},
		{name: "canceled", err: backend.ErrCanceled("op", "canceled", context.Canceled), kind: KindTimeout},
		{name: "not implemented", err: backend.ErrNotImplemented("op", "later"), kind: KindNotImplemented},
		{name: "unknown kind", err: backend.NewBackendError(backend.ErrorKind("custom"), "op", "weird", errors.New("cause")), kind: KindInternal},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := translateBackendError(tc.err)
			if got == nil || got.Kind != tc.kind {
				t.Fatalf("translateBackendError = %+v, want %s", got, tc.kind)
			}
		})
	}
}

func TestIssueServiceAdditionalBackendErrorBranches(t *testing.T) {
	ctx := context.Background()

	t.Run("create nil result", func(t *testing.T) {
		svc := newServiceWithFake(&fakeIssueBackend{})
		_, err := svc.CreateIssue(ctx, CreateIssueParams{Title: "T", IssueType: "task", Priority: 1})
		if !serviceErrorKind(err, KindInternal) {
			t.Fatalf("CreateIssue nil result err = %v", err)
		}
	})

	t.Run("patch label mutation and backend validation", func(t *testing.T) {
		fb := &fakeIssueBackend{updateErr: backend.ErrValidation("Update", "bad label")}
		svc := newServiceWithFake(fb)
		err := svc.PatchIssue(ctx, PatchIssueParams{IssueID: "i-1", AddLabels: []string{"alpha"}})
		if !serviceErrorKind(err, KindValidation) {
			t.Fatalf("PatchIssue err = %v, want validation", err)
		}
		if len(fb.updateCalls) != 1 || len(fb.updateCalls[0].params.AddLabels) != 1 {
			t.Fatalf("update calls = %+v, want label mutation", fb.updateCalls)
		}
	})

	t.Run("close nil result", func(t *testing.T) {
		svc := newServiceWithFake(&fakeIssueBackend{})
		_, err := svc.CloseIssue(ctx, CloseIssueParams{IssueID: "i-1"})
		if !serviceErrorKind(err, KindInternal) {
			t.Fatalf("CloseIssue nil result err = %v", err)
		}
	})

	t.Run("claim nil before claim", func(t *testing.T) {
		svc := newServiceWithFake(&fakeIssueBackend{})
		_, err := svc.ClaimIssue(ctx, ClaimIssueParams{IssueID: "i-1"})
		if !serviceErrorKind(err, KindInternal) {
			t.Fatalf("ClaimIssue nil detail err = %v", err)
		}
	})

	t.Run("claim post update error still returns issue", func(t *testing.T) {
		fb := &fakeIssueBackend{
			getResult: &backend.IssueDetailData{
				IssueData: backend.IssueData{ID: "i-1", Title: "T", Status: "in_progress", Priority: 1, CreatedAt: time.Now(), UpdatedAt: time.Now()},
			},
			overrideUpdateClaim: true,
			postClaimUpdateErr:  backend.ErrUnavailable("Update", "temporary", nil),
		}
		svc := newServiceWithFake(fb)
		raw, err := svc.ClaimIssue(ctx, ClaimIssueParams{IssueID: "i-1"})
		if err != nil || len(raw) == 0 {
			t.Fatalf("ClaimIssue raw=%s err=%v, want success despite post-update error", raw, err)
		}
	})

	t.Run("delete backend error", func(t *testing.T) {
		svc := newServiceWithFake(&fakeIssueBackend{deleteErr: backend.ErrNotFound("Delete", "missing")})
		_, err := svc.DeleteIssue(ctx, "missing")
		if !serviceErrorKind(err, KindNotFound) {
			t.Fatalf("DeleteIssue err = %v, want not found", err)
		}
	})
}

func TestIssueServiceCommentsSearchReopenAndDependenciesBranches(t *testing.T) {
	ctx := context.Background()
	now := time.Now().UTC()

	t.Run("add comment too long", func(t *testing.T) {
		svc := newServiceWithFake(&fakeIssueBackend{})
		_, err := svc.AddComment(ctx, AddCommentParams{IssueID: "i-1", Text: strings.Repeat("x", maxCommentLength+1)})
		if !serviceErrorKind(err, KindValidation) {
			t.Fatalf("AddComment long err = %v", err)
		}
	})

	t.Run("add comment nil and backend error", func(t *testing.T) {
		svc := newServiceWithFake(&fakeIssueBackend{})
		_, err := svc.AddComment(ctx, AddCommentParams{IssueID: "i-1", Text: "hello"})
		if !serviceErrorKind(err, KindInternal) {
			t.Fatalf("AddComment nil err = %v", err)
		}

		svc = newServiceWithFake(&fakeIssueBackend{addCommentErr: backend.ErrUnavailable("AddComment", "offline", nil)})
		_, err = svc.AddComment(ctx, AddCommentParams{IssueID: "i-1", Text: "hello"})
		if !serviceErrorKind(err, KindUnavailable) {
			t.Fatalf("AddComment backend err = %v", err)
		}
	})

	t.Run("search success validation and error", func(t *testing.T) {
		fb := &fakeIssueBackend{searchResult: []backend.IssueData{{ID: "i-1", Title: "Match", Status: "open", CreatedAt: now, UpdatedAt: now}}}
		svc := newServiceWithFake(fb)
		raw, err := svc.SearchIssues(ctx, SearchIssuesParams{Query: "match", Limit: 5})
		if err != nil || !strings.Contains(string(raw), "Match") {
			t.Fatalf("SearchIssues raw=%s err=%v", raw, err)
		}
		if fb.searchCalls[0].query != "match" || fb.searchCalls[0].limit != 5 {
			t.Fatalf("search calls = %+v", fb.searchCalls)
		}
		if _, err := svc.SearchIssues(ctx, SearchIssuesParams{Query: " "}); !serviceErrorKind(err, KindValidation) {
			t.Fatalf("SearchIssues blank err = %v", err)
		}
		if _, err := svc.SearchIssues(ctx, SearchIssuesParams{Query: "x", Limit: -1}); !serviceErrorKind(err, KindValidation) {
			t.Fatalf("SearchIssues negative limit err = %v", err)
		}

		svc = newServiceWithFake(&fakeIssueBackend{searchErr: backend.ErrTimeout("Search", "slow", nil)})
		if _, err := svc.SearchIssues(ctx, SearchIssuesParams{Query: "x"}); !serviceErrorKind(err, KindTimeout) {
			t.Fatalf("SearchIssues backend err = %v", err)
		}
	})

	t.Run("reopen success validation and error", func(t *testing.T) {
		fb := &fakeIssueBackend{}
		svc := newServiceWithFake(fb)
		if err := svc.ReopenIssue(ctx, ReopenIssueParams{IssueID: "i-1", Reason: "retry"}); err != nil {
			t.Fatalf("ReopenIssue: %v", err)
		}
		if len(fb.reopenCalls) != 1 || fb.reopenCalls[0].params.Reason != "retry" {
			t.Fatalf("reopen calls = %+v", fb.reopenCalls)
		}
		if err := svc.ReopenIssue(ctx, ReopenIssueParams{IssueID: " "}); !serviceErrorKind(err, KindValidation) {
			t.Fatalf("ReopenIssue blank err = %v", err)
		}

		svc = newServiceWithFake(&fakeIssueBackend{reopenErr: backend.ErrConflict("Reopen", "blocked")})
		if err := svc.ReopenIssue(ctx, ReopenIssueParams{IssueID: "i-1"}); !serviceErrorKind(err, KindConflict) {
			t.Fatalf("ReopenIssue backend err = %v", err)
		}
	})

	t.Run("list comments success validation and error", func(t *testing.T) {
		fb := &fakeIssueBackend{listCommentsResult: []backend.CommentData{{ID: 1, IssueID: "i-1", Author: "alice", Text: "note", CreatedAt: now}}}
		svc := newServiceWithFake(fb)
		comments, err := svc.ListComments(ctx, "i-1")
		if err != nil || len(comments) != 1 || comments[0].Text != "note" {
			t.Fatalf("ListComments comments=%+v err=%v", comments, err)
		}
		if _, err := svc.ListComments(ctx, " "); !serviceErrorKind(err, KindValidation) {
			t.Fatalf("ListComments blank err = %v", err)
		}

		svc = newServiceWithFake(&fakeIssueBackend{listCommentsErr: backend.ErrNotFound("ListComments", "missing")})
		if _, err := svc.ListComments(ctx, "missing"); !serviceErrorKind(err, KindNotFound) {
			t.Fatalf("ListComments backend err = %v", err)
		}
	})

	t.Run("list dependencies success validation nil and error", func(t *testing.T) {
		fb := &fakeIssueBackend{getResult: &backend.IssueDetailData{
			IssueData:    backend.IssueData{ID: "i-1", Title: "T", Status: "open", CreatedAt: now, UpdatedAt: now},
			Dependencies: []backend.DependencyData{{IssueID: "i-1", DependsOnID: "i-0", Type: "blocks", Status: "open", CreatedAt: now}},
		}}
		svc := newServiceWithFake(fb)
		raw, err := svc.ListDependencies(ctx, "i-1")
		if err != nil || !strings.Contains(string(raw), "i-0") {
			t.Fatalf("ListDependencies raw=%s err=%v", raw, err)
		}
		if _, err := svc.ListDependencies(ctx, " "); !serviceErrorKind(err, KindValidation) {
			t.Fatalf("ListDependencies blank err = %v", err)
		}

		svc = newServiceWithFake(&fakeIssueBackend{})
		if _, err := svc.ListDependencies(ctx, "missing"); !serviceErrorKind(err, KindNotFound) {
			t.Fatalf("ListDependencies nil detail err = %v", err)
		}

		svc = newServiceWithFake(&fakeIssueBackend{getErr: backend.ErrUnavailable("Get", "offline", nil)})
		if _, err := svc.ListDependencies(ctx, "i-1"); !serviceErrorKind(err, KindUnavailable) {
			t.Fatalf("ListDependencies backend err = %v", err)
		}
	})
}

type fakeIssuePool struct {
	client       *rpc.Client
	getErr       error
	putCalls     int
	discardCalls int
}

func (p *fakeIssuePool) Get(context.Context) (*rpc.Client, error) { return p.client, p.getErr }
func (p *fakeIssuePool) Put(*rpc.Client)                          { p.putCalls++ }
func (p *fakeIssuePool) PutAfterError(*rpc.Client)                {}
func (p *fakeIssuePool) Discard(*rpc.Client)                      { p.discardCalls++ }
func (p *fakeIssuePool) Stats() webuidaemon.PoolStats             { return webuidaemon.PoolStats{} }
func (p *fakeIssuePool) Close() error                             { return nil }
