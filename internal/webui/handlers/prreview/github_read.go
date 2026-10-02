package prreview

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"

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
	res, err := m.dispatcher.Dispatch(ctx, connector.Request{WorkspaceKey: ws, RunID: "agent-github-read:" + agentID + ":" + nonce,
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
// starts with args.head, as `loom stack status` shows them.
func (m *Module) stackHealth(ctx context.Context, owner, repo string, args map[string]any) (map[string]any, error) {
	token, err := m.resolveGitHubToken()
	if err != nil {
		return nil, err
	}
	prefix, _ := args["head"].(string)
	forge := stackpublish.NewGitHubForge(token, nil, os.Getenv(connector.GitHubBaseURLEnvVar))
	health, err := forge.PRStatuses(ctx, owner, repo, prefix)
	if err != nil {
		return nil, err
	}
	items := []any{}
	for head, s := range health {
		items = append(items, map[string]any{"head": head, "number": s.Number, "checks": s.Checks, "review": s.Review, "mergeable": s.Mergeable})
	}
	sort.Slice(items, func(i, j int) bool {
		return items[i].(map[string]any)["head"].(string) < items[j].(map[string]any)["head"].(string)
	})
	return map[string]any{"op": "stack_health", "items": items}, nil
}
