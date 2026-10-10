// Ported from T3 Code apps/web/src/components/chat/MessagesTimeline.logic.ts
// at commit 2daff8c25. Copyright (c) 2026 T3 Tools Inc. MIT License; see
// THIRD_PARTY_NOTICES.md. Changes: the work-log grouping only, over Loom's
// harness-neutral chat items; a tool's action comes from its name and input.

import type { AgentEvent } from "@/api/agentsv1";
import type { ChatItem, ToolCall } from "@/hooks";
import { argPreview, argPreviewFromJSON, truncate } from "@/utils/toolPreview";

/** Work entries a mixed group shows before "Show N earlier steps". */
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

export type StartedItem = Extract<ChatItem, { kind: "started" }>;

export type TimelineRow =
  | { kind: "item"; id: string; item: ChatItem }
  | {
      kind: "work";
      id: string;
      entry: WorkEntry;
      inGroup: boolean;
      /** A Loom bridge call's plain label, shown in place of its raw input. */
      label?: string;
    }
  /**
   * Children started back to back, with the Lead's agent_create calls that
   * started them folded in ("2 tool calls ›"); expanding shows each call.
   */
  | {
      kind: "started";
      id: string;
      item: StartedItem;
      calls: ToolEntry[];
      groupId: string;
      expanded: boolean;
    }
  /** A Loom bridge call (agent_get and the like) as one muted line. */
  | { kind: "bridge"; id: string; entry: ToolEntry; label: string }
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

/** A tool call's heading: its name, capitalized; code it ran as "Ran code". */
export function toolHeading(entry: ToolEntry): string {
  if (isExecute(entry))
    return entry.status === "running" ? "Running code" : "Ran code";
  const name = entry.tool.name?.trim() || "Tool call";
  return name.charAt(0).toUpperCase() + name.slice(1);
}

/**
 * The thing a tool call is about (its command, path or query), or "";
 * never the code it ran (CL4).
 */
