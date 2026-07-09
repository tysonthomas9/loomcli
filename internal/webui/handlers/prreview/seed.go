package prreview

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"strings"

	vault "github.com/tysonthomas9/loomcli/internal/connector"
	"github.com/tysonthomas9/loomcli/internal/connector/providers"
	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/store"
)

var prReviewActions = []string{
	providers.ActionGitHubPullRequestRead,
	providers.ActionGitHubPullsList,
	providers.ActionGitHubCompareRead,
	providers.ActionGitHubReviewPost,
}

func (m *Module) ensureConnectorAndGrants(ctx context.Context, ws, owner, repo string, actions []string) error {
	if m == nil || m.dispatcher == nil {
		return errEgressUnavailable
	}
	// Fast-path: once the connector + grants for this canonical repo are
	// ensured, the sealed credential lives in the store and the dispatcher
	// unseals it per call — so a polled read API need not re-seal/re-Create.
	cacheKey := ws + "|" + prResource(owner, repo)
	if _, done := m.seeded.Load(cacheKey); done {
		return nil
	}
	token := strings.TrimSpace(os.Getenv(webuiGitHubTokenEnv))
	if token == "" {
		return errEgressUnavailable
	}
	sealer, err := vault.NewVaultFromEnv()
	if err != nil {
		return errEgressUnavailable
	}
	sealed, err := sealer.Seal([]byte(token), vault.CredentialAAD(ws, connectorID))
	if err != nil {
		return fmt.Errorf("seal webui github credential: %w", err)
	}
	if _, err := m.store.Connectors().Create(ctx, store.ConnectorCreate{
		WorkspaceKey:             ws,
		ConnectorID:              connectorID,
		SourceKind:               domain.ConnectorSourceGitHub,
		DisplayName:              "GitHub (web UI PR review)",
		OutboundCredentialSealed: sealed,
		Status:                   domain.ConnectorStatusActive,
		CreatedBy:                bindingID,
	}); err != nil && !errors.Is(err, domain.ErrConnectorExists) && !errors.Is(err, domain.ErrAlreadyExists) {
		return fmt.Errorf("seed webui github connector: %w", err)
	}
	resourcePattern := prResource(owner, repo)
	for _, action := range actions {
		if _, err := m.store.ConnectorGrants().Create(ctx, store.ConnectorGrantCreate{
			WorkspaceKey:    ws,
			GrantID:         grantID(owner, repo, action),
			ConnectorID:     connectorID,
			BindingID:       bindingID,
			Action:          action,
			ResourcePattern: resourcePattern,
		}); err != nil && !errors.Is(err, domain.ErrAlreadyExists) {
			return fmt.Errorf("seed webui github grant %q: %w", action, err)
		}
	}
	m.seeded.Store(cacheKey, struct{}{})
	return nil
}

// grantID is derived from the EXACT resource pattern + action via a hash, not
// a lossy case/punctuation fold, so the grant id and its ResourcePattern can
// never diverge (which would leave a stored pattern that fails to match the
// dispatched resource — a permanent spurious 403). Distinct repos therefore
// get distinct ids even when they fold to the same lowercased-dashed string.
func grantID(owner, repo, action string) string {
	sum := sha256.Sum256([]byte(prResource(owner, repo) + "#" + action))
	return "grant-webui-review-" + hex.EncodeToString(sum[:8])
}

func prResource(owner, repo string) string {
	return "repo:" + owner + "/" + repo
}
