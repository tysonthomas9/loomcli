package capabilitycomposition

import (
	"context"
	"errors"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/backend"
	"github.com/tysonthomas9/loomcli/internal/modules/workitems"
)

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
