// Canonical backend-stream -> transcript.Event conversion shared by the local
// task runner and scout task runner. Keep entry fields aligned with
// internal/sessions/transcript.Event; the host bridge consumes these entries
// from the top-level `transcript_entries` result field.

const TRANSCRIPT_BUILDERS = {
  claude: claudeTranscript,
  codex: codexTranscript,
  cursor: cursorTranscript,
  gemini: geminiTranscript,
  opencode: opencodeTranscript,
};

export function hasTranscriptConverter(backend) {
  return Object.prototype.hasOwnProperty.call(TRANSCRIPT_BUILDERS, backend);
}

// parseStreamJSONTranscript turns a backend's stream-json stdout into canonical
// Loom transcript entries. Lines that do not parse as JSON and unknown events
// are ignored; callers retain the raw stdout separately as the log artifact.
export function parseStreamJSONTranscript(backend, stdout) {
  const events = [];
  for (const line of String(stdout || "").split("\n")) {
    const trimmed = line.trim();
    if (!trimmed) continue;
    const start = trimmed.indexOf("{");
    if (start < 0) continue;
    try {
      events.push(JSON.parse(trimmed.slice(start)));
    } catch {
      // Raw logs retain malformed/non-JSON lines.
    }
  }
  const build = TRANSCRIPT_BUILDERS[backend] || codexTranscript;
  const fallbackTs = new Date().toISOString();
  const resolved = build(events).map((entry) => ({ entry, ts: toISO(entry.timestamp) }));
  const firstReal = (resolved.find((item) => item.ts) || {}).ts || fallbackTs;
  const entries = [{ seq: 1, timestamp: firstReal, ...sessionMetaEntry(backend) }];
  let seq = 2;
  let cursorTs = firstReal;
  for (const { entry, ts } of resolved) {
    if (ts && ts > cursorTs) cursorTs = ts;
    const { timestamp, ...rest } = entry;
    entries.push({ seq: seq++, timestamp: cursorTs, ...rest });
  }
  return entries;
}

// Scout does not synthesize a transcript for plain-only output. Returning
// undefined lets the result omit transcript_entries while preserving raw logs.
export function transcriptEntriesForBackend(backend, stdout) {
  if (!hasTranscriptConverter(backend)) return undefined;
  const entries = parseStreamJSONTranscript(backend, stdout);
  return entries.some((entry) => entry.type !== "session_meta") ? entries : undefined;
}

export function taskUsageFromEntries(entries) {
  if (!Array.isArray(entries)) return {};
  let usage = null;
  for (let i = entries.length - 1; i >= 0; i--) {
    const entry = entries[i];
    if (entry && entry.type === "result" && entry.output) {
      try {
        usage = JSON.parse(entry.output);
      } catch {
        usage = null;
      }
      break;
    }
  }
  if (!usage || typeof usage !== "object") return {};
  const out = {};
  const set = (key, value) => {
    const num = Number(value);
    if (Number.isFinite(num)) out[key] = num;
  };
  set("input_tokens", usage.input_tokens);
  set("output_tokens", usage.output_tokens);
  set("cache_read_tokens", usage.cache_read_tokens);
  set("cache_write_tokens", usage.cache_write_tokens);
  set("estimated_cost_usd", usage.cost_usd != null ? usage.cost_usd : usage.estimated_cost_usd);
  return out;
}

export function streamFailureMessage(backend, stdout) {
  if (backend !== "opencode") return "";
  for (const line of String(stdout || "").split("\n")) {
    const trimmed = line.trim();
    if (!trimmed) continue;
    const start = trimmed.indexOf("{");
    if (start < 0) continue;
    let event;
    try {
      event = JSON.parse(trimmed.slice(start));
    } catch {
      continue;
    }
    if (!event || typeof event !== "object" || event.type !== "error") continue;
    const msg = rawString(event.error && event.error.message)
      || rawString(event.error && event.error.data && event.error.data.message)
      || rawString(event.message);
    return msg || "opencode reported an error";
  }
  return "";
}

export function ensureSessionMetaLead(entries, backend, label) {
  if (entries[0]?.type === "session_meta") return entries;
  const meta = sessionMetaEntry(backend, label);
  const firstTs = entries.find((entry) => entry && entry.timestamp)?.timestamp;
  if (firstTs) meta.timestamp = firstTs;
  return [meta, ...entries];
}

