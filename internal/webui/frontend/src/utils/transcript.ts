export type TranscriptRow =
  | { kind: "plain"; text: string }
  | { kind: "unparsed"; text: string }
  | {
      kind: "command";
      command: string;
      exitCode: number | null;
      status: string;
      output: string;
    }
  | {
      kind: "fileChange";
      status: string;
      changes: { path: string; kind: string }[];
    }
  | { kind: "message"; text: string }
  | { kind: "reasoning"; text: string }
  | {
      kind: "turnCompleted";
      usage: {
        inputTokens: number;
        cachedInputTokens: number;
        outputTokens: number;
      };
    }
  | { kind: "turnFailed"; message: string }
  | { kind: "other"; label: string; raw: string };

export interface ParsedTranscript {
  rows: TranscriptRow[];
  codexEventCount: number;
}

type JsonRecord = Record<string, unknown>;

function isRecord(value: unknown): value is JsonRecord {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}

function stringField(record: JsonRecord, field: string): string {
  const value = record[field];
  return typeof value === "string" ? value : "";
}

function numberField(record: JsonRecord, field: string): number {
  const value = record[field];
  return typeof value === "number" && Number.isFinite(value) ? value : 0;
}

function itemLabel(itemType: string): string {
  switch (itemType) {
    case "todo_list":
      return "Todo list";
    case "mcp_tool_call":
      return "MCP tool call";
    case "web_search":
      return "Web search";
    default:
      return `Item completed · ${itemType || "unknown item"}`;
  }
}

function eventLabel(eventType: string): string {
  const words = eventType.replace(/[._-]+/g, " ");
  return words.charAt(0).toUpperCase() + words.slice(1);
}

function compactJson(value: JsonRecord): string {
  return JSON.stringify(value);
}

function narrativeText(item: JsonRecord): string {
  const text = stringField(item, "text");
  if (text) return text;

  const summary = item.summary;
  if (typeof summary === "string") return summary;
  if (!Array.isArray(summary)) return "";

  return summary
    .map((entry) => {
      if (typeof entry === "string") return entry;
      return isRecord(entry) ? stringField(entry, "text") : "";
    })
    .filter(Boolean)
    .join("\n");
}

function errorMessage(value: unknown, fallback: string): string {
  if (typeof value === "string" && value) return value;
  if (!isRecord(value)) return fallback;

  const message = stringField(value, "message");
  if (message) return message;
  const error = stringField(value, "error");
  return error || fallback;
}

function completedItemRow(event: JsonRecord, raw: string): TranscriptRow {
  const item = event.item;
  if (!isRecord(item)) {
    return { kind: "other", label: "Item completed", raw };
  }

  const itemType = stringField(item, "type");
  switch (itemType) {
    case "command_execution": {
      const exitCode = item.exit_code;
      return {
        kind: "command",
        command: stringField(item, "command"),
        exitCode:
          typeof exitCode === "number" && Number.isFinite(exitCode)
            ? exitCode
            : null,
        status: stringField(item, "status") || "unknown",
        output: stringField(item, "aggregated_output"),
      };
    }
    case "file_change": {
      const changes = Array.isArray(item.changes)
        ? item.changes.flatMap((change) =>
            isRecord(change)
              ? [
                  {
                    path: stringField(change, "path"),
                    kind: stringField(change, "kind") || "unknown",
                  },
                ]
              : [],
          )
        : [];
      return {
        kind: "fileChange",
        status: stringField(item, "status") || "unknown",
        changes,
      };
    }
    case "agent_message":
      return { kind: "message", text: stringField(item, "text") };
    case "reasoning":
      return { kind: "reasoning", text: narrativeText(item) };
    default:
      return { kind: "other", label: itemLabel(itemType), raw };
  }
}

function eventRow(event: JsonRecord, raw: string): TranscriptRow | null {
  const eventType = stringField(event, "type");
  switch (eventType) {
    case "item.started":
    case "item.updated":
    case "thread.started":
      return null;
    case "item.completed":
      return completedItemRow(event, raw);
    case "turn.completed": {
      const usage = isRecord(event.usage) ? event.usage : {};
      return {
        kind: "turnCompleted",
        usage: {
          inputTokens: numberField(usage, "input_tokens"),
          cachedInputTokens: numberField(usage, "cached_input_tokens"),
          outputTokens: numberField(usage, "output_tokens"),
        },
      };
    }
    case "turn.failed":
      return {
        kind: "turnFailed",
        message: errorMessage(event.error, "Turn failed"),
      };
    case "error":
      return {
        kind: "turnFailed",
        message: errorMessage(event.message, "Codex error"),
      };
    default:
      return { kind: "other", label: eventLabel(eventType), raw };
  }
}

export function parseTranscript(content: string): ParsedTranscript {
  const rows: TranscriptRow[] = [];
  let codexEventCount = 0;

  for (const line of content.split("\n")) {
    const trimmed = line.trim();
    if (!trimmed) continue;

    if (!trimmed.startsWith("{")) {
      rows.push({
        kind: trimmed.startsWith("…") ? "unparsed" : "plain",
        text: line,
      });
      continue;
    }

    let parsed: unknown;
    try {
      parsed = JSON.parse(trimmed);
    } catch {
      rows.push({ kind: "unparsed", text: line });
      continue;
    }

    if (!isRecord(parsed) || typeof parsed.type !== "string") {
      rows.push({ kind: "unparsed", text: line });
      continue;
    }

    codexEventCount += 1;
    const row = eventRow(parsed, compactJson(parsed));
    if (row) rows.push(row);
  }

  return { rows, codexEventCount };
}
