package fleet

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/tysonthomas9/loomcli/internal/backend"
	"github.com/tysonthomas9/loomcli/internal/types"
)

// Compile-time proof that the fleet backend satisfies the one-round-trip
// summary contract; see GetSummary.
var _ backend.IssueSummaryBackend = (*FleetBackend)(nil)

// fleetDepWire is one row from fleet-db's GET /issues/{id}/deps response. Each
// row links IssueID -> DependsOnID with a relationship Type.
type fleetDepWire struct {
	IssueID     string    `json:"issue_id"`
	DependsOnID string    `json:"depends_on_id"`
	Type        string    `json:"type"`
	Title       string    `json:"title,omitempty"`
	Status      string    `json:"status,omitempty"`
	Priority    int       `json:"priority,omitempty"`
	IssueType   string    `json:"issue_type,omitempty"`
	CreatedAt   time.Time `json:"created_at,omitempty"`
	CreatedBy   string    `json:"created_by,omitempty"`
}

// addCreateDependencies adds the requested blocks-edges for a freshly created
// issue. fleet-db's CreateIssueRequest has no inline dependencies field, so
// Create composes the dedicated POST /issues/{id}/deps calls after the issue
// exists — the same compose-after-write pattern Update uses for labels. On
// failure the returned error names the already-created issue so callers know
// the create itself succeeded and which edge was lost.
func (b *FleetBackend) addCreateDependencies(ctx context.Context, id string, deps []string) error {
	for _, depID := range deps {
		err := b.AddDependency(ctx, backend.DepAddParams{FromID: id, ToID: depID, DepType: "blocks"})
		if err == nil {
			continue
		}
		kind := backend.KindInternal
		var be *backend.BackendError
		if errors.As(err, &be) {
			kind = be.Kind
		}
		return backend.NewBackendError(kind, "Create",
			fmt.Sprintf("issue %s was created, but adding its dependency on %s failed", id, depID), err)
	}
	return nil
}

// fetchDependencies calls fleet-db's GET /issues/{id}/deps and projects the
// native payload into backend.DependencyData, split by perspective relative to
// id:
//   - deps:       rows where the viewed issue depends on the other issue
//     (issue_id == id). The related issue is depends_on_id.
//   - dependents: rows where the other issue depends on the viewed issue
//     (depends_on_id == id), e.g. an epic's children. The related issue is
//     issue_id.
//
// The single /deps endpoint returns both kinds (fleet-db stores parent-child
// rows on the child's side), so we must classify each row rather than assume
// every row is a dependency.
func (b *FleetBackend) fetchDependencies(ctx context.Context, id string) (deps, dependents []backend.DependencyData, err error) {
	resp, err := b.exec(ctx, "Get", "GET", "/issues/"+url.PathEscape(id)+"/deps", nil)
	if err != nil {
		return nil, nil, err
	}
	if !hasData(resp) {
		return nil, nil, nil
	}
	var wrap struct {
		Dependencies []fleetDepWire `json:"dependencies"`
	}
	if json.Unmarshal(resp.Data, &wrap) != nil {
		return nil, nil, nil
	}
	rows := make([]depRow, 0, len(wrap.Dependencies))
	for _, d := range wrap.Dependencies {
		rows = append(rows, depWireToRow(d, id))
	}
	b.hydrateDepRows(ctx, rows)
	for _, row := range rows {
		if row.isDependent {
			dependents = append(dependents, row.data)
		} else {
			deps = append(deps, row.data)
		}
	}
	return deps, dependents, nil
}

// Dependency-row hydration bounds. fleet-db's /deps rows serialize only the
// edge (issue_id, depends_on_id, type, created_*), so the display fields are
// always empty and every distinct related issue needs a summary lookup. These
// cap what that is allowed to cost: a wide epic must not fan out one request
// per child.
const (
	// depHydrateMax is the largest number of distinct related issues worth
	// hydrating. Past it no request is issued at all and rows keep whatever
	// the wire gave them — a bare ID renders, which beats a 200-request
	// `loom data show`.
	depHydrateMax = 25
	// depHydrateConcurrency bounds in-flight hydration requests.
	depHydrateConcurrency = 8
	// depHydrateTimeout caps the whole hydration pass; display metadata must
	// never hold up the dependency projection for long.
	depHydrateTimeout = 2 * time.Second
)

// depRow is one /deps wire row projected into backend form, carrying the
// classification and the related issue's ID so hydration can be hoisted out of
// the projection loop.
type depRow struct {
	data        backend.DependencyData
	relatedID   string
	isDependent bool
}

// depWireToRow converts one /deps wire row into a DependencyData and reports
// which issue is the *related* one — the side that is not viewID — along with
// whether the row is a dependent (the other issue depends on viewID) rather
// than a dependency (viewID depends on the other issue).
//
// It is a pure wire→data projection: no context, no I/O. Filling in missing
// display fields is the caller's job, so it can be batched.
func depWireToRow(d fleetDepWire, viewID string) depRow {
	row := depRow{
		data: backend.DependencyData{
			IssueID:     d.IssueID,
			DependsOnID: d.DependsOnID,
			Type:        d.Type,
			Title:       d.Title,
			Status:      d.Status,
			Priority:    d.Priority,
			IssueType:   d.IssueType,
			CreatedAt:   d.CreatedAt,
			CreatedBy:   d.CreatedBy,
		},
		relatedID: d.DependsOnID,
	}
	if d.DependsOnID == viewID && d.IssueID != "" {
		row.relatedID = d.IssueID
		row.isDependent = true
	}
	return row
}

