package fleet

import (
	"context"
	"encoding/json"
	"net/url"
	"sort"
	"strings"

	"github.com/tysonthomas9/loomcli/internal/modules/workitems"
)

// --- Dependency operations ---

func (b *FleetBackend) AddDependency(ctx context.Context, command workitems.AddDependencyCommand) error {
	if err := b.postDependency(ctx, command); err != nil {
		return err
	}
	return b.waitForDependencyState(ctx, "AddDependency", command.IssueID, command.DependsOnID, true)
}

func (b *FleetBackend) postDependency(ctx context.Context, command workitems.AddDependencyCommand) error {
	// fleet-db mounts dependency routes at /issues/{id}/deps (abbreviated)
	// and its AddDependencyRequest names the dep kind "type" (not
	// "dep_type"). Path + body tweaked to match.
	type depReq struct {
		DependsOnID string `json:"depends_on_id"`
		Type        string `json:"type,omitempty"`
	}
	req := depReq{
		DependsOnID: command.DependsOnID,
		Type:        command.Type,
	}
	if _, err := b.exec(ctx, "AddDependency", "POST", "/issues/"+url.PathEscape(command.IssueID)+"/deps", req); err != nil {
		return err
	}
	return nil
}

func (b *FleetBackend) RemoveDependency(ctx context.Context, command workitems.RemoveDependencyCommand) error {
	// fleet-db delete route is /issues/{id}/deps/{depends_on_id}; abbreviated
	// to match the add route. The server also requires the dependency type so
	// it can distinguish multiple edge kinds between the same issues.
	depType := strings.TrimSpace(command.Type)
	if depType == "" {
		depType = "blocks"
	}
	path := "/issues/" + url.PathEscape(command.IssueID) + "/deps/" + url.PathEscape(command.DependsOnID) + "?type=" + url.QueryEscape(depType)
	if _, err := b.exec(ctx, "RemoveDependency", "DELETE", path, nil); err != nil {
		return err
	}
	if err := b.waitForDependencyState(ctx, "RemoveDependency", command.IssueID, command.DependsOnID, false); err != nil {
		return err
	}
	return b.clearBlockedStatusAfterDependencyRemoval(ctx, command.IssueID)
}

func (b *FleetBackend) addLabel(ctx context.Context, id string, label string) error {
	req := struct {
		Label string `json:"label"`
	}{Label: label}
	_, err := b.exec(ctx, "AddLabel", "POST", "/issues/"+url.PathEscape(id)+"/labels", req)
	return err
}

func (b *FleetBackend) removeLabel(ctx context.Context, id string, label string) error {
	_, err := b.exec(ctx, "RemoveLabel", "DELETE", "/issues/"+url.PathEscape(id)+"/labels/"+url.PathEscape(label), nil)
	return err
}

// --- Comment operations ---

func (b *FleetBackend) ListComments(ctx context.Context, query workitems.ListCommentsQuery) ([]*workitems.Comment, error) {
	// fleet-db exposes GET /issues/{id}/comments as a first-class endpoint.
	resp, err := b.exec(ctx, "ListComments", "GET", "/issues/"+url.PathEscape(query.IssueID)+"/comments", nil)
	if err != nil {
		return nil, err
	}
	if !hasData(resp) {
		return []*workitems.Comment{}, nil
	}
	// Try native wrapper {"comments":[...]} first, then bare array.
	var wrap struct {
		Comments []fleetCommentWire `json:"comments"`
	}
	if json.Unmarshal(resp.Data, &wrap) == nil && wrap.Comments != nil {
		out := make([]*workitems.Comment, 0, len(wrap.Comments))
		for _, w := range wrap.Comments {
			c := w.toTypesComment()
			out = append(out, &c)
		}
		sortCommentsByCreation(out)
		return out, nil
	}
	var bare []fleetCommentWire
	if err := json.Unmarshal(resp.Data, &bare); err != nil {
		return nil, workitems.AdapterInternal("ListComments", "unmarshal response", err)
	}
	out := make([]*workitems.Comment, 0, len(bare))
	for _, w := range bare {
		c := w.toTypesComment()
		out = append(out, &c)
	}
	sortCommentsByCreation(out)
	return out, nil
}

func sortCommentsByCreation(comments []*workitems.Comment) {
	sort.SliceStable(comments, func(i, j int) bool {
		if comments[i].CreatedAt.Equal(comments[j].CreatedAt) {
			return comments[i].ID < comments[j].ID
		}
		return comments[i].CreatedAt.Before(comments[j].CreatedAt)
	})
}

func (b *FleetBackend) AddComment(ctx context.Context, command workitems.AddCommentCommand) (*workitems.Comment, error) {
	// Response body: fleet-db returns a "body" field + string ID; loom's
	// canonical workitems.Comment has "text" + int64 ID. Unmarshal into a
	// local struct that mirrors fleet-db's wire shape, then project to
	// workitems.Comment.
	type commentReq struct {
		Body string `json:"body"`
	}
	resp, err := b.exec(ctx, "AddComment", "POST", "/issues/"+url.PathEscape(command.IssueID)+"/comments", commentReq{Body: command.Text})
	if err != nil {
		return nil, err
	}
	if !hasData(resp) {
		return nil, workitems.AdapterInternal("AddComment", "empty response from server", nil)
	}
	var wire fleetCommentWire
	if err := json.Unmarshal(resp.Data, &wire); err != nil {
		return nil, workitems.AdapterInternal("AddComment", "unmarshal response", err)
	}
	comment := wire.toTypesComment()
	if comment.IssueID == "" {
		comment.IssueID = command.IssueID
	}
	return &comment, nil
}
