package backends

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"
)

type typedToolRuntimeBridge struct {
	provider string
	mu       sync.Mutex
	tools    map[string]TypedToolDefinition
	executor TypedToolExecutor
	calls    []TypedToolCallEvent
	seen     map[string]bool
}

func newTypedToolRuntimeBridge(provider string) *typedToolRuntimeBridge {
	return &typedToolRuntimeBridge{provider: strings.TrimSpace(provider)}
}

func (b *typedToolRuntimeBridge) SetTypedTools(tools []TypedToolDefinition) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.tools = make(map[string]TypedToolDefinition, len(tools))
	for _, tool := range tools {
		name := strings.TrimSpace(tool.Name)
		if name == "" {
			continue
		}
		tool.Name = name
		b.tools[name] = tool
	}
	return nil
}

func (b *typedToolRuntimeBridge) SetTypedToolExecutor(executor TypedToolExecutor) error {
	if executor == nil {
		return fmt.Errorf("typed tool executor is required")
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.executor = executor
	return nil
}

func (b *typedToolRuntimeBridge) BeginTypedToolInvocation() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.calls = nil
	b.seen = make(map[string]bool)
}

func (b *typedToolRuntimeBridge) TypedToolCalls(_ string) []TypedToolCallEvent {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := make([]TypedToolCallEvent, len(b.calls))
	for i, call := range b.calls {
		out[i] = cloneTypedToolCallEvent(call)
	}
	return out
}

func (b *typedToolRuntimeBridge) IngestTypedToolProviderLine(ctx context.Context, line string) {
	if ctx == nil {
		ctx = context.Background()
	}
	if strings.TrimSpace(line) == "" {
		return
	}
	var value any
	if err := json.Unmarshal([]byte(line), &value); err != nil {
		return
	}
	for _, call := range extractProviderTypedToolCalls(value) {
		b.handleProviderTypedToolCall(ctx, call)
	}
}

func (b *typedToolRuntimeBridge) handleProviderTypedToolCall(ctx context.Context, call providerTypedToolCall) {
	name := strings.TrimSpace(call.name)
	if name == "" {
		return
	}
	key := call.dedupeKey()
	b.mu.Lock()
	if b.seen == nil {
		b.seen = make(map[string]bool)
	}
	if b.seen[key] {
		b.mu.Unlock()
		return
	}
	tool, allowed := b.tools[name]
	executor := b.executor
	if !allowed && !call.explicit {
		b.mu.Unlock()
		return
	}
	b.seen[key] = true
	b.mu.Unlock()

	if !allowed {
		b.appendTypedToolCall(deniedProviderTypedToolCall(call, "typed tool call "+quote(name)+" is not declared in the reviewed TypeScript tool manifest"))
		return
	}
	if executor == nil {
		b.appendTypedToolCall(deniedProviderTypedToolCall(call, "typed tool executor is not configured"))
		return
	}

	request := TypedToolExecutionRequest{
		CallID:    strings.TrimSpace(call.callID),
		Name:      tool.Name,
		Arguments: cloneAnyMap(call.arguments),
	}
	event, err := executor.ExecuteTypedTool(ctx, request)
	if err != nil {
		b.appendTypedToolCall(failedProviderTypedToolCall(call, err.Error()))
		return
	}
	b.appendTypedToolCall(event)
}

func (b *typedToolRuntimeBridge) appendTypedToolCall(event TypedToolCallEvent) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.calls = append(b.calls, cloneTypedToolCallEvent(event))
}

type providerTypedToolCall struct {
	callID    string
	name      string
	arguments map[string]any
	explicit  bool
}

func (c providerTypedToolCall) dedupeKey() string {
	if id := strings.TrimSpace(c.callID); id != "" {
		return strings.TrimSpace(c.name) + ":" + id
	}
	payload, _ := json.Marshal(struct {
		Name      string         `json:"name"`
		Arguments map[string]any `json:"arguments,omitempty"`
		Explicit  bool           `json:"explicit,omitempty"`
	}{Name: strings.TrimSpace(c.name), Arguments: c.arguments, Explicit: c.explicit})
	sum := sha256.Sum256(payload)
	return hex.EncodeToString(sum[:])
}

