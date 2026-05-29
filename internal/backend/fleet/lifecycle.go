package fleet

import (
	"context"
	"encoding/json"
	"net/url"

	"github.com/tysonthomas9/loomcli/internal/backend"
	"github.com/tysonthomas9/loomcli/internal/types"
)

func (b *FleetBackend) Close(ctx context.Context, id string, params backend.CloseParams) (*backend.CloseResult, error) {
	type closeReq struct {
		Reason string `json:"reason,omitempty"`
	}
	req := closeReq{
		Reason: params.Reason,
	}
	resp, err := b.exec(ctx, "Close", "POST", "/issues/"+url.PathEscape(id)+"/close", req)
	if err != nil {
		return nil, err
	}
	// The close endpoint returns a close result with closed issue and unblocked issues.
	// TODO(fleet-q6ox): fleet-db does not yet return unblocked issues on close;
	// Unblocked will be empty until fleet-db adds unblocked-on-close support.
	if !hasData(resp) {
		return nil, backend.ErrInternal("Close", "empty response from server", nil)
	}
	var cr closeResultJSON
	if err := json.Unmarshal(resp.Data, &cr); err != nil {
		return nil, backend.ErrInternal("Close", "unmarshal response", err)
	}
	if cr.Closed == nil && len(cr.Unblocked) == 0 {
		var issue types.Issue
		if err := json.Unmarshal(resp.Data, &issue); err == nil && issue.ID != "" {
			closed := issueToData(&issue)
			return &backend.CloseResult{Closed: &closed, Unblocked: []backend.IssueData{}}, nil
		}
	}
	return closeResultJSONToData(&cr), nil
}

func (b *FleetBackend) Reopen(ctx context.Context, id string, params backend.ReopenParams) error {
	if id == "" {
		return backend.ErrValidation("Reopen", "id must not be empty")
	}
	// fleet-db has a dedicated reopen route (see internal/api/issues.go:49);
	// previous implementation used PATCH status=open, but fleet-db's
	// UpdateIssueRequest schema doesn't accept `status` under
	// disallowUnknownFields, so every reopen 400'd with "unknown field
	// status". The per-issue endpoint is also semantically richer: it
	// runs the reopen state machine server-side and allows concurrent
	// close-reopen ordering guarantees.
	_, err := b.exec(ctx, "Reopen", "POST", "/issues/"+url.PathEscape(id)+"/reopen", map[string]interface{}{})
	if err != nil {
		return err
	}
	// Record reason as a comment per the IssueBackend interface contract.
	// Best-effort: the status transition already succeeded.
	if params.Reason != "" {
		type commentReq struct {
			Body string `json:"body"`
		}
		_, _ = b.exec(ctx, "Reopen", "POST", "/issues/"+url.PathEscape(id)+"/comments", commentReq{Body: params.Reason})
	}
	return nil
}

func (b *FleetBackend) Delete(ctx context.Context, params backend.DeleteParams) error {
	if len(params.IDs) == 0 {
		return backend.ErrValidation("Delete", "IDs must not be empty")
	}
	// The server's DELETE endpoint handles single issue. Delete each one.
	for _, id := range params.IDs {
		_, err := b.exec(ctx, "Delete", "DELETE", "/issues/"+url.PathEscape(id), nil)
		if err != nil {
			if params.Force && backend.IsKind(err, backend.KindNotFound) {
				continue
			}
			return err
		}
	}
	return nil
}
