package capabilitycomposition

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/backend"
	"github.com/tysonthomas9/loomcli/internal/modules/workitems"
)

type claimOnlyBackend struct {
	backend.IssueBackend
	claimCalls int
	getCalls   int
}

func (b *claimOnlyBackend) ClaimIssue(_ context.Context, id string, ttl time.Duration) error {
	b.claimCalls++
	if id != "TASK-1" || ttl != 0 {
		return errors.New("unexpected claim input")
	}
	return nil
}

func (b *claimOnlyBackend) Get(_ context.Context, id string) (*backend.IssueDetailData, error) {
	b.getCalls++
	return &backend.IssueDetailData{IssueData: backend.IssueData{ID: id, Status: "in_progress", Labels: []string{}}}, nil
}

func TestNewWorkItemsWithoutProviderRemainsUnavailable(t *testing.T) {
	api, err := NewWorkItems(nil)
	if err != nil {
		t.Fatal(err)
	}
	if api != nil {
		t.Fatal("expected nil capability without a backend provider")
	}
}

func TestTranslateWorkItemsBackendError(t *testing.T) {
	tests := []struct {
		kind backend.ErrorKind
		want error
	}{
		{backend.KindNotFound, workitems.ErrNotFound},
		{backend.KindValidation, workitems.ErrInvalid},
		{backend.KindConflict, workitems.ErrConflict},
		{backend.KindUnavailable, workitems.ErrUnavailable},
		{backend.KindTimeout, workitems.ErrTimeout},
	}
	for _, test := range tests {
		t.Run(string(test.kind), func(t *testing.T) {
			err := translateWorkItemsBackendError(&backend.BackendError{Kind: test.kind, Message: "failed"})
			if !errors.Is(err, test.want) {
				t.Fatalf("expected %v, got %v", test.want, err)
			}
		})
	}
}

func TestWorkItemsBackendStoreRejectsMissingBackend(t *testing.T) {
	store := &workItemsBackendStore{provider: func(context.Context) backend.IssueBackend { return nil }}
	_, err := store.AddComment(context.Background(), workitems.AddCommentCommand{IssueID: "TASK-1", Text: "hello"})
	if !errors.Is(err, workitems.ErrUnavailable) {
		t.Fatalf("expected unavailable, got %v", err)
	}
}

func TestWorkItemsClaimUsesOneAtomicMutationThenRead(t *testing.T) {
	be := &claimOnlyBackend{}
	api, err := NewWorkItems(func(context.Context) backend.IssueBackend { return be })
	if err != nil {
		t.Fatal(err)
	}
	result, err := api.Claim(context.Background(), workitems.ClaimCommand{IssueID: "TASK-1"})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "in_progress" || be.claimCalls != 1 || be.getCalls != 1 {
		t.Fatalf("unexpected claim result=%#v claimCalls=%d getCalls=%d", result, be.claimCalls, be.getCalls)
	}
}
