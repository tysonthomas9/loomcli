package fleetdb

import (
	"context"
	"encoding/json"
	"net/url"
	"strings"

	"github.com/tysonthomas9/loomcli/internal/modules/workitems"
)

func (b *Adapter) RequireRepositoryAdmission(context.Context) error { return nil }

// BlockRepositoryRequired atomically moves a repository-less task to the
// canonical repository-required blocked state owned by fleet-db.
func (b *Adapter) BlockRepositoryRequired(ctx context.Context, id string) (*workitems.RepositoryAdmissionResult, error) {
	if strings.TrimSpace(id) == "" {
		return nil, workitems.AdapterInvalid("BlockRepositoryRequired", "id must not be empty")
	}

	resp, err := b.exec(ctx, "BlockRepositoryRequired", "POST", "/issues/"+url.PathEscape(id)+"/repository-requirement/block", map[string]any{})
	if err != nil {
		return nil, err
	}
	if !hasData(resp) {
		return nil, workitems.AdapterInternal("BlockRepositoryRequired", "empty response from server", nil)
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
		return nil, workitems.AdapterInternal("BlockRepositoryRequired", "unmarshal response", err)
	}
	if wire.Issue == nil {
		return nil, workitems.AdapterInternal("BlockRepositoryRequired", "response is missing canonical issue", nil)
	}
	issue := wire.Issue.toIssueSummary()
	return &workitems.RepositoryAdmissionResult{
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

func (b *Adapter) AssignRepository(ctx context.Context, command workitems.AssignRepositoryCommand) (*workitems.IssueSummary, error) {
	id := command.IssueID
	repo := command.Repository
	if strings.TrimSpace(id) == "" {
		return nil, workitems.AdapterInvalid("AssignRepository", "id must not be empty")
	}
	repo = strings.TrimSpace(repo)
	if repo == "" {
		return nil, workitems.AdapterInvalid("AssignRepository", "repo must not be empty")
	}

	body := struct {
		Repo string `json:"repo"`
	}{Repo: repo}
	resp, err := b.exec(ctx, "AssignRepository", "PUT", "/issues/"+url.PathEscape(id)+"/repository", body)
	if err != nil {
		return nil, err
	}
	if !hasData(resp) {
		return nil, workitems.AdapterInternal("AssignRepository", "empty response from server", nil)
	}

	var wire fleetIssueWithCountsWire
	if err := json.Unmarshal(resp.Data, &wire); err != nil {
		return nil, workitems.AdapterInternal("AssignRepository", "unmarshal response", err)
	}
	if strings.TrimSpace(wire.ID) == "" {
		return nil, workitems.AdapterInternal("AssignRepository", "response is missing canonical issue", nil)
	}
	issue := wire.toIssueSummary()
	return &issue, nil
}
