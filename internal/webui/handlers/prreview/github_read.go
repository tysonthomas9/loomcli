package prreview

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/tysonthomas9/loomcli/internal/connector"
	"github.com/tysonthomas9/loomcli/internal/connector/providers"
	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/stackpublish"
	"github.com/tysonthomas9/loomcli/internal/webui/storeadapter"
)

// GitHubRead serves an agent's github_read op (R-B) through the host GitHub
// connector, with the host credential, on the one GitHub repo registered in
// ws at repoPath, the agent's own clone. The repo comes from the workspace's
// registry, never from the agent or its clone's git config. stack_health is
// `loom stack status`'s live PR health, from the host's stack forge.
func (m *Module) GitHubRead(ctx context.Context, ws, agentID, repoPath, op string, args map[string]any) (map[string]any, error) {
	body, err := m.githubRead(ctx, ws, agentID, repoPath, op, args)
	if errors.Is(err, errEgressUnavailable) {
		err = errNoGitHubToken
	}
	return body, err
}

// errNoGitHubToken is github_read's answer when the host has no GitHub token.
var errNoGitHubToken = errors.New("GitHub is not configured on this Loom host: no GitHub token is set in its settings")

func (m *Module) githubRead(ctx context.Context, ws, agentID, repoPath, op string, args map[string]any) (map[string]any, error) {
	owner, repo, err := m.boundRepo(ctx, ws, repoPath)
	if err != nil {
		return nil, err
	}
	if _, ok := providers.GitHubReadOps[op]; !ok && op != "stack_health" {
		return nil, fmt.Errorf("github_read has no op %q: %w", op, domain.ErrInvalid)
	}
	if err := m.ensureConnectorAndGrants(ctx, ws, owner, repo, []string{providers.ActionGitHubRead}); err != nil {
		return nil, err
	}
	if op == "stack_health" {
		return m.stackHealth(ctx, owner, repo, args)
	}
	return m.dispatchRead(ctx, ws, "agent-github-read:"+agentID+":", owner, repo, op, args)
}

// dispatchRead is one github.read op on owner/repo through the dispatcher,
// which authorizes it and hands the provider the host credential in CallSpec
// only. The run is runPrefix and a nonce.
func (m *Module) dispatchRead(ctx context.Context, ws, runPrefix, owner, repo, op string, args map[string]any) (map[string]any, error) {
	nonce, err := randomHex(8)
	if err != nil {
		return nil, err
	}
	call := map[string]any{"op": op, "owner": owner, "repo": repo}
	for k, v := range args {
		if k != "op" && k != "owner" && k != "repo" {
			call[k] = v
		}
	}
	res, err := m.dispatcher.Dispatch(ctx, connector.Request{WorkspaceKey: ws, RunID: runPrefix + nonce,
		BindingID: bindingID, ConnectorID: connectorID, Action: providers.ActionGitHubRead,
		Resource: prResource(owner, repo), Args: call})
	return res.Body, err
}

// boundRepo is the GitHub owner/repo of the workspace repo cloned at
// repoPath; any other path is refused.
func (m *Module) boundRepo(ctx context.Context, ws, repoPath string) (string, string, error) {
	data, err := storeadapter.BuildWorkspaceDataForKey(ctx, m.store, ws)
	if err != nil {
		return "", "", err
	}
	for _, r := range data.Repos {
		if r.Path == "" || repoPath == "" || filepath.Clean(r.Path) != filepath.Clean(repoPath) {
			continue
		}
		if owner, repo, ok := parseGitHubOwnerRepo(r.RemoteURL); ok {
			return owner, repo, nil
		}
	}
	return "", "", fmt.Errorf("the agent's repo %q is not a GitHub repo of workspace %s: %w", repoPath, ws, domain.ErrNotOwner)
}

// stackHealth is checks, review and mergeable for the open PRs whose head
// starts with args.head, as `loom stack status` shows them, one bounded page
// (args.page, args.perPage) at a time, in head order.
func (m *Module) stackHealth(ctx context.Context, owner, repo string, args map[string]any) (map[string]any, error) {
	token, err := m.resolveGitHubToken()
	if err != nil {
		return nil, err
	}
	page, per, err := providers.ReadPage(args)
	if err != nil {
		return nil, err
	}
	prefix, _ := args["head"].(string)
	forge := stackpublish.NewGitHubForge(token, nil, os.Getenv(connector.GitHubBaseURLEnvVar))
	health, err := forge.PRStatuses(ctx, owner, repo, prefix)
	if err != nil { // the forge scrubs only env tokens; this one may be from settings
		return nil, errors.New(strings.ReplaceAll(err.Error(), token, "[redacted]"))
	}
	items := []any{}
	for head, s := range health {
		items = append(items, map[string]any{"head": head, "number": s.Number, "checks": s.Checks, "review": s.Review, "mergeable": s.Mergeable})
	}
	sort.Slice(items, func(i, j int) bool {
		return items[i].(map[string]any)["head"].(string) < items[j].(map[string]any)["head"].(string)
	})
	start, end := len(items), len(items)
	if page <= len(items)/per+1 {
		start, end = (page-1)*per, min(page*per, len(items))
	}
	out := map[string]any{"op": "stack_health", "items": items[start:end]}
	if end < len(items) {
		out["next"] = strconv.Itoa(page + 1)
	}
	return out, nil
}
