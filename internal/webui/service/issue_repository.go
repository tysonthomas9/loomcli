package service

import (
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"time"

	"github.com/tysonthomas9/loomcli/internal/backend"
)

var _ IssueRepositoryService = (*issueServiceImpl)(nil)

// SetIssueRepository delegates repository assignment and conditional recovery
// to the fleet-owned atomic command, then returns its canonical issue
// projection. Loom never synthesizes a reopen locally.
func (s *issueServiceImpl) SetIssueRepository(ctx context.Context, params SetIssueRepositoryParams) (json.RawMessage, error) {
	if strings.TrimSpace(params.IssueID) == "" {
		return nil, ErrValidation("issue ID is required")
	}
	repo := strings.TrimSpace(params.Repo)
	if repo == "" {
		return nil, ErrValidation("repository is required")
	}

	be, svcErr := s.resolveBackend(ctx)
	if svcErr != nil {
		return nil, svcErr
	}
	repositoryBackend, ok := be.(backend.RepositoryRequirementBackend)
	if !ok {
		return nil, ErrNotImplemented("issue backend does not support repository assignment")
	}

	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	issue, err := repositoryBackend.SetIssueRepository(ctx, params.IssueID, repo)
	if err != nil {
		slog.Error("backend error in SetIssueRepository", "issue_id", params.IssueID, "repo", repo, "err", err)
		return nil, translateBackendError(err)
	}
	if issue == nil {
		return nil, ErrInternal("repository assignment returned no canonical issue", nil)
	}

	wire, err := json.Marshal(issueDataToWire(issue))
	if err != nil {
		return nil, ErrInternal("failed to marshal repository assignment result", err)
	}
	return wire, nil
}