func extractProviderTypedToolCalls(value any) []providerTypedToolCall {
	var out []providerTypedToolCall
	extractProviderTypedToolCallsInto(value, false, &out)
	return compactProviderTypedToolCalls(out)
}

func extractProviderTypedToolCallsInto(value any, inheritedExplicit bool, out *[]providerTypedToolCall) {
	switch typed := value.(type) {
	case map[string]any:
		explicit := inheritedExplicit || mapHasExplicitTypedToolMarker(typed)
		if call, ok := providerTypedToolCallFromMap(typed, explicit); ok {
			*out = append(*out, call)
		}
		for key, nested := range typed {
			nestedExplicit := key == "loom_typed_tool" || key == "typed_tool" || key == "tool_call" || key == "tool_calls"
			extractProviderTypedToolCallsInto(nested, nestedExplicit, out)
		}
	case []any:
		for _, item := range typed {
			extractProviderTypedToolCallsInto(item, inheritedExplicit, out)
		}
	case string:
		extractExplicitTypedToolCallsFromText(typed, out)
	}
}

func extractExplicitTypedToolCallsFromText(text string, out *[]providerTypedToolCall) {
	if !strings.Contains(text, "loom.typed_tool") && !strings.Contains(text, "typed_tool.call") {
		return
	}
	for _, line := range strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "```") || (!strings.Contains(line, "loom.typed_tool") && !strings.Contains(line, "typed_tool.call")) {
			continue
		}
		for _, candidate := range explicitTypedToolJSONCandidates(line) {
			var value any
			if err := json.Unmarshal([]byte(candidate), &value); err != nil {
				continue
			}
			extractProviderTypedToolCallsInto(value, true, out)
		}
	}
}

func explicitTypedToolJSONCandidates(line string) []string {
	candidates := []string{line}
	if start, end := strings.Index(line, "{"), strings.LastIndex(line, "}"); start >= 0 && end > start {
		candidates = append(candidates, line[start:end+1])
	}
	if start, end := strings.Index(line, "["), strings.LastIndex(line, "]"); start >= 0 && end > start {
		candidates = append(candidates, line[start:end+1])
	}
	return candidates
}

func providerTypedToolCallFromMap(m map[string]any, explicit bool) (providerTypedToolCall, bool) {
	name := firstStringValue(m, "name", "tool_name", "toolName", "function_name", "functionName")
	if name == "" {
		name = nestedStringValue(m, "function", "name")
	}
	if name == "" {
		name = nestedStringValue(m, "functionCall", "name")
	}
	if name == "" {
		name = nestedStringValue(m, "function_call", "name")
	}
	if name == "" {
		return providerTypedToolCall{}, false
	}

	args, ok := firstArgumentsValue(m, "arguments", "args", "input", "parameters")
	if !ok {
		args, ok = nestedArgumentsValue(m, "function", "arguments")
	}
	if !ok {
		args, ok = nestedArgumentsValue(m, "functionCall", "args")
	}
	if !ok {
		args, ok = nestedArgumentsValue(m, "function_call", "arguments")
	}
	if !ok {
		args = map[string]any{}
	}

	toolish := explicit || mapLooksLikeProviderToolCall(m)
	if !toolish {
		return providerTypedToolCall{}, false
	}

	return providerTypedToolCall{
		callID:    firstStringValue(m, "call_id", "callID", "tool_call_id", "toolCallID", "id"),
		name:      strings.TrimSpace(name),
		arguments: args,
		explicit:  explicit,
	}, true
}

func mapHasExplicitTypedToolMarker(m map[string]any) bool {
	typ := strings.ToLower(firstStringValue(m, "type", "event", "kind"))
	return typ == "loom.typed_tool.call" ||
		typ == "loom.typed_tool_call" ||
		typ == "typed_tool.call" ||
		typ == "typed_tool_call" ||
		firstBoolValue(m, "loom_typed_tool", "typed_tool")
}

func mapLooksLikeProviderToolCall(m map[string]any) bool {
	typ := strings.ToLower(firstStringValue(m, "type", "event", "kind"))
	if strings.Contains(typ, "tool_use") || strings.Contains(typ, "tool_call") || strings.Contains(typ, "function_call") {
		return true
	}
	if _, ok := m["functionCall"]; ok {
		return true
	}
	if _, ok := m["function_call"]; ok {
		return true
	}
	if _, ok := m["tool_call"]; ok {
		return true
	}
	return false
}

