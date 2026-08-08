package placement

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/store"
)

const (
	defaultReaperGrace         = 2 * time.Minute
	reaperDeadLetterThreshold  = 5
	reaperDeleteBackoffBase    = 30 * time.Second
	reaperDeleteBackoffMax     = 10 * time.Minute
	reaperStateOrphan          = "orphan"
	reaperStateDeleted         = "deleted"
	reaperStateNeedsAttention  = "needs_attention"
	reaperActionMarkReleased   = "mark_released"
	reaperActionAdoptDelete    = "adopt_delete_mark_released"
	reaperActionMarkLost       = "mark_lost"
	reaperActionDeleteReleased = "delete_mark_released"
	reaperActionDeleteRetry    = "delete_retry"
	reaperActionDeleteOrphan   = "delete_orphan"
	reaperActionDeadLetter     = "dead_letter"
	reaperActionObserve        = "observe"
)

// PlacementReaper reconciles Daytona placement records with provider state.
type PlacementReaper struct {
	broker  *Broker
	enforce bool
	grace   time.Duration
	now     func() time.Time
}

// ReaperConfig configures a PlacementReaper.
type ReaperConfig struct {
	Enforce bool
	Grace   time.Duration
	Now     func() time.Time
}

// ReaperResult is the structured diff produced by one reaper pass.
type ReaperResult struct {
	Examined     int
	Acted        int
	DeadLettered int
	Actions      []ReaperAction
}

// ReaperAction describes one intended or enforced reconciliation action.
type ReaperAction struct {
	NodeID     string
	Workspace  string
	Agent      string
	SandboxID  string
	Generation int64
	FromState  string
	ToState    string
	Action     string
}

// NewPlacementReaper constructs a placement reaper. Writes remain disabled
// unless cfg.Enforce is true.
func NewPlacementReaper(b *Broker, cfg ReaperConfig) *PlacementReaper {
	now := cfg.Now
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	grace := cfg.Grace
	if grace <= 0 {
		grace = defaultReaperGrace
	}
	return &PlacementReaper{
		broker:  b,
		enforce: cfg.Enforce,
		grace:   grace,
		now:     now,
	}
}

// RunOnce performs one reconciliation pass. Per-node failures are logged and
// joined into the returned error after the rest of the pass completes.
func (r *PlacementReaper) RunOnce(ctx context.Context) (ReaperResult, error) {
	var result ReaperResult
	if r == nil || r.broker == nil {
		return result, fmt.Errorf("placement reaper broker required: %w", domain.ErrInvalid)
	}
	if ctx == nil {
		ctx = context.Background()
	}
	var errs []error
	keys, err := r.broker.workspaceKeys(ctx, "")
	if err != nil {
		return result, fmt.Errorf("list placement workspaces: %w", err)
	}
	for _, workspaceKey := range keys {
		nodes, err := r.broker.store.Nodes().List(ctx, workspaceKey)
		if err != nil {
			errs = append(errs, fmt.Errorf("list placement nodes for workspace %q: %w", workspaceKey, err))
			slog.ErrorContext(ctx, "placement reaper list nodes failed", "workspace", workspaceKey, "error", err)
			continue
		}
		for _, node := range nodes {
			if ctx.Err() != nil {
				return result, errors.Join(append(errs, ctx.Err())...)
			}
			if !reaperShouldExamineNode(node) {
				continue
			}
			result.Examined++
			if err := r.reapRecordNode(ctx, node, &result); err != nil {
				errs = append(errs, err)
			}
		}
	}
	if err := r.reapProviderOrphans(ctx, &result); err != nil {
		errs = append(errs, err)
	}
	return result, errors.Join(errs...)
}

func reaperShouldExamineNode(node *domain.Node) bool {
	return node != nil &&
		node.Placement != nil &&
		node.RuntimeProvider == domain.RuntimeProviderDaytona
}

func (r *PlacementReaper) reapRecordNode(ctx context.Context, node *domain.Node, result *ReaperResult) error {
	switch node.Placement.State {
	case domain.PlacementStateProvisioning:
		return r.reapProvisioning(ctx, node, result)
	case domain.PlacementStateActive:
		return r.reapActive(ctx, node, result)
	case domain.PlacementStateReleasing:
		return r.reapReleasing(ctx, node, result)
	case domain.PlacementStateReleased:
		return r.reapReleased(ctx, node, result)
	case domain.PlacementStateLost:
		return r.reapLost(ctx, node, result)
	default:
		return nil
	}
}

