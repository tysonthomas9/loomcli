package fleet

import (
	"context"
	"encoding/json"
	"net/url"
	"strings"

	"github.com/tysonthomas9/loomcli/internal/backend"
)

// BlockRepositoryRequired atomically moves a repository-less task to the
// canonical repository-required blocked state owned by fleet-db.
func (b *FleetBackend) BlockRepositoryRequired(ctx context.Context, id string) (*backend.RepositoryRequirementResult, error) {
	if strings.TrimSpace(id) == "" {
		return nil, backend.ErrValidation("BlockRepositoryRequired", "id must not be empty")
	}

	resp, err := b.exec(ctx, "BlockRepositoryRequired", "POST", "/issues/"+url.PathEscape(id)+"/repository-requirement/block", map[string]any{})
	if err != nil {
		return nil, err
	}
	if !hasData(resp) {
		return nil, backend.ErrInternal("BlockRepositoryRequired", "empty response from server", nil)
	}

	var wire struct {
		Issue         *fleetIssueWithCountsWire `json:"issue"`
		Changed       bool                      `json:"changed"`
		Replayed      bool                      `json:"replayed"`
		DispatchReady bool                      `json:"dispatch_ready"`
		Blocked       bool                      `json:"blocked"`
		Reopened      bool                      `json:"reopened"`
		Outcome       string                    `json:"outcome"`
	}
	if err := json.Unmarshal(resp.Data, &wire); err != nil {
		return nil, backend.ErrInternal("BlockRepositoryRequired", "unmarshal response", err)
	}
	if wire.Issue == nil {
		return nil, backend.ErrInternal("BlockRepositoryRequired", "response is missing canonical issue", nil)
	}
	issue := wire.Issue.toIssueData()
	return &backend.RepositoryRequirementResult{
		Issue:         &issue,
		Changed:       wire.Changed,
		Replayed:      wire.Replayed,
		DispatchReady: wire.DispatchReady,
		Blocked:       wire.Blocked,
		Reopened:      wire.Reopened,
		Outcome:       wire.Outcome,
	}, nil
}

// SetIssueRepository atomically assigns the canonical source repository and
// reopens a task only when it is in fleet-db's repository-required blocked
// state. The returned issue is the authoritative post-command projection.
func (b *FleetBackend) SetIssueRepository(ctx context.Context, id, repo string) (*backend.IssueData, error) {
	if strings.TrimSpace(id) == "" {
		return nil, backend.ErrValidation("SetIssueRepository", "id must not be empty")
	}
	repo = strings.TrimSpace(repo)
	if repo == "" {
		return nil, backend.ErrValidation("SetIssueRepository", "repo must not be empty")
	}

	body := struct {
		Repo string `json:"repo"`
	}{Repo: repo}
	resp, err := b.exec(ctx, "SetIssueRepository", "PUT", "/issues/"+url.PathEscape(id)+"/repository", body)
	if err != nil {
		return nil, err
	}
	if !hasData(resp) {
		return nil, backend.ErrInternal("SetIssueRepository", "empty response from server", nil)
	}

	issue, err := unmarshalCanonicalRepositoryIssue(resp.Data)
	if err != nil {
		return nil, err
	}
	return issue, nil
}

// unmarshalCanonicalRepositoryIssue accepts the native fleet-db command
// response (`{"issue": ...}`) and a bare issue for compatibility with an
// envelope whose data field is already the canonical projection.
func unmarshalCanonicalRepositoryIssue(data []byte) (*backend.IssueData, error) {
	var wrapped struct {
		Issue *fleetIssueWithCountsWire `json:"issue"`
	}
	if err := json.Unmarshal(data, &wrapped); err == nil && wrapped.Issue != nil {
		issue := wrapped.Issue.toIssueData()
		return &issue, nil
	}

	var bare fleetIssueWithCountsWire
	if err := json.Unmarshal(data, &bare); err != nil {
		return nil, backend.ErrInternal("SetIssueRepository", "unmarshal response", err)
	}
	if strings.TrimSpace(bare.ID) == "" {
		return nil, backend.ErrInternal("SetIssueRepository", "response is missing canonical issue", nil)
	}
	issue := bare.toIssueData()
	return &issue, nil
}
