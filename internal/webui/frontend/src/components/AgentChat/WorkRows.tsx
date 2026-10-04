// Ported from T3 Code apps/web/src/components/chat/MessagesTimeline.tsx
// (PlainWorkEntryRow, WorkGroupToggleTimelineRow, LiveWorkEntryTimelineRow,
// LiveActivityRow and ThinkingActivityRow) at commit 2daff8c25. Copyright (c)
// 2026 T3 Tools Inc. MIT License; see THIRD_PARTY_NOTICES.md. Changes: CSS
// modules in place of Tailwind, small inline glyphs in place of lucide icons,
// and Loom's tool input and output in the expanded body.

import { useState, type KeyboardEvent } from "react";
import {
  entryFailed,
  firstLine,
  toolGroupAction,
  toolHeading,
  toolPreview,
  type TimelineRow,
  type ToolEntry,
  type ToolGroupAction,
  type WorkEntry,
} from "./timelineRows";
import styles from "./Timeline.module.css";

type IconName = ToolGroupAction | "mixed" | "thinking" | "failed" | "chevron";

const GLYPHS: Record<IconName, string> = {
  command: "$",
  read: "◎",
  edit: "✎",
  "code-search": "⌕",
  search: "◍",
  other: "⚙",
  mixed: "⚙",
  thinking: "✦",
  failed: "✕",
  chevron: "⌄",
};

function Icon({ name, label }: { name: IconName; label?: string | undefined }) {
  return (
    <span
      className={styles.icon}
      data-icon={name}
      role={label ? "img" : undefined}
      aria-label={label}
      aria-hidden={label ? undefined : true}
    >
      {GLYPHS[name]}
    </span>
  );
}

/** A tool call's input as shown: pretty JSON when it is JSON, else as is. */
function prettyInput(input: string | undefined): string {
  const raw = input?.trim() ?? "";
  if (!raw.startsWith("{") && !raw.startsWith("[")) return raw;
  try {
    return JSON.stringify(JSON.parse(raw), null, 2);
  } catch {
    return raw;
  }
}

function entryIcon(entry: WorkEntry): IconName {
  if (entryFailed(entry)) return "failed";
  return entry.kind === "tool" ? toolGroupAction(entry) : "thinking";
}

/**
 * One tool call or reasoning entry: its icon, heading and preview; when it
 * has a body (a tool's input and output, reasoning's text), the row expands.
 */
export function WorkEntryRow({
  entry,
  inGroup,
}: {
  entry: WorkEntry;
  inGroup: boolean;
}) {
  const [expanded, setExpanded] = useState(false);
  const failed = entryFailed(entry);
  const tool = entry.kind === "tool" ? entry : null;
  const heading = tool ? toolHeading(tool) : "Thinking";
  const text = entry.kind === "reasoning" ? entry.text : "";
  const preview = tool ? toolPreview(tool) : firstLine(text);
  const input = tool ? prettyInput(tool.tool.input) : "";
  const output = tool ? (tool.tool.output ?? "") : text;
  const canExpand = !!(input || output.trim());
  const status = tool?.status ?? "completed";
  const label = `${heading}${preview ? ` ${preview}` : ""}${failed ? ", tool call failed" : ""}`;
  const toggle = () => setExpanded((v) => !v);
  const rowProps = canExpand
    ? {
        role: "button" as const,
        tabIndex: 0,
        "aria-label": label,
        "aria-expanded": expanded,
        onClick: toggle,
        onKeyDown: (e: KeyboardEvent<HTMLDivElement>) => {
          if (e.key === "Enter" || e.key === " ") {
            e.preventDefault();
            toggle();
          }
        },
      }
    : {};
  return (
    <div
      className={styles.entry}
      data-testid={tool ? "tool-call" : "reasoning"}
      data-status={status}
      data-in-group={inGroup ? "true" : "false"}
    >
      <div
        className={styles.entryRow}
        data-expandable={canExpand}
        {...rowProps}
      >
        <Icon
          name={entryIcon(entry)}
          label={failed ? "Tool call failed" : undefined}
        />
        <span className={styles.heading} data-failed={failed}>
          {heading}
        </span>
        {preview && <span className={styles.preview}>{preview}</span>}
        {status === "running" && (
          <span className={styles.status}>Running…</span>
        )}
        {canExpand && (
          <span className={styles.chevron} data-open={expanded}>
            <Icon name="chevron" />
          </span>
        )}
      </div>
      {expanded && canExpand && (
        <div className={styles.body}>
          {tool ? (
            <>
              {input && (
                <>
                  <div className={styles.bodyLabel}>Input</div>
                  <pre className={styles.pre}>{input}</pre>
                </>
              )}
              {output.trim() && (
                <>
                  <div className={styles.bodyLabel}>
                    {failed ? "Error" : "Output"}
                  </div>
                  <pre className={styles.pre} data-failed={failed}>
                    {output}
                  </pre>
                </>
              )}
            </>
          ) : (
            <pre className={styles.pre}>{output}</pre>
          )}
        </div>
      )}
    </div>
  );
}