export function minimalTranscript(backend, label, prompt, stdout) {
  const now = new Date().toISOString();
  return [
    { seq: 1, timestamp: now, ...sessionMetaEntry(backend, label) },
    { seq: 2, timestamp: now, role: "user", type: "text", text: textTail(prompt, 4000) },
    { seq: 3, timestamp: now, role: "assistant", type: "text", text: textTail(stdout, 4000) },
  ];
}

function rawString(value) {
  return value === undefined || value === null ? "" : String(value);
}

function stringValue(value) {
  return value === undefined || value === null ? "" : String(value).trim();
}

function textTail(value, max = 4000) {
  const text = String(value || "").trim();
  return text.length <= max ? text : text.slice(text.length - max);
}

function toISO(timestamp) {
  if (typeof timestamp === "number" && Number.isFinite(timestamp)) {
    return new Date(timestamp).toISOString();
  }
  if (typeof timestamp === "string" && timestamp.trim()) {
    const parsed = new Date(timestamp);
    return Number.isNaN(parsed.getTime()) ? "" : parsed.toISOString();
  }
  return "";
}

function normalizeUsage(fields) {
  const out = {};
  for (const [key, value] of Object.entries(fields)) {
    if (value == null) continue;
    const num = Number(value);
    if (Number.isFinite(num)) out[key] = num;
  }
  return Object.keys(out).length ? out : null;
}

function accumulateUsage(previous, summed, latest) {
  const out = { ...(previous || {}) };
  for (const [key, value] of Object.entries(summed)) {
    if (value == null) continue;
    const num = Number(value);
    if (Number.isFinite(num)) out[key] = (out[key] || 0) + num;
  }
  for (const [key, value] of Object.entries(latest || {})) {
    if (value == null) continue;
    const num = Number(value);
    if (Number.isFinite(num)) out[key] = num;
  }
  return Object.keys(out).length ? out : null;
}

function sessionMetaEntry(backend, label) {
  return {
    role: "system",
    type: "session_meta",
    text: `local-cli-${backend} session` + (label ? ` for ${label}` : ""),
  };
}

function resultEntry(status, usage, timestamp) {
  const bits = [];
  if (status) bits.push(status);
  if (usage) {
    const parts = [];
    const labels = {
      input_tokens: "in",
      output_tokens: "out",
      cache_read_tokens: "cache_read",
      cache_write_tokens: "cache_write",
      reasoning_tokens: "reasoning",
      cost_usd: "cost",
      duration_ms: "duration_ms",
      num_turns: "turns",
    };
    for (const [key, label] of Object.entries(labels)) {
      if (usage[key] != null) parts.push(`${label}=${usage[key]}`);
    }
    if (parts.length) bits.push(parts.join(" "));
  }
  const entry = { role: "system", type: "result", text: bits.join(" | ") || "completed" };
  if (usage) entry.output = JSON.stringify(usage);
  if (timestamp) entry.timestamp = timestamp;
  return entry;
}

function toolResultText(content) {
  if (typeof content === "string") return content;
  if (Array.isArray(content)) {
    return content
      .map((block) => (typeof block === "string" ? block : rawString(block && block.text)))
      .filter(Boolean)
      .join("\n");
  }
  return rawString(content);
}

function claudeTranscript(events) {
  const out = [];
  let usage = null;
  let status = null;
  let lastTs;
  for (const event of events) {
    if (!event || typeof event !== "object") continue;
    const ts = event.timestamp;
    if (ts) lastTs = ts;
    if (event.type === "assistant" && event.message && Array.isArray(event.message.content)) {
      for (const block of event.message.content) {
        if (!block || typeof block !== "object") continue;
        if (block.type === "text" && stringValue(block.text)) {
          out.push({ role: "assistant", type: "text", text: rawString(block.text), timestamp: ts });
        } else if (block.type === "thinking" && stringValue(block.thinking)) {
          out.push({ role: "assistant", type: "reasoning", text: rawString(block.thinking), timestamp: ts });
        } else if (block.type === "tool_use") {
          out.push({ role: "assistant", type: "tool_use", tool_name: stringValue(block.name), tool_use_id: stringValue(block.id), tool_input: block.input, timestamp: ts });
        }
      }
    } else if (event.type === "user" && event.message && Array.isArray(event.message.content)) {
      for (const block of event.message.content) {
        if (block && block.type === "tool_result") {
          out.push({ role: "tool", type: "tool_result", tool_use_id: stringValue(block.tool_use_id), output: toolResultText(block.content), timestamp: ts });
        }
      }
    } else if (event.type === "result") {
      usage = normalizeUsage({
        input_tokens: event.usage && event.usage.input_tokens,
        output_tokens: event.usage && event.usage.output_tokens,
        cache_read_tokens: event.usage && event.usage.cache_read_input_tokens,
        cache_write_tokens: event.usage && event.usage.cache_creation_input_tokens,
        cost_usd: event.total_cost_usd,
        duration_ms: event.duration_ms,
        num_turns: event.num_turns,
      });
      status = event.is_error ? "failed" : "completed";
    }
  }
  if (status || usage) out.push(resultEntry(status, usage, lastTs));
  return out;
}

