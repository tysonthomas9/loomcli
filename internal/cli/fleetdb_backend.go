package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"sync"
	"time"
)

// FleetService provides typed access to fleet-db operations using loomcli types.
// This interface decouples fleetDBBackend from fleet-db's internal packages.
// The adapter (implemented in task .4) converts between fleet-db and loomcli types.
type FleetService interface {
	// Queries
	GetReady(ctx context.Context, limit int, parentID string) ([]BdIssue, error)
	ListIssues(ctx context.Context, status, issueType, assignee string, limit int) ([]BdIssue, error)
	GetBlocked(ctx context.Context) ([]BdIssue, error)
	CountByStatus(ctx context.Context) (BdStats, error)
	GetIssue(ctx context.Context, id string) (*BdIssue, error)
	GetIssueText(ctx context.Context, id string) (string, error)
	GetDependencies(ctx context.Context, id string) ([]Dependency, error)

	// Mutations
	ClaimIssue(ctx context.Context, id string) error
	CloseIssue(ctx context.Context, id, reason string) error
	ReopenIssue(ctx context.Context, id string) error
	DeferIssue(ctx context.Context, id string, until time.Time) error
	AssignIssue(ctx context.Context, id, assignee string) error
	UpdateFields(ctx context.Context, id string, fields map[string]*string) error
}

// fleetDBBackend implements IssueTracker by wrapping a FleetService.
// It provides a drop-in replacement for bdBackend: callers invoke bd-style CLI
// commands via RunCommand and the backend dispatches to typed service methods,
// returning JSON matching bd's output format exactly.
type fleetDBBackend struct {
	svc    FleetService
	logger *slog.Logger
}

// newFleetDBBackend creates a fleetDBBackend wrapping the given FleetService.
func newFleetDBBackend(svc FleetService, logger *slog.Logger) *fleetDBBackend {
	return &fleetDBBackend{
		svc:    svc,
		logger: logger,
	}
}

// RunCommand parses bd-style CLI args and dispatches to the appropriate
// fleet-db service method. Implements IssueBackend.
func (f *fleetDBBackend) RunCommand(_ string, args ...string) (string, error) {
	if len(args) == 0 {
		return "", fmt.Errorf("fleetdb: no command specified")
	}
	ctx := context.Background()
	switch args[0] {
	case "ready":
		return f.handleReady(ctx, args[1:])
	case "list":
		return f.handleList(ctx, args[1:])
	case "blocked":
		return f.handleBlocked(ctx, args[1:])
	case "stats":
		return f.handleStats(ctx, args[1:])
	case "show":
		return f.handleShow(ctx, args[1:])
	case "update":
		return f.handleUpdate(ctx, args[1:])
	case "close":
		return f.handleClose(ctx, args[1:])
	case "sync":
		return "synced\n", nil
	case "daemon":
		return "", nil
	default:
		return "", fmt.Errorf("fleetdb: unknown command: %s", args[0])
	}
}

// --- Typed IssueTracker methods ---

func (f *fleetDBBackend) Ready(ctx context.Context, opts ReadyOpts) ([]BdIssue, error) {
	issues, err := f.svc.GetReady(ctx, opts.Limit, opts.ParentID)
	if err != nil {
		return nil, fmt.Errorf("fleetdb ready: %w", err)
	}
	return f.hydrateIssuesWithDeps(ctx, issues)
}

func (f *fleetDBBackend) List(ctx context.Context, opts ListOpts) ([]BdIssue, error) {
	issues, err := f.svc.ListIssues(ctx, opts.Status, opts.IssueType, opts.Assignee, opts.Limit)
	if err != nil {
		return nil, fmt.Errorf("fleetdb list: %w", err)
	}
	return issues, nil
}

func (f *fleetDBBackend) Blocked(ctx context.Context) ([]BdIssue, error) {
	issues, err := f.svc.GetBlocked(ctx)
	if err != nil {
		return nil, fmt.Errorf("fleetdb blocked: %w", err)
	}
	return issues, nil
}

func (f *fleetDBBackend) Stats(ctx context.Context) (BdStats, error) {
	stats, err := f.svc.CountByStatus(ctx)
	if err != nil {
		return BdStats{}, fmt.Errorf("fleetdb stats: %w", err)
	}
	return stats, nil
}

func (f *fleetDBBackend) GetIssue(ctx context.Context, id string) (*BdIssue, error) {
	issue, err := f.svc.GetIssue(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("fleetdb show %s: %w", id, err)
	}
	if issue == nil {
		return nil, fmt.Errorf("fleetdb show %s: not found", id)
	}
	deps, err := f.svc.GetDependencies(ctx, id)
	if err != nil {
		f.logger.Warn("fleetdb: failed to fetch dependencies", "issue", id, "error", err)
		deps = []Dependency{}
	}
	issue.Dependencies = deps
	return issue, nil
}

