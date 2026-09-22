package taskdelivery

import (
	"errors"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/domain"
)

func TestAcceptRuntimeMetadata(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		plan     Plan
		metadata map[string]string
		wantKind ReceiptKind
		wantErr  error
	}{
		{
			name: "applied patch satisfies working copy",
			plan: Plan{PlanID: "plan-1", Requirement: domain.TaskDeliveryWorkingCopy},
			metadata: map[string]string{
				"patch_back_status":   "applied",
				"patch_back_base_sha": "base",
				"patch_back_head_sha": "head",
			},
			wantKind: ReceiptWorkingCopy,
		},
		{
			name: "verified PR satisfies pull request",
			plan: Plan{PlanID: "plan-2", Requirement: domain.TaskDeliveryPullRequest},
			metadata: map[string]string{
				"github_pr_url":   "https://github.com/acme/app/pull/7",
				"github_head_sha": "head",
				"github_branch":   "feature/task-7",
			},
			wantKind: ReceiptPullRequest,
		},
		{
			name: "no change satisfies either requirement",
			plan: Plan{PlanID: "plan-3", Requirement: domain.TaskDeliveryPullRequest},
			metadata: map[string]string{
				"files_changed": "0",
				"head_sha":      "head",
			},
			wantKind: ReceiptNoChange,
		},
		{
			name:     "missing PR evidence fails closed",
			plan:     Plan{PlanID: "plan-4", Requirement: domain.TaskDeliveryPullRequest},
			metadata: map[string]string{"files_changed": "1"},
			wantErr:  ErrUnsatisfied,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			receipt, err := AcceptRuntimeMetadata(tt.plan, tt.metadata)
			if tt.wantErr != nil {
				if !errors.Is(err, tt.wantErr) {
					t.Fatalf("AcceptRuntimeMetadata() error = %v, want %v", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("AcceptRuntimeMetadata() error: %v", err)
			}
			if receipt.Kind != tt.wantKind || receipt.PlanID != tt.plan.PlanID {
				t.Fatalf("receipt = %+v, want kind %s plan %s", receipt, tt.wantKind, tt.plan.PlanID)
			}
		})
	}
}

func TestAcceptCommittedCheckout(t *testing.T) {
	t.Parallel()

	plan := Plan{PlanID: "plan-1", Requirement: domain.TaskDeliveryWorkingCopy}
	receipt, err := AcceptCommittedCheckout(plan, "base", "head", true)
	if err != nil {
		t.Fatalf("AcceptCommittedCheckout() error: %v", err)
	}
	if receipt.Kind != ReceiptWorkingCopy || receipt.WorkingCopy == nil || receipt.WorkingCopy.Method != "committed_checkout" {
		t.Fatalf("receipt = %+v, want committed working copy", receipt)
	}

	if _, err := AcceptCommittedCheckout(plan, "base", "head", false); !errors.Is(err, ErrUnsatisfied) {
		t.Fatalf("dirty checkout error = %v, want %v", err, ErrUnsatisfied)
	}
	if _, err := AcceptCommittedCheckout(Plan{Requirement: domain.TaskDeliveryPullRequest}, "base", "head", true); !errors.Is(err, ErrUnsatisfied) {
		t.Fatalf("PR checkout error = %v, want %v", err, ErrUnsatisfied)
	}
}
