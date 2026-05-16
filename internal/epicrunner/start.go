// Package epicrunner owns the shared business rules for starting an epic run.
package epicrunner

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/tysonthomas9/loomcli/internal/bootstrap"
	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/lockfile"
	"github.com/tysonthomas9/loomcli/internal/store"
)

const (
	defaultBindLockTimeout      = 30 * time.Second
	defaultBindLockPollInterval = 100 * time.Millisecond
)

// ErrorKind classifies epic-runner start errors for HTTP/CLI callers.
type ErrorKind string

const (
	ErrorKindValidation  ErrorKind = "validation"
	ErrorKindNotFound    ErrorKind = "not_found"
	ErrorKindConflict    ErrorKind = "conflict"
	ErrorKindUnavailable ErrorKind = "unavailable"
	ErrorKindInternal    ErrorKind = "internal"
)

// Error is returned for expected epic-runner start failures.
type Error struct {
	Kind ErrorKind
	Msg  string
	Err  error
}

func (e *Error) Error() string {
	if e == nil {
		return ""
	}
	if e.Err == nil {
		return e.Msg
	}
	return e.Msg + ": " + e.Err.Error()
}

func (e *Error) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

// ErrorKindOf returns the classified kind for err.
func ErrorKindOf(err error) ErrorKind {
	var runErr *Error
	if errors.As(err, &runErr) {
		return runErr.Kind
	}
	if errors.Is(err, domain.ErrNotFound) {
		return ErrorKindNotFound
	}
	return ErrorKindInternal
}

// StartState describes what happened during Start.
type StartState string

const (
	StartStateUnassigned StartState = "unassigned"
	StartStateAssigned   StartState = "assigned"
	StartStateResumed    StartState = "resumed"
	StartStateDryRun     StartState = "dry_run"
)

// StartInput describes a request to start or resume an epic run for a lead.
type StartInput struct {
	WorkspaceKey          string
	EpicID                string
	LeadName              string
	OrchestratorSessionID string
	Mutate                bool
}

// StartResult is the provider-neutral result of binding an epic run.
type StartResult struct {
	WorkspaceKey          string        `json:"workspace_key"`
	EpicID                string        `json:"epic_id"`
	LeadName              string        `json:"lead_name,omitempty"`
	Lead                  *domain.Agent `json:"lead,omitempty"`
	OrchestratorSessionID string        `json:"orchestrator_session_id,omitempty"`
	State                 StartState    `json:"state"`
	DeliveryState         string        `json:"delivery_state,omitempty"`
}

// Start validates and records the lead-to-epic binding for an epic run.
//
// A blank LeadName is allowed for CLI usage that wants an unattached runner.
// When LeadName is set, Start enforces strict ownership:
//   - the lead must exist and have role lead/orchestrator
//   - the lead cannot already own a different epic
//   - another lead cannot already own the requested epic
//   - same lead + same epic is idempotent resume
func Start(ctx context.Context, st store.Store, in StartInput) (*StartResult, error) {
	in.WorkspaceKey = strings.TrimSpace(in.WorkspaceKey)
	in.EpicID = strings.TrimSpace(in.EpicID)
	in.LeadName = strings.TrimSpace(in.LeadName)
	in.OrchestratorSessionID = strings.TrimSpace(in.OrchestratorSessionID)

	if st == nil {
		return nil, runError(ErrorKindUnavailable, "store not configured", nil)
	}
	if in.WorkspaceKey == "" {
		return nil, runError(ErrorKindValidation, "workspace key required", nil)
	}
	if in.EpicID == "" {
		return nil, runError(ErrorKindValidation, "epic id required", nil)
	}
	if in.LeadName == "" {
		return &StartResult{
			WorkspaceKey:          in.WorkspaceKey,
			EpicID:                in.EpicID,
			OrchestratorSessionID: in.OrchestratorSessionID,
			State:                 StartStateUnassigned,
		}, nil
	}

	if in.Mutate {
		unlock, err := AcquireBindLock(in.WorkspaceKey, in.LeadName)
		if err != nil {
			return nil, err
		}
		defer unlock()
	}

	return startWithLockHeld(ctx, st, in)
}