func (f *fleetDBBackend) GetIssueText(ctx context.Context, id string) (string, error) {
	text, err := f.svc.GetIssueText(ctx, id)
	if err != nil {
		return "", fmt.Errorf("fleetdb show %s: %w", id, err)
	}
	return text, nil
}

func (f *fleetDBBackend) UpdateStatus(ctx context.Context, id, status, assignee string) error {
	if err := f.applyStatusChange(ctx, id, status); err != nil {
		return fmt.Errorf("fleetdb update %s: %w", id, err)
	}
	if assignee != "" {
		if err := f.svc.AssignIssue(ctx, id, assignee); err != nil {
			return fmt.Errorf("fleetdb update %s: %w", id, err)
		}
	}
	return nil
}

func (f *fleetDBBackend) UpdateExternalRef(_ context.Context, id, _ string) error {
	f.logger.Warn("fleetdb: --external-ref not yet supported, ignoring", "issue", id)
	return nil
}

func (f *fleetDBBackend) CloseIssue(ctx context.Context, id, reason string) error {
	if err := f.svc.CloseIssue(ctx, id, reason); err != nil {
		return fmt.Errorf("fleetdb close %s: %w", id, err)
	}
	return nil
}

func (f *fleetDBBackend) SyncStatus(_ context.Context) (string, error) {
	return "synced", nil
}

func (f *fleetDBBackend) BackendName() string {
	return "fleetdb"
}

// --- RunCommand handlers ---

func (f *fleetDBBackend) handleReady(ctx context.Context, args []string) (string, error) {
	pa := parseArgs(args)
	var limit int
	if v, ok := pa["limit"]; ok {
		if n, err := strconv.Atoi(v); err == nil {
			limit = n
		}
	}
	parentID := pa["parent"]

	issues, err := f.svc.GetReady(ctx, limit, parentID)
	if err != nil {
		return "", fmt.Errorf("fleetdb ready: %w", err)
	}
	hydrated, err := f.hydrateIssuesWithDeps(ctx, issues)
	if err != nil {
		return "", fmt.Errorf("fleetdb ready: %w", err)
	}
	return marshalJSON(hydrated)
}

func (f *fleetDBBackend) handleList(ctx context.Context, args []string) (string, error) {
	pa := parseArgs(args)
	status := pa["status"]
	issueType := pa["type"]
	assignee := pa["assignee"]
	var limit int
	if v, ok := pa["limit"]; ok {
		if n, err := strconv.Atoi(v); err == nil {
			limit = n
		}
	}
	issues, err := f.svc.ListIssues(ctx, status, issueType, assignee, limit)
	if err != nil {
		return "", fmt.Errorf("fleetdb list: %w", err)
	}
	return marshalJSON(issues)
}

func (f *fleetDBBackend) handleBlocked(ctx context.Context, _ []string) (string, error) {
	issues, err := f.svc.GetBlocked(ctx)
	if err != nil {
		return "", fmt.Errorf("fleetdb blocked: %w", err)
	}
	return marshalJSON(issues)
}

func (f *fleetDBBackend) handleStats(ctx context.Context, _ []string) (string, error) {
	stats, err := f.svc.CountByStatus(ctx)
	if err != nil {
		return "", fmt.Errorf("fleetdb stats: %w", err)
	}
	return marshalJSON(stats)
}

func (f *fleetDBBackend) handleShow(ctx context.Context, args []string) (string, error) {
	pa := parseArgs(args)
	id, ok := pa["_0"]
	if !ok {
		return "", fmt.Errorf("fleetdb show: issue ID required")
	}

	if _, hasJSON := pa["json"]; hasJSON {
		issue, err := f.GetIssue(ctx, id)
		if err != nil {
			return "", err // already wrapped by GetIssue
		}
		return marshalJSON([]BdIssue{*issue})
	}

	text, err := f.GetIssueText(ctx, id)
	if err != nil {
		return "", err // already wrapped by GetIssueText
	}
	return text, nil
}