/** A group's summary or "+N previous" toggle. */
export function WorkGroupToggleRow({
  row,
  onToggle,
}: {
  row: Extract<TimelineRow, { kind: "work-toggle" }>;
  onToggle: () => void;
}) {
  if (row.onlyToolEntries && row.summary) {
    const calls = `${row.count} tool ${row.count === 1 ? "call" : "calls"}`;
    return (
      <button
        type="button"
        className={styles.toggle}
        data-testid="tool-group"
        aria-label={`${row.summary}, ${calls}${row.hasFailure ? ", tool call failed" : ""}`}
        aria-expanded={row.expanded}
        onClick={onToggle}
      >
        <Icon name={row.action ?? "mixed"} />
        <span className={styles.preview}>{row.summary}</span>
        <span className={styles.count}>
          {row.count === 1 ? "Tool call" : `Tool calls (${row.count})`}
        </span>
        <span className={styles.chevron} data-open={row.expanded}>
          <Icon name="chevron" />
        </span>
      </button>
    );
  }
  const noun = row.onlyToolEntries
    ? row.hiddenCount === 1
      ? "tool call"
      : "tool calls"
    : row.hiddenCount === 1
      ? "log entry"
      : "log entries";
  return (
    <button
      type="button"
      className={styles.toggle}
      data-testid="work-toggle"
      aria-expanded={row.expanded}
      aria-label={
        row.hasFailure && !row.expanded
          ? `+${row.hiddenCount} previous ${noun}, includes a failure`
          : undefined
      }
      onClick={onToggle}
    >
      <span
        className={styles.chevron}
        data-lead="true"
        data-open={row.expanded}
      >
        <Icon name="chevron" />
      </span>
      <span className={styles.toggleText}>
        {row.expanded
          ? `Show fewer ${row.onlyToolEntries ? "tool calls" : "log entries"}`
          : `+${row.hiddenCount} previous ${noun}`}
      </span>
    </button>
  );
}

function LiveActivityRow({
  label,
  icon,
}: {
  label: string;
  icon?: IconName | undefined;
}) {
  return (
    <div className={styles.live}>
      {icon && <Icon name={icon} />}
      <span className={styles.liveLabel}>{label}</span>
    </div>
  );
}

/** A running tool group: the call that runs now; it expands to the group. */
export function LiveWorkEntryRow({
  row,
  onToggle,
}: {
  row: Extract<TimelineRow, { kind: "work-live" }>;
  onToggle: () => void;
}) {
  const entry: ToolEntry = row.entry;
  const preview = toolPreview(entry);
  return (
    <button
      type="button"
      className={styles.toggle}
      data-testid="tool-live"
      aria-expanded={row.expanded}
      onClick={onToggle}
    >
      <LiveActivityRow
        label={`${toolHeading(entry)}${preview ? ` ${preview}` : ""}`}
        icon={toolGroupAction(entry)}
      />
    </button>
  );
}

/** The agent is reasoning now. */
export function ThinkingActivityRow() {
  return (
    <div data-testid="thinking">
      <LiveActivityRow label="Thinking" />
    </div>
  );
}
