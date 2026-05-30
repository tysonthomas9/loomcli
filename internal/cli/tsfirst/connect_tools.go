package tsfirst

import (
	"fmt"
	"strings"

	backendcaps "github.com/tysonthomas9/loomcli/internal/cli/backends"
	defspkg "github.com/tysonthomas9/loomcli/internal/defs"
)

const (
	connectToolRuntimeDeclared = "declared"
	connectToolRuntimeEcho     = "offline_echo_no_model_tool_execution"
	connectToolRuntimeBackend  = "backend_typed_tool_runtime"
)

func localConnectToolRuntime(plan *defspkg.Plan, agent defspkg.AgentModule) *connectToolRuntime {
	tools := localConnectTypedTools(plan, agent)
	if len(tools) == 0 {
		return nil
	}
	return &connectToolRuntime{
		Status:     connectToolRuntimeDeclared,
		Message:    "typed model tools are declared and require echo offline mode or a backend typed-tool runtime before prompt execution",
		TypedTools: tools,
	}
}

func localConnectTypedTools(plan *defspkg.Plan, agent defspkg.AgentModule) []connectTypedTool {
	if plan == nil || len(agent.Tools) == 0 || len(plan.Tools) == 0 {
		return nil
	}
	byName := make(map[string]defspkg.ToolModule, len(plan.Tools))
	for _, tool := range plan.Tools {
		name := strings.TrimSpace(tool.Name)
		if name == "" {
			continue
		}
		byName[name] = tool
	}
	out := make([]connectTypedTool, 0, len(agent.Tools))
	for _, name := range compactStrings(agent.Tools) {
		tool, ok := byName[name]
		if !ok {
			continue
		}
		out = append(out, connectTypedTool{
			Name:        tool.Name,
			Description: tool.Description,
			Version:     tool.Version,
			SourcePath:  tool.SourcePath,
			SourceHash:  tool.SourceHash,
			Parameters:  tool.Parameters,
			Handler:     tool.Handler,
			Runtime:     tool.Runtime,
			Repos:       compactStrings(tool.Repos),
			Env:         compactStrings(tool.Env),
			ReadOnly:    tool.ReadOnly,
		})
	}
	return out
}

func echoToolRuntime(policy *connectToolRuntime) *connectToolRuntime {
	if policy == nil || len(policy.TypedTools) == 0 {
		return nil
	}
	out := cloneToolRuntime(policy)
	out.Status = connectToolRuntimeEcho
	out.Message = "typed tools are visible in local session evidence, but the echo backend does not execute model tool calls"
	return out
}

func enforceBackendTypedTools(backendName string, backend any, policy *connectToolRuntime) (*connectToolRuntime, error) {
	if policy == nil || len(policy.TypedTools) == 0 {
		return nil, nil
	}
	runtime, ok := backend.(backendcaps.TypedToolRuntimeBackend)
	if !ok {
		return nil, fmt.Errorf("backend %q cannot run TypeScript-defined model tools for local connect: %s; use backend \"echo\" for offline prompt testing or a backend with typed-tool runtime support", backendName, describeTypedTools(policy.TypedTools))
	}
	if err := runtime.SetTypedTools(backendTypedToolDefinitions(policy.TypedTools)); err != nil {
		return nil, fmt.Errorf("configure typed model tools for backend %q: %w", backendName, err)
	}
	out := cloneToolRuntime(policy)
	out.Status = connectToolRuntimeBackend
	out.Message = "typed model tools were handed to the backend typed-tool runtime before prompt execution"
	return out, nil
}

func backendTypedToolDefinitions(tools []connectTypedTool) []backendcaps.TypedToolDefinition {
	out := make([]backendcaps.TypedToolDefinition, 0, len(tools))
	for _, tool := range tools {
		out = append(out, backendcaps.TypedToolDefinition{
			Name:        tool.Name,
			Description: tool.Description,
			Version:     tool.Version,
			SourcePath:  tool.SourcePath,
			SourceHash:  tool.SourceHash,
			Parameters:  tool.Parameters,
			Handler:     tool.Handler,
			Runtime:     tool.Runtime,
			Repos:       compactStrings(tool.Repos),
			Env:         compactStrings(tool.Env),
			ReadOnly:    tool.ReadOnly,
		})
	}
	return out
}

func cloneToolRuntime(policy *connectToolRuntime) *connectToolRuntime {
	if policy == nil {
		return nil
	}
	out := *policy
	if len(policy.TypedTools) > 0 {
		out.TypedTools = append([]connectTypedTool(nil), policy.TypedTools...)
	}
	return &out
}

func describeTypedTools(tools []connectTypedTool) string {
	parts := make([]string, 0, len(tools))
	for _, tool := range tools {
		name := strings.TrimSpace(tool.Name)
		if name == "" {
			continue
		}
		detail := name
		if handler := strings.TrimSpace(tool.Handler); handler != "" {
			detail += " handler=" + handler
		}
		if runtime := strings.TrimSpace(tool.Runtime); runtime != "" {
			detail += " runtime=" + runtime
		}
		parts = append(parts, detail)
	}
	if len(parts) == 0 {
		return "typed tools declared"
	}
	return strings.Join(parts, ", ")
}

func typedToolNames(policy *connectToolRuntime) string {
	if policy == nil {
		return ""
	}
	names := make([]string, 0, len(policy.TypedTools))
	for _, tool := range policy.TypedTools {
		if name := strings.TrimSpace(tool.Name); name != "" {
			names = append(names, name)
		}
	}
	return strings.Join(names, ", ")
}