func startWithLockHeld(ctx context.Context, st store.Store, in StartInput) (*StartResult, error) {
	lead, conflictingOwner, err := loadLeadAndEpicOwner(ctx, st, in.WorkspaceKey, in.LeadName, in.EpicID)
	if err != nil {
		return nil, err
	}
	if err := validateLeadStart(lead, conflictingOwner, in.EpicID); err != nil {
		return nil, err
	}

	orchestratorID, err := effectiveLeadOrchestratorID(ctx, st, in.WorkspaceKey, in.OrchestratorSessionID, lead)
	if err != nil {
		return nil, runError(ErrorKindInternal, fmt.Sprintf("resolve orchestrator session for lead %q", in.LeadName), err)
	}

	result := &StartResult{
		WorkspaceKey:          in.WorkspaceKey,
		EpicID:                in.EpicID,
		LeadName:              lead.Name,
		Lead:                  lead,
		OrchestratorSessionID: orchestratorID,
		DeliveryState:         "pending",
	}

	if !in.Mutate {
		result.State = StartStateDryRun
		return result, nil
	}
	if lead.Parent == in.EpicID {
		result.State = StartStateResumed
		result.DeliveryState = "delivered"
		return result, nil
	}
	return bindLeadParent(ctx, st, in, result, lead)
}

func bindLeadParent(ctx context.Context, st store.Store, in StartInput, result *StartResult, lead *domain.Agent) (*StartResult, error) {
	updated, err := st.Agents().Update(ctx, in.WorkspaceKey, lead.Name, store.AgentUpdate{Parent: &in.EpicID})
	if err != nil {
		return nil, runError(ErrorKindInternal, fmt.Sprintf("bind lead %s to epic %s", lead.Name, in.EpicID), err)
	}
	if updated == nil {
		return nil, runError(ErrorKindInternal, fmt.Sprintf("bind lead %s returned nil", lead.Name), nil)
	}
	if updated.Parent != in.EpicID {
		return nil, runError(ErrorKindConflict,
			fmt.Sprintf("lead %s is already running epic %s; ask the lead to clear or finish that epic before running %s", lead.Name, updated.Parent, in.EpicID),
			nil)
	}
	result.Lead = updated
	result.State = StartStateAssigned
	return result, nil
}

func validateLeadStart(lead *domain.Agent, conflictingOwner, epicID string) error {
	if lead.Parent != "" && lead.Parent != epicID {
		return runError(ErrorKindConflict,
			fmt.Sprintf("lead %s is already running epic %s; ask the lead to clear or finish that epic before running %s", lead.Name, lead.Parent, epicID),
			nil)
	}
	if conflictingOwner != "" {
		return runError(ErrorKindConflict,
			fmt.Sprintf("epic %s is already claimed by lead %s", epicID, conflictingOwner),
			nil)
	}
	return nil
}

func loadLeadAndEpicOwner(ctx context.Context, st store.Store, workspace, leadName, epicID string) (*domain.Agent, string, error) {
	agents, err := st.Agents().List(ctx, workspace)
	if err != nil {
		return nil, "", runError(ErrorKindInternal, "list agents", err)
	}

	var lead *domain.Agent
	var conflictingOwner string
	for _, agent := range agents {
		if agent == nil {
			continue
		}
		if agent.Name == leadName {
			lead = agent
			continue
		}
		if IsLeadRole(agent.RoleName) && agent.Parent == epicID {
			conflictingOwner = agent.Name
		}
	}
	if lead == nil {
		return nil, "", runError(ErrorKindNotFound,
			fmt.Sprintf("lead agent %q was not found in workspace %s; create it with `loom agentdef add %s --role lead` or rerun without --lead", leadName, workspace, leadName),
			domain.ErrNotFound)
	}
	if !IsLeadRole(lead.RoleName) {
		return nil, "", runError(ErrorKindValidation,
			fmt.Sprintf("agent %q has role %q; `loom epic run` requires a lead agent when --lead or LOOM_AGENT_NAME is set", leadName, lead.RoleName),
			nil)
	}
	return lead, conflictingOwner, nil
}

