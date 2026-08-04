package fleet

import (
	"context"
	"encoding/json"
	"net/url"
	"strings"

	"github.com/tysonthomas9/loomcli/internal/backend"
)

// --- Dependency operations ---

func (b *FleetBackend) AddDependency(ctx context.Context, params backend.DepAddParams) error {
	if err := b.postDependency(ctx, params); err != nil {
		return err
	}
	return b.waitForDependencyState(ctx, "AddDependency", params.FromID, params.ToID, true)
}

func (b *FleetBackend) postDependency(ctx context.Context, params backend.DepAddParams) error {
	// fleet-db mounts dependency routes at /issues/{id}/deps (abbreviated)
	// and its AddDependencyRequest names the dep kind "type" (not
	// "dep_type"). Path + body tweaked to match.
	type depReq struct {
		DependsOnID string `json:"depends_on_id"`
		Type        string `json:"type,omitempty"`
	}
	req := depReq{
		DependsOnID: params.ToID,
		Type:        params.DepType,
	}
	if _, err := b.exec(ctx, "AddDependency", "POST", "/issues/"+url.PathEscape(params.FromID)+"/deps", req); err != nil {
		return err
	}
	return nil
}

func (b *FleetBackend) RemoveDependency(ctx context.Context, params backend.DepRemoveParams) error {
	// fleet-db delete route is /issues/{id}/deps/{depends_on_id}; abbreviated
	// to match the add route. The server also requires the dependency type so
	// it can distinguish multiple edge kinds between the same issues.
	depType := strings.TrimSpace(params.DepType)
	if depType == "" {
		depType = "blocks"
	}
	path := "/issues/" + url.PathEscape(params.FromID) + "/deps/" + url.PathEscape(params.ToID) + "?type=" + url.QueryEscape(depType)
	if _, err := b.exec(ctx, "RemoveDependency", "DELETE", path, nil); err != nil {
		return err
	}
	if err := b.waitForDependencyState(ctx, "RemoveDependency", params.FromID, params.ToID, false); err != nil {
		return err
	}
	return b.clearBlockedStatusAfterDependencyRemoval(ctx, params.FromID)
}

// --- Label operations ---

func (b *FleetBackend) AddLabel(ctx context.Context, id string, label string) error {
	req := struct {
		Label string `json:"label"`
	}{Label: label}
	_, err := b.exec(ctx, "AddLabel", "POST", "/issues/"+url.PathEscape(id)+"/labels", req)
	return err
}

func (b *FleetBackend) RemoveLabel(ctx context.Context, id string, label string) error {
	_, err := b.exec(ctx, "RemoveLabel", "DELETE", "/issues/"+url.PathEscape(id)+"/labels/"+url.PathEscape(label), nil)
	return err
}

// --- Comment operations ---

func (b *FleetBackend) ListComments(ctx context.Context, id string) ([]backend.CommentData, error) {
	// fleet-db exposes GET /issues/{id}/comments as a first-class endpoint.
	resp, err := b.exec(ctx, "ListComments", "GET", "/issues/"+url.PathEscape(id)+"/comments", nil)
	if err != nil {
		return nil, err
	}
	if !hasData(resp) {
		return []backend.CommentData{}, nil
	}
	// Try native wrapper {"comments":[...]} first, then bare array.
	var wrap struct {
		Comments []fleetCommentWire `json:"comments"`
	}
	if json.Unmarshal(resp.Data, &wrap) == nil && wrap.Comments != nil {
		out := make([]backend.CommentData, 0, len(wrap.Comments))
		for _, w := range wrap.Comments {
			c := w.toTypesComment()
			out = append(out, commentToData(&c))
		}
		backend.SortCommentsByCreation(out)
		return out, nil
	}
	var bare []fleetCommentWire
	if err := json.Unmarshal(resp.Data, &bare); err != nil {
		return nil, backend.ErrInternal("ListComments", "unmarshal response", err)
	}
	out := make([]backend.CommentData, 0, len(bare))
	for _, w := range bare {
		c := w.toTypesComment()
		out = append(out, commentToData(&c))
	}
	backend.SortCommentsByCreation(out)
	return out, nil
}

func (b *FleetBackend) AddComment(ctx context.Context, params backend.CommentAddParams) (*backend.CommentData, error) {
	// Response body: fleet-db returns a "body" field + string ID; loom's
	// canonical types.Comment has "text" + int64 ID. Unmarshal into a
	// local struct that mirrors fleet-db's wire shape, then project to
	// types.Comment.
	type commentReq struct {
		Body string `json:"body"`
	}
	resp, err := b.exec(ctx, "AddComment", "POST", "/issues/"+url.PathEscape(params.IssueID)+"/comments", commentReq{Body: params.Text})
	if err != nil {
		return nil, err
	}
	if !hasData(resp) {
		return nil, backend.ErrInternal("AddComment", "empty response from server", nil)
	}
	var wire fleetCommentWire
	if err := json.Unmarshal(resp.Data, &wire); err != nil {
		return nil, backend.ErrInternal("AddComment", "unmarshal response", err)
	}
	comment := wire.toTypesComment()
	result := commentToData(&comment)
	return &result, nil
}