func (r *PlacementReaper) reapProvisioning(ctx context.Context, node *domain.Node, result *ReaperResult) error {
	matches, err := r.broker.providerSandboxesForPlacement(ctx, node.NodeID)
	if err != nil {
		if errors.Is(err, domain.ErrConflict) {
			return r.deadLetterNode(ctx, result, node, "", err)
		}
		logActionError(ctx, "placement reaper provider match failed", reaperActionFromNode(node, "", reaperActionObserve, string(node.Placement.State), ""), err)
		return fmt.Errorf("list provider sandboxes for provisioning placement %q: %w", node.NodeID, err)
	}
	if len(matches) > 0 {
		return r.reapProvisioningMatchedSandbox(ctx, node, matches[0], result)
	}

	sandboxID := strings.TrimSpace(node.Placement.SandboxID)
	if sandboxID == "" {
		return r.reapProvisioningWithoutSandbox(ctx, node, result)
	}
	return r.reapProvisioningRecordedSandbox(ctx, node, sandboxID, result)
}

func (r *PlacementReaper) reapProvisioningMatchedSandbox(ctx context.Context, node *domain.Node, sandbox ProviderSandbox, result *ReaperResult) error {
	sandboxID := strings.TrimSpace(sandbox.ID)
	if !r.broker.provisioningDeadlineExpired(node) {
		r.logObserve(ctx, node, sandboxID, "provisioning sandbox exists before deadline")
		return nil
	}
	return r.withFreshNodeAction(ctx, result, node, sandboxID, domain.PlacementStateReleased, reaperActionAdoptDelete, func(current *domain.Node) bool {
		return samePlacementCandidate(current, node) &&
			current.Placement.State == domain.PlacementStateProvisioning &&
			r.broker.provisioningDeadlineExpired(current)
	}, func(current *domain.Node) error {
		adopted, err := r.broker.adoptSandboxForRelease(ctx, current, sandboxID)
		if err != nil {
			return err
		}
		if err := r.broker.deleteAndConfirmSandbox(ctx, sandboxID); err != nil {
			return err
		}
		_, err = r.broker.markReleased(ctx, adopted.WorkspaceKey, adopted.NodeID, ReleaseFence{
			Generation: adopted.Placement.Generation,
			SandboxID:  sandboxID,
		})
		return err
	})
}

func (r *PlacementReaper) reapProvisioningWithoutSandbox(ctx context.Context, node *domain.Node, result *ReaperResult) error {
	if !r.broker.provisioningDeadlineExpired(node) {
		r.logObserve(ctx, node, "", "provisioning without sandbox before deadline")
		return nil
	}
	return r.withFreshNodeAction(ctx, result, node, "", domain.PlacementStateReleased, reaperActionMarkReleased, func(current *domain.Node) bool {
		return samePlacementCandidate(current, node) &&
			current.Placement.State == domain.PlacementStateProvisioning &&
			strings.TrimSpace(current.Placement.SandboxID) == "" &&
			r.broker.provisioningDeadlineExpired(current)
	}, func(current *domain.Node) error {
		_, err := r.broker.markReleased(ctx, current.WorkspaceKey, current.NodeID, ReleaseFence{
			Generation: current.Placement.Generation,
		})
		return err
	})
}

func (r *PlacementReaper) reapProvisioningRecordedSandbox(ctx context.Context, node *domain.Node, sandboxID string, result *ReaperResult) error {
	sandbox, err := r.broker.provider.Get(ctx, sandboxID)
	if errors.Is(err, ErrSandboxNotFound) || sandbox.State == ProviderSandboxAbsent {
		if !r.broker.provisioningDeadlineExpired(node) {
			r.logObserve(ctx, node, sandboxID, "recorded provisioning sandbox absent before deadline")
			return nil
		}
		return r.releaseAbsentProvisioningSandbox(ctx, node, sandboxID, result)
	}
	if err != nil {
		logActionError(ctx, "placement reaper provider get failed", reaperActionFromNode(node, sandboxID, reaperActionObserve, string(node.Placement.State), ""), err)
		return fmt.Errorf("get provisioning sandbox %q for placement %q: %w", sandboxID, node.NodeID, err)
	}
	r.logObserve(ctx, node, sandboxID, "recorded provisioning sandbox still exists")
	return nil
}