func compactProviderTypedToolCalls(in []providerTypedToolCall) []providerTypedToolCall {
	seen := make(map[string]bool, len(in))
	out := make([]providerTypedToolCall, 0, len(in))
	for _, call := range in {
		if strings.TrimSpace(call.name) == "" {
			continue
		}
		key := call.dedupeKey()
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, call)
	}
	return out
}

func deniedProviderTypedToolCall(call providerTypedToolCall, message string) TypedToolCallEvent {
	started := time.Now().UTC()
	completed := time.Now().UTC()
	return TypedToolCallEvent{
		CallID:              strings.TrimSpace(call.callID),
		Name:                strings.TrimSpace(call.name),
		Status:              "failed",
		Error:               message,
		StartedAt:           started.Format(time.RFC3339Nano),
		CompletedAt:         completed.Format(time.RFC3339Nano),
		DurationMS:          completed.Sub(started).Milliseconds(),
		IdempotencyKey:      typedToolBridgeIdempotencyKey(call),
		AuthorizationStatus: "denied",
		Redacted:            true,
	}
}

func failedProviderTypedToolCall(call providerTypedToolCall, message string) TypedToolCallEvent {
	started := time.Now().UTC()
	completed := time.Now().UTC()
	return TypedToolCallEvent{
		CallID:              strings.TrimSpace(call.callID),
		Name:                strings.TrimSpace(call.name),
		Status:              "failed",
		Arguments:           cloneAnyMap(call.arguments),
		Error:               message,
		StartedAt:           started.Format(time.RFC3339Nano),
		CompletedAt:         completed.Format(time.RFC3339Nano),
		DurationMS:          completed.Sub(started).Milliseconds(),
		IdempotencyKey:      typedToolBridgeIdempotencyKey(call),
		AuthorizationStatus: "authorized",
	}
}

func typedToolBridgeIdempotencyKey(call providerTypedToolCall) string {
	id := strings.TrimSpace(call.callID)
	if id == "" {
		id = "1"
	}
	name := strings.TrimSpace(call.name)
	if name == "" {
		name = "unknown"
	}
	return "local-connect:" + name + ":" + id
}

func firstStringValue(m map[string]any, keys ...string) string {
	for _, key := range keys {
		if value, ok := m[key].(string); ok {
			if out := strings.TrimSpace(value); out != "" {
				return out
			}
		}
	}
	return ""
}

func nestedStringValue(m map[string]any, objectKey, valueKey string) string {
	nested, ok := m[objectKey].(map[string]any)
	if !ok {
		return ""
	}
	return firstStringValue(nested, valueKey)
}

func firstBoolValue(m map[string]any, keys ...string) bool {
	for _, key := range keys {
		if value, ok := m[key].(bool); ok && value {
			return true
		}
	}
	return false
}

func firstArgumentsValue(m map[string]any, keys ...string) (map[string]any, bool) {
	for _, key := range keys {
		if args, ok := argumentsValue(m[key]); ok {
			return args, true
		}
	}
	return nil, false
}

func nestedArgumentsValue(m map[string]any, objectKey, valueKey string) (map[string]any, bool) {
	nested, ok := m[objectKey].(map[string]any)
	if !ok {
		return nil, false
	}
	return argumentsValue(nested[valueKey])
}

func argumentsValue(value any) (map[string]any, bool) {
	switch typed := value.(type) {
	case map[string]any:
		return cloneAnyMap(typed), true
	case string:
		if strings.TrimSpace(typed) == "" {
			return map[string]any{}, true
		}
		var decoded map[string]any
		if err := json.Unmarshal([]byte(typed), &decoded); err != nil {
			return map[string]any{"value": typed}, true
		}
		return decoded, true
	default:
		return nil, false
	}
}

func cloneTypedToolCallEvent(in TypedToolCallEvent) TypedToolCallEvent {
	out := in
	out.Arguments = cloneAnyMap(in.Arguments)
	return out
}

func cloneAnyMap(in map[string]any) map[string]any {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]any, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}

func quote(value string) string {
	return fmt.Sprintf("%q", value)
}
