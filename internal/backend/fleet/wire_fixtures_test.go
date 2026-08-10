package fleet

import (
	"time"

	"github.com/tysonthomas9/loomcli/internal/modules/workitems"
)

// These fixtures describe FleetDB response JSON used by adapter tests. They
// deliberately model the wire protocol rather than recreating the retired
// internal/types product model.
type testIssue struct {
	ID                 string               `json:"id,omitempty"`
	Title              string               `json:"title,omitempty"`
	Description        string               `json:"description,omitempty"`
	Design             string               `json:"design,omitempty"`
	DesignArtifactID   string               `json:"design_artifact_id,omitempty"`
	DesignFormat       string               `json:"design_format,omitempty"`
	HasDesign          bool                 `json:"has_design"`
	AcceptanceCriteria string               `json:"acceptance_criteria,omitempty"`
	Notes              string               `json:"notes,omitempty"`
	Status             workitems.Status     `json:"status,omitempty"`
	Priority           int                  `json:"priority"`
	IssueType          workitems.IssueType  `json:"type,omitempty"`
	Assignee           string               `json:"assignee,omitempty"`
	Owner              string               `json:"owner,omitempty"`
	Labels             []string             `json:"labels,omitempty"`
	SourceRepo         string               `json:"source_repo,omitempty"`
	Parent             string               `json:"parent,omitempty"`
	CreatedAt          time.Time            `json:"created_at,omitempty"`
	CreatedBy          string               `json:"created_by,omitempty"`
	UpdatedAt          time.Time            `json:"updated_at,omitempty"`
	ClosedAt           *time.Time           `json:"closed_at,omitempty"`
	CloseReason        string               `json:"close_reason,omitempty"`
	ClosedBySession    string               `json:"closed_by_session,omitempty"`
	ExternalRef        *string              `json:"external_ref,omitempty"`
	EstimatedMinutes   *int                 `json:"estimated_minutes,omitempty"`
	DueAt              *time.Time           `json:"due_at,omitempty"`
	DeferUntil         *time.Time           `json:"defer_until,omitempty"`
	Dependencies       []*testDependency    `json:"dependencies,omitempty"`
	Comments           []*workitems.Comment `json:"comments,omitempty"`
}

type testIssueWithCounts struct {
	*testIssue
	DependencyCount int `json:"dependency_count,omitempty"`
	DependentCount  int `json:"dependent_count,omitempty"`
}

type testIssueWithDependencyMetadata struct {
	testIssue
	DependencyType testDependencyType `json:"dependency_type"`
}

type testIssueDetails struct {
	testIssue
	Labels       []string                           `json:"labels"`
	Dependencies []*testIssueWithDependencyMetadata `json:"dependencies"`
	Dependents   []*testIssueWithDependencyMetadata `json:"dependents"`
	Comments     []*workitems.Comment               `json:"comments"`
	Parent       *string                            `json:"parent,omitempty"`
}

type testBlockedIssue struct {
	testIssue
	BlockedByCount int      `json:"blocked_by_count"`
	BlockedBy      []string `json:"blocked_by"`
}

type testDependencyType string

const testDepBlocks testDependencyType = "blocks"

type testDependency struct {
	IssueID     string             `json:"issue_id"`
	DependsOnID string             `json:"depends_on_id"`
	Type        testDependencyType `json:"type"`
}
