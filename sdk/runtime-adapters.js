export function createFlueTranscriptCollector(options = {}) {
  const state = createTranscriptState();
  const entries = [];
  return {
    entries,
    push(event) {
      const converted = flueEventToTranscriptEntries(event, state, options);
      entries.push(...converted);
      return converted;
    },
  };
}

export function flueEventToTranscriptEntries(event, state = createTranscriptState(), options = {}) {
  if (!event || typeof event !== "object") {
    return [];
  }
  ensureTranscriptState(state);
  switch (event.type) {
    case "turn_request":
      return turnRequestEntry(event, state, options);
    case "text_delta":
      return textEntry(event, state, "assistant", stringValue(event.text), { uuid: event.turnId });
    case "tool_start":
      return toolUseEntry(event, state);
    case "tool_call":
      return toolResultEntry(event, state);
    case "task_start":
      return sessionMetaEntry(event, state, taskStartText(event));
    case "task":
      return sessionMetaEntry(event, state, taskEndText(event));
    case "operation_start":
      return sessionMetaEntry(event, state, operationStartText(event));
    case "operation":
      return sessionMetaEntry(event, state, operationEndText(event));
    case "compaction_start":
      return sessionMetaEntry(event, state, compactionStartText(event));
    case "compaction":
      return sessionMetaEntry(event, state, compactionText(event));
    case "log":
      return textEntry(event, state, "system", logText(event));
    default:
      return [];
  }
}

export function serializeTranscriptJSONL(entries = []) {
  return entries.map((entry) => JSON.stringify(entry)).join("\n") + (entries.length > 0 ? "\n" : "");
}

export function flueUsageToTaskUsage(usage, options = {}) {
  if (!usage || typeof usage !== "object") {
    return {};
  }
  const out = {};
  const input = finiteNumber(firstDefined(usage.input, usage.input_tokens, usage.inputTokens));
  const output = finiteNumber(firstDefined(usage.output, usage.output_tokens, usage.outputTokens));
  const cacheRead = finiteNumber(firstDefined(usage.cacheRead, usage.cache_read_tokens, usage.cacheReadTokens));
  const cacheWrite = finiteNumber(firstDefined(usage.cacheWrite, usage.cache_write_tokens, usage.cacheWriteTokens));
  if (input !== undefined) out.input_tokens = input;
  if (output !== undefined) out.output_tokens = output;
  if (cacheRead !== undefined) out.cache_read_tokens = cacheRead;
  if (cacheWrite !== undefined) out.cache_write_tokens = cacheWrite;

  const unit = stringValue(firstDefined(options.costUnit, usage.costUnit, usage.cost_unit, usage.cost?.unit)).toLowerCase();
  const cost = finiteNumber(usage.cost && usage.cost.total);
  if (unit === "usd" && cost !== undefined) {
    out.estimated_cost_usd = cost;
  }
  return out;
}

export function flueEventsToTaskUsage(events = [], options = {}) {
  const seenTurns = new Set();
  const total = {
    input: 0,
    output: 0,
    cacheRead: 0,
    cacheWrite: 0,
    cost: { total: 0 },
  };
  let hasUsage = false;
  let index = 0;
  for (const event of events || []) {
    index += 1;
    if (!event || event.type !== "turn" || !event.usage || typeof event.usage !== "object") {
      continue;
    }
    const key = stringValue(event.turnId) || `turn-index-${index}`;
    if (seenTurns.has(key)) {
      continue;
    }
    seenTurns.add(key);
    hasUsage = true;
    total.input += finiteNumber(event.usage.input) || 0;
    total.output += finiteNumber(event.usage.output) || 0;
    total.cacheRead += finiteNumber(event.usage.cacheRead) || 0;
    total.cacheWrite += finiteNumber(event.usage.cacheWrite) || 0;
    total.cost.total += finiteNumber(event.usage.cost && event.usage.cost.total) || 0;
  }
  return hasUsage ? flueUsageToTaskUsage(total, options) : {};
}

export function flueEventsToLogText(events = []) {
  const lines = [];
  for (const event of events || []) {
    if (!event || typeof event !== "object") {
      continue;
    }
    switch (event.type) {
      case "log":
        lines.push(logText(event));
        break;
      case "operation":
        lines.push(operationEndText(event));
        break;
      case "task":
        lines.push(taskEndText(event));
        break;
      case "tool_call":
        lines.push(`tool ${stringValue(event.toolName) || "tool"} ${event.isError ? "failed" : "completed"}`);
        break;
      default:
        break;
    }
  }
  return lines.join("\n") + (lines.length > 0 ? "\n" : "");
}

