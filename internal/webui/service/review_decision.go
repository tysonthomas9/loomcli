package service

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

const (
	ReviewDecisionApprove        = "approve"
	ReviewDecisionRequestChanges = "request_changes"
)

// ReviewDecisionParams is one durable user intent. DecisionID must remain
// stable across retries of the same UI submission.
type ReviewDecisionParams struct {
	IssueID    string
	Decision   string
	Reason     string
	Actor      string
	DecisionID string
}

type ReviewDecisionResult struct {
	IssueID     string `json:"issue_id"`
	Decision    string `json:"decision"`
	DecisionID  string `json:"decision_id"`
	GitHubStage string `json:"github_stage"`
	LoomStage   string `json:"loom_stage"`
	Replayed    bool   `json:"replayed"`
}

// ReviewDecisionService coordinates review state through one server-side
// operation. GitHub-linked issues deliberately fail before any Loom mutation
// until a GitHub decision executor is configured, preventing false success.
type ReviewDecisionService struct{ issues IssueService }

type reviewIssueState struct {
	Status      string   `json:"status"`
	ExternalRef string   `json:"external_ref"`
	Notes       string   `json:"notes"`
	Labels      []string `json:"labels"`
	CloseReason string   `json:"close_reason"`
}

func NewReviewDecisionService(issues IssueService) *ReviewDecisionService {
	return &ReviewDecisionService{issues: issues}
}

func (s *ReviewDecisionService) Apply(ctx context.Context, params ReviewDecisionParams) (*ReviewDecisionResult, error) {
	if s == nil || s.issues == nil {
		return nil, ErrUnavailable("review decision service unavailable")
	}
	var err error
	if params, err = normalizeReviewDecisionParams(params); err != nil {
		return nil, err
	}
	issue, err := s.loadReviewIssue(ctx, params.IssueID)
	if err != nil {
		return nil, err
	}
	if isGitHubPullRequestRef(issue.ExternalRef) {
		return nil, ErrUnavailable("GitHub review execution is not configured; Loom state was not changed")
	}
	result := &ReviewDecisionResult{
		IssueID: params.IssueID, Decision: params.Decision, DecisionID: params.DecisionID,
		GitHubStage: "not_applicable", LoomStage: "applied",
	}
	if params.Decision == ReviewDecisionApprove {
		return s.applyApproval(ctx, params, issue, result)
	}
	return s.applyChangeRequest(ctx, params, issue, result)
}

func normalizeReviewDecisionParams(params ReviewDecisionParams) (ReviewDecisionParams, error) {
	params.IssueID = strings.TrimSpace(params.IssueID)
	params.Decision = strings.TrimSpace(params.Decision)
	params.Reason = strings.TrimSpace(params.Reason)
	params.Actor = strings.TrimSpace(params.Actor)
	params.DecisionID = strings.TrimSpace(params.DecisionID)
	if params.IssueID == "" || params.DecisionID == "" {
		return params, ErrValidation("issue ID and decision ID are required")
	}
	if params.Actor == "" {
		return params, ErrValidation("review actor is required")
	}
	if params.Decision != ReviewDecisionApprove && params.Decision != ReviewDecisionRequestChanges {
		return params, ErrValidation("decision must be approve or request_changes")
	}
	if params.Decision == ReviewDecisionRequestChanges && params.Reason == "" {
		return params, ErrValidation("a reason is required when requesting changes")
	}
	return params, nil
}

func (s *ReviewDecisionService) loadReviewIssue(ctx context.Context, issueID string) (reviewIssueState, error) {
	raw, err := s.issues.GetIssue(ctx, issueID)
	if err != nil {
		return reviewIssueState{}, err
	}
	var issue reviewIssueState
	if err := json.Unmarshal(raw, &issue); err != nil {
		return reviewIssueState{}, ErrInternal("failed to decode review issue", err)
	}
	return issue, nil
}

func (s *ReviewDecisionService) applyApproval(ctx context.Context, params ReviewDecisionParams, issue reviewIssueState, result *ReviewDecisionResult) (*ReviewDecisionResult, error) {
	reason := params.Reason
	if reason == "" {
		reason = "Approved by " + params.Actor
	}
	if issue.Status == "closed" && issue.CloseReason == reason {
		result.LoomStage, result.Replayed = "replayed", true
		return result, nil
	}
	if _, err := s.issues.CloseIssue(ctx, CloseIssueParams{IssueID: params.IssueID, Reason: reason}); err != nil {
		return nil, err
	}
	return result, nil
}

func (s *ReviewDecisionService) applyChangeRequest(ctx context.Context, params ReviewDecisionParams, issue reviewIssueState, result *ReviewDecisionResult) (*ReviewDecisionResult, error) {
	marker := fmt.Sprintf("[review-decision:%s] %s requested changes: %s", params.DecisionID, params.Actor, params.Reason)
	labels := append([]string(nil), issue.Labels...)
	if !containsString(labels, "needs-revision") {
		labels = append(labels, "needs-revision")
	}
	notes := strings.TrimSpace(issue.Notes)
	if strings.Contains(notes, "[review-decision:"+params.DecisionID+"]") {
		result.Replayed = true
		result.LoomStage = "replayed"
		if issue.Status == "open" && containsString(issue.Labels, "needs-revision") {
			return result, nil
		}
	} else if notes == "" {
		notes = marker
	} else {
		notes += "\n\n" + marker
	}
	status := "open"
	if err := s.issues.PatchIssue(ctx, PatchIssueParams{
		IssueID: params.IssueID, Status: &status, Notes: &notes, SetLabels: labels,
	}); err != nil {
		return nil, err
	}
	return result, nil
}

func isGitHubPullRequestRef(ref string) bool {
	ref = strings.ToLower(strings.TrimSpace(ref))
	return strings.HasPrefix(ref, "https://github.com/") && strings.Contains(ref, "/pull/")
}

func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
