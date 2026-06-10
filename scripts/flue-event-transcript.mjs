export function createTranscriptCollector() {
  const state = { nextSeq: 1 };
  const entries = [];
  return {
    entries,
    push(event) {
      const converted = flueEventToTranscriptEntries(event, state);
      entries.push(...converted);
      return converted;
    },
  };
}

export function flueEventToTranscriptEntries(event, state = {}) {
  if (!event || typeof event !== "object") {
    return [];
  }
  switch (event.type) {
    case "turn_request": {
      const text = lastUserMessageText(event.input && event.input.messages);
      return text ? [entry(state, event, "user", "text", { text })] : [];
    }
    case "text_delta":
      return event.text ? [entry(state, event, "assistant", "text", { text: event.text })] : [];
    case "tool_start":
      return [
        entry(state, event, "assistant", "tool_use", {
          tool_name: event.toolName || "tool",
          tool_use_id: event.toolCallId || "",
          tool_input: event.args || {},
        }),
      ];
    case "tool_call":
      return [
        entry(state, event, "tool", "tool_result", {
          tool_name: event.toolName || "tool",
          tool_use_id: event.toolCallId || "",
          output: formatToolResult(event.result, event.isError),
        }),
      ];
    case "operation_start":
      return [
        entry(state, event, "system", "session_meta", {
          text: `${event.operationKind || "operation"} started`,
        }),
      ];
    case "operation":
      return [
        entry(state, event, "system", "session_meta", {
          text: `${event.operationKind || "operation"} ${event.isError ? "failed" : "completed"}`,
        }),
      ];
    case "compaction":
      return [
        entry(state, event, "system", "session_meta", {
          text: `compacted context from ${event.messagesBefore || 0} to ${event.messagesAfter || 0} messages`,
        }),
      ];
    case "log":
      return [
        entry(state, event, "system", "text", {
          text: `[${event.level || "info"}] ${event.message || ""}`.trim(),
        }),
      ];
    default:
      return [];
  }
}

export function serializeTranscriptJSONL(entries) {
  return (entries || []).map((entry) => JSON.stringify(entry)).join("\n") + ((entries || []).length ? "\n" : "");
}

export function flueUsageToTaskUsage(usage) {
  if (!usage || typeof usage !== "object") {
    return {};
  }
  return {
    input_tokens: numberValue(usage.input),
    output_tokens: numberValue(usage.output),
    cache_read_tokens: numberValue(usage.cacheRead),
    cache_write_tokens: numberValue(usage.cacheWrite),
    estimated_cost_usd: numberValue(usage.cost && usage.cost.total),
  };
}

function entry(state, event, role, type, fields = {}) {
  const seq = Number.isFinite(state.nextSeq) ? state.nextSeq : 1;
  state.nextSeq = seq + 1;
  return {
    seq,
    timestamp: event.timestamp || new Date().toISOString(),
    role,
    type,
    ...fields,
  };
}

function lastUserMessageText(messages) {
  if (!Array.isArray(messages)) {
    return "";
  }
  for (let i = messages.length - 1; i >= 0; i--) {
    const message = messages[i];
    if (message && message.role === "user") {
      return messageContentText(message.content);
    }
  }
  return "";
}

function messageContentText(content) {
  if (typeof content === "string") {
    return content;
  }
  if (!Array.isArray(content)) {
    return "";
  }
  return content
    .map((part) => {
      if (!part || typeof part !== "object") {
        return "";
      }
      if (typeof part.text === "string") {
        return part.text;
      }
      if (typeof part.content === "string") {
        return part.content;
      }
      return "";
    })
    .filter(Boolean)
    .join("\n");
}

function formatToolResult(result, isError) {
  if (typeof result === "string") {
    return result;
  }
  if (result && Array.isArray(result.content)) {
    const text = result.content
      .map((part) => (part && typeof part.text === "string" ? part.text : ""))
      .filter(Boolean)
      .join("\n");
    if (text) {
      return text;
    }
  }
  if (result === undefined || result === null) {
    return isError ? "tool failed" : "";
  }
  try {
    return JSON.stringify(result);
  } catch {
    return String(result);
  }
}

function numberValue(value) {
  return Number.isFinite(value) ? value : 0;
}
