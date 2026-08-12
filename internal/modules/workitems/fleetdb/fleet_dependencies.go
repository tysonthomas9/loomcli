package fleetdb

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/tysonthomas9/loomcli/internal/modules/workitems"
)

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

func (b *Adapter) ListDependencies(ctx context.Context, query workitems.ListDependenciesQuery) ([]workitems.Dependency, error) {
	dependencies, _, err := b.fetchDependencies(ctx, query.IssueID)
	if err != nil {
		return nil, err
	}
	if dependencies == nil {
		dependencies = []workitems.Dependency{}
	}
	return dependencies, nil
}

// addCreateDependencies adds the requested blocks-edges for a freshly created
// issue. fleet-db's CreateIssueRequest has no inline dependencies field, so
// Create composes the dedicated POST /issues/{id}/deps calls after the issue
// exists — the same compose-after-write pattern Update uses for labels. On
// failure the returned error names the already-created issue so callers know
// the create itself succeeded and which edge was lost.
func (b *Adapter) addCreateDependencies(ctx context.Context, id string, deps []string) error {
	for _, depID := range deps {
		err := b.AddDependency(ctx, workitems.AddDependencyCommand{IssueID: id, DependsOnID: depID, Type: "blocks"})
		if err == nil {
			continue
		}
		kind := workitems.KindInternal
		var be *workitems.OperationError
		if errors.As(err, &be) {
			kind = be.Kind
		}
		return workitems.NewOperationError(kind, "Create",
			fmt.Sprintf("issue %s was created, but adding its dependency on %s failed", id, depID), err)
	}
	return nil
}

// fetchDependencies calls fleet-db's GET /issues/{id}/deps and projects the
// native payload into the Work Items dependency projection, split by perspective relative to
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
func (b *Adapter) fetchDependencies(ctx context.Context, id string) (deps, dependents []workitems.Dependency, err error) {
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
	for _, d := range wrap.Dependencies {
		dep, isDependent := b.depWireToData(ctx, d, id)
		if isDependent {
			dependents = append(dependents, dep)
		} else {
			deps = append(deps, dep)
		}
	}
	return deps, dependents, nil
}

// depWireToData converts one /deps wire row into a DependencyData and reports
// whether it is a dependent (the other issue depends on viewID) rather than a
// dependency (viewID depends on the other issue). Missing display fields are
// hydrated from the *related* issue's summary — the side that is not viewID —
// so an epic's children carry their own metadata rather than the epic's.
func (b *Adapter) depWireToData(ctx context.Context, d fleetDepWire, viewID string) (workitems.Dependency, bool) {
	relatedID := d.DependsOnID
	isDependent := false
	if d.DependsOnID == viewID && d.IssueID != "" {
		relatedID = d.IssueID
		isDependent = true
	}
	dep := workitems.Dependency{
		ID:             relatedID,
		DependencyType: d.Type,
		Title:          d.Title,
		Status:         d.Status,
		Priority:       d.Priority,
		IssueType:      d.IssueType,
		CreatedAt:      d.CreatedAt,
		CreatedBy:      d.CreatedBy,
	}
	if dep.Title == "" {
		if issue, err := b.fetchIssueSummary(ctx, relatedID); err == nil && issue != nil {
			hydrateDependencyData(&dep, *issue)
		}
	}
	return dep, isDependent
}

func (b *Adapter) fetchIssueSummary(ctx context.Context, id string) (*workitems.IssueSummary, error) {
	if strings.TrimSpace(id) == "" {
		return nil, workitems.AdapterInvalid("GetDependency", "depends_on_id is required")
	}
	resp, err := b.exec(ctx, "GetDependency", "GET", "/issues/"+url.PathEscape(id), nil)
	if err != nil {
		return nil, err
	}
	if !hasData(resp) {
		return nil, workitems.AdapterNotFound("GetDependency", "issue not found")
	}
	var wire fleetIssueWithCountsWire
	if err := json.Unmarshal(resp.Data, &wire); err != nil {
		return nil, workitems.AdapterInternal("GetDependency", "unmarshal response", err)
	}
	data := wire.toIssueSummary()
	data.Labels = append([]string(nil), wire.Labels...)
	return &data, nil
}

func hydrateDependencyData(dep *workitems.Dependency, issue workitems.IssueSummary) {
	if dep.ID == "" {
		dep.ID = issue.ID
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

func (b *Adapter) waitForDependencyState(ctx context.Context, op, fromID, toID string, wantPresent bool) error {
	ticker := time.NewTicker(25 * time.Millisecond)
	defer ticker.Stop()
	timeout := time.NewTimer(2 * time.Second)
	defer timeout.Stop()

	var lastErr error
	for {
		detail, err := b.Get(ctx, workitems.GetQuery{IssueID: fromID})
		if err == nil && detail != nil && containsDependency(detail.Dependencies, toID) == wantPresent {
			return nil
		}
		if err != nil {
			lastErr = err
		}

		select {
		case <-ctx.Done():
			return workitems.AdapterTimeout(op, "dependency projection did not settle", ctx.Err())
		case <-timeout.C:
			return workitems.AdapterTimeout(op, "dependency projection did not settle", lastErr)
		case <-ticker.C:
		}
	}
}

func (b *Adapter) clearBlockedStatusAfterDependencyRemoval(ctx context.Context, id string) error {
	detail, err := b.Get(ctx, workitems.GetQuery{IssueID: id})
	if err != nil {
		return err
	}
	if detail == nil || detail.Status != string(workitems.StatusBlocked) || len(detail.Dependencies) > 0 {
		return nil
	}
	_, err = b.exec(ctx, "RemoveDependency", "PATCH", "/issues/"+url.PathEscape(id), map[string]interface{}{
		"status": string(workitems.StatusOpen),
	})
	return err
}

func containsDependency(values []workitems.Dependency, toID string) bool {
	for _, value := range values {
		if value.ID == toID {
			return true
		}
	}
	return false
}
