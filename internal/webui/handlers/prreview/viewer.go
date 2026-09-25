package prreview

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/tysonthomas9/loomcli/internal/cli"
	"github.com/tysonthomas9/loomcli/internal/connector"
	"github.com/tysonthomas9/loomcli/internal/connector/providers"
	"github.com/tysonthomas9/loomcli/internal/webui/server/middleware"
)

const (
	viewerResource = "user:self"
	viewerCacheTTL = 10 * time.Minute

	githubViewerStatusAvailable   = "available"
	githubViewerStatusUnavailable = "unavailable"
	githubViewerStatusRateLimited = "rate_limited"
	githubViewerStatusError       = "error"

	githubViewerSourceConnector = "connector"
	githubViewerSourceGhCLI     = "gh_cli"
	githubViewerSourceNone      = "none"
)

var viewerActions = []string{providers.ActionGitHubViewerRead}

// githubViewerIdentity is the verified GitHub login for the workspace PR-list
// credential. login is present only when status=available — never from JWT name.
type githubViewerIdentity struct {
	Status      string `json:"status"`
	Login       string `json:"login,omitempty"`
	Source      string `json:"source"`
	ConnectorID string `json:"connector_id,omitempty"`
	ObservedAt  string `json:"observed_at,omitempty"`
	Message     string `json:"message,omitempty"`
}

type viewerCache struct {
	mu      sync.Mutex
	entries map[string]viewerCacheEntry
}

type viewerCacheEntry struct {
	identity  githubViewerIdentity
	expiresAt time.Time
}

func (c *viewerCache) get(key string, now time.Time) (githubViewerIdentity, bool) {
	if c == nil {
		return githubViewerIdentity{}, false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.entries[key]
	if !ok || !e.expiresAt.After(now) {
		return githubViewerIdentity{}, false
	}
	return e.identity, true
}

func (c *viewerCache) put(key string, identity githubViewerIdentity, expiresAt time.Time) {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.entries == nil {
		c.entries = map[string]viewerCacheEntry{}
	}
	c.entries[key] = viewerCacheEntry{identity: identity, expiresAt: expiresAt}
}

func (c *viewerCache) clear() {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries = nil
}

func (c *viewerCache) clearWorkspace(ws string) {
	if c == nil {
		return
	}
	prefix := ws + "|"
	c.mu.Lock()
	defer c.mu.Unlock()
	for k := range c.entries {
		if strings.HasPrefix(k, prefix) {
			delete(c.entries, k)
		}
	}
}

func viewerCacheKey(ws, fingerprint, source string) string {
	return ws + "|" + fingerprint + "|" + source
}

func credentialFingerprint(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:16])
}

func (m *Module) clock() time.Time {
	if m != nil && m.now != nil {
		return m.now()
	}
	return time.Now()
}

// resolveGitHubViewer returns the verified viewer for the same credential path
// used to list registered-repo PRs. preferConnector uses github-webui when a
// token is configured; otherwise (and on gh list fallback) uses gh api user.
func (m *Module) resolveGitHubViewer(r *http.Request, ws string, preferConnector bool) githubViewerIdentity {
	if preferConnector && m != nil && m.connectorListAvailable() {
		return m.resolveViewerViaConnector(r, ws)
	}
	return m.resolveViewerViaGh(r.Context(), ws)
}

func (m *Module) resolveViewerViaConnector(r *http.Request, ws string) githubViewerIdentity {
	now := m.clock()
	token, err := m.resolveGitHubToken()
	if err != nil || strings.TrimSpace(token) == "" {
		return githubViewerIdentity{
			Status:  githubViewerStatusUnavailable,
			Source:  githubViewerSourceNone,
			Message: "GitHub credential unavailable for viewer lookup",
		}
	}
	fp := credentialFingerprint(token)
	cacheKey := viewerCacheKey(ws, fp, githubViewerSourceConnector)
	if cached, ok := m.viewer.get(cacheKey, now); ok {
		return cached
	}

	if err := m.ensureViewerGrant(r.Context(), ws); err != nil {
		return githubViewerIdentity{
			Status:  githubViewerStatusUnavailable,
			Source:  githubViewerSourceNone,
			Message: "GitHub viewer grant unavailable: " + sanitizeWarning(err),
		}
	}

	userID := "unknown"
	if identity, ok := middleware.UserIdentityFromContext(r.Context()); ok && strings.TrimSpace(identity.UserID) != "" {
		userID = strings.TrimSpace(identity.UserID)
	}
	res, dispatchErr := m.dispatcher.Dispatch(r.Context(), connector.Request{
		WorkspaceKey: ws,
		RunID:        "webui-review:" + userID + ":viewer:" + providers.ActionGitHubViewerRead,
		BindingID:    bindingID,
		ConnectorID:  connectorID,
		Action:       providers.ActionGitHubViewerRead,
		Resource:     viewerResource,
		CallSeq:      0,
	})
	if dispatchErr != nil {
		return m.mapViewerDispatchError(dispatchErr, ws, githubViewerSourceConnector, connectorID)
	}
	login := strings.TrimSpace(stringValue(res.Body["login"]))
	if login == "" {
		return githubViewerIdentity{
			Status:      githubViewerStatusError,
			Source:      githubViewerSourceConnector,
			ConnectorID: connectorID,
			ObservedAt:  now.UTC().Format(time.RFC3339),
			Message:     "GitHub viewer response missing login",
		}
	}
	out := githubViewerIdentity{
		Status:      githubViewerStatusAvailable,
		Login:       login,
		Source:      githubViewerSourceConnector,
		ConnectorID: connectorID,
		ObservedAt:  now.UTC().Format(time.RFC3339),
	}
	m.viewer.put(cacheKey, out, now.Add(viewerCacheTTL))
	return out
}