// hydrateDepRows fills the display fields of rows the wire left empty, in
// place. Lookups are deduplicated by related ID (an epic's rows frequently
// repeat one), bounded by depHydrateMax, and run concurrently under a short
// sub-context. Every failure is skipped: a row without a title degrades to a
// bare ID, never to a failed projection.
func (b *FleetBackend) hydrateDepRows(ctx context.Context, rows []depRow) {
	var (
		needed []string
		seen   = make(map[string]bool, len(rows))
	)
	for i := range rows {
		id := rows[i].relatedID
		if rows[i].data.Title != "" || strings.TrimSpace(id) == "" || seen[id] {
			continue
		}
		seen[id] = true
		needed = append(needed, id)
	}
	if len(needed) == 0 {
		return
	}
	if len(needed) > depHydrateMax {
		slog.Debug("dependency hydration skipped: over cap",
			"needed", len(needed), "cap", depHydrateMax)
		return
	}

	summaries := b.fetchIssueSummaries(ctx, needed)
	for i := range rows {
		if rows[i].data.Title != "" {
			continue
		}
		if issue, ok := summaries[rows[i].relatedID]; ok {
			hydrateDependencyData(&rows[i].data, issue)
		}
	}
}

// fetchIssueSummaries resolves the given distinct issue IDs concurrently,
// returning whatever came back. Misses are logged at debug and dropped.
func (b *FleetBackend) fetchIssueSummaries(ctx context.Context, ids []string) map[string]backend.IssueData {
	fetchCtx, cancel := context.WithTimeout(ctx, depHydrateTimeout)
	defer cancel()

	var (
		mu     sync.Mutex
		wg     sync.WaitGroup
		out    = make(map[string]backend.IssueData, len(ids))
		tokens = make(chan struct{}, depHydrateConcurrency)
	)
	for _, id := range ids {
		wg.Add(1)
		go func(id string) {
			defer wg.Done()
			tokens <- struct{}{}
			defer func() { <-tokens }()

			issue, err := b.fetchIssueSummary(fetchCtx, id)
			if err != nil || issue == nil {
				slog.Debug("dependency hydration: summary failed", "issue_id", id, "err", err)
				return
			}
			mu.Lock()
			out[id] = *issue
			mu.Unlock()
		}(id)
	}
	wg.Wait()
	return out
}

// GetSummary returns the slim issue record in a single round-trip, without the
// dependency and comment lookups Get performs. It implements
// backend.IssueSummaryBackend for callers that only need scalar fields.
func (b *FleetBackend) GetSummary(ctx context.Context, id string) (*backend.IssueData, error) {
	return b.fetchIssueSummary(ctx, id)
}

func (b *FleetBackend) fetchIssueSummary(ctx context.Context, id string) (*backend.IssueData, error) {
	if strings.TrimSpace(id) == "" {
		return nil, backend.ErrValidation("GetDependency", "depends_on_id is required")
	}
	resp, err := b.exec(ctx, "GetDependency", "GET", "/issues/"+url.PathEscape(id), nil)
	if err != nil {
		return nil, err
	}
	if !hasData(resp) {
		return nil, backend.ErrNotFound("GetDependency", "issue not found")
	}
	var wire fleetIssueWithCountsWire
	if err := json.Unmarshal(resp.Data, &wire); err != nil {
		return nil, backend.ErrInternal("GetDependency", "unmarshal response", err)
	}
	data := wire.toIssueData()
	data.Labels = append([]string(nil), wire.Labels...)
	return &data, nil
}

func hydrateDependencyData(dep *backend.DependencyData, issue backend.IssueData) {
	if dep.DependsOnID == "" {
		dep.DependsOnID = issue.ID
	}
	if dep.Title == "" {
		dep.Title = issue.Title
	}
	if dep.Status == "" {
		dep.Status = issue.Status
	}
	if dep.Priority == 0 {
		dep.Priority = issue.Priority
	}
	if dep.IssueType == "" {
		dep.IssueType = issue.IssueType
	}
	if dep.CreatedAt.IsZero() {
		dep.CreatedAt = issue.CreatedAt
	}
	if dep.CreatedBy == "" {
		dep.CreatedBy = issue.CreatedBy
	}
}

func (b *FleetBackend) waitForDependencyState(ctx context.Context, op, fromID, toID string, wantPresent bool) error {
	ticker := time.NewTicker(25 * time.Millisecond)
	defer ticker.Stop()
	timeout := time.NewTimer(2 * time.Second)
	defer timeout.Stop()

	var lastErr error
	for {
		detail, err := b.Get(ctx, fromID)
		if err == nil && detail != nil && containsDependency(detail.Dependencies, toID) == wantPresent {
			return nil
		}
		if err != nil {
			lastErr = err
		}

		select {
		case <-ctx.Done():
			return backend.ErrTimeout(op, "dependency projection did not settle", ctx.Err())
		case <-timeout.C:
			return backend.ErrTimeout(op, "dependency projection did not settle", lastErr)
		case <-ticker.C:
		}
	}
}

func (b *FleetBackend) clearBlockedStatusAfterDependencyRemoval(ctx context.Context, id, actor string) error {
	detail, err := b.Get(ctx, id)
	if err != nil {
		return err
	}
	if detail == nil || detail.Status != string(types.StatusBlocked) || len(detail.Dependencies) > 0 {
		return nil
	}
	err = b.execAsActor(ctx, "RemoveDependency", "PATCH", "/issues/"+url.PathEscape(id), map[string]interface{}{
		"status": string(types.StatusOpen),
	}, actor)
	return err
}

func containsDependency(values []backend.DependencyData, toID string) bool {
	for _, value := range values {
		if value.DependsOnID == toID {
			return true
		}
	}
	return false
}