function codexTranscript(events) {
  const out = [];
  let usage = null;
  let status = null;
  for (const event of events) {
    if (!event || typeof event !== "object") continue;
    if (event.type === "turn.completed") {
      usage = normalizeUsage({
        input_tokens: event.usage && event.usage.input_tokens,
        output_tokens: event.usage && event.usage.output_tokens,
        cache_read_tokens: event.usage && event.usage.cached_input_tokens,
        reasoning_tokens: event.usage && event.usage.reasoning_output_tokens,
      });
      status = status || "completed";
      continue;
    }
    if (event.type === "turn.failed" || event.type === "error") {
      status = "failed";
      continue;
    }
    if (event.type === "item.started") continue;
    if (event.type === "item.completed" && event.item && typeof event.item === "object") {
      const item = event.item;
      const itemType = stringValue(item.type);
      if (itemType === "agent_message" || itemType.includes("message")) {
        const text = rawString(item.text);
        if (text) out.push({ role: "assistant", type: "text", text });
      } else if (itemType === "reasoning") {
        const text = rawString(item.text);
        if (text) out.push({ role: "assistant", type: "reasoning", text });
      } else if (itemType === "command_execution") {
        const entry = { role: "assistant", type: "tool_use", tool_name: "shell", tool_input: { command: stringValue(item.command) } };
        const failed = item.exit_code != null && String(item.exit_code) !== "0";
        const output = (failed ? `[exit ${item.exit_code}]\n` : "") + rawString(item.aggregated_output);
        if (output) entry.output = output;
        out.push(entry);
      } else if (itemType === "file_change") {
        const changes = Array.isArray(item.changes) ? item.changes : [];
        out.push({
          role: "assistant",
          type: "tool_use",
          tool_name: "apply_patch",
          tool_input: { changes: changes.map((change) => ({ path: stringValue(change && change.path), kind: stringValue(change && change.kind) })) },
          output: changes.map((change) => `${stringValue(change && change.kind)} ${stringValue(change && change.path)}`).join("\n"),
        });
      }
      continue;
    }
    const type = stringValue(event.type);
    const text = rawString(event.text) || rawString(event.message) || (event.msg && rawString(event.msg.text)) || "";
    if (text && (type.includes("message") || type.includes("agent") || type.includes("assistant") || type.includes("output"))) {
      out.push({ role: "assistant", type: "text", text });
    }
  }
  if (status || usage) out.push(resultEntry(status, usage));
  return out;
}

function cursorTranscript(events) {
  const out = [];
  const byCall = new Map();
  let usage = null;
  let status = null;
  let lastTs;
  for (const event of events) {
    if (!event || typeof event !== "object") continue;
    const ts = event.timestamp_ms;
    if (ts != null) lastTs = ts;
    if ((event.type === "assistant" || event.type === "user") && event.message && Array.isArray(event.message.content)) {
      const role = event.type === "user" ? "user" : "assistant";
      for (const block of event.message.content) {
        if (!block || typeof block !== "object") continue;
        if (block.type === "text" && stringValue(block.text)) {
          out.push({ role, type: "text", text: rawString(block.text), timestamp: ts });
        } else if (block.type === "tool_use") {
          out.push({ role: "assistant", type: "tool_use", tool_name: stringValue(block.name), tool_use_id: stringValue(block.id), tool_input: block.input, timestamp: ts });
        }
      }
    } else if (event.type === "tool_call" && event.tool_call && typeof event.tool_call === "object") {
      const call = event.tool_call;
      const callId = stringValue(event.call_id) || stringValue(call.toolCallId);
      let name;
      let detail;
      for (const key of Object.keys(call)) {
        const match = /^(.+)ToolCall$/.exec(key);
        if (match && call[key] && typeof call[key] === "object") {
          name = match[1];
          detail = call[key];
          break;
        }
      }
      if (!name) continue;
      let entry = callId ? byCall.get(callId) : undefined;
      if (!entry) {
        entry = { role: "assistant", type: "tool_use", tool_name: name, tool_use_id: callId, timestamp: ts };
        if (detail.args !== undefined) entry.tool_input = detail.args;
        out.push(entry);
        if (callId) byCall.set(callId, entry);
      } else if (entry.tool_input === undefined && detail.args !== undefined) {
        entry.tool_input = detail.args;
      }
      if (event.subtype === "completed" && detail.result !== undefined) {
        const isError = detail.result && typeof detail.result === "object" && detail.result.error !== undefined;
        const body = typeof detail.result === "string" ? detail.result : JSON.stringify(detail.result);
        entry.output = (isError ? "[error] " : "") + body;
      }
    } else if (event.type === "result") {
      const backendUsage = event.usage || {};
      usage = normalizeUsage({
        input_tokens: backendUsage.inputTokens ?? backendUsage.input_tokens,
        output_tokens: backendUsage.outputTokens ?? backendUsage.output_tokens,
        cache_read_tokens: backendUsage.cacheReadTokens ?? backendUsage.cache_read_tokens,
        cache_write_tokens: backendUsage.cacheWriteTokens ?? backendUsage.cache_write_tokens,
        duration_ms: event.duration_ms,
      });
      status = event.is_error ? "failed" : "completed";
    }
  }
  if (status || usage) out.push(resultEntry(status, usage, lastTs));
  return out;
}

