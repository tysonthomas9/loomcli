package prreview

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"sync"

	"github.com/tysonthomas9/loomcli/internal/connector/providers"
	"github.com/tysonthomas9/loomcli/internal/domain"
)

// HostGitHub is the host's own GitHub reader (OR10): PR-watch registration
// and sweeps read through the host GitHub connector's dispatcher, with no
// agent or bridge, on a GitHub repo registered in the workspace. Like
// AgentGitHubRead, every call re-resolves the host credential, so a settings
// change applies at once.
type HostGitHub struct {
	m       *Module
	mu      sync.Mutex
	gen     uint64                // bumped when a 401 drops the cached viewers
	viewers map[string]hostViewer // by workspace connector
}

// hostViewer is a cached viewer login and the credential version (a digest
// of the sealed credential) it was read with.
type hostViewer struct{ version, login string }

// NewHostGitHub is the host GitHub reader on m's store and dispatcher.
func NewHostGitHub(m *Module) *HostGitHub {
	return &HostGitHub{m: m, viewers: map[string]hostViewer{}}
}

// Repo is the GitHub owner/repo of the workspace repo cloned at repoPath.
func (h *HostGitHub) Repo(ctx context.Context, ws, repoPath string) (string, string, error) {
	return h.m.boundRepo(ctx, ws, repoPath)
}

// Read serves one github_read op on owner/repo, a GitHub repo of ws.
func (h *HostGitHub) Read(ctx context.Context, ws, owner, repo, op string, args map[string]any) (map[string]any, error) {
	if _, ok := providers.GitHubReadOps[op]; !ok {
		return nil, fmt.Errorf("github_read has no op %q: %w", op, domain.ErrInvalid)
	}
	owner, repo, err := h.authorize(ctx, ws, owner, repo)
	if err != nil {
		return nil, err
	}
	return h.dispatch(ctx, ws, owner, repo, op, args)
}

// Viewer is the GitHub login the host credential authenticates as, read on
// owner/repo (a GitHub repo of ws). It is cached per workspace connector and
// credential version: a rotated credential, or any host read answered 401,
// drops it.
func (h *HostGitHub) Viewer(ctx context.Context, ws, owner, repo string) (string, error) {
	owner, repo, err := h.authorize(ctx, ws, owner, repo)
	if err != nil {
		return "", err
	}
	sealed, err := h.m.store.Connectors().ResolveOutboundCredentialSealed(ctx, ws, connectorID)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(sealed)
	version, cacheKey := hex.EncodeToString(sum[:]), ws+"|"+connectorID
	h.mu.Lock()
	v, ok := h.viewers[cacheKey]
	gen := h.gen
	h.mu.Unlock()
	if ok && v.version == version {
		return v.login, nil
	}
	body, err := h.dispatch(ctx, ws, owner, repo, "viewer", nil)
	if err != nil {
		return "", err
	}
	item, _ := body["item"].(map[string]any)
	login, _ := item["login"].(string)
	if login == "" {
		return "", errors.New("GitHub answered the host viewer read with no login")
	}
	h.mu.Lock()
	if gen == h.gen {
		h.viewers[cacheKey] = hostViewer{version: version, login: login}
	}
	h.mu.Unlock()
	return login, nil
}

// authorize returns owner/repo as ws registers it, refusing any other repo,
// and ensures the host connector's read grant on it with the current host
// credential.
func (h *HostGitHub) authorize(ctx context.Context, ws, owner, repo string) (string, string, error) {
	canonOwner, canonRepo, ok, err := h.m.workspaceHasRepo(ctx, ws, owner, repo)
	if err != nil {
		return "", "", err
	}
	if !ok {
		return "", "", fmt.Errorf("%s/%s is not a GitHub repo of workspace %s: %w", owner, repo, ws, domain.ErrNotOwner)
	}
	h.m.InvalidateCredentialSeeds()
	if err := h.m.ensureConnectorAndGrants(ctx, ws, canonOwner, canonRepo, []string{providers.ActionGitHubRead}); err != nil {
		if errors.Is(err, errEgressUnavailable) {
			err = errNoGitHubToken
		}
		return "", "", err
	}
	return canonOwner, canonRepo, nil
}

// dispatch is one github.read call through the dispatcher, which authorizes
// it and hands the provider the unsealed credential in CallSpec only.
func (h *HostGitHub) dispatch(ctx context.Context, ws, owner, repo, op string, args map[string]any) (map[string]any, error) {
	body, err := h.m.dispatchRead(ctx, ws, "host-github-read:", owner, repo, op, args)
	if up := (*providers.UpstreamError)(nil); errors.As(err, &up) && up.Status == http.StatusUnauthorized {
		h.mu.Lock()
		clear(h.viewers)
		h.gen++
		h.mu.Unlock()
	}
	return body, err
}
