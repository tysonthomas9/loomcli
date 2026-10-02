package agentmcp

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/webui/handlers/agentsv1"
)

// hostRead is a host GitHub reader that records what the Agent API asked.
type hostRead struct {
	mu    sync.Mutex
	calls []string
}

func (h *hostRead) read(_ context.Context, ws, agentID, repoPath, op string, args map[string]any) (map[string]any, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	raw, _ := json.Marshal(args)
	h.calls = append(h.calls, fmt.Sprintf("%s %s %s %s %s", ws, agentID, repoPath, op, raw))
	if op == "graphql" {
		return nil, fmt.Errorf("github_read has no op %q: %w", op, domain.ErrInvalid)
	}
	return map[string]any{"op": op, "item": map[string]any{"number": 8.0}, "next": "2"}, nil
}

var githubTools = append(append([]string{}, leadTools...), "github_read")

// TestGitHubReadToolsReadOnly (bridge): github_read takes only typed read
// arguments. A method, URL, raw path, GraphQL query, shell command or
// repo field is refused by the tool's schema before the Agent API is
// called; an op outside the allowlist is a typed github_invalid error.
func TestGitHubReadToolsReadOnly(t *testing.T) {
	host := &hostRead{}
	srv, tokens := agentAPI(t, host.read)
	cs := connect(t, Config{API: srv.URL, Workspace: "ws", Token: tokens.Agent("ws", "a1"), Repo: "/repo", Tools: githubTools})
	for _, extra := range []map[string]any{{"method": "POST"}, {"url": "https://api.github.com/graphql"}, {"endpoint": "/repos/x/y/merges"},
		{"graphql": "mutation{}"}, {"command": "gh pr merge 8"}, {"owner": "evil"}, {"repo": "evil/other"}, {"args": []any{"pr", "merge"}}} {
		args := map[string]any{"op": "pr_view", "number": 8}
		for k, v := range extra {
			args[k] = v
		}
		if res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: "github_read", Arguments: args}); err == nil && !res.IsError {
			t.Errorf("github_read with %v was accepted", extra)
		}
	}
	if _, msg := call(t, cs, "github_read", map[string]any{"op": "graphql"}); !strings.Contains(msg, "github_invalid") && !strings.Contains(msg, "no op") {
		t.Errorf("op graphql = %q; want github_invalid", msg)
	}
	out, msg := call(t, cs, "github_read", map[string]any{"op": "pr_view", "number": 8})
	if msg != "" || out["op"] != "pr_view" || out["next"] != "2" || out["item"].(map[string]any)["number"] != 8.0 {
		t.Fatalf("pr_view = %v %q", out, msg)
	}
	if got := host.calls; len(got) != 2 || got[1] != `ws a1 /repo pr_view {"number":8}` {
		t.Errorf("host reads = %q; want only the typed pr_view", got)
	}
}

// TestGitHubReadRepoScope (bridge): the read is always on the calling
// agent's own repo, named by its token; an agent without github_read, a
// token for another workspace or a forged token reaches nothing.
func TestGitHubReadRepoScope(t *testing.T) {
	host := &hostRead{}
	srv, tokens := agentAPI(t, host.read)
	for name, cfg := range map[string]Config{
		"task agent without github_read": {Token: tokens.Agent("ws", "c1")},
		"another workspace's token":      {Token: tokens.Agent("ws2", "a1")},
		"forged token":                   {Token: agentsv1.NewTokens([]byte(strings.Repeat("x", 32))).Agent("ws", "a1")},
	} {
		cfg.API, cfg.Workspace, cfg.Repo, cfg.Tools = srv.URL, "ws", "/repo", githubTools
		cs := connect(t, cfg)
		if _, msg := call(t, cs, "github_read", map[string]any{"op": "repo_view"}); msg == "" {
			t.Errorf("%s: github_read succeeded", name)
		}
	}
	b1 := connect(t, Config{API: srv.URL, Workspace: "ws", Token: tokens.Agent("ws", "b1"), Repo: "/other", Tools: githubTools})
	if _, msg := call(t, b1, "github_read", map[string]any{"op": "repo_view"}); msg != "" {
		t.Fatal(msg)
	}
	if len(host.calls) != 1 || host.calls[0] != "ws b1 /repo repo_view {}" {
		t.Errorf("host reads = %q; want b1's recorded repo, not its bridge settings", host.calls)
	}
}

// TestGitHubReadCredentialIsolation (bridge): the bridge's launch settings
// carry its Loom token only, never a GitHub token, even when the host has
// one in its environment.
func TestGitHubReadCredentialIsolation(t *testing.T) {
	t.Setenv("GITHUB_TOKEN", "ghp-host-token")
	t.Setenv("GH_TOKEN", "ghp-host-token")
	env := Config{API: "http://127.0.0.1:1", Workspace: "ws", Token: "loom-agent-token", Repo: "/repo", Tools: githubTools}.Env()
	for k, v := range env {
		if strings.Contains(k, "GH") || strings.Contains(k, "GITHUB") || strings.Contains(v, "ghp-host-token") {
			t.Errorf("bridge env %s=%s carries a GitHub credential", k, v)
		}
	}
}