export function redactText(value, secrets = []) {
  let text = String(value || "");
  for (const secret of secrets || []) {
    const needle = String(secret || "");
    if (needle) {
      text = text.split(needle).join("[redacted]");
    }
  }
  return text;
}

export function redactTranscriptEntries(entries = [], secrets = []) {
  if (!Array.isArray(entries) || entries.length === 0) {
    return [];
  }
  const json = redactText(JSON.stringify(entries), secrets);
  try {
    return JSON.parse(json);
  } catch {
    return entries.map((entry) => redactEntry(entry, secrets));
  }
}

function createTranscriptState() {
  return {
    nextSeq: 1,
    seenPromptKeys: new Set(),
  };
}

function ensureTranscriptState(state) {
  if (!Number.isSafeInteger(state.nextSeq) || state.nextSeq < 1) {
    state.nextSeq = 1;
  }
  if (!state.seenPromptKeys || typeof state.seenPromptKeys.add !== "function") {
    state.seenPromptKeys = new Set();
  }
}

function baseEntry(event, state, role, type, extra = {}) {
  const entry = {
    seq: state.nextSeq,
    timestamp: timestampValue(event && event.timestamp),
    role,
    type,
    ...extra,
  };
  state.nextSeq += 1;
  return entry;
}

function turnRequestEntry(event, state, options) {
  if (event.purpose && event.purpose !== "agent") {
    return [];
  }
  const text = lastUserText(event.input && event.input.messages);
  if (!text) {
    return [];
  }
  const promptKey = stringValue(event.operationId)
    ? `operation:${event.operationId}:${text}`
    : `text:${text}`;
  if (options.dedupePrompts !== false && state.seenPromptKeys.has(promptKey)) {
    return [];
  }
  state.seenPromptKeys.add(promptKey);
  return [baseEntry(event, state, "user", "text", { text })];
}

function textEntry(event, state, role, text, extra = {}) {
  if (!text) {
    return [];
  }
  const entry = baseEntry(event, state, role, "text", { text });
  if (extra.uuid) {
    entry.uuid = String(extra.uuid);
  }
  return [entry];
}

function toolUseEntry(event, state) {
  const entry = baseEntry(event, state, "assistant", "tool_use", {
    tool_name: stringValue(event.toolName) || "tool",
    tool_use_id: stringValue(event.toolCallId),
    tool_input: normalizeToolInput(event.args),
  });
  if (event.turnId) {
    entry.uuid = String(event.turnId);
  }
  return [entry];
}

function toolResultEntry(event, state) {
  return [baseEntry(event, state, "tool", "tool_result", {
    tool_name: stringValue(event.toolName) || "tool",
    tool_use_id: stringValue(event.toolCallId),
    output: formatToolResult(event.result, event),
  })];
}

function sessionMetaEntry(event, state, text) {
  if (!text) {
    return [];
  }
  return [baseEntry(event, state, "system", "session_meta", { text })];
}

function lastUserText(messages) {
  if (!Array.isArray(messages)) {
    return "";
  }
  for (let i = messages.length - 1; i >= 0; i -= 1) {
    const message = messages[i];
    if (!message || message.role !== "user") {
      continue;
    }
    const text = contentText(message.content).trim();
    if (text) {
      return text;
    }
  }
  return "";
}

function contentText(content) {
  if (typeof content === "string") {
    return content;
  }
  if (!Array.isArray(content)) {
    return "";
  }
  return content
    .map((part) => {
      if (!part || typeof part !== "object") return "";
      if (part.type === "text" && typeof part.text === "string") return part.text;
      return "";
    })
    .filter(Boolean)
    .join("\n");
}

function normalizeToolInput(args) {
  if (args === undefined || args === null) {
    return {};
  }
  if (typeof args === "object" && !Array.isArray(args)) {
    return cloneJSON(args);
  }
  return { value: cloneJSON(args) };
}

function formatToolResult(result, event) {
  const text = contentTextFromToolResult(result);
  if (text) {
    return text;
  }
  if (result === undefined || result === null) {
    return event && event.isError ? "tool failed" : "";
  }
  if (typeof result === "string") {
    return result;
  }
  try {
    return JSON.stringify(result);
  } catch {
    return String(result);
  }
}