func (r *PlacementReaper) releaseAbsentProvisioningSandbox(ctx context.Context, node *domain.Node, sandboxID string, result *ReaperResult) error {
	return r.withFreshNodeAction(ctx, result, node, sandboxID, domain.PlacementStateReleased, reaperActionMarkReleased, func(current *domain.Node) bool {
		return samePlacementCandidate(current, node) &&
			current.Placement.State == domain.PlacementStateProvisioning &&
			strings.TrimSpace(current.Placement.SandboxID) == sandboxID &&
			r.broker.provisioningDeadlineExpired(current)
	}, func(current *domain.Node) error {
		_, err := r.broker.markReleased(ctx, current.WorkspaceKey, current.NodeID, ReleaseFence{
			Generation: current.Placement.Generation,
			SandboxID:  sandboxID,
		})
		return err
	})
}

func (r *PlacementReaper) reapActive(ctx context.Context, node *domain.Node, result *ReaperResult) error {
	sandboxID := strings.TrimSpace(node.Placement.SandboxID)
	if sandboxID == "" {
		return r.deadLetterNode(ctx, result, node, "", fmt.Errorf("active placement %q has empty sandbox id: %w", node.NodeID, domain.ErrInvalid))
	}
	sandbox, err := r.broker.confirmRecordedSandbox(ctx, node, sandboxID)
	if errors.Is(err, ErrSandboxNotFound) || sandbox.State == ProviderSandboxAbsent {
		return r.withFreshNodeAction(ctx, result, node, sandboxID, domain.PlacementStateLost, reaperActionMarkLost, func(current *domain.Node) bool {
			return samePlacementCandidate(current, node) &&
				current.Placement.State == domain.PlacementStateActive &&
				strings.TrimSpace(current.Placement.SandboxID) == sandboxID
		}, func(current *domain.Node) error {
			return r.broker.markLost(ctx, current)
		})
	}
	if err != nil {
		if errors.Is(err, domain.ErrConflict) {
			return r.deadLetterNode(ctx, result, node, sandboxID, err)
		}
		logActionError(ctx, "placement reaper active sandbox get failed", reaperActionFromNode(node, sandboxID, reaperActionObserve, string(node.Placement.State), ""), err)
		return fmt.Errorf("get active sandbox %q for placement %q: %w", sandboxID, node.NodeID, err)
	}
	switch sandbox.State {
	case ProviderSandboxStopped:
		r.logObserve(ctx, node, sandboxID, "active placement sandbox is stopped")
	default:
		if nodeExpired(node, r.now()) {
			r.logObserve(ctx, node, sandboxID, "active placement sandbox is running but node is expired")
			return nil
		}
		r.logObserve(ctx, node, sandboxID, "active placement sandbox is healthy")
	}
	return nil
}

func (r *PlacementReaper) reapReleasing(ctx context.Context, node *domain.Node, result *ReaperResult) error {
	sandboxID := strings.TrimSpace(node.Placement.SandboxID)
	if sandboxID == "" {
		return r.deadLetterNode(ctx, result, node, "", fmt.Errorf("releasing placement %q has empty sandbox id: %w", node.NodeID, domain.ErrInvalid))
	}
	if next := node.Placement.NextDeleteAt; !next.IsZero() && r.now().UTC().Before(next.UTC()) {
		r.logObserve(ctx, node, sandboxID, "releasing placement delete backoff not due")
		return nil
	}
	return r.withFreshNodeAction(ctx, result, node, sandboxID, domain.PlacementStateReleased, reaperActionDeleteReleased, func(current *domain.Node) bool {
		if !samePlacementCandidate(current, node) ||
			current.Placement.State != domain.PlacementStateReleasing ||
			strings.TrimSpace(current.Placement.SandboxID) != sandboxID {
			return false
		}
		next := current.Placement.NextDeleteAt
		return next.IsZero() || !r.now().UTC().Before(next.UTC())
	}, func(current *domain.Node) error {
		if err := r.broker.deleteAndConfirmSandbox(ctx, sandboxID); err != nil {
			attempts := current.Placement.DeleteAttempts + 1
			nextDeleteAt := r.now().UTC().Add(reaperDeleteBackoff(attempts))
			updated, updateErr := r.broker.recordDeleteRetry(ctx, current, attempts, err.Error(), nextDeleteAt)
			if updateErr != nil {
				return errors.Join(err, updateErr)
			}
			if PlacementNeedsAttention(updated) {
				result.DeadLettered++
				r.logAction(ctx, reaperActionFromNode(updated, sandboxID, reaperActionDeleteRetry, string(domain.PlacementStateReleasing), string(domain.PlacementStateReleasing)), slog.LevelWarn)
			}
			return err
		}
		_, err := r.broker.markReleased(ctx, current.WorkspaceKey, current.NodeID, ReleaseFence{
			Generation: current.Placement.Generation,
			SandboxID:  sandboxID,
		})
		return err
	})
}

