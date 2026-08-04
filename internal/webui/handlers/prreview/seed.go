package prreview

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/tysonthomas9/loomcli/internal/connector"
	"github.com/tysonthomas9/loomcli/internal/connector/providers"
	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/infra/connectorsvault"
	"github.com/tysonthomas9/loomcli/internal/localsettings"
	connectorsmodule "github.com/tysonthomas9/loomcli/internal/modules/connectors"
)

const prReviewWriteAction = providers.ActionGitHubReviewPost

const credentialSeedAttempts = 2

var prReadActions = []string{
	providers.ActionGitHubPullRequestRead,
	providers.ActionGitHubPullsList,
	providers.ActionGitHubCompareRead,
}

var prReviewSubmissionActions = []string{
	providers.ActionGitHubPullRequestRead,
	prReviewWriteAction,
}

func (m *Module) ensureConnectorAndGrants(ctx context.Context, ws, owner, repo string, actions []string) error {
	if m == nil || m.dispatcher == nil || m.connectorManagement == nil {
		return errEgressUnavailable
	}
	// Fast-path: once the connector + requested action set for this canonical repo are
	// ensured, the sealed credential lives in the store and the dispatcher
	// unseals it per call — so a polled read API need not re-seal/re-Create.
	cacheKey := grantSeedCacheKey(ws, prResource(owner, repo), actions)
	for range credentialSeedAttempts {
		generation := m.credentialSeedGeneration.Load()
		if _, done := m.seeded.Load(cacheKey); done && generation == m.credentialSeedGeneration.Load() {
			return nil
		}
		token, sealer, sealed, err := m.prepareCredentialSeed(ws)
		if err != nil {
			return err
		}
		if m.beforeCredentialSeedCommit != nil {
			m.beforeCredentialSeedCommit()
		}

		m.credentialSeedMu.Lock()
		if generation != m.credentialSeedGeneration.Load() {
			m.credentialSeedMu.Unlock()
			continue
		}
		err = m.seedConnectorAndGrants(ctx, ws, owner, repo, token, sealer, sealed, actions)
		if err == nil && generation == m.credentialSeedGeneration.Load() {
			m.seeded.Store(cacheKey, struct{}{})
		}
		m.credentialSeedMu.Unlock()
		return err
	}
	return errEgressUnavailable
}

func (m *Module) prepareCredentialSeed(ws string) (string, connector.Sealer, []byte, error) {
	token, err := m.resolveGitHubToken()
	if err != nil {
		return "", nil, nil, err
	}
	sealer, err := connector.NewVaultFromEnvOrKeyFile(m.localSettingsDir)
	if err != nil {
		return "", nil, nil, errEgressUnavailable
	}
	sealed, err := sealer.Seal([]byte(token), connector.CredentialAAD(ws, connectorID))
	if err != nil {
		return "", nil, nil, fmt.Errorf("seal webui github credential: %w", err)
	}
	return token, sealer, sealed, nil
}

func (m *Module) seedConnectorAndGrants(
	ctx context.Context,
	ws, owner, repo, token string,
	sealer connector.Sealer,
	sealed []byte,
	actions []string,
) error {
	if _, err := m.connectorManagement.CreateConnector(ctx, connectorsmodule.CreateConnectorCommand{
		WorkspaceKey:             ws,
		ConnectorID:              connectorID,
		SourceKind:               connectorsmodule.ConnectorSourceGitHub,
		DisplayName:              "GitHub (web UI PR review)",
		OutboundCredentialSealed: sealed,
		Status:                   connectorsmodule.ConnectorStatusActive,
		CreatedBy:                bindingID,
	}); err != nil {
		if !errors.Is(err, domain.ErrConnectorExists) && !errors.Is(err, domain.ErrAlreadyExists) {
			return fmt.Errorf("seed webui github connector: %w", err)
		}
		if err := m.rotateConnectorCredentialIfChanged(ctx, ws, token, sealer); err != nil {
			return err
		}
	}
	resourcePattern := prResource(owner, repo)
	for _, action := range actions {
		if _, err := m.connectorManagement.CreateGrant(ctx, connectorsmodule.CreateGrantCommand{
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
	return nil
}

func (m *Module) resolveGitHubToken() (string, error) {
	if token := strings.TrimSpace(os.Getenv(webuiGitHubTokenEnv)); token != "" {
		return token, nil
	}
	if m == nil || m.localSettingsDir == "" {
		return "", errEgressUnavailable
	}
	settings, err := localsettings.Load(m.localSettingsDir)
	if err != nil {
		return "", errEgressUnavailable
	}
	token, err := localsettings.UnsealRuntimeCredential(
		m.localSettingsDir,
		settings,
		localsettings.RuntimeCredentialProviderGitHub,
	)
	if err != nil || strings.TrimSpace(token) == "" {
		return "", errEgressUnavailable
	}
	return strings.TrimSpace(token), nil
}

func (m *Module) githubTokenConfigured() bool {
	if strings.TrimSpace(os.Getenv(webuiGitHubTokenEnv)) != "" {
		return true
	}
	if m == nil || m.localSettingsDir == "" {
		return false
	}
	settings, err := localsettings.Load(m.localSettingsDir)
	if err != nil {
		return false
	}
	return strings.TrimSpace(settings.RuntimeCredentials.GitHub.Sealed) != ""
}

func (m *Module) rotateConnectorCredentialIfChanged(
	ctx context.Context,
	ws, token string,
	sealer connector.Sealer,
) error {
	vaultAdapter, err := connectorsvault.New(sealer)
	if err != nil {
		return fmt.Errorf("compose webui connector credential vault: %w", err)
	}
	management, err := connectorsmodule.NewManagementWithCredentialVault(
		m.connectorManagementStore, vaultAdapter, time.Now,
	)
	if err != nil {
		return fmt.Errorf("compose webui connector secret lifecycle: %w", err)
	}
	credential := []byte(token)
	if _, err := management.SynchronizeConnectorCredential(ctx, connectorsmodule.SynchronizeConnectorCredentialCommand{
		WorkspaceKey: ws, ConnectorID: connectorID, DesiredCredential: credential,
	}); err != nil {
		return fmt.Errorf("synchronize webui github credential: %w", err)
	}
	return nil
}

func grantSeedCacheKey(ws, resource string, actions []string) string {
	set := make(map[string]struct{}, len(actions))
	for _, action := range actions {
		action = strings.TrimSpace(action)
		if action != "" {
			set[action] = struct{}{}
		}
	}
	canonical := make([]string, 0, len(set))
	for action := range set {
		canonical = append(canonical, action)
	}
	sort.Strings(canonical)
	return ws + "|" + resource + "|" + strings.Join(canonical, ",")
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