export function toolPreview(entry: ToolEntry): string {
  return isExecute(entry) ? "" : argPreviewFromJSON(entry.tool.input);
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

const SECRETS: [RegExp, string][] = [
  [/\b(bearer|basic|token)(\s+)\S+/gi, "$1$2•••"],
  [
    /\b([\w-]*(?:key|token|secret|passw(?:or)?d|pass|pwd|auth\w*|credential\w*|cookie))(["']?\s*[=:]\s*["']?)[^\s"'&]+/gi,
    "$1$2•••",
  ],
  [/(--?(?:password|passwd|token|secret|api-?key|auth)[= ])\S+/gi, "$1•••"],
  [/(\/\/[^/\s:@]+:)[^@\s/]+@/g, "$1•••@"],
  [
    /\b(sk|pk|rk|ghp|gho|ghs|ghu|github_pat|xox[abprs]|glpat|AKIA)[-_]?[A-Za-z0-9_-]{6,}/g,
    "•••",
  ],
  [/\beyJ[\w-]{8,}\.[\w-]+\.[\w-]+/g, "•••"],
];

/** Text with credential-looking values (tokens, keys, passwords) masked. */
export function maskSecrets(text: string): string {
  return SECRETS.reduce((t, [re, to]) => t.replace(re, to), text);
}

/**
 * What a tool step is about, for the tray: the salient argument of a JSON
 * input, its secrets masked before it is cut; "" for unstructured input,
 * which is never shown.
 */
function stepPreview(input: string | undefined): string {
  const raw = (input ?? "").trim();
  if (!raw.startsWith("{")) return "";
  let parsed: unknown;
  try {
    parsed = JSON.parse(raw);
  } catch {
    return "";
  }
  if (!parsed || typeof parsed !== "object" || Array.isArray(parsed)) return "";
  const masked = Object.fromEntries(
    Object.entries(parsed).map(([k, v]) => [
      k,
      typeof v === "string" ? maskSecrets(v.replace(/\s+/g, " ")) : v,
    ]),
  );
  return argPreview(masked);
}

/**
 * A working agent's latest step as the agent tray shows it (DF2): a tool
 * start or completed tool as its action and what it is about ("▸ Ran
 * command · npm test"), a completed reasoning item as its first line
 * ("💭 Thinking · Checking routes"); null for any other event. A tool's
 * input shows only as its salient JSON argument with secrets masked, never
 * raw; without one, only the action shows.
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
    status: e.kind === "tool.started" ? "running" : "completed",
  };
  const about = preview ? stepPreview(entry.tool.input) : "";
  const action = STEP_BY_ACTION[toolGroupAction(entry)] ?? toolHeading(entry);
  const shown = about ? ` · ${about}` : "";
  return truncate(`▸ ${action}${shown}`, STEP_MAX);
}

const BRIDGE_NAME =
  /(?:^|[^a-z])(agent_(?:create|list|get|send|archive)|github_read)$/i;
const CODE_CALL = /tools\.loom\.(\w+)\s*\(/g;
const AGENT_ARG = /\bagent\s*:\s*["'`]([^"'`]+)["'`]/;
const NAME_ARG = /\bname\s*:\s*["'`]([^"'`]+)["'`]/;

/** One Loom bridge tool call: the tool, and the agent or name it names. */
export interface BridgeCall {
  tool: string;
  agent?: string;
  name?: string;
}

function jsonInput(entry: ToolEntry): Record<string, unknown> | null {
  try {
    const v: unknown = JSON.parse(entry.tool.input ?? "");
    return v && typeof v === "object" && !Array.isArray(v)
      ? (v as Record<string, unknown>)
      : null;
  } catch {
    return null;
  }
}

const str = (v: unknown) => (typeof v === "string" && v ? v : undefined);

/** A code mode call (execute), whose input is code, never a preview. */
const isExecute = (entry: ToolEntry) =>
  /(?:^|[^a-z])execute$/i.test((entry.tool.name ?? "").trim());

/**
 * The Loom bridge calls a tool call makes, or [] when it is not one: a
 * bridge tool called by name (agent_get, mcp__loom__agent_get), or code
 * mode's execute running tools.loom.agent_get({agent: ...}).
 */
export function bridgeCalls(entry: ToolEntry): BridgeCall[] {
  const name = (entry.tool.name ?? "").trim();
  const input = jsonInput(entry);
  const direct = BRIDGE_NAME.exec(name)?.[1];
  if (direct) {
    const call: BridgeCall = { tool: direct.toLowerCase() };
    const agent = str(input?.agent);
    const n = str(input?.name);
    if (agent) call.agent = agent;
    if (n) call.name = n;
    return [call];
  }
  const code = str(input?.code) ?? "";
  const found = [...code.matchAll(CODE_CALL)];
  return found.flatMap((m, i) => {
    const tool = m[1]!;
    if (!BRIDGE_NAME.test(tool)) return [];
    const args = code.slice(m.index, found[i + 1]?.index ?? code.length);
    const call: BridgeCall = { tool };
    const agent = AGENT_ARG.exec(args)?.[1];
    const n = NAME_ARG.exec(args)?.[1];
    if (agent) call.agent = agent;
    if (n) call.name = n;
    return [call];
  });
}

const BRIDGE_VERBS: Record<string, [done: string, running: string]> = {
  agent_create: ["Started", "Starting"],
  agent_list: ["Listed agents", "Listing agents"],
  agent_get: ["Checked", "Checking"],
  agent_send: ["Messaged", "Messaging"],
  agent_archive: ["Archived", "Archiving"],
  github_read: ["Read GitHub", "Reading GitHub"],
};

/** Who a bridge call is about, by name when known: "kid", "kid and kid2". */
function whom(calls: BridgeCall[], names: ReadonlyMap<string, string>) {
  const who = [
    ...new Set(
      calls.map((c) => (c.agent ? (names.get(c.agent) ?? c.agent) : c.name)),
    ),
  ].filter((w): w is string => !!w);
  if (who.length === 0)
    return calls.length > 1 ? `${calls.length} agents` : "an agent";
  if (who.length <= 2) return who.join(" and ");
  return `${who[0]} and ${who.length - 1} more`;
}

/**
 * A bridge tool call as one plain line, never its raw input: "Checked
 * ui-test-agent-1", "Messaging kid", "Listed agents"; null when the call is
 * not a bridge call. names maps agent ids to names.
 */
export function bridgeLabel(
  entry: ToolEntry,
  names: ReadonlyMap<string, string>,
): string | null {
  const calls = bridgeCalls(entry);
  const first = calls[0];
  if (!first) return null;
  const same = calls.filter((c) => c.tool === first.tool);
  const [done, running] = BRIDGE_VERBS[first.tool] ?? [first.tool, first.tool];
  const verb = entry.status === "running" ? running : done;
  const about = ["agent_create", "agent_get", "agent_send", "agent_archive"];
  let label = about.includes(first.tool)
    ? `${verb} ${whom(same, names)}`
    : verb;
  const others = calls.length - same.length;
  if (others > 0)
    label += ` and ${others} more ${others === 1 ? "call" : "calls"}`;
  return label;
}

/**
 * A finished agent_create call, which the Started marker next to it folds
 * in: one the bridge parses, or loom code naming agent_create another way,
 * such as search({namespace:'loom', query:'agent_create'}), or loom code
 * whose saved input was cut short before it (CL4); or, whatever its saved
 * input holds and however it ended, the call a child of started names (CL5).
 */
const isCreateCall = (i: ChatItem, started: StartedItem): i is ToolEntry =>
  i.kind === "tool" &&
  (started.children.some((c) => !!c.call && c.call === i.itemId) ||
    (i.status === "completed" &&
      (bridgeCalls(i).some((c) => c.tool === "agent_create") ||
        (isExecute(i) &&
          /["'`]loom\b|\bloom\./.test(i.tool.input ?? "") &&
          (/\bagent_create\b/.test(i.tool.input ?? "") ||
            (!jsonInput(i) &&
              !/tools\.loom\.\w+\s*\(/.test(i.tool.input ?? "")))))));

type Unit =
  | ChatItem
  | { kind: "started-unit"; item: StartedItem; calls: ToolEntry[] };

/**
 * Started markers with the agent_create calls next to them folded in, and
 * markers that only those calls kept apart merged.
 */
function foldStarted(items: readonly ChatItem[]): Unit[] {
  const out: Unit[] = [];
  for (const item of items) {
    const last = out[out.length - 1];
    if (item.kind === "started") {
      const calls: ToolEntry[] = [];
      while (out.length > 0) {
        const t = out[out.length - 1]!;
        if (t.kind === "started-unit" || !isCreateCall(t, item)) break;
        calls.unshift(t);
        out.pop();
      }
      const prev = out[out.length - 1];
      if (prev?.kind === "started-unit") {
        prev.item = {
          ...prev.item,
          children: [...prev.item.children, ...item.children],
        };
        prev.calls.push(...calls);
      } else out.push({ kind: "started-unit", item, calls });
      continue;
    }
    if (last?.kind === "started-unit" && isCreateCall(item, last.item)) {
      last.calls.push(item);
      continue;
    }
    out.push(item);
  }
  return out;
}

/** Each child's name, from the Started markers and result cards. */
function agentNames(items: readonly ChatItem[]): Map<string, string> {
  const names = new Map<string, string>();
  for (const i of items) {
    if (i.kind === "started")
      i.children.forEach((c) => names.set(c.child, c.name));
    if (i.kind === "completion") names.set(i.record.child, i.name);
  }
  return names;
}

/**
 * The transcript's rows. Consecutive tool calls and reasoning form a work
 * group. A group of only tool calls is one summary row ("Ran 2 commands"),
 * or, while one runs, a live row naming it; expanding shows each call. A
 * mixed group shows its last entry after "Show N earlier steps". Live
 * reasoning is one "Thinking" row. A Started marker folds in the
 * agent_create calls next to it, and any other Loom bridge call is one
 * muted line ("Checked kid"), never raw tool input (CL1).
 */
export function deriveTimelineRows(
  items: readonly ChatItem[],
  expandedGroups: ReadonlySet<string>,
): TimelineRow[] {
  const names = agentNames(items);
  const units = foldStarted(items);
  const label = (e: WorkEntry) =>
    e.kind === "tool" ? (bridgeLabel(e, names) ?? undefined) : undefined;
  const isBridge = (u: Unit): u is ToolEntry =>
    u.kind === "tool" && bridgeCalls(u).length > 0;
  const isPlainWork = (u: Unit): u is WorkEntry =>
    u.kind !== "started-unit" && isWork(u) && !isBridge(u);
  const rows: TimelineRow[] = [];
  let thinking = false;
  for (let i = 0; i < units.length; i++) {
    const unit = units[i]!;
    if (unit.kind === "started-unit") {
      const groupId = `started:${unit.item.key}`;
      const expanded = expandedGroups.has(groupId);
      rows.push({
        kind: "started",
        id: unit.item.key,
        item: unit.item,
        calls: unit.calls,
        groupId,
        expanded,
      });
      if (expanded)
        unit.calls.forEach((entry) =>
          rows.push({
            kind: "work",
            id: entry.key,
            entry,
            inGroup: true,
            ...(label(entry) ? { label: label(entry)! } : {}),
          }),
        );
      continue;
    }
    if (unit.kind === "reasoning" && unit.streaming) {
      thinking = true;
      continue;
    }
    if (isBridge(unit)) {
      rows.push({
        kind: "bridge",
        id: unit.key,
        entry: unit,
        label: bridgeLabel(unit, names)!,
      });
      continue;
    }
    if (!isPlainWork(unit)) {
      rows.push({ kind: "item", id: unit.key, item: unit });
      continue;
    }
    const group: WorkEntry[] = [unit];
    while (i + 1 < units.length && isPlainWork(units[i + 1]!)) {
      group.push(units[++i] as WorkEntry);
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