func (r *PlacementReaper) reapReleased(ctx context.Context, node *domain.Node, result *ReaperResult) error {
	matches, err := r.broker.providerSandboxesForPlacement(ctx, node.NodeID)
	if err != nil {
		if errors.Is(err, domain.ErrConflict) {
			return r.deadLetterNode(ctx, result, node, "", err)
		}
		logActionError(ctx, "placement reaper released sandbox list failed", reaperActionFromNode(node, "", reaperActionObserve, string(node.Placement.State), ""), err)
		return fmt.Errorf("list provider sandboxes for released placement %q: %w", node.NodeID, err)
	}
	for _, sandbox := range matches {
		if sandbox.State == ProviderSandboxRunning {
			return r.deadLetterNode(ctx, result, node, sandbox.ID, fmt.Errorf("released placement %q still has running sandbox %q: %w", node.NodeID, sandbox.ID, domain.ErrConflict))
		}
		r.logObserve(ctx, node, sandbox.ID, "released placement has non-running provider match")
	}
	return nil
}

func (r *PlacementReaper) reapLost(ctx context.Context, node *domain.Node, result *ReaperResult) error {
	matches, err := r.broker.providerSandboxesForPlacement(ctx, node.NodeID)
	if err != nil {
		if errors.Is(err, domain.ErrConflict) {
			return r.deadLetterNode(ctx, result, node, "", err)
		}
		logActionError(ctx, "placement reaper lost sandbox list failed", reaperActionFromNode(node, "", reaperActionObserve, string(node.Placement.State), ""), err)
		return fmt.Errorf("list provider sandboxes for lost placement %q: %w", node.NodeID, err)
	}
	for _, sandbox := range matches {
		return r.deadLetterNode(ctx, result, node, sandbox.ID, fmt.Errorf("lost placement %q reappeared as sandbox %q: %w", node.NodeID, sandbox.ID, domain.ErrConflict))
	}
	return nil
}

