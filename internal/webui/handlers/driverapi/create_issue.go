package driverapi

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/tysonthomas9/loomcli/internal/backend"
	"github.com/tysonthomas9/loomcli/internal/domain"
	driverpkg "github.com/tysonthomas9/loomcli/internal/driver"
)

// create-issue lets a running workflow (the scout lane) mint a fleet-db issue
// through the run-scoped driver API. Provenance is unforgeable: unlike
// claim-ready there is no client actor override — the backend always acts as
// driverpkg.DriverRunActor(parent.RunID), which fleet-db stamps as created_by
// and uses to scope both idempotency layers. Dedup (hard-idempotency replay
// or the soft-duplicate guard) is transparent: fleet-db returns the existing
// issue and the workflow just receives its fields.

// Create-time limits mirrored from fleet-db so bad requests fail here as 400
// invalid instead of a translated backend error.
const (
	maxCreateIssueTitleLen          = 500
	maxCreateIssueIdempotencyKeyLen = 128
)

// createIssueParams is the camelCase driver-op request wire. Fields on the
// contract's not-accepted list (actor, assignee/owner, metadata,
// acceptanceCriteria, estimatedMinutes, dependencies, createdBy, force) are
// deliberately absent and rejected by the strict decode.
type createIssueParams struct {
	// Title is required, <=500 characters.
	Title string `json:"title"`
	// Description also carries acceptance criteria (as a "## Acceptance
	// Criteria" section) — fleet-db has no acceptance-criteria write path.
	Description string `json:"description,omitempty"`
	// IssueType is task|bug|feature|epic|chore; empty lets fleet-db default
	// to task.
	IssueType string `json:"issueType,omitempty"`
	// Priority is 0-4 (Loom semantics passthrough); omitted/0 lets fleet-db
	// default P2 — the wire body drops zero.
	Priority int      `json:"priority,omitempty"`
	Labels   []string `json:"labels,omitempty"`
	// Repo maps to CreateParams.SourceRepo (fleet-db "repo").
	Repo string `json:"repo,omitempty"`
	// Parent is create-time only: fleet-db's PATCH cannot set it later.
	Parent string `json:"parent,omitempty"`
	Design string `json:"design,omitempty"`
	// Status on create is restricted to open|deferred|review. review is the
	// scout's quarantine state: recommendations surface in the human review
	// queue and stay out of the ready set until approved.
	Status string `json:"status,omitempty"`
	// IdempotencyKey is optional (<=128 printable ASCII). Omitted, the
	// handler defaults it per run+day+body (see createIssue).
	IdempotencyKey string `json:"idempotencyKey,omitempty"`
}

// decodeCreateIssueParams decodes strictly, unlike decodeParams: an unknown
// field — including everything on the not-accepted list — fails loudly as
// invalid rather than being silently dropped, so a client-supplied actor or
// force can never appear to work.
func decodeCreateIssueParams(body []byte) (createIssueParams, error) {
	var params createIssueParams
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&params); err != nil {
		return params, fmt.Errorf("decode driver op params: %s: %w", err.Error(), domain.ErrInvalid)
	}
	return params, nil
}

func (p createIssueParams) validate() error {
	if strings.TrimSpace(p.Title) == "" {
		return fmt.Errorf("title required: %w", domain.ErrInvalid)
	}
	if len(p.Title) > maxCreateIssueTitleLen {
		return fmt.Errorf("title exceeds maximum length of %d characters: %w", maxCreateIssueTitleLen, domain.ErrInvalid)
	}
	switch p.IssueType {
	case "", "task", "bug", "feature", "epic", "chore":
	default:
		return fmt.Errorf("invalid issueType %q: %w", p.IssueType, domain.ErrInvalid)
	}
	if p.Priority < 0 || p.Priority > 4 {
		return fmt.Errorf("invalid priority %d: must be between 0 and 4: %w", p.Priority, domain.ErrInvalid)
	}
	switch p.Status {
	case "", "open", "deferred", "review":
	default:
		return fmt.Errorf("invalid status %q: only open, deferred, or review can be set on create: %w", p.Status, domain.ErrInvalid)
	}
	if p.IdempotencyKey != "" {
		if len(p.IdempotencyKey) > maxCreateIssueIdempotencyKeyLen {
			return fmt.Errorf("idempotencyKey exceeds %d characters: %w", maxCreateIssueIdempotencyKeyLen, domain.ErrInvalid)
		}
		for _, c := range p.IdempotencyKey {
			if c < 0x21 || c > 0x7e {
				return fmt.Errorf("idempotencyKey must be printable ASCII without spaces: %w", domain.ErrInvalid)
			}
		}
	}
	return nil
}

