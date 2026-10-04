// Ported from T3 Code apps/web/src/components/chat/MessagesTimeline.logic.ts
// at commit 2daff8c25. Copyright (c) 2026 T3 Tools Inc. MIT License; see
// THIRD_PARTY_NOTICES.md. Changes: the work-log grouping only, over Loom's
// harness-neutral chat items; a tool's action comes from its name and input.

import type { AgentEvent } from "@/api/agentsv1";
import type { ChatItem, ToolCall } from "@/hooks";
import { argPreviewFromJSON, truncate } from "@/utils/toolPreview";

/** Work entries a mixed group shows before "+N previous log entries". */
export const MAX_VISIBLE_WORK_LOG_ENTRIES = 1;

export type ToolEntry = Extract<ChatItem, { kind: "tool" }>;
export type WorkEntry = ToolEntry | Extract<ChatItem, { kind: "reasoning" }>;

export type ToolGroupAction =
  | "read"
  | "edit"
  | "command"
  | "code-search"
  | "search"
  | "other";

export type TimelineRow =
  | { kind: "item"; id: string; item: ChatItem }
  | { kind: "work"; id: string; entry: WorkEntry; inGroup: boolean }
  | {
      kind: "work-toggle";
      id: string;
      groupId: string;
      count: number;
      hiddenCount: number;
      expanded: boolean;
      onlyToolEntries: boolean;
      summary: string | null;
      action: ToolGroupAction | "mixed" | null;
      hasFailure: boolean;
    }
  | {
      kind: "work-live";
      id: string;
      groupId: string;
      entry: ToolEntry;
      expanded: boolean;
    }
  | { kind: "thinking"; id: string };

const isWork = (i: ChatItem): i is WorkEntry =>
  i.kind === "tool" || (i.kind === "reasoning" && !i.streaming);

const isTool = (e: WorkEntry): e is ToolEntry => e.kind === "tool";

/** A tool call failed; a reasoning entry never does. */
export const entryFailed = (e: WorkEntry) =>
  e.kind === "tool" && e.status === "failed";

const ACTION_BY_NAME: [RegExp, ToolGroupAction][] = [
  [/^(read|view|cat|read_?file|notebook_?read|image_?view)$/i, "read"],
  [
    /^(edit|write|multi_?edit|patch|apply_?patch|str_?replace\w*|notebook_?edit|create_?file)$/i,
    "edit",
  ],
  [/^(bash|shell|command|exec(_?command)?|terminal|run\w*)$/i, "command"],
  [/^(grep|glob|find|ls|list|codesearch|search_?code|rg)$/i, "code-search"],
  [/^(web_?search|web_?fetch|fetch|browse)$/i, "search"],
];

/** What a tool call did, from its name, as T3's toolGroupAction groups it. */
export function toolGroupAction(entry: ToolEntry): ToolGroupAction {
  const name = (entry.tool.name ?? "").split("/").pop() ?? "";
  return ACTION_BY_NAME.find(([re]) => re.test(name))?.[1] ?? "other";
}

function toolGroupActionLabel(action: ToolGroupAction, count: number): string {
  switch (action) {
    case "read":
      return `Read ${count} ${count === 1 ? "file" : "files"}`;
    case "edit":
      return `Changed ${count} ${count === 1 ? "file" : "files"}`;
    case "command":
      return `Ran ${count} ${count === 1 ? "command" : "commands"}`;
    case "search":
      return `Searched the web ${count} ${count === 1 ? "time" : "times"}`;
    case "code-search":
      return `Searched code ${count} ${count === 1 ? "time" : "times"}`;
    case "other":
      return `Used ${count} ${count === 1 ? "tool" : "tools"}`;
  }
}

/** One sentence for a group of tool calls, such as "Ran 2 commands and read 1 file". */
export function summarizeToolGroup(entries: readonly ToolEntry[]): string {
  const counts = new Map<ToolGroupAction, number>();
  for (const e of entries) {
    const a = toolGroupAction(e);
    counts.set(a, (counts.get(a) ?? 0) + 1);
  }
  const labels = [...counts].map(([a, n], i) => {
    const label = toolGroupActionLabel(a, n);
    return i === 0 ? label : label.charAt(0).toLowerCase() + label.slice(1);
  });
  if (labels.length < 2) return labels[0] ?? "";
  if (labels.length === 2) return labels.join(" and ");
  return `${labels.slice(0, -1).join(", ")}, and ${labels[labels.length - 1]}`;
}

function summaryAction(
  entries: readonly ToolEntry[],
): ToolGroupAction | "mixed" {
  const actions = new Set(entries.map(toolGroupAction));
  return actions.size === 1 ? actions.values().next().value! : "mixed";
}

/** A tool call's heading: its name, capitalized. */
export function toolHeading(entry: ToolEntry): string {
  const name = entry.tool.name?.trim() || "Tool call";
  return name.charAt(0).toUpperCase() + name.slice(1);
}

/** The thing a tool call is about (its command, path or query), or "". */
export function toolPreview(entry: ToolEntry): string {
  return argPreviewFromJSON(entry.tool.input);
}