func (r *PlacementReaper) reapProviderOrphans(ctx context.Context, result *ReaperResult) error {
	sandboxes, err := r.broker.listProviderSandboxes(ctx, map[string]string{
		EnvironmentLabelKey: r.broker.deploymentID,
	})
	if err != nil {
		slog.ErrorContext(ctx, "placement reaper provider orphan list failed", "error", err)
		return fmt.Errorf("list provider sandboxes for deployment %q: %w", r.broker.deploymentID, err)
	}
	var errs []error
	for _, sandbox := range sandboxes {
		if ctx.Err() != nil {
			return errors.Join(append(errs, ctx.Err())...)
		}
		placementID := strings.TrimSpace(sandbox.Labels[PlacementLabelKey])
		if placementID == "" {
			continue
		}
		result.Examined++
		if err := r.reapProviderOrphan(ctx, sandbox, placementID, result); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func (r *PlacementReaper) reapProviderOrphan(ctx context.Context, sandbox ProviderSandbox, placementID string, result *ReaperResult) error {
	if strings.TrimSpace(sandbox.Labels[EnvironmentLabelKey]) != r.broker.deploymentID {
		return nil
	}
	workspaceKey, agentName, err := r.providerOrphanIdentity(ctx, sandbox, placementID, result)
	if err != nil {
		return err
	}
	exists, err := r.providerOrphanRecordExists(ctx, workspaceKey, placementID, "get")
	if err != nil || exists {
		return err
	}
	if !r.providerOrphanPastGrace(ctx, sandbox, placementID, workspaceKey, agentName) {
		return nil
	}
	return r.deleteProviderOrphan(ctx, sandbox, placementID, workspaceKey, agentName, result)
}

func (r *PlacementReaper) providerOrphanIdentity(ctx context.Context, sandbox ProviderSandbox, placementID string, result *ReaperResult) (string, string, error) {
	workspaceKey := strings.TrimSpace(sandbox.Labels["loom-workspace"])
	agentName := strings.TrimSpace(sandbox.Labels["loom-agent"])
	if workspaceKey != "" && agentName != "" {
		return workspaceKey, agentName, nil
	}
	action := providerOrphanAction(sandbox, placementID, workspaceKey, agentName, reaperStateNeedsAttention, reaperActionDeadLetter)
	result.DeadLettered++
	result.Actions = append(result.Actions, action)
	r.logAction(ctx, action, slog.LevelError)
	return "", "", fmt.Errorf("orphan sandbox %q missing workspace or agent label: %w", sandbox.ID, domain.ErrInvalid)
}

func (r *PlacementReaper) providerOrphanRecordExists(ctx context.Context, workspaceKey, placementID, prefix string) (bool, error) {
	if _, err := r.broker.store.Nodes().Get(ctx, workspaceKey, placementID); err == nil {
		return true, nil
	} else if !errors.Is(err, domain.ErrNotFound) {
		return false, fmt.Errorf("%s placement %q/%q for provider orphan check: %w", prefix, workspaceKey, placementID, err)
	}
	return false, nil
}

func (r *PlacementReaper) providerOrphanPastGrace(ctx context.Context, sandbox ProviderSandbox, placementID, workspaceKey, agentName string) bool {
	if sandbox.CreatedAt.IsZero() || r.now().UTC().Sub(sandbox.CreatedAt.UTC()) < r.grace {
		r.logAction(ctx, providerOrphanAction(sandbox, placementID, workspaceKey, agentName, reaperStateOrphan, reaperActionObserve), slog.LevelInfo)
		return false
	}
	return true
}

func (r *PlacementReaper) deleteProviderOrphan(ctx context.Context, sandbox ProviderSandbox, placementID, workspaceKey, agentName string, result *ReaperResult) error {
	unlock := r.broker.lockPlacement(workspaceKey, agentName)
	defer unlock()
	exists, err := r.providerOrphanRecordExists(ctx, workspaceKey, placementID, "fresh get")
	if err != nil || exists {
		return err
	}
	action := providerOrphanAction(sandbox, placementID, workspaceKey, agentName, reaperStateDeleted, reaperActionDeleteOrphan)
	result.Acted++
	result.Actions = append(result.Actions, action)
	r.logAction(ctx, action, slog.LevelInfo)
	if !r.enforce {
		return nil
	}
	return r.broker.deleteAndConfirmSandbox(ctx, sandbox.ID)
}

func providerOrphanAction(sandbox ProviderSandbox, placementID, workspaceKey, agentName, toState, actionName string) ReaperAction {
	return ReaperAction{
		NodeID:    placementID,
		Workspace: workspaceKey,
		Agent:     agentName,
		SandboxID: strings.TrimSpace(sandbox.ID),
		FromState: reaperStateOrphan,
		ToState:   toState,
		Action:    actionName,
	}
}

func (r *PlacementReaper) withFreshNodeAction(
	ctx context.Context,
	result *ReaperResult,
	candidate *domain.Node,
	sandboxID string,
	toState domain.PlacementState,
	actionName string,
	stillCandidate func(*domain.Node) bool,
	mutate func(*domain.Node) error,
) error {
	agentName := placementAgentName(candidate)
	if agentName == "" {
		return r.deadLetterNode(ctx, result, candidate, sandboxID, fmt.Errorf("placement %q agent missing: %w", candidate.NodeID, domain.ErrInvalid))
	}
	unlock := r.broker.lockPlacement(candidate.WorkspaceKey, agentName)
	defer unlock()
	current, err := r.broker.Get(ctx, candidate.WorkspaceKey, candidate.NodeID)
	if errors.Is(err, domain.ErrNotFound) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("fresh get placement %q/%q: %w", candidate.WorkspaceKey, candidate.NodeID, err)
	}
	if !stillCandidate(current) {
		r.logObserve(ctx, current, sandboxID, "placement candidate changed before reaper mutation")
		return nil
	}
	action := reaperActionFromNode(current, sandboxID, actionName, string(current.Placement.State), string(toState))
	result.Acted++
	result.Actions = append(result.Actions, action)
	r.logAction(ctx, action, slog.LevelInfo)
	if !r.enforce {
		return nil
	}
	return mutate(current)
}

func samePlacementCandidate(current, candidate *domain.Node) bool {
	return current != nil &&
		current.Placement != nil &&
		candidate != nil &&
		candidate.Placement != nil &&
		current.Placement.Generation == candidate.Placement.Generation
}

func (r *PlacementReaper) deadLetterNode(ctx context.Context, result *ReaperResult, node *domain.Node, sandboxID string, err error) error {
	action := reaperActionFromNode(node, sandboxID, reaperActionDeadLetter, string(node.Placement.State), reaperStateNeedsAttention)
	result.DeadLettered++
	result.Actions = append(result.Actions, action)
	r.logAction(ctx, action, slog.LevelError)
	logActionError(ctx, "placement reaper dead-lettered placement", action, err)
	return err
}

func (r *PlacementReaper) logObserve(ctx context.Context, node *domain.Node, sandboxID, message string) {
	action := reaperActionFromNode(node, sandboxID, reaperActionObserve, string(node.Placement.State), string(node.Placement.State))
	attrs := append(actionLogAttrs(action), slog.String("reason", message))
	slog.LogAttrs(ctx, slog.LevelInfo, "placement reaper observed placement", attrs...)
}

func (r *PlacementReaper) logAction(ctx context.Context, action ReaperAction, level slog.Level) {
	logger := slog.Default()
	if !logger.Enabled(ctx, level) {
		return
	}
	logger.LogAttrs(ctx, level, "placement reaper action", actionLogAttrs(action)...)
}

func reaperActionFromNode(node *domain.Node, sandboxID, actionName, fromState, toState string) ReaperAction {
	action := ReaperAction{
		SandboxID: strings.TrimSpace(sandboxID),
		FromState: fromState,
		ToState:   toState,
		Action:    actionName,
	}
	if node == nil {
		return action
	}
	action.NodeID = node.NodeID
	action.Workspace = node.WorkspaceKey
	action.Agent = placementAgentName(node)
	if node.Placement != nil {
		action.Generation = node.Placement.Generation
	}
	if action.SandboxID == "" && node.Placement != nil {
		action.SandboxID = strings.TrimSpace(node.Placement.SandboxID)
	}
	return action
}

func actionLogAttrs(action ReaperAction) []slog.Attr {
	return []slog.Attr{
		slog.String("nodeID", action.NodeID),
		slog.String("ws", action.Workspace),
		slog.String("agent", action.Agent),
		slog.String("sandboxID", action.SandboxID),
		slog.Int64("generation", action.Generation),
		slog.String("from_state", action.FromState),
		slog.String("to_state", action.ToState),
		slog.String("action", action.Action),
	}
}

func logActionError(ctx context.Context, message string, action ReaperAction, err error) {
	attrs := append(actionLogAttrs(action), slog.Any("error", err))
	slog.LogAttrs(ctx, slog.LevelError, message, attrs...)
}

func nodeExpired(node *domain.Node, now time.Time) bool {
	return node != nil && !node.ExpiresAt.IsZero() && !now.UTC().Before(node.ExpiresAt.UTC())
}

func reaperDeleteBackoff(attempts int) time.Duration {
	if attempts < 0 {
		attempts = 0
	}
	backoff := reaperDeleteBackoffBase
	for range attempts {
		if backoff >= reaperDeleteBackoffMax/2 {
			return reaperDeleteBackoffMax
		}
		backoff *= 2
	}
	if backoff > reaperDeleteBackoffMax {
		return reaperDeleteBackoffMax
	}
	return backoff
}

func (b *Broker) recordDeleteRetry(ctx context.Context, node *domain.Node, attempts int, lastErr string, nextDeleteAt time.Time) (*domain.Node, error) {
	if node == nil || node.Placement == nil {
		return nil, fmt.Errorf("placement record required: %w", domain.ErrInvalid)
	}
	placement := clonePlacement(node.Placement)
	placement.DeleteAttempts = attempts
	placement.LastDeleteError = strings.TrimSpace(lastErr)
	placement.NextDeleteAt = nextDeleteAt.UTC()
	placementPtr := &placement
	updated, err := b.store.Nodes().Update(ctx, node.WorkspaceKey, node.NodeID, store.NodeUpdate{Placement: &placementPtr})
	if err != nil {
		return nil, err
	}
	if updated == nil || updated.Placement == nil {
		return nil, fmt.Errorf("record delete retry for placement %q returned no placement: %w", node.NodeID, domain.ErrInvalid)
	}
	return updated, nil
}

// PlacementNeedsAttention derives whether a placement needs operator attention.
func PlacementNeedsAttention(node *domain.Node) bool {
	return node != nil &&
		node.Placement != nil &&
		(node.Placement.DeleteAttempts >= reaperDeadLetterThreshold ||
			node.Placement.State == domain.PlacementStateLost)
}
