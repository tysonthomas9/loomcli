package agentmcp

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/tysonthomas9/loomcli/internal/loomagent"
	"github.com/tysonthomas9/loomcli/internal/loomstore"
	"github.com/tysonthomas9/loomcli/internal/webui/handlers/agentsv1"
)

// The child-agent tools (design v2 §10.2), listed only for lead. The Agent
// API makes the caller each child's Parent and answers agent_not_found for
// any agent that is not the caller's own child.

type createIn struct {
	Preset      string            `json:"preset,omitempty" jsonschema:"the child's preset; only task"`
	Name        string            `json:"name" jsonschema:"a short name for the child"`
	Brief       string            `json:"brief" jsonschema:"the task brief, the child's first message"`
	Subject     *agentsv1.Subject `json:"subject,omitempty" jsonschema:"what the child works on"`
	ExternalKey string            `json:"externalKey,omitempty" jsonschema:"your own key for the task, for example task:<ticket>"`
}

func addAgentCreate(s *mcp.Server, b *bridge) {
	mcp.AddTool(s, &mcp.Tool{Name: "agent_create", Description: "Create a task agent as your child, in its own " +
		"worktree branched from your branch. It starts on the brief and you get task_completed when it finishes."},
		func(ctx context.Context, _ *mcp.CallToolRequest, in createIn) (*mcp.CallToolResult, agentsv1.Agent, error) {
			if in.Preset != "" && in.Preset != "task" {
				return nil, agentsv1.Agent{}, errors.New("agent_create makes task agents only")
			}
			body := agentsv1.CreateBody{Preset: "task", Name: in.Name, Repo: b.repo, ExternalKey: in.ExternalKey,
				FirstMessage: in.Brief, Overrides: agentsv1.Overrides{Harness: b.harness}}
			if in.Subject != nil {
				body.Subject = *in.Subject
			}
			// The same call made again (a retry) returns the same child.
			key, _ := json.Marshal(in)
			sum := sha256.Sum256(key)
			a, err := b.api.Create(ctx, "agent_create-"+hex.EncodeToString(sum[:16]), body)
			return nil, a, err
		})
}

type listIn struct {
	State string `json:"state,omitempty" jsonschema:"only children in this state"`
}

type listOut struct {
	Agents []agentsv1.Agent `json:"agents"`
}

func addAgentList(s *mcp.Server, b *bridge) {
	mcp.AddTool(s, &mcp.Tool{Name: "agent_list", Description: "List your child agents."},
		func(ctx context.Context, _ *mcp.CallToolRequest, in listIn) (*mcp.CallToolResult, listOut, error) {
			out := listOut{Agents: []agentsv1.Agent{}}
			f := loomstore.AgentFilter{State: in.State}
			for {
				page, err := b.api.List(ctx, f)
				if err != nil {
					return nil, listOut{}, err
				}
				out.Agents = append(out.Agents, page.Agents...)
				if f.After = page.Next; f.After == "" {
					return nil, out, nil
				}
			}
		})
}

type agentIn struct {
	Agent string `json:"agent" jsonschema:"the child's agent id"`
}

func addAgentGet(s *mcp.Server, b *bridge) {
	mcp.AddTool(s, &mcp.Tool{Name: "agent_get", Description: "Get one of your child agents."},
		func(ctx context.Context, _ *mcp.CallToolRequest, in agentIn) (*mcp.CallToolResult, agentsv1.Agent, error) {
			a, err := b.api.Get(ctx, in.Agent)
			return nil, a, err
		})
}

type sendIn struct {
	Agent     string `json:"agent" jsonschema:"the child's agent id"`
	Text      string `json:"text" jsonschema:"the message"`
	Interrupt bool   `json:"interrupt,omitempty" jsonschema:"stop the child's running turn and hand this message over first"`
}

func addAgentSend(s *mcp.Server, b *bridge) {
	mcp.AddTool(s, &mcp.Tool{Name: "agent_send", Description: "Send a message to one of your child agents; " +
		"to a finished task it starts a new attempt. replaced is true when it replaced your earlier message " +
		"still waiting for that child, so include both instructions."},
		func(ctx context.Context, _ *mcp.CallToolRequest, in sendIn) (*mcp.CallToolResult, agentsv1.SendResult, error) {
			send := b.api.Send
			if in.Interrupt {
				send = b.api.Interrupt
			}
			r, err := send(ctx, requestID(), in.Agent, in.Text)
			return nil, r, err
		})
}

type archiveIn struct {
	Agent  string `json:"agent" jsonschema:"the child's agent id"`
	Cancel bool   `json:"cancel,omitempty" jsonschema:"stop its work as cancelled instead of done"`
}

type archiveOut struct {
	Archived bool `json:"archived"`
}

func addAgentArchive(s *mcp.Server, b *bridge) {
	mcp.AddTool(s, &mcp.Tool{Name: "agent_archive", Description: "Archive one of your child agents."},
		func(ctx context.Context, _ *mcp.CallToolRequest, in archiveIn) (*mcp.CallToolResult, archiveOut, error) {
			reason := loomagent.ArchiveDone
			if in.Cancel {
				reason = loomagent.ArchiveCancelled
			}
			err := b.api.Archive(ctx, requestID(), in.Agent, reason)
			return nil, archiveOut{Archived: err == nil}, err
		})
}

func requestID() string { return "agent_tool-" + rand.Text() }
