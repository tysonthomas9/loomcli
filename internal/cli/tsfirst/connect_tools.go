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

func collectBackendTypedToolCalls(backend any, workDir string, policy *connectToolRuntime) []connectToolCall {
	reporter, ok := backend.(backendcaps.TypedToolCallReporter)
	if !ok {
		return nil
	}
	events := reporter.TypedToolCalls(workDir)
	if len(events) == 0 {
		return nil
	}
	allowed := indexConnectTypedTools(policy)
	out := make([]connectToolCall, 0, len(events))
	for index, event := range events {
		name := strings.TrimSpace(event.Name)
		if name == "" {
			continue
		}
		tool, authorized := allowed[name]
		status := strings.TrimSpace(event.Status)
		if status == "" {
			status = "completed"
		}
		authorizationStatus := strings.TrimSpace(event.AuthorizationStatus)
		if authorizationStatus == "" {
			if authorized {
				authorizationStatus = "authorized"
			} else {
				authorizationStatus = "denied"
			}
		}
		errorMessage := strings.TrimSpace(event.Error)
		redacted := event.Redacted
		if !authorized {
			status = "failed"
			errorMessage = fmt.Sprintf("typed tool call %q is not declared in the reviewed TypeScript tool manifest", name)
			redacted = true
		}
		out = append(out, connectToolCall{
			CallID:              event.CallID,
			Name:                name,
			Status:              status,
			ToolVersion:         tool.Version,
			SourceHash:          tool.SourceHash,
			Handler:             tool.Handler,
			Runtime:             tool.Runtime,
			ReadOnly:            tool.ReadOnly,
			Arguments:           cloneMap(event.Arguments),
			Result:              event.Result,
			Error:               errorMessage,
			StartedAt:           event.StartedAt,
			CompletedAt:         event.CompletedAt,
			DurationMS:          event.DurationMS,
			IdempotencyKey:      typedToolCallIdempotencyKey(event, name, index),
			AuthorizationStatus: authorizationStatus,
			Redacted:            redacted,
		})
	}
	return out
}

func indexConnectTypedTools(policy *connectToolRuntime) map[string]connectTypedTool {
	if policy == nil || len(policy.TypedTools) == 0 {
		return map[string]connectTypedTool{}
	}
	out := make(map[string]connectTypedTool, len(policy.TypedTools))
	for _, tool := range policy.TypedTools {
		name := strings.TrimSpace(tool.Name)
		if name == "" {
			continue
		}
		out[name] = tool
	}
	return out
}

func typedToolCallIdempotencyKey(event backendcaps.TypedToolCallEvent, name string, index int) string {
	if key := strings.TrimSpace(event.IdempotencyKey); key != "" {
		return key
	}
	callID := strings.TrimSpace(event.CallID)
	if callID == "" {
		callID = fmt.Sprintf("%d", index+1)
	}
	return "local-connect:" + name + ":" + callID
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

func cloneMap(in map[string]any) map[string]any {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]any, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
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