function opencodeTranscript(events) {
  const out = [];
  let usage = null;
  let status = null;
  let lastTs;
  for (const event of events) {
    if (!event || typeof event !== "object") continue;
    const part = event.part && typeof event.part === "object" ? event.part : event;
    const ts = (part.time && part.time.start) || event.timestamp;
    if (ts != null) lastTs = ts;
    if (event.type === "text") {
      const text = rawString(part.text);
      if (text) out.push({ role: "assistant", type: "text", text, timestamp: ts });
    } else if (event.type === "tool_use" && part.type === "tool") {
      const state = part.state && typeof part.state === "object" ? part.state : {};
      if (state.status && state.status !== "completed" && state.status !== "error") continue;
      const isError = state.status === "error";
      const entry = { role: "assistant", type: "tool_use", tool_name: stringValue(part.tool), tool_use_id: stringValue(part.callID), tool_input: state.input, timestamp: ts };
      const output = rawString(state.output) || rawString(state.error);
      if (output || isError) entry.output = (isError ? "[error] " : "") + output;
      out.push(entry);
    } else if (event.type === "step_finish") {
      const tokens = part.tokens && typeof part.tokens === "object" ? part.tokens : {};
      usage = accumulateUsage(usage, {
        input_tokens: tokens.input,
        output_tokens: tokens.output,
        reasoning_tokens: tokens.reasoning,
        cost_usd: part.cost,
      }, {
        cache_read_tokens: tokens.cache && tokens.cache.read,
      });
      if (part.reason === "stop" && !status) status = "completed";
    } else if (event.type === "error") {
      const message = rawString(event.error && (event.error.message || event.error)) || rawString(event.message);
      status = message ? "failed: " + message : "failed";
    }
  }
  if (status || usage) out.push(resultEntry(status, usage, lastTs));
  return out;
}

function geminiTranscript(events) {
  const out = [];
  let usage = null;
  for (const event of events) {
    if (!event || typeof event !== "object") continue;
    const candidates = Array.isArray(event.candidates) ? event.candidates : [];
    for (const candidate of candidates) {
      const parts = candidate && candidate.content && Array.isArray(candidate.content.parts) ? candidate.content.parts : [];
      for (const part of parts) {
        const text = rawString(part && part.text);
        if (text) out.push({ role: "assistant", type: "text", text });
      }
    }
    if (!candidates.length) {
      const flat = rawString(event.text) || rawString(event.content) || rawString(event.response) || (event.message && rawString(event.message.text)) || "";
      const type = stringValue(event.type);
      if (flat && (!type || type.includes("content") || type.includes("text") || type.includes("assistant") || type.includes("message") || type.includes("response"))) {
        out.push({ role: "assistant", type: "text", text: flat });
      }
    }
    if (event.usage && typeof event.usage === "object") {
      usage = normalizeUsage({
        input_tokens: event.usage.input_tokens != null ? event.usage.input_tokens : event.usage.inputTokens,
        output_tokens: event.usage.output_tokens != null ? event.usage.output_tokens : event.usage.outputTokens,
      });
    } else if (event.usageMetadata && typeof event.usageMetadata === "object") {
      usage = normalizeUsage({
        input_tokens: event.usageMetadata.promptTokenCount,
        output_tokens: event.usageMetadata.candidatesTokenCount,
      });
    }
  }
  if (usage) out.push(resultEntry(null, usage));
  return out;
}
