package tsfirst

import (
	"bytes"
	"context"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	backendcaps "github.com/tysonthomas9/loomcli/internal/cli/backends"
	defspkg "github.com/tysonthomas9/loomcli/internal/defs"
)

//go:embed typed_tool_runner.js
var typedToolRunnerSource string

const (
	connectToolRuntimeDeclared = "declared"
	connectToolRuntimeEcho     = "offline_echo_no_model_tool_execution"
	connectToolRuntimeBackend  = "backend_typed_tool_runtime"

	connectToolHandlerExecutionConfigured = "trusted_executor_configured"
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
			Timeout:     tool.Timeout,
			Cancellable: tool.Cancellable,
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

func enforceBackendTypedTools(backendName string, backend any, policy *connectToolRuntime, root string) (*connectToolRuntime, error) {
	if policy == nil || len(policy.TypedTools) == 0 {
		if runtime, ok := backend.(backendcaps.TypedToolRuntimeBackend); ok {
			_ = runtime.SetTypedTools(nil)
		}
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
	if executorBackend, ok := backend.(backendcaps.TypedToolExecutorBackend); ok {
		if err := executorBackend.SetTypedToolExecutor(newLocalConnectTypedToolExecutor(root, policy)); err != nil {
			return nil, fmt.Errorf("configure trusted typed tool executor for backend %q: %w", backendName, err)
		}
		out.HandlerExecution = connectToolHandlerExecutionConfigured
		out.Message = "typed model tools and Loom trusted handler executor were handed to the backend before prompt execution"
	}
	return out, nil
}

func beginBackendTypedToolInvocation(backend any) {
	if lifecycle, ok := backend.(backendcaps.TypedToolInvocationLifecycleBackend); ok {
		lifecycle.BeginTypedToolInvocation()
	}
}

func ingestBackendTypedToolProviderLine(ctx context.Context, backend any, line string) {
	if bridge, ok := backend.(backendcaps.TypedToolProviderLineBackend); ok {
		bridge.IngestTypedToolProviderLine(ctx, line)
	}
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
			Timeout:     tool.Timeout,
			Cancellable: tool.Cancellable,
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
			Timeout:             tool.Timeout,
			Cancellable:         tool.Cancellable,
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

type localConnectTypedToolExecutor struct {
	root  string
	tools map[string]connectTypedTool
}

func newLocalConnectTypedToolExecutor(root string, policy *connectToolRuntime) *localConnectTypedToolExecutor {
	return &localConnectTypedToolExecutor{
		root:  strings.TrimSpace(root),
		tools: indexConnectTypedTools(policy),
	}
}

func (e *localConnectTypedToolExecutor) ExecuteTypedTool(ctx context.Context, request backendcaps.TypedToolExecutionRequest) (backendcaps.TypedToolCallEvent, error) {
	started := time.Now().UTC()
	name := strings.TrimSpace(request.Name)
	event := backendcaps.TypedToolCallEvent{
		CallID:         strings.TrimSpace(request.CallID),
		Name:           name,
		Arguments:      cloneMap(request.Arguments),
		StartedAt:      started.Format(time.RFC3339Nano),
		IdempotencyKey: strings.TrimSpace(request.IdempotencyKey),
	}
	if event.IdempotencyKey == "" {
		event.IdempotencyKey = typedToolExecutionIdempotencyKey(name, event.CallID)
	}
	tool, ok := e.tools[name]
	if name == "" || !ok {
		return e.finishToolExecutionEvent(event, started, "failed", "denied", "typed tool call is not declared in the reviewed TypeScript tool manifest", true), nil
	}
	if err := e.validateToolSource(tool); err != nil {
		return e.finishToolExecutionEvent(event, started, "failed", "denied", err.Error(), true), nil
	}
	execCtx := ctx
	cancel := func() {}
	if timeout := strings.TrimSpace(tool.Timeout); timeout != "" {
		duration, err := time.ParseDuration(timeout)
		if err != nil {
			return e.finishToolExecutionEvent(event, started, "failed", "denied", "invalid typed tool timeout: "+err.Error(), true), nil
		}
		execCtx, cancel = context.WithTimeout(ctx, duration)
	}
	defer cancel()
	result, err := executeTypeScriptTypedTool(execCtx, e.root, tool, event.Arguments)
	if err != nil {
		status := "failed"
		if errors.Is(execCtx.Err(), context.DeadlineExceeded) {
			err = fmt.Errorf("typed tool execution deadline exceeded: %w", execCtx.Err())
		}
		return e.finishToolExecutionEvent(event, started, status, "authorized", err.Error(), false), nil
	}
	event.Result = result
	return e.finishToolExecutionEvent(event, started, "completed", "authorized", "", false), nil
}

func (e *localConnectTypedToolExecutor) finishToolExecutionEvent(event backendcaps.TypedToolCallEvent, started time.Time, status, authorization, message string, redacted bool) backendcaps.TypedToolCallEvent {
	completed := time.Now().UTC()
	event.Status = status
	event.AuthorizationStatus = authorization
	event.CompletedAt = completed.Format(time.RFC3339Nano)
	event.DurationMS = completed.Sub(started).Milliseconds()
	event.Redacted = redacted
	if message != "" {
		event.Error = message
	}
	return event
}

func (e *localConnectTypedToolExecutor) validateToolSource(tool connectTypedTool) error {
	if strings.TrimSpace(e.root) == "" {
		return fmt.Errorf("typed tool executor root is not configured")
	}
	sourcePath := strings.TrimSpace(tool.SourcePath)
	if sourcePath == "" {
		return fmt.Errorf("typed tool %q has no source path", tool.Name)
	}
	absRoot, err := filepath.Abs(e.root)
	if err != nil {
		return err
	}
	absSource, err := filepath.Abs(sourcePath)
	if err != nil {
		return err
	}
	rel, err := filepath.Rel(absRoot, absSource)
	if err != nil {
		return err
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return fmt.Errorf("typed tool %q source path resolves outside project root", tool.Name)
	}
	data, err := os.ReadFile(absSource)
	if err != nil {
		return fmt.Errorf("read typed tool %q source: %w", tool.Name, err)
	}
	hash := sha256.Sum256(data)
	if want := strings.TrimSpace(tool.SourceHash); want != "" && !strings.EqualFold(hex.EncodeToString(hash[:]), want) {
		return fmt.Errorf("typed tool %q source hash does not match reviewed manifest", tool.Name)
	}
	return nil
}

func typedToolExecutionIdempotencyKey(name, callID string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		name = "unknown"
	}
	callID = strings.TrimSpace(callID)
	if callID == "" {
		callID = "1"
	}
	return "local-connect:" + name + ":" + callID
}

func executeTypeScriptTypedTool(ctx context.Context, root string, tool connectTypedTool, args map[string]any) (any, error) {
	request := map[string]any{
		"root":       root,
		"sourcePath": tool.SourcePath,
		"arguments":  args,
	}
	payload, err := json.Marshal(request)
	if err != nil {
		return nil, err
	}
	cmd := exec.CommandContext(ctx, "node", "--no-warnings", "-e", typedToolRunnerSource) //nolint:gosec // Runner source is embedded; tool source path is hash-checked against the reviewed manifest.
	cmd.Stdin = bytes.NewReader(payload)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, fmt.Errorf("execute TypeScript typed tool %q: %w: %s", tool.Name, err, strings.TrimSpace(stderr.String()))
	}
	var response struct {
		Result any `json:"result"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &response); err != nil {
		return nil, fmt.Errorf("decode TypeScript typed tool %q result: %w", tool.Name, err)
	}
	return response.Result, nil
}