func (f *fleetDBBackend) handleUpdate(ctx context.Context, args []string) (string, error) {
	pa := parseArgs(args)
	id, ok := pa["_0"]
	if !ok {
		return "", fmt.Errorf("fleetdb update: issue ID required")
	}

	// Handle status change.
	if status, ok := pa["status"]; ok {
		if err := f.applyStatusChange(ctx, id, status); err != nil {
			return "", fmt.Errorf("fleetdb update %s: %w", id, err)
		}
	}

	// Handle assignee change.
	if assignee, ok := pa["assignee"]; ok {
		if err := f.svc.AssignIssue(ctx, id, assignee); err != nil {
			return "", fmt.Errorf("fleetdb update %s: %w", id, err)
		}
	}

	// Handle external-ref (Phase 1: warn + noop).
	if _, ok := pa["external-ref"]; ok {
		f.logger.Warn("fleetdb: --external-ref not yet supported, ignoring", "issue", id)
	}

	// Handle design/notes/title/description updates.
	fields := make(map[string]*string)
	for _, key := range []string{"design", "notes", "title", "description"} {
		if v, ok := pa[key]; ok {
			fields[key] = &v
		}
	}
	if len(fields) > 0 {
		if err := f.svc.UpdateFields(ctx, id, fields); err != nil {
			return "", fmt.Errorf("fleetdb update %s: %w", id, err)
		}
	}

	return fmt.Sprintf("✓ Updated issue: %s\n", id), nil
}

func (f *fleetDBBackend) handleClose(ctx context.Context, args []string) (string, error) {
	pa := parseArgs(args)
	id, ok := pa["_0"]
	if !ok {
		return "", fmt.Errorf("fleetdb close: issue ID required")
	}
	reason := pa["reason"]
	if err := f.svc.CloseIssue(ctx, id, reason); err != nil {
		return "", fmt.Errorf("fleetdb close %s: %w", id, err)
	}
	return fmt.Sprintf("✓ Closed issue: %s\n", id), nil
}

// --- Status dispatch ---

// applyStatusChange maps a bd-style status string to the appropriate
// fleet-db service method.
func (f *fleetDBBackend) applyStatusChange(ctx context.Context, id, status string) error {
	switch status {
	case "in_progress":
		return f.svc.ClaimIssue(ctx, id)
	case "closed":
		return f.svc.CloseIssue(ctx, id, "")
	case "open":
		return f.svc.ReopenIssue(ctx, id)
	case "deferred":
		return f.svc.DeferIssue(ctx, id, time.Now().Add(30*24*time.Hour))
	default:
		return fmt.Errorf("unsupported status %q: no fleet-db operation mapped", status)
	}
}

// --- Dependency hydration ---

// hydrateIssuesWithDeps fetches dependencies for each issue with bounded concurrency.
func (f *fleetDBBackend) hydrateIssuesWithDeps(ctx context.Context, issues []BdIssue) ([]BdIssue, error) {
	if len(issues) == 0 {
		return issues, nil
	}

	type depResult struct {
		idx  int
		deps []Dependency
	}

	var wg sync.WaitGroup
	sem := make(chan struct{}, 10) // bounded concurrency
	ch := make(chan depResult, len(issues))

	for i, issue := range issues {
		wg.Add(1)
		go func(idx int, id string) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			deps, err := f.svc.GetDependencies(ctx, id)
			if err != nil {
				f.logger.Warn("fleetdb: failed to fetch dependencies",
					"issue", id, "error", err)
				ch <- depResult{idx: idx, deps: []Dependency{}}
				return
			}
			ch <- depResult{idx: idx, deps: deps}
		}(i, issue.ID)
	}

	go func() {
		wg.Wait()
		close(ch)
	}()

	result := make([]BdIssue, len(issues))
	copy(result, issues)
	for dr := range ch {
		result[dr.idx].Dependencies = dr.deps
	}

	return result, nil
}

// --- Arg parsing ---

// parseArgs parses bd-style CLI flags into a map.
// Supports --flag=value, --flag value, and --flag (boolean = "true").
// Positional args are stored as _0, _1, etc.
func parseArgs(args []string) map[string]string {
	m := make(map[string]string)
	posIdx := 0
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if !strings.HasPrefix(arg, "--") {
			m[fmt.Sprintf("_%d", posIdx)] = arg
			posIdx++
			continue
		}
		key := strings.TrimPrefix(arg, "--")
		if idx := strings.IndexByte(key, '='); idx >= 0 {
			m[key[:idx]] = key[idx+1:]
			continue
		}
		// Check if next arg is a value (not a flag).
		// Treat anything starting with "-" as a flag, not a value.
		if i+1 < len(args) && !strings.HasPrefix(args[i+1], "-") {
			m[key] = args[i+1]
			i++
		} else {
			m[key] = "true"
		}
	}
	return m
}

// --- JSON helpers ---

// marshalJSON marshals a value to a JSON string with a trailing newline.
func marshalJSON(v interface{}) (string, error) {
	data, err := json.Marshal(v)
	if err != nil {
		return "", fmt.Errorf("fleetdb: marshal: %w", err)
	}
	return string(data) + "\n", nil
}