/** Reasoning's first line as plain text: no emphasis, code or heading marks. */
export function firstLine(text: string, max = 120): string {
  let line = (text.trim().split("\n")[0] ?? "").replace(/^#{1,6}\s+/, "");
  for (let prev = ""; prev !== line; ) {
    prev = line;
    line = line.replace(/(\*\*|__|\*|_|`)(.+?)\1/g, "$2");
  }
  line = line.trim();
  return line.length > max ? `${line.slice(0, max - 1)}…` : line;
}

/** The agent tray's step line, at most this long. */
export const STEP_MAX = 60;

const STEP_BY_ACTION: Partial<Record<ToolGroupAction, string>> = {
  read: "Read file",
  edit: "Changed file",
  command: "Ran command",
  "code-search": "Searched code",
  search: "Searched the web",
};

/**
 * A working agent's latest step as the agent tray shows it (DF2): a tool
 * start or completed tool as its action and what it is about ("▸ Ran
 * command · npm test"), a completed reasoning item as its first line
 * ("💭 Thinking · Checking routes"); null for any other event. A tool's
 * input shows only as its salient argument, never as raw JSON; without a
 * preview, only the action shows.
 */
export function stepLabel(e: AgentEvent, preview = true): string | null {
  const p = (e.payload ?? {}) as {
    itemKind?: string;
    text?: string;
    tool?: ToolCall;
  };
  if (e.kind === "item.completed" && p.itemKind === "reasoning") {
    const line = preview ? firstLine(p.text ?? "") : "";
    return truncate(`💭 Thinking${line ? ` · ${line}` : ""}`, STEP_MAX);
  }
  const tool =
    e.kind === "tool.started" ||
    (e.kind === "item.completed" && p.itemKind === "tool");
  if (!tool) return null;
  const entry: ToolEntry = {
    key: e.event_id,
    kind: "tool",
    tool: p.tool ?? {},
    status: "running",
  };
  const about = preview ? toolPreview(entry) : "";
  const action = STEP_BY_ACTION[toolGroupAction(entry)] ?? toolHeading(entry);
  const shown = about && !/^[[{]/.test(about) ? ` · ${about}` : "";
  return truncate(`▸ ${action}${shown}`, STEP_MAX);
}

/**
 * The transcript's rows. Consecutive tool calls and reasoning form a work
 * group. A group of only tool calls is one summary row ("Ran 2 commands"),
 * or, while one runs, a live row naming it; expanding shows each call. A
 * mixed group shows its last entry after "+N previous log entries". Live
 * reasoning is one "Thinking" row.
 */
export function deriveTimelineRows(
  items: readonly ChatItem[],
  expandedGroups: ReadonlySet<string>,
): TimelineRow[] {
  const rows: TimelineRow[] = [];
  let thinking = false;
  for (let i = 0; i < items.length; i++) {
    const item = items[i]!;
    if (item.kind === "reasoning" && item.streaming) {
      thinking = true;
      continue;
    }
    if (!isWork(item)) {
      rows.push({ kind: "item", id: item.key, item });
      continue;
    }
    const group: WorkEntry[] = [item];
    while (i + 1 < items.length && isWork(items[i + 1]!)) {
      group.push(items[++i] as WorkEntry);
    }
    pushGroup(rows, group, expandedGroups);
  }
  if (thinking) rows.push({ kind: "thinking", id: "thinking" });
  return rows;
}

function pushGroup(
  rows: TimelineRow[],
  group: WorkEntry[],
  expandedGroups: ReadonlySet<string>,
) {
  const groupId = `work-group:${group[0]!.key}`;
  const expanded = expandedGroups.has(groupId);
  const each = (entries: WorkEntry[]) =>
    entries.forEach((entry) =>
      rows.push({ kind: "work", id: entry.key, entry, inGroup: true }),
    );
  const tools = group.filter(isTool);
  if (tools.length === group.length) {
    const running = tools.filter((t) => t.status === "running");
    if (running.length > 0) {
      rows.push({
        kind: "work-live",
        id: `work-live:${group[0]!.key}`,
        groupId,
        entry: running[running.length - 1]!,
        expanded,
      });
    } else {
      rows.push({
        kind: "work-toggle",
        id: `work-toggle:${group[0]!.key}`,
        groupId,
        count: tools.length,
        hiddenCount: tools.length,
        expanded,
        onlyToolEntries: true,
        summary: summarizeToolGroup(tools),
        action: summaryAction(tools),
        hasFailure: tools.some(entryFailed),
      });
    }
    if (expanded) each(group);
    return;
  }
  if (group.length <= MAX_VISIBLE_WORK_LOG_ENTRIES) {
    group.forEach((entry) =>
      rows.push({ kind: "work", id: entry.key, entry, inGroup: false }),
    );
    return;
  }
  const hidden = group.slice(0, -MAX_VISIBLE_WORK_LOG_ENTRIES);
  (expanded ? group : group.slice(-MAX_VISIBLE_WORK_LOG_ENTRIES)).forEach(
    (entry) =>
      rows.push({ kind: "work", id: entry.key, entry, inGroup: false }),
  );
  rows.push({
    kind: "work-toggle",
    id: `work-toggle:${group[0]!.key}`,
    groupId,
    count: group.length,
    hiddenCount: hidden.length,
    expanded,
    onlyToolEntries: hidden.every(isTool),
    summary: null,
    action: null,
    hasFailure: hidden.some(entryFailed),
  });
}
