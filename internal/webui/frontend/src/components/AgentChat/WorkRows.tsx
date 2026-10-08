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
  label: bridge,
}: {
  entry: WorkEntry;
  inGroup: boolean;
  /** A Loom bridge call's plain label, in place of its name and input. */
  label?: string | undefined;
}) {
  const [expanded, setExpanded] = useState(false);
  const failed = entryFailed(entry);
  const tool = entry.kind === "tool" ? entry : null;
  const heading = bridge ?? (tool ? toolHeading(tool) : "Thinking");
  const text = entry.kind === "reasoning" ? entry.text : "";
  const preview = bridge ? "" : tool ? toolPreview(tool) : firstLine(text);
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
        {!tool && !text.trim() && (
          <span className={styles.preview}>No reasoning text available</span>
        )}
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

/** A group's summary or "Show N earlier steps" toggle. */
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
  const plural = row.onlyToolEntries ? "tool calls" : "steps";
  const noun =
    row.hiddenCount === 1
      ? row.onlyToolEntries
        ? "tool call"
        : "step"
      : plural;
  const more = `Show ${row.hiddenCount} earlier ${noun}`;
  return (
    <button
      type="button"
      className={styles.toggle}
      data-testid="work-toggle"
      aria-expanded={row.expanded}
      aria-label={
        row.hasFailure && !row.expanded
          ? `${more}, includes a failure`
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
        {row.expanded ? `Hide earlier ${plural}` : more}
      </span>
    </button>
  );
}

/**
 * A Loom bridge call the Lead made (agent_get and the like) as one muted
 * line, such as "· Checked ui-test-agent-1", never its raw input (CL1).
 */
export function BridgeCallRow({
  row,
}: {
  row: Extract<TimelineRow, { kind: "bridge" }>;
}) {
  const failed = entryFailed(row.entry);
  return (
    <div
      className={styles.bridge}
      data-testid="bridge-call"
      data-status={row.entry.status}
    >
      <span aria-hidden="true">·</span>
      <span>
        {row.label}
        {row.entry.status === "running" && "…"}
      </span>
      {failed && <span className={styles.bridgeFailed}>failed</span>}
    </div>
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