function contentTextFromToolResult(result) {
  if (!result || typeof result !== "object") {
    return "";
  }
  if (typeof result.output === "string") {
    return result.output;
  }
  if (typeof result.stdout === "string" || typeof result.stderr === "string") {
    return [result.stdout, result.stderr].filter(Boolean).join("\n");
  }
  if (Array.isArray(result.content)) {
    return result.content
      .map((part) => {
        if (typeof part === "string") return part;
        if (part && typeof part === "object" && part.type === "text" && typeof part.text === "string") {
          return part.text;
        }
        return "";
      })
      .filter(Boolean)
      .join("\n");
  }
  return "";
}

function taskStartText(event) {
  return joinParts([
    `task ${stringValue(event.taskId) || "task"} started`,
    event.agent ? `agent=${event.agent}` : "",
    event.cwd ? `cwd=${event.cwd}` : "",
    event.parentSession ? `parent_session=${event.parentSession}` : "",
    event.session ? `session=${event.session}` : "",
  ]);
}

function taskEndText(event) {
  return joinParts([
    `task ${stringValue(event.taskId) || "task"} ${event.isError ? "failed" : "completed"}`,
    event.agent ? `agent=${event.agent}` : "",
    Number.isFinite(event.durationMs) ? `duration_ms=${event.durationMs}` : "",
  ]);
}

function operationStartText(event) {
  return joinParts([
    `operation ${stringValue(event.operationId) || "operation"} started`,
    event.operationKind ? `kind=${event.operationKind}` : "",
  ]);
}

function operationEndText(event) {
  return joinParts([
    `operation ${stringValue(event.operationId) || "operation"} ${event.isError ? "failed" : "completed"}`,
    event.operationKind ? `kind=${event.operationKind}` : "",
    Number.isFinite(event.durationMs) ? `duration_ms=${event.durationMs}` : "",
  ]);
}

function compactionStartText(event) {
  return joinParts([
    "compaction started",
    event.reason ? `reason=${event.reason}` : "",
    Number.isFinite(event.estimatedTokens) ? `estimated_tokens=${event.estimatedTokens}` : "",
  ]);
}

function compactionText(event) {
  return joinParts([
    "compaction completed",
    Number.isFinite(event.messagesBefore) ? `messages_before=${event.messagesBefore}` : "",
    Number.isFinite(event.messagesAfter) ? `messages_after=${event.messagesAfter}` : "",
    Number.isFinite(event.durationMs) ? `duration_ms=${event.durationMs}` : "",
  ]);
}

function logText(event) {
  const level = stringValue(event.level) || "info";
  const message = stringValue(event.message);
  return message ? `[${level}] ${message}` : `[${level}]`;
}

function timestampValue(value) {
  if (value instanceof Date && !Number.isNaN(value.getTime())) {
    return value.toISOString();
  }
  if (typeof value === "number" && Number.isFinite(value)) {
    const date = new Date(value);
    if (!Number.isNaN(date.getTime())) {
      return date.toISOString();
    }
  }
  if (typeof value === "string" && value.trim()) {
    const date = new Date(value);
    if (!Number.isNaN(date.getTime())) {
      return date.toISOString();
    }
  }
  return new Date().toISOString();
}

function redactEntry(entry, secrets) {
  if (!entry || typeof entry !== "object") {
    return entry;
  }
  const out = Array.isArray(entry) ? [] : {};
  for (const [key, value] of Object.entries(entry)) {
    if (typeof value === "string") {
      out[key] = redactText(value, secrets);
    } else if (value && typeof value === "object") {
      out[key] = redactEntry(value, secrets);
    } else {
      out[key] = value;
    }
  }
  return out;
}

function cloneJSON(value) {
  try {
    return JSON.parse(JSON.stringify(value));
  } catch {
    return String(value);
  }
}

function finiteNumber(value) {
  const number = Number(value);
  return Number.isFinite(number) ? number : undefined;
}

function firstDefined(...values) {
  for (const value of values) {
    if (value !== undefined && value !== null) {
      return value;
    }
  }
  return undefined;
}

function joinParts(parts) {
  return parts.map(stringValue).filter(Boolean).join(" ");
}

function stringValue(value) {
  return value === undefined || value === null ? "" : String(value).trim();
}
