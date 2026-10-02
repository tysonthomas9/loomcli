package agentmcp

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/tysonthomas9/loomcli/internal/webui/handlers/agentsv1"
)

// addGitHubRead is github_read (R-B): typed, allowlisted GitHub reads of the
// agent's own repo, served by serve's host GitHub connector with the host's
// credential. The tool has no repo, method, URL, GraphQL or shell argument,
// and the bridge never holds a GitHub token.
func addGitHubRead(s *mcp.Server, b *bridge) {
	mcp.AddTool(s, &mcp.Tool{Name: "github_read", Description: "Read GitHub for your repo: PRs (view, diff files, " +
		"list, search, reviews, review comments), issues (view, list, search, comments), checks and commit status, " +
		"Actions runs, jobs and job logs, repo, releases, commits, compare, branches, contents, users, and stack_health " +
		"(checks, review and mergeable for open PRs whose head starts with head). Lists page: pass next back as page."},
		func(ctx context.Context, _ *mcp.CallToolRequest, in agentsv1.GitHubReadBody) (*mcp.CallToolResult, agentsv1.GitHubReadResult, error) {
			r, err := b.api.GitHubRead(ctx, in)
			return nil, r, err
		})
}
