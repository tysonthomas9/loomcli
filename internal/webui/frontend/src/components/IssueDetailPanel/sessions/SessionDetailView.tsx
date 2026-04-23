/**
 * SessionDetailView — detail panel for a selected session.
 *
 * Renders the canonical transcript.Event stream as an agent worklog. The
 * kickoff user prompt is surfaced once in the masthead; each subsequent
 * assistant response is a turn with inline collapsible tool calls; mid-run
 * user messages render as distinct interjection blocks.
 */

import { useMemo, useState } from "react";

import { CodeMirrorEditor } from "@/components/CodeMirrorEditor";
import { useSessionTranscript, useSessionDiff } from "@/hooks/terminal";
import type { SessionRecord, TranscriptEntry } from "@/types/agent";

import { MarkdownRenderer } from "../sections/MarkdownRenderer";
import styles from "./SessionsTab.module.css";

export interface SessionDetailViewProps {
  taskId: string;
  session: SessionRecord;
}

type InnerTab = "transcript" | "diff";

function formatExitCode(code: number): string {
  if (code === 0) return "0 (success)";
  return String(code);
}

function formatToolInput(input: unknown): string {
  if (input == null) return "";
  if (typeof input === "string") return input;
  try {
    return JSON.stringify(input, null, 2);
  } catch {
    return String(input);
  }
}

function formatTimestamp(ts: string | undefined): string {
  if (!ts) return "";
  const d = new Date(ts);
  if (isNaN(d.getTime())) return ts;
  const hh = String(d.getHours()).padStart(2, "0");
  const mm = String(d.getMinutes()).padStart(2, "0");
  const ss = String(d.getSeconds()).padStart(2, "0");
  const ms = String(d.getMilliseconds()).padStart(3, "0");
  return `${hh}:${mm}:${ss}.${ms}`;
}

function formatCost(usd: number): string {
  if (usd === 0) return "$0";
  if (usd < 0.01) return `$${usd.toFixed(4)}`;
  return `$${usd.toFixed(2)}`;
}

function formatTokens(n: number): string {
  if (n < 1000) return String(n);
  if (n < 1000000) return `${(n / 1000).toFixed(1)}k`;
  return `${(n / 1000000).toFixed(1)}M`;
}

function formatDuration(s: number | undefined): string {
  if (!s || s <= 0) return "—";
  if (s < 60) return `${s.toFixed(1)}s`;
  const m = Math.floor(s / 60);
  const rem = s - m * 60;
  return `${m}m ${rem.toFixed(0)}s`;
}

// ─── Event grouping ────────────────────────────────────────────────────

type ToolItem = {
  kind: "tool";
  seq: number;
  name: string;
  input: unknown;
  inputPreview: string;
  result?: string;
  resultTimestamp?: string;
};

type TextItem = { kind: "text"; seq: number; text: string };

type TurnItem = TextItem | ToolItem;

type RenderBlock =
  | {
      kind: "interjection";
      seq: number;
      text: string;
      timestamp?: string;
    }
  | {
      kind: "turn";
      seq: number;
      timestamp?: string;
      items: TurnItem[];
    };

interface GroupedEvents {
  prompt: { text: string; timestamp?: string } | null;
  blocks: RenderBlock[];
}

/**
 * Walk the event stream and produce render-ready blocks:
 *  - the first user text becomes the "prompt" (shown once in the masthead)
 *  - subsequent user text messages become "interjection" blocks
 *  - assistant text + tool_use events are grouped into "turn" blocks
 *  - tool_result events are matched to their tool_use by tool_use_id and
 *    rendered inline inside the turn (never as their own block)
 *
 * Consecutive assistant events sharing a uuid (a single native message
 * with mixed content blocks) collapse into one turn.
 */
