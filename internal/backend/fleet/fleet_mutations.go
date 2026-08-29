package fleet

import (
	"context"
	"encoding/json"
	"fmt"
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

// fleetDependencyTypes is fleet-db's dependency vocabulary (models.DependencyType).
// It is deliberately narrower than loom's in-process types/entity vocabularies,
// which also declare "waits-for" and "conditional-blocks" — those describe
// semantics no storage backend implements, so fleet-db rejects them with
// ErrInvalidDepType. Catching it here turns an opaque server 400 into a message
// that names the supported set at the call site.
var fleetDependencyTypes = map[string]struct{}{
	"blocks":        {},
	"parent-child":  {},
	"related":       {},
	"duplicate-of":  {},
	"superseded-by": {},
}

func validateFleetDepType(depType string) error {
	depType = strings.TrimSpace(depType)
	if depType == "" {
		return nil // server defaults to "blocks"
	}
	if _, ok := fleetDependencyTypes[depType]; ok {
		return nil
	}
	return backend.ErrValidation("AddDependency", fmt.Sprintf(
		"dependency type %q is not storable in fleet-db (supported: blocks, parent-child, related, duplicate-of, superseded-by)", depType))
}

func (b *FleetBackend) postDependency(ctx context.Context, params backend.DepAddParams) error {
	if err := validateFleetDepType(params.DepType); err != nil {
		return err
	}
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
	if err := b.execAsActor(ctx, "AddDependency", "POST", "/issues/"+url.PathEscape(params.FromID)+"/deps", req, params.Actor); err != nil {
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
	if err := b.execAsActor(ctx, "RemoveDependency", "DELETE", path, nil, params.Actor); err != nil {
		return err
	}
	if err := b.waitForDependencyState(ctx, "RemoveDependency", params.FromID, params.ToID, false); err != nil {
		return err
	}
	return b.clearBlockedStatusAfterDependencyRemoval(ctx, params.FromID, params.Actor)
}

// --- Label operations ---

func (b *FleetBackend) AddLabel(ctx context.Context, id string, label string) error {
	return b.addLabel(ctx, id, label, "")
}

func (b *FleetBackend) addLabel(ctx context.Context, id string, label string, actor string) error {
	req := struct {
		Label string `json:"label"`
	}{Label: label}
	return b.execAsActor(ctx, "AddLabel", "POST", "/issues/"+url.PathEscape(id)+"/labels", req, actor)
}

func (b *FleetBackend) RemoveLabel(ctx context.Context, id string, label string) error {
	return b.removeLabel(ctx, id, label, "")
}

func (b *FleetBackend) removeLabel(ctx context.Context, id string, label string, actor string) error {
	return b.execAsActor(ctx, "RemoveLabel", "DELETE", "/issues/"+url.PathEscape(id)+"/labels/"+url.PathEscape(label), nil, actor)
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
	resp, err := b.execResponseAsActor(ctx, "AddComment", "POST", "/issues/"+url.PathEscape(params.IssueID)+"/comments", commentReq{Body: params.Text}, params.Actor)
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