func (m *Module) resolveViewerViaGh(ctx context.Context, ws string) githubViewerIdentity {
	now := m.clock()
	cacheKey := viewerCacheKey(ws, "gh_cli", githubViewerSourceGhCLI)
	if cached, ok := m.viewer.get(cacheKey, now); ok {
		return cached
	}

	lookup := m.lookupGhUser
	if lookup == nil {
		lookup = defaultGhUserLookup
	}
	login, err := lookup(ctx)
	if err != nil {
		msg := sanitizeWarning(err)
		if msg == "" {
			msg = "local gh viewer lookup failed"
		}
		status := githubViewerStatusUnavailable
		if strings.Contains(strings.ToLower(msg), "rate limit") {
			status = githubViewerStatusRateLimited
		} else if !strings.Contains(strings.ToLower(msg), "not found") &&
			!strings.Contains(strings.ToLower(msg), "not installed") &&
			!strings.Contains(strings.ToLower(msg), "auth") {
			status = githubViewerStatusError
		}
		return githubViewerIdentity{
			Status:     status,
			Source:     githubViewerSourceGhCLI,
			ObservedAt: now.UTC().Format(time.RFC3339),
			Message:    msg,
		}
	}
	login = strings.TrimSpace(login)
	if login == "" {
		return githubViewerIdentity{
			Status:     githubViewerStatusError,
			Source:     githubViewerSourceGhCLI,
			ObservedAt: now.UTC().Format(time.RFC3339),
			Message:    "gh api user returned empty login",
		}
	}
	out := githubViewerIdentity{
		Status:     githubViewerStatusAvailable,
		Login:      login,
		Source:     githubViewerSourceGhCLI,
		ObservedAt: now.UTC().Format(time.RFC3339),
	}
	m.viewer.put(cacheKey, out, now.Add(viewerCacheTTL))
	return out
}

func defaultGhUserLookup(ctx context.Context) (string, error) {
	result := cli.GetDeps(nil).ExecCtx.Run(ctx, "", "gh", "api", "user")
	if result.Err != nil {
		msg := strings.TrimSpace(result.Stderr)
		if msg == "" {
			msg = result.Err.Error()
		}
		return "", errors.New(msg)
	}
	var payload struct {
		Login string `json:"login"`
	}
	if err := json.Unmarshal([]byte(result.Stdout), &payload); err != nil {
		return "", fmt.Errorf("decode gh api user: %w", err)
	}
	return strings.TrimSpace(payload.Login), nil
}

func (m *Module) mapViewerDispatchError(err error, ws, source, connID string) githubViewerIdentity {
	now := m.clock()
	var rl *providers.RateLimited
	if errors.As(err, &rl) {
		msg := "GitHub rate limited viewer lookup"
		if rl.RetryAfter > 0 {
			msg = fmt.Sprintf("%s (retry after %ds)", msg, int(rl.RetryAfter.Round(time.Second)/time.Second))
		}
		return githubViewerIdentity{
			Status:      githubViewerStatusRateLimited,
			Source:      source,
			ConnectorID: connID,
			ObservedAt:  now.UTC().Format(time.RFC3339),
			Message:     msg,
		}
	}
	var ue *providers.UpstreamError
	if errors.As(err, &ue) && ue.Status == http.StatusUnauthorized {
		m.viewer.clearWorkspace(ws)
		return githubViewerIdentity{
			Status:      githubViewerStatusUnavailable,
			Source:      source,
			ConnectorID: connID,
			ObservedAt:  now.UTC().Format(time.RFC3339),
			Message:     "GitHub credential rejected for viewer lookup",
		}
	}
	return githubViewerIdentity{
		Status:      githubViewerStatusError,
		Source:      source,
		ConnectorID: connID,
		ObservedAt:  now.UTC().Format(time.RFC3339),
		Message:     "GitHub viewer lookup failed: " + sanitizeWarning(err),
	}
}

func (m *Module) attachGitHubViewer(r *http.Request, ws string, out *pullRequestsData, preferConnector bool) {
	if out == nil {
		return
	}
	out.GitHubViewer = m.resolveGitHubViewer(r, ws, preferConnector)
}
