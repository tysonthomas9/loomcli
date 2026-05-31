package tsfirst

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/tysonthomas9/loomcli/internal/cli"
	backendcaps "github.com/tysonthomas9/loomcli/internal/cli/backends"
)

const localConnectNoStdoutResponse = "backend completed; no stdout response was captured"

type typedToolFollowupInvocation struct {
	backendName        string
	backend            cli.Backend
	streamer           backendcaps.StreamingBackend
	workDir            string
	agentName          string
	message            string
	stream             io.Writer
	appliedToolRuntime *connectToolRuntime
}

func maybeInvokeTypedToolResultFollowup(ctx context.Context, in typedToolFollowupInvocation, first localInvocationResult) (localInvocationResult, error) {
	if !shouldInvokeTypedToolResultFollowup(first) {
		return first, nil
	}
	followupRuntime := sameTurnToolResultRuntime(in.appliedToolRuntime)
	followupPrompt := localConnectTypedToolFollowupPrompt(in.agentName, in.message, first.ToolCalls)
	var (
		followup localInvocationResult
		err      error
	)
	if in.streamer != nil {
		followup, err = invokeStreamingLocalAgent(ctx, streamingLocalInvocation{
			backendName:        in.backendName,
			backend:            in.backend,
			streamer:           in.streamer,
			workDir:            in.workDir,
			prompt:             followupPrompt,
			agentName:          in.agentName,
			stream:             in.stream,
			appliedToolRuntime: followupRuntime,
		})
	} else {
		followup, err = invokeNonStreamingLocalAgentWithResume(in.backendName, in.backend, in.workDir, followupPrompt, in.agentName, in.stream, nil, followupRuntime)
	}
	if err != nil {
		return localInvocationResult{}, fmt.Errorf("typed tool result follow-up: %w", err)
	}
	return mergeTypedToolFollowupResult(first, followup, followupRuntime), nil
}

func shouldInvokeTypedToolResultFollowup(result localInvocationResult) bool {
	if len(result.ToolCalls) == 0 {
		return false
	}
	response := strings.TrimSpace(result.Response)
	return response == "" || response == localConnectNoStdoutResponse
}

func sameTurnToolResultRuntime(policy *connectToolRuntime) *connectToolRuntime {
	out := cloneToolRuntime(policy)
	if out == nil {
		return nil
	}
	out.ResultFeed = connectToolResultFeedSameTurnPrompt
	out.Message = "typed tool results were fed back to the provider once in the same local connect operation"
	return out
}

func localConnectTypedToolFollowupPrompt(agentName, message string, calls []connectToolCall) string {
	var b strings.Builder
	fmt.Fprintf(&b, "You are continuing the same Loom local-connect operation for TypeScript-defined agent %q.\n", agentName)
	fmt.Fprintln(&b, "Loom executed the reviewed typed tool calls from your previous response through trusted handlers.")
	appendLocalConnectTypedToolResultEntries(&b, "Typed tool results for this operation", localConnectTypedToolResultEntries("", calls))
	fmt.Fprintln(&b, "\nUse these results to answer the user directly. Do not repeat tool calls unless another reviewed tool call is strictly required.")
	fmt.Fprintf(&b, "\nOriginal user message:\n%s\n", message)
	return b.String()
}

func mergeTypedToolFollowupResult(first, followup localInvocationResult, runtime *connectToolRuntime) localInvocationResult {
	out := first
	if strings.TrimSpace(followup.Response) != "" {
		out.Response = followup.Response
	}
	if followup.ProviderSessionID != "" {
		out.ProviderSessionID = followup.ProviderSessionID
	}
	if followup.ProviderModel != "" {
		out.ProviderModel = followup.ProviderModel
	}
	out.ProviderMetadata = mergeTypedToolFollowupProviderMetadata(first.ProviderMetadata, followup.ProviderMetadata)
	out.Usage = addConnectUsage(first.Usage, followup.Usage)
	out.ToolRuntime = runtime
	out.ToolCalls = mergeConnectToolCalls(first.ToolCalls, followup.ToolCalls)
	out.Resume = first.Resume
	return out
}

func mergeTypedToolFollowupProviderMetadata(first, followup map[string]any) map[string]any {
	out := cloneMap(first)
	if out == nil {
		out = make(map[string]any, 1)
	}
	event := map[string]any{
		"count":       1,
		"result_feed": connectToolResultFeedSameTurnPrompt,
	}
	if len(followup) > 0 {
		event["provider"] = cloneMap(followup)
	}
	out["typed_tool_result_followup"] = event
	return out
}

func mergeConnectToolCalls(first, followup []connectToolCall) []connectToolCall {
	if len(first) == 0 {
		return append([]connectToolCall(nil), followup...)
	}
	out := append([]connectToolCall(nil), first...)
	seen := make(map[string]struct{}, len(first))
	for index, call := range first {
		seen[connectToolCallMergeKey(call, index)] = struct{}{}
	}
	for index, call := range followup {
		key := connectToolCallMergeKey(call, index)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, call)
	}
	return out
}

func connectToolCallMergeKey(call connectToolCall, index int) string {
	if call.IdempotencyKey != "" {
		return "idempotency:" + call.IdempotencyKey
	}
	if call.CallID != "" {
		return "call:" + call.Name + ":" + call.CallID
	}
	return fmt.Sprintf("index:%s:%d", call.Name, index)
}

func addConnectUsage(first, next *connectUsage) *connectUsage {
	if first == nil && next == nil {
		return nil
	}
	out := &connectUsage{}
	if first != nil {
		*out = *first
	}
	if next != nil {
		out.InputTokens += next.InputTokens
		out.OutputTokens += next.OutputTokens
		out.CacheReadInputTokens += next.CacheReadInputTokens
		out.CacheCreationInputTokens += next.CacheCreationInputTokens
	}
	out.TotalTokens = connectUsageTotal(out)
	return out
}
