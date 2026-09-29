package domain

import (
	"errors"
	"fmt"
	"strconv"
	"time"
)

// Delivery group error codes mirrored from FleetDB's OpenAPI conflict /
// precondition envelopes. The Loom facade surfaces these codes unchanged so
// clients can branch on the same contract as the canonical store.
const (
	DeliveryGroupConflictPRInOtherGroup     = "pr_in_other_group"
	DeliveryGroupConflictAlreadyExists      = "already_exists"
	DeliveryGroupConflictIdempotencyReused  = "idempotency_key_reused"
	DeliveryGroupConflictReplayUnverifiable = "replay_unverifiable"
	DeliveryGroupConflictInvalidTransition  = "invalid_transition"
	DeliveryGroupConflictRetryable          = "conflict"
	DeliveryGroupPreconditionFailedCode     = "precondition_failed"
	DeliveryGroupPreconditionRequiredCode   = "precondition_required"
	DeliveryGroupInconsistentCode           = "delivery_group_inconsistent"
	DeliveryGroupMembersInvalidCode         = "validation_failed"
)

// Sentinel errors for delivery-group writes. Callers match with errors.Is /
// errors.As; structured facts live on DeliveryGroupConflictError and
// DeliveryGroupPreconditionError.
var (
	ErrDeliveryGroupInconsistent = errors.New("domain: delivery group inconsistent")
	ErrDeliveryGroupConflict     = errors.New("domain: delivery group conflict")
	ErrDeliveryGroupPrecondition = errors.New("domain: delivery group precondition failed")
)

// DeliveryGroupState is the lifecycle of a delivery group.
type DeliveryGroupState string

const (
	DeliveryGroupActive   DeliveryGroupState = "active"
	DeliveryGroupArchived DeliveryGroupState = "archived"
)

// DeliveryGroupMemberSource records how a member was added.
type DeliveryGroupMemberSource string

const (
	DeliveryGroupMemberManual             DeliveryGroupMemberSource = "manual"
	DeliveryGroupMemberLoomTask           DeliveryGroupMemberSource = "loom_task"
	DeliveryGroupMemberLineageAdopt       DeliveryGroupMemberSource = "lineage_adopt"
	DeliveryGroupMemberNativeStackSuggest DeliveryGroupMemberSource = "native_stack_suggestion"
	DeliveryGroupMemberIdentityHeal       DeliveryGroupMemberSource = "identity_heal"
)

// DeliveryGroupMember is one ordered PR in a delivery group.
type DeliveryGroupMember struct {
	PRKey          string                    `json:"pr_key"`
	RepoName       string                    `json:"repo_name"`
	PRNumber       int                       `json:"pr_number"`
	GitHubNodeID   string                    `json:"github_node_id,omitempty"`
	Source         DeliveryGroupMemberSource `json:"source"`
	TaskID         string                    `json:"task_id,omitempty"`
	LineageStackID string                    `json:"lineage_stack_id,omitempty"`
	AddedAt        time.Time                 `json:"added_at"`
	AddedBy        string                    `json:"added_by,omitempty"`
}

// DeliveryGroup is the durable ordered cross-repository delivery plan.
type DeliveryGroup struct {
	WorkspaceKey string                `json:"workspace_key"`
	ID           string                `json:"id"`
	Title        string                `json:"title"`
	EpicID       string                `json:"epic_id,omitempty"`
	Owner        string                `json:"owner,omitempty"`
	State        DeliveryGroupState    `json:"state"`
	Revision     int64                 `json:"revision"`
	Members      []DeliveryGroupMember `json:"members"`
	LastOpID     string                `json:"last_op_id"`
	LastOpDigest string                `json:"last_op_digest,omitempty"`
	CreatedAt    time.Time             `json:"created_at"`
	UpdatedAt    time.Time             `json:"updated_at"`
	CreatedBy    string                `json:"created_by,omitempty"`
	UpdatedBy    string                `json:"updated_by,omitempty"`
	Integrity    string                `json:"integrity,omitempty"`
	Inconsistent bool                  `json:"inconsistent,omitempty"`
}

// DeliveryGroupMemberInput is the create/replace member payload.
type DeliveryGroupMemberInput struct {
	PRKey          string                    `json:"pr_key,omitempty"`
	RepoName       string                    `json:"repo_name,omitempty"`
	PRNumber       int                       `json:"pr_number,omitempty"`
	GitHubNodeID   string                    `json:"github_node_id,omitempty"`
	Source         DeliveryGroupMemberSource `json:"source,omitempty"`
	TaskID         string                    `json:"task_id,omitempty"`
	LineageStackID string                    `json:"lineage_stack_id,omitempty"`
}

// DeliveryGroupCreate is the create request body.
type DeliveryGroupCreate struct {
	ID      string                     `json:"id"`
	Title   string                     `json:"title"`
	EpicID  string                     `json:"epic_id,omitempty"`
	Owner   string                     `json:"owner,omitempty"`
	Members []DeliveryGroupMemberInput `json:"members,omitempty"`
}

// DeliveryGroupUpdate is the header patch body. Empty EpicID/Owner clear.
type DeliveryGroupUpdate struct {
	Title  *string `json:"title,omitempty"`
	EpicID *string `json:"epic_id,omitempty"`
	Owner  *string `json:"owner,omitempty"`
}

// DeliveryGroupConflictError is a structured 409 from FleetDB.
type DeliveryGroupConflictError struct {
	Code      string
	Message   string
	PRKey     string
	GroupID   string
	Revision  int64
	Retryable bool
	Meta      map[string]string
}

func (e *DeliveryGroupConflictError) Error() string {
	if e == nil {
		return "domain: delivery group conflict"
	}
	if e.Message != "" {
		return e.Message
	}
	return fmt.Sprintf("domain: delivery group conflict (%s)", e.Code)
}

func (e *DeliveryGroupConflictError) Unwrap() error { return ErrDeliveryGroupConflict }

// DeliveryGroupPreconditionError is a structured 412 (stale revision / superseded key).
type DeliveryGroupPreconditionError struct {
	Code             string
	Message          string
	ExpectedRevision int64
	StoredRevision   int64
	Meta             map[string]string
}

func (e *DeliveryGroupPreconditionError) Error() string {
	if e == nil {
		return "domain: delivery group precondition failed"
	}
	if e.Message != "" {
		return e.Message
	}
	return fmt.Sprintf("domain: delivery group precondition failed (expected=%d stored=%d)", e.ExpectedRevision, e.StoredRevision)
}

func (e *DeliveryGroupPreconditionError) Unwrap() error { return ErrDeliveryGroupPrecondition }

// ParseRevisionMeta reads an int64 revision from error meta, accepting either
// key FleetDB documents (expected_revision / stored_revision).
func ParseRevisionMeta(meta map[string]string, key string) int64 {
	if meta == nil {
		return 0
	}
	raw := meta[key]
	if raw == "" {
		return 0
	}
	n, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return 0
	}
	return n
}
