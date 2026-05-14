package fleet

import (
	"context"
	"encoding/json"
	"net/url"
	"strings"
	"time"

	"github.com/tysonthomas9/loomcli/internal/backend"
	"github.com/tysonthomas9/loomcli/internal/types"
)

// fetchDependencies calls fleet-db's GET /issues/{id}/deps and projects the
// native payload into the backend.DependencyData shape.
func (b *FleetBackend) fetchDependencies(ctx context.Context, id string) ([]backend.DependencyData, error) {
	resp, err := b.exec(ctx, "Get", "GET", "/issues/"+url.PathEscape(id)+"/deps", nil)
	if err != nil {
		return nil, err
	}
	if !hasData(resp) {
		return nil, nil
	}
	type depWire struct {
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
	var wrap struct {
		Dependencies []depWire `json:"dependencies"`
	}
	if json.Unmarshal(resp.Data, &wrap) != nil {
		return nil, nil
	}
	out := make([]backend.DependencyData, 0, len(wrap.Dependencies))
	for _, d := range wrap.Dependencies {
		dep := backend.DependencyData{
			IssueID:     d.IssueID,
			DependsOnID: d.DependsOnID,
			Type:        d.Type,
			Title:       d.Title,
			Status:      d.Status,
			Priority:    d.Priority,
			IssueType:   d.IssueType,
			CreatedAt:   d.CreatedAt,
			CreatedBy:   d.CreatedBy,
		}
		if dep.Title == "" {
			if issue, err := b.fetchIssueSummary(ctx, d.DependsOnID); err == nil && issue != nil {
				hydrateDependencyData(&dep, *issue)
			}
		}
		out = append(out, dep)
	}
	return out, nil
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

func (b *FleetBackend) clearBlockedStatusAfterDependencyRemoval(ctx context.Context, id string) error {
	detail, err := b.Get(ctx, id)
	if err != nil {
		return err
	}
	if detail == nil || detail.Status != string(types.StatusBlocked) || len(detail.Dependencies) > 0 {
		return nil
	}
	_, err = b.exec(ctx, "RemoveDependency", "PATCH", "/issues/"+url.PathEscape(id), map[string]interface{}{
		"status": string(types.StatusOpen),
	})
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
