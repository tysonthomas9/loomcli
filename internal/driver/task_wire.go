package driver

import "time"

// TaskMutationResult is the stable Driver API response for task completion and
// release operations. Mutation authority lives in the Execution capability.
type TaskMutationResult struct {
	ID       string `json:"id"`
	Status   string `json:"status,omitempty"`
	Released bool   `json:"released,omitempty"`
	Reason   string `json:"reason,omitempty"`
}

// ClaimedTask is the stable Driver API projection of a committed Work Item
// claim. Claim policy and fencing live in the Execution capability.
type ClaimedTask struct {
	ID            string    `json:"id"`
	Title         string    `json:"title,omitempty"`
	Status        string    `json:"status,omitempty"`
	Priority      int       `json:"priority"`
	IssueType     string    `json:"issueType,omitempty"`
	Assignee      string    `json:"assignee,omitempty"`
	Labels        []string  `json:"labels,omitempty"`
	SourceRepo    string    `json:"sourceRepo,omitempty"`
	Parent        string    `json:"parent,omitempty"`
	ClaimedBy     string    `json:"claimedBy,omitempty"`
	ClaimedAt     time.Time `json:"claimedAt,omitempty"`
	ClaimActionID string    `json:"claimActionId,omitempty"`
}
