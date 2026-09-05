package service

import (
	"context"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/backend"
)

func TestHistoryRecoveryRejectsNilBackendResult(t *testing.T) {
	be := &journeyHistoryBackend{fakeIssueBackend: &fakeIssueBackend{}, listHistory: func(backend.EventHistoryParams) (*backend.EventHistoryData, error) { return nil, nil }}
	svc := NewIssueServiceWithBackend(nil, nil, nil, func(context.Context) backend.IssueBackend { return be })
	if result, err := svc.ListEventHistory(context.Background(), EventListParams{IssueID: "issue", Limit: 8}); err == nil || result != nil {
		t.Fatalf("missing backend result acknowledged: %+v err=%v", result, err)
	}
}
