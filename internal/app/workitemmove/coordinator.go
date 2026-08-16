// Package workitemmove coordinates the backend-atomic cross-workspace move
// without taking ownership of either Workspace or Work Item records.
package workitemmove

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/tysonthomas9/loomcli/internal/modules/workitems"
	"github.com/tysonthomas9/loomcli/internal/modules/workspace"
)

const moveTimeout = 30 * time.Second

var (
	ErrForbidden             = errors.New("work item move forbidden")
	ErrInvalid               = workitems.ErrInvalid
	ErrNotFound              = workitems.ErrNotFound
	ErrConflict              = workitems.ErrConflict
	ErrUnavailable           = workitems.ErrUnavailable
	ErrTimeout               = workitems.ErrTimeout
	ErrInvalidPersistedState = workitems.ErrInvalidPersistedState
)

// Adapter errors keep the workflow's public vocabulary independent of a
// concrete owner transport while preserving the Work Items HTTP contract.
func AdapterInvalid(operation, detail string) error {
	return workitems.AdapterInvalid(operation, detail)
}

func AdapterNotFound(operation, detail string) error {
	return workitems.AdapterNotFound(operation, detail)
}

func AdapterConflict(operation, detail string) error {
	return workitems.AdapterConflict(operation, detail)
}

func AdapterUnavailable(operation, detail string, cause error) error {
	return workitems.AdapterUnavailable(operation, detail, cause)
}

func AdapterTimeout(operation, detail string, cause error) error {
	return workitems.AdapterTimeout(operation, detail, cause)
}

// AtomicMover is the one write port required by this application workflow.
// Its implementation must commit source retirement and target creation as one
// owner transaction; a sequence of generic Work Items writes is not valid.
type AtomicMover interface {
	MoveAtomic(context.Context, AtomicCommand) (*AtomicResult, error)
}

type WorkspaceCatalog interface {
	Resolve(context.Context, workspace.ResolveQuery) (*workspace.Reference, error)
}

type Reference struct {
	Workspace string `json:"workspace"`
	IssueID   string `json:"issue_id"`
}

type AtomicCommand struct {
	SourceWorkspace        string
	SourceIssueID          string
	ExpectedSourceRevision time.Time
	TargetWorkspace        string
	RequestID              string
}

type AtomicResult struct {
	Source   Reference
	Target   Reference
	Replayed bool
}

type Command struct {
	IssueID                string
	SourceWorkspace        string
	TargetWorkspace        string
	ExpectedSourceRevision time.Time
	RequestID              string
}

type Result struct {
	SourceID string `json:"source_id"`
	TargetID string `json:"target_id"`
	Replayed bool   `json:"replayed"`
}

type Commands interface {
	Move(context.Context, Command) (*Result, error)
}

type Coordinator struct {
	mover      AtomicMover
	workspaces WorkspaceCatalog
}

var _ Commands = (*Coordinator)(nil)

func New(mover AtomicMover, workspaces WorkspaceCatalog) (*Coordinator, error) {
	if mover == nil || workspaces == nil {
		return nil, fmt.Errorf("compose work item move: atomic mover and workspace catalog are required: %w", ErrUnavailable)
	}
	return &Coordinator{mover: mover, workspaces: workspaces}, nil
}

func (c *Coordinator) Move(ctx context.Context, command Command) (*Result, error) {
	issueID := strings.TrimSpace(command.IssueID)
	sourceRef := strings.TrimSpace(command.SourceWorkspace)
	targetRef := strings.TrimSpace(command.TargetWorkspace)
	requestID := strings.TrimSpace(command.RequestID)
	if issueID == "" || sourceRef == "" || targetRef == "" || requestID == "" ||
		command.ExpectedSourceRevision.IsZero() {
		return nil, fmt.Errorf("issue id, source workspace, target workspace, expected source revision, and request id are required: %w", ErrInvalid)
	}
	if len(requestID) > 200 {
		return nil, fmt.Errorf("request id must not exceed 200 bytes: %w", ErrInvalid)
	}
	ctx, cancel := context.WithTimeout(ctx, moveTimeout)
	defer cancel()
	sourceWorkspace, err := c.resolveWorkspace(ctx, sourceRef)
	if err != nil {
		return nil, err
	}
	targetWorkspace, err := c.resolveWorkspace(ctx, targetRef)
	if err != nil {
		return nil, err
	}
	if targetWorkspace.Key == sourceWorkspace.Key {
		return nil, fmt.Errorf("cannot move issue to the same workspace: %w", ErrInvalid)
	}
	result, err := c.mover.MoveAtomic(ctx, AtomicCommand{
		SourceWorkspace: sourceWorkspace.Key, SourceIssueID: issueID,
		ExpectedSourceRevision: command.ExpectedSourceRevision.UTC(),
		TargetWorkspace:        targetWorkspace.Key, RequestID: requestID,
	})
	if err != nil {
		return nil, err
	}
	if result == nil || result.Source.Workspace != sourceWorkspace.Key ||
		result.Source.IssueID != issueID || result.Target.Workspace != targetWorkspace.Key ||
		strings.TrimSpace(result.Target.IssueID) == "" {
		return nil, fmt.Errorf("atomic move returned divergent owner state: %w", ErrInvalidPersistedState)
	}
	return &Result{SourceID: result.Source.IssueID, TargetID: result.Target.IssueID, Replayed: result.Replayed}, nil
}

func (c *Coordinator) resolveWorkspace(ctx context.Context, reference string) (*workspace.Reference, error) {
	value, err := c.workspaces.Resolve(ctx, workspace.ResolveQuery{Reference: reference})
	if err != nil {
		if errors.Is(err, workspace.ErrNotFound) || errors.Is(err, workspace.ErrInvalid) {
			return nil, fmt.Errorf("workspace %q is invalid or not found: %w", reference, ErrInvalid)
		}
		if errors.Is(err, workspace.ErrUnavailable) {
			return nil, fmt.Errorf("workspace catalog unavailable: %w", ErrUnavailable)
		}
		return nil, err
	}
	if value == nil || strings.TrimSpace(value.Key) == "" {
		return nil, fmt.Errorf("workspace %q resolved without a key: %w", reference, ErrInvalidPersistedState)
	}
	return value, nil
}
