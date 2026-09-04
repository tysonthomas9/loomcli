package taskdelivery

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/tysonthomas9/loomcli/internal/domain"
)

var ErrUnsatisfied = errors.New("task delivery requirement not satisfied")

type ReceiptKind string

const (
	ReceiptWorkingCopy ReceiptKind = "working_copy"
	ReceiptPullRequest ReceiptKind = "pull_request"
	ReceiptNoChange    ReceiptKind = "no_change"
)

type WorkingCopyReceipt struct {
	BaseSHA string `json:"base_sha,omitempty"`
	HeadSHA string `json:"head_sha"`
	Method  string `json:"method"`
}

type PullRequestReceipt struct {
	URL     string `json:"url"`
	HeadSHA string `json:"head_sha,omitempty"`
	Branch  string `json:"branch,omitempty"`
}

type NoChangeReceipt struct {
	HeadSHA string `json:"head_sha,omitempty"`
}

// Receipt is a discriminated union. Exactly one payload matching Kind is set.
type Receipt struct {
	Kind        ReceiptKind         `json:"kind"`
	PlanID      string              `json:"plan_id"`
	WorkingCopy *WorkingCopyReceipt `json:"working_copy,omitempty"`
	PullRequest *PullRequestReceipt `json:"pull_request,omitempty"`
	NoChange    *NoChangeReceipt    `json:"no_change,omitempty"`
}

// AcceptRuntimeMetadata validates runner evidence against a frozen plan.
func AcceptRuntimeMetadata(plan Plan, metadata map[string]string) (Receipt, error) {
	filesChanged := strings.TrimSpace(metadata["files_changed"])
	delivery := strings.TrimSpace(metadata["delivery"])
	if filesChanged == "0" || delivery == "pull_request_skipped_no_changes" {
		return Receipt{
			Kind:     ReceiptNoChange,
			PlanID:   plan.PlanID,
			NoChange: &NoChangeReceipt{HeadSHA: firstNonEmpty(metadata["patch_back_head_sha"], metadata["head_sha"])},
		}, nil
	}

	switch plan.Requirement {
	case domain.TaskDeliveryWorkingCopy:
		method := "patch_back"
		headSHA := metadata["patch_back_head_sha"]
		baseSHA := metadata["patch_back_base_sha"]
		if delivery == "stack_branch" {
			method = "stack_branch"
			headSHA = metadata["github_head_sha"]
			baseSHA = metadata["repo_head_before"]
		}
		if strings.TrimSpace(headSHA) == "" || (method == "patch_back" && metadata["patch_back_status"] != "applied") {
			return Receipt{}, fmt.Errorf("%w: working_copy requires an applied patch or committed checkout", ErrUnsatisfied)
		}
		return Receipt{
			Kind:   ReceiptWorkingCopy,
			PlanID: plan.PlanID,
			WorkingCopy: &WorkingCopyReceipt{
				BaseSHA: baseSHA,
				HeadSHA: headSHA,
				Method:  method,
			},
		}, nil
	case domain.TaskDeliveryPullRequest:
		if strings.TrimSpace(metadata["github_pr_url"]) == "" {
			return Receipt{}, fmt.Errorf("%w: pull_request requires a verified PR URL", ErrUnsatisfied)
		}
		return Receipt{
			Kind:   ReceiptPullRequest,
			PlanID: plan.PlanID,
			PullRequest: &PullRequestReceipt{
				URL:     metadata["github_pr_url"],
				HeadSHA: firstNonEmpty(metadata["github_head_sha"], metadata["head_sha"]),
				Branch:  firstNonEmpty(metadata["github_branch"], metadata["branch"]),
			},
		}, nil
	default:
		return Receipt{}, ErrInvalidRequirement
	}
}

// AcceptCommittedCheckout validates the direct daemon's local checkout after
// an agent exits. A clean unchanged checkout is a valid no-change outcome;
// changed work must be represented by a new clean commit.
func AcceptCommittedCheckout(plan Plan, beforeSHA, afterSHA string, clean bool) (Receipt, error) {
	if plan.Requirement != domain.TaskDeliveryWorkingCopy {
		return Receipt{}, fmt.Errorf("%w: direct checkout delivery cannot satisfy %s", ErrUnsatisfied, plan.Requirement)
	}
	if !clean || strings.TrimSpace(afterSHA) == "" {
		return Receipt{}, fmt.Errorf("%w: working_copy requires a clean committed checkout", ErrUnsatisfied)
	}
	if beforeSHA == afterSHA {
		return Receipt{Kind: ReceiptNoChange, PlanID: plan.PlanID, NoChange: &NoChangeReceipt{HeadSHA: afterSHA}}, nil
	}
	return Receipt{
		Kind:   ReceiptWorkingCopy,
		PlanID: plan.PlanID,
		WorkingCopy: &WorkingCopyReceipt{
			BaseSHA: beforeSHA,
			HeadSHA: afterSHA,
			Method:  "committed_checkout",
		},
	}, nil
}

func EncodeReceipt(receipt Receipt) (string, error) {
	b, err := json.Marshal(receipt)
	if err != nil {
		return "", fmt.Errorf("encode task delivery receipt: %w", err)
	}
	return string(b), nil
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}