function groupEvents(entries: TranscriptEntry[]): GroupedEvents {
  // Pair tool_use_id → tool_result. Built up front so the grouping pass
  // doesn't care about ordering.
  const resultById = new Map<string, TranscriptEntry>();
  for (const e of entries) {
    if (e.type === "tool_result" && e.tool_use_id) {
      resultById.set(e.tool_use_id, e);
    }
  }

  let prompt: GroupedEvents["prompt"] = null;
  let sawFirstUserText = false;
  const blocks: RenderBlock[] = [];
  let current: Extract<RenderBlock, { kind: "turn" }> | null = null;
  let currentUuid: string | undefined;

  const flushCurrent = () => {
    if (current && current.items.length > 0) blocks.push(current);
    current = null;
    currentUuid = undefined;
  };

  for (const e of entries) {
    // Skip tool_result — rendered inline inside its tool_use
    if (e.type === "tool_result") continue;

    if (e.role === "user" && e.type === "text") {
      const text = (e.text ?? "").trim();
      if (!text) continue;
      if (!sawFirstUserText) {
        sawFirstUserText = true;
        prompt = e.timestamp ? { text, timestamp: e.timestamp } : { text };
        continue;
      }
      flushCurrent();
      blocks.push(
        e.timestamp
          ? { kind: "interjection", seq: e.seq, text, timestamp: e.timestamp }
          : { kind: "interjection", seq: e.seq, text },
      );
      continue;
    }

    if (e.role === "assistant") {
      // Group only when both events carry the same uuid (one native
      // message's mixed content blocks). Without uuids, every event
      // starts its own turn — so each tool call gets its own timestamp.
      const shouldGroup =
        current && e.uuid && currentUuid && e.uuid === currentUuid;
      if (!shouldGroup) {
        flushCurrent();
        current = e.timestamp
          ? { kind: "turn", seq: e.seq, timestamp: e.timestamp, items: [] }
          : { kind: "turn", seq: e.seq, items: [] };
        currentUuid = e.uuid;
      }
      // After the branch above, current is non-null (either existing or
      // just assigned). Narrow with a local alias for TS.
      const turn = current!;
      if (e.type === "text") {
        const text = (e.text ?? "").trim();
        if (text) turn.items.push({ kind: "text", seq: e.seq, text });
      } else if (e.type === "tool_use") {
        const paired = e.tool_use_id
          ? resultById.get(e.tool_use_id)
          : undefined;
        const tool: ToolItem = {
          kind: "tool",
          seq: e.seq,
          name: e.tool_name ?? "(unknown)",
          input: e.tool_input,
          inputPreview: formatToolInput(e.tool_input),
        };
        if (paired?.output) tool.result = paired.output;
        if (paired?.timestamp) tool.resultTimestamp = paired.timestamp;
        turn.items.push(tool);
      }
      continue;
    }

    // Ignore system / other roles for now (not emitted by the current parser)
  }
  flushCurrent();

  return { prompt, blocks };
}

// ─── Sub-components ────────────────────────────────────────────────────

function ToolBlock({
  item,
  expanded,
  onToggle,
}: {
  item: ToolItem;
  expanded: boolean;
  onToggle: () => void;
}): JSX.Element {
  const payload = item.inputPreview;
  const resultText = item.result ?? "";

  return (
    <div
      className={styles.toolBlock}
      data-testid="transcript-event"
      data-type="tool_use"
    >
      <button
        type="button"
        className={`${styles.toolPill} ${expanded ? styles.toolPillOpen : ""}`}
        onClick={onToggle}
        aria-expanded={expanded}
        data-testid="tool-pill"
      >
        <span className={styles.toolPillIcon}>{item.name}</span>
        <span className={styles.toolPillArg}>{argPreview(item.input)}</span>
        <span className={styles.toolPillCaret}>{expanded ? "▾" : "▸"}</span>
      </button>
      {expanded && (
        <div className={styles.toolBody}>
          {payload && <pre className={styles.toolInput}>{payload}</pre>}
          {resultText && (
            <>
              <div className={styles.toolResultLabel}>
                Result
                {item.resultTimestamp && (
                  <span className={styles.ts}>
                    · {formatTimestamp(item.resultTimestamp)}
                  </span>
                )}
              </div>
              <pre className={styles.toolOutput}>{resultText}</pre>
            </>
          )}
        </div>
      )}
    </div>
  );
}

/** Return a short preview of the most salient tool-input arg (path, command, etc.). */
function argPreview(input: unknown): string {
  if (input == null) return "";
  if (typeof input === "string") return truncate(input, 60);
  if (typeof input !== "object") return "";
  const rec = input as Record<string, unknown>;
  for (const key of [
    "file_path",
    "filePath",
    "path",
    "notebook_path",
    "url",
    "pattern",
    "command",
    "query",
    "skill",
  ]) {
    const v = rec[key];
    if (typeof v === "string" && v) return truncate(v, 60);
  }
  return "";
}