// createIssueResponse is the camelCase wire projection of the created issue,
// mirroring ClaimedTask — new ops define a wire struct, never raw snake_case
// backend.IssueData.
type createIssueResponse struct {
	ID         string    `json:"id"`
	Title      string    `json:"title"`
	Status     string    `json:"status"`
	Priority   int       `json:"priority"`
	IssueType  string    `json:"issueType,omitempty"`
	Labels     []string  `json:"labels,omitempty"`
	SourceRepo string    `json:"sourceRepo,omitempty"`
	Parent     string    `json:"parent,omitempty"`
	CreatedBy  string    `json:"createdBy,omitempty"`
	CreatedAt  time.Time `json:"createdAt"`
}

func (m *Module) createIssue(ctx context.Context, ws string, id driverIdentity, body []byte) (any, error) {
	params, err := decodeCreateIssueParams(body)
	if err != nil {
		return nil, err
	}
	parent, err := m.verifyParent(ctx, ws, id)
	if err != nil {
		return nil, err
	}
	if err := params.validate(); err != nil {
		return nil, err
	}
	createParams := backend.CreateParams{
		Title:          params.Title,
		Description:    params.Description,
		IssueType:      params.IssueType,
		Priority:       params.Priority,
		Labels:         params.Labels,
		SourceRepo:     params.Repo,
		Parent:         params.Parent,
		Design:         params.Design,
		Status:         params.Status,
		IdempotencyKey: params.IdempotencyKey,
	}
	if createParams.IdempotencyKey == "" {
		// The fleet backend never self-defaults the key, so default it here
		// (mirroring cli/data): sha256(UTC date + wire body), actor-scoped
		// server-side — retries within a run dedupe, a later run always
		// mints. Best-effort: an unhashable body just skips dedup.
		if key, keyErr := createParams.FleetCreateIdempotencyKey(time.Now()); keyErr == nil {
			createParams.IdempotencyKey = key
		}
	}
	issueBackend, err := m.issueBackends(ws, driverpkg.DriverRunActor(parent.RunID))
	if err != nil {
		return nil, err
	}
	issue, err := issueBackend.Create(ctx, createParams)
	if err != nil {
		return nil, translateCreateIssueBackendError(err)
	}
	if issue == nil {
		return nil, fmt.Errorf("create issue: backend returned no issue")
	}
	return createIssueResponse{
		ID:         issue.ID,
		Title:      issue.Title,
		Status:     issue.Status,
		Priority:   issue.Priority,
		IssueType:  issue.IssueType,
		Labels:     issue.Labels,
		SourceRepo: issue.SourceRepo,
		Parent:     issue.Parent,
		CreatedBy:  issue.CreatedBy,
		CreatedAt:  issue.CreatedAt,
	}, nil
}

// translateCreateIssueBackendError maps backend error kinds onto the domain
// sentinels writeDomainOpError understands: BackendError does not wrap them
// itself, so without this a fleet-db 409 (idempotency key reused with a
// different body, or an in-flight same-key create) would surface as 500
// internal. Timeout/cancel kinds need no branch — their causes already
// unwrap to the context errors the envelope maps to 504/499.
func translateCreateIssueBackendError(err error) error {
	switch {
	case backend.IsKind(err, backend.KindConflict):
		return fmt.Errorf("create issue: %s: %w", err.Error(), domain.ErrConflict)
	case backend.IsKind(err, backend.KindValidation):
		return fmt.Errorf("create issue: %s: %w", err.Error(), domain.ErrInvalid)
	case backend.IsKind(err, backend.KindNotFound):
		return fmt.Errorf("create issue: %s: %w", err.Error(), domain.ErrNotFound)
	}
	return fmt.Errorf("create issue: %w", err)
}