func effectiveLeadOrchestratorID(ctx context.Context, st store.Store, workspace, orchestratorID string, lead *domain.Agent) (string, error) {
	if orchestratorID != "" {
		return orchestratorID, nil
	}
	return store.OrchestrationSessionIDFor(ctx, st, workspace, lead.Name)
}

// IsLeadRole reports whether roleName is treated as a lead/orchestrator.
func IsLeadRole(roleName string) bool {
	switch strings.ToLower(strings.TrimSpace(roleName)) {
	case "lead", "orchestrator":
		return true
	default:
		return false
	}
}

// AcquireBindLock serializes lead/epic ownership changes for a workspace.
func AcquireBindLock(workspace, leadName string) (func(), error) {
	return AcquireBindLockWithTimeout(workspace, leadName, defaultBindLockTimeout, defaultBindLockPollInterval)
}

// AcquireBindLockWithTimeout is exported for CLI tests that assert timeout behavior.
func AcquireBindLockWithTimeout(workspace, leadName string, timeout, pollInterval time.Duration) (func(), error) {
	dir := bootstrap.LoomDir()
	if dir == "" {
		return func() {}, runError(ErrorKindInternal, "cannot resolve loom data directory for lead assignment lock", nil)
	}
	lockDir := filepath.Join(dir, "epic-runner-locks")
	if err := os.MkdirAll(lockDir, 0755); err != nil {
		return func() {}, runError(ErrorKindInternal, "create lead assignment lock directory", err)
	}
	lockName := sanitizeLockName(workspace)
	if lockName == "" {
		lockName = "lead"
	}
	lockPath := filepath.Join(lockDir, lockName+".lock")
	f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0600) //nolint:gosec // path is under loom data dir with sanitized filename
	if err != nil {
		return func() {}, runError(ErrorKindInternal, fmt.Sprintf("open lead assignment lock %s", lockPath), err)
	}

	if pollInterval <= 0 {
		pollInterval = defaultBindLockPollInterval
	}
	deadline := time.Now().Add(timeout)
	for {
		if err := lockfile.TryLockExclusive(f); err != nil {
			if !errors.Is(err, lockfile.ErrLocked) {
				_ = f.Close()
				return func() {}, runError(ErrorKindInternal, fmt.Sprintf("acquire lead assignment lock %s", lockPath), err)
			}
			if timeout <= 0 || !time.Now().Before(deadline) {
				_ = f.Close()
				return func() {}, runError(ErrorKindConflict, fmt.Sprintf("timed out acquiring lead assignment lock %s for lead %q after %s", lockPath, leadName, timeout), lockfile.ErrLocked)
			}
			sleepFor := pollInterval
			if remaining := time.Until(deadline); remaining < sleepFor {
				sleepFor = remaining
			}
			time.Sleep(sleepFor)
			continue
		}
		break
	}
	return func() {
		_ = lockfile.FlockUnlock(f)
		_ = f.Close()
	}, nil
}

func runError(kind ErrorKind, msg string, err error) error {
	return &Error{Kind: kind, Msg: msg, Err: err}
}

func sanitizeLockName(s string) string {
	out := make([]byte, 0, len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= 'a' && c <= 'z', c >= '0' && c <= '9', c == '.', c == '_', c == '-':
			out = append(out, c)
		case c >= 'A' && c <= 'Z':
			out = append(out, c+('a'-'A'))
		default:
			out = append(out, '-')
		}
	}
	return string(out)
}