function truncate(s: string, n: number): string {
  return s.length > n ? s.slice(0, n - 1) + "…" : s;
}

// ─── Main component ───────────────────────────────────────────────────

export function SessionDetailView({
  taskId,
  session,
}: SessionDetailViewProps): JSX.Element {
  const [innerTab, setInnerTab] = useState<InnerTab>("transcript");
  const [expandedTools, setExpandedTools] = useState<Set<number>>(
    () => new Set(),
  );

  const {
    entries,
    isLoading: transcriptLoading,
    error: transcriptError,
  } = useSessionTranscript(taskId, session.session_id, session.is_active);

  const {
    diff,
    isLoading: diffLoading,
    error: diffError,
  } = useSessionDiff(
    taskId,
    session.session_id,
    innerTab === "diff" && session.has_diff,
  );

  const grouped = useMemo(() => groupEvents(entries), [entries]);

  const toggleTool = (seq: number) => {
    setExpandedTools((prev) => {
      const next = new Set(prev);
      if (next.has(seq)) next.delete(seq);
      else next.add(seq);
      return next;
    });
  };

  const totalTokens =
    (session.input_tokens ?? 0) +
    (session.output_tokens ?? 0) +
    (session.cache_read_tokens ?? 0) +
    (session.cache_write_tokens ?? 0);

  return (
    <div className={styles.detail} data-testid="session-detail-view">
      {/* ── Masthead ── */}
      <header className={styles.masthead}>
        <div className={styles.mastheadHeader}>
          <span className={styles.agentName}>{session.agent_name}</span>
          <span className={styles.sep}>·</span>
          <span className={styles.agentBackend}>{session.backend}</span>
          {session.model && (
            <>
              <span className={styles.sep}>·</span>
              <span className={styles.metaItem}>
                <span className={styles.metaLabel}>Model:</span>
                <span className={styles.metaValue}>{session.model}</span>
              </span>
            </>
          )}
          {session.is_active && (
            <span className={styles.activeBadge}>active</span>
          )}
        </div>

        {grouped.prompt && (
          <div className={styles.promptBlock}>
            <div className={styles.promptLabel}>Prompt</div>
            <div className={styles.promptBody}>
              <MarkdownRenderer content={grouped.prompt.text} />
            </div>
          </div>
        )}

        <div className={styles.statRow}>
          <div className={styles.stat}>
            <div className={styles.statLabel}>Outcome</div>
            <div
              className={`${styles.statValue} ${statusToClass(session.status)}`}
            >
              <span className={styles.statusDot} />
              {session.status}
            </div>
          </div>
          <div className={styles.stat}>
            <div className={styles.statLabel}>Exit</div>
            <div className={styles.statValue}>
              {formatExitCode(session.exit_code)}
            </div>
          </div>
          <div className={styles.stat}>
            <div className={styles.statLabel}>Duration</div>
            <div className={styles.statValue}>
              {formatDuration(session.duration_s)}
            </div>
          </div>
          <div className={styles.stat}>
            <div className={styles.statLabel}>Tokens</div>
            <div className={styles.statValue}>{formatTokens(totalTokens)}</div>
          </div>
          <div className={styles.stat}>
            <div className={styles.statLabel}>Cost</div>
            <div className={styles.statValue}>
              {formatCost(session.estimated_cost_usd)}
            </div>
          </div>
          {(session.files_changed > 0 ||
            session.lines_added > 0 ||
            session.lines_removed > 0) && (
            <div className={styles.stat}>
              <div className={styles.statLabel}>Files</div>
              <div className={styles.statValue}>
                {session.files_changed}
                {(session.lines_added > 0 || session.lines_removed > 0) && (
                  <span className={styles.statSub}>
                    {" "}
                    +{session.lines_added} -{session.lines_removed}
                  </span>
                )}
              </div>
            </div>
          )}
        </div>
      </header>

      {session.files_touched && session.files_touched.length > 0 && (
        <details className={styles.filesTouchedSection}>
          <summary className={styles.filesTouchedSummary}>
            Files Touched ({session.files_touched.length})
          </summary>
          <ul className={styles.filesTouchedList}>
            {session.files_touched.map((path) => (
              <li key={path} className={styles.fileTouchedItem} title={path}>
                {path}
              </li>
            ))}
          </ul>
        </details>
      )}

      <div className={styles.innerTabBar}>
        <button
          type="button"
          className={`${styles.innerTab} ${innerTab === "transcript" ? styles.activeInnerTab : ""}`}
          onClick={() => setInnerTab("transcript")}
          data-testid="session-inner-tab-transcript"
        >
          Transcript
        </button>
        <button
          type="button"
          className={`${styles.innerTab} ${innerTab === "diff" ? styles.activeInnerTab : ""}`}
          onClick={() => setInnerTab("diff")}
          disabled={!session.has_diff}
          title={session.has_diff ? "View diff" : "No diff available"}
          data-testid="session-inner-tab-diff"
        >
          Diff
        </button>
      </div>

      {innerTab === "transcript" && (
        <div
          className={styles.transcriptContainer}
          data-testid="session-transcript"
        >
          {transcriptLoading && entries.length === 0 && (
            <div className={styles.emptyState}>Loading transcript...</div>
          )}
          {transcriptError && (
            <div className={styles.errorText}>
              Failed to load transcript: {transcriptError.message}
            </div>
          )}
          {!transcriptLoading && !transcriptError && entries.length === 0 && (
            <div className={styles.emptyState}>No transcript entries</div>
          )}

          {grouped.blocks.map((block) => {
            if (block.kind === "interjection") {
              return (
                <article
                  key={`int-${block.seq}`}
                  className={styles.interjection}
                  data-testid="transcript-interjection"
                >
                  <div className={styles.interjectionHeader}>
                    <span className={styles.interjectionLabel}>
                      User message
                    </span>
                    {block.timestamp && (
                      <span className={styles.ts}>
                        {formatTimestamp(block.timestamp)}
                      </span>
                    )}
                  </div>
                  <div className={styles.msg}>{block.text}</div>
                </article>
              );
            }
            const toolCount = block.items.filter(
              (i) => i.kind === "tool",
            ).length;
            return (
              <article
                key={`turn-${block.seq}`}
                className={styles.turn}
                data-testid="transcript-event"
                data-role="assistant"
              >
                <div className={styles.turnHeader}>
                  {block.timestamp && (
                    <span className={styles.ts}>
                      {formatTimestamp(block.timestamp)}
                    </span>
                  )}
                  {toolCount > 0 && (
                    <span className={styles.turnState}>
                      <span className={styles.turnStateGlyph} />
                      {toolCount === 1
                        ? "1 tool call"
                        : `${toolCount} tool calls`}
                    </span>
                  )}
                </div>
                {block.items.map((item) =>
                  item.kind === "text" ? (
                    <div key={item.seq} className={styles.msg}>
                      {item.text}
                    </div>
                  ) : (
                    <ToolBlock
                      key={item.seq}
                      item={item}
                      expanded={expandedTools.has(item.seq)}
                      onToggle={() => toggleTool(item.seq)}
                    />
                  ),
                )}
              </article>
            );
          })}
        </div>
      )}

      {innerTab === "diff" && (
        <div className={styles.diffContainer} data-testid="session-diff">
          {diffLoading && (
            <div className={styles.emptyState}>Loading diff...</div>
          )}
          {diffError && (
            <div className={styles.errorText}>
              Failed to load diff: {diffError.message}
            </div>
          )}
          {!diffLoading && !diffError && diff && (
            <div className={styles.diffCodeMirror}>
              <CodeMirrorEditor
                value={diff}
                language="diff"
                readOnly
                hideLineNumbers
              />
            </div>
          )}
          {!diffLoading && !diffError && !diff && (
            <div className={styles.diffEmpty}>No diff available</div>
          )}
        </div>
      )}
    </div>
  );
}

function statusToClass(status: string): string {
  switch (status) {
    case "completed":
      return styles.statValueOk ?? "";
    case "failed":
      return styles.statValueErr ?? "";
    case "running":
      return styles.statValueActive ?? "";
    default:
      return "";
  }
}
