/**
 * SessionRunDetail — shared transcript/diff renderer for a selected session.
 *
 * Renders the canonical transcript.Event stream as an agent worklog. The
 * kickoff user prompt is surfaced once in the masthead; each subsequent
 * assistant response is a turn with inline collapsible tool calls; mid-run
 * user messages render as distinct interjection blocks.
 */

import { useMemo, useState } from "react";

import { CodeMirrorEditor } from "@/components/CodeMirrorEditor";
import { MarkdownRenderer } from "@/components/MarkdownRenderer";
import { useSessionTranscript, useSessionDiff } from "@/hooks/terminal";
import styles from "@/styles/SessionRunDetail.module.css";
import type { SessionRecord, TranscriptEntry } from "@/types/agent";
import { formatStatusLabel } from "@/utils/issue";

export interface SessionRunDetailProps {
  taskId: string;
  /** Agent owner for an interactive session that has no taskId. */
  agentId?: string;
  session: SessionRecord;
  /**
   * Retry an initially unavailable transcript. Used only for a synthesized
   * terminal workflow session while the durable session projection catches up.
   */
  retryTranscriptUnavailable?: boolean;
  /** Whether exit_code came from durable session evidence. */
  exitCodeKnown?: boolean;
  /** Whether token and cost fields came from durable usage evidence. */
  telemetryKnown?: boolean;
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

function evidenceLabel(value: string | undefined): string {
  return (value ?? "").replace(/_/g, " ").trim();
}

function safeExternalURL(value: string | undefined): string | null {
  if (!value) return null;
  try {
    const parsed = new URL(value);
    if (parsed.protocol === "https:" || parsed.protocol === "http:") {
      return parsed.toString();
    }
  } catch {
    // Invalid or relative metadata is not an external navigation target.
  }
  return null;
}

function runErrorSummary(session: SessionRecord): string | null {
  if (session.last_error) return session.last_error;
  if (session.error_class) return session.error_class;
  if (session.status === "failed") return "Agent run failed.";
  if (session.status === "aborted") return "Agent run was aborted.";
  return null;
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
      kind: "notice";
      seq: number;
    }
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

    // Canonical capture emits one exact system/session_meta marker when Loom
    // truncates source history or bounded output. Render a fixed product notice
    // rather than the backend text so arbitrary system metadata and reasoning
    // remain hidden.
    if (
      e.role === "system" &&
      e.type === "session_meta" &&
      (e.text ?? "").startsWith("Transcript truncated by Loom because ")
    ) {
      flushCurrent();
      blocks.push({ kind: "notice", seq: e.seq });
      continue;
    }

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

    // Reasoning, result, session metadata, and system events are accepted by
    // the canonical wire type but intentionally not exposed in this worklog.
    // In particular, do not leak hidden reasoning merely because it is present
    // in a backend transcript.
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

// formatTranscriptError avoids the "Failed to load transcript: failed to load
// transcript" doubling: the server's message already describes the transcript
// problem (e.g. "failed to load transcript", "transcript content is no longer
// available"), so a message that already mentions "transcript" is shown as-is
// (capitalized); an unrelated cause (e.g. "Network error") keeps the prefix.
function formatTranscriptError(message: string): string {
  const raw = message.trim();
  if (!raw) return "Failed to load transcript";
  if (/transcript/i.test(raw)) {
    return raw.charAt(0).toUpperCase() + raw.slice(1);
  }
  return `Failed to load transcript: ${raw}`;
}

// ─── Main component ───────────────────────────────────────────────────

export function SessionRunDetail(props: SessionRunDetailProps): JSX.Element {
  // SessionRunDetail owns tab selection and diff/transcript hook state. A keyed
  // inner component guarantees none of that evidence survives when a caller
  // switches the selected run without unmounting this wrapper.
  return <SessionRunDetailContent key={props.session.session_id} {...props} />;
}

function SessionRunDetailContent({
  taskId,
  agentId,
  session,
  retryTranscriptUnavailable = false,
  exitCodeKnown = true,
  telemetryKnown = true,
}: SessionRunDetailProps): JSX.Element {
  const [innerTab, setInnerTab] = useState<InnerTab>("transcript");
  const [expandedTools, setExpandedTools] = useState<Set<number>>(
    () => new Set(),
  );

  const {
    entries,
    isLoading: transcriptLoading,
    isUnavailable: transcriptUnavailable,
    error: transcriptError,
  } = useSessionTranscript(taskId, session.session_id, session.is_active, {
    retryUnavailable: retryTranscriptUnavailable,
    ...(agentId ? { agentId } : {}),
  });

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
  const runError = runErrorSummary(session);
  const executionBranch = session.local_branch || session.github_branch;
  const githubPRURL = safeExternalURL(session.github_pr_url);
  const hasExecutionEvidence = Boolean(
    session.runtime_strategy ||
    session.delivery ||
    session.patch_back_status ||
    session.logs_ref ||
    executionBranch ||
    session.head_sha ||
    githubPRURL,
  );

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
            <MarkdownRenderer
              content={grouped.prompt.text}
              className={styles.promptBody}
            />
          </div>
        )}

        {runError && (
          <div
            className={styles.runErrorBanner}
            role="alert"
            data-testid="run-error-banner"
          >
            <div className={styles.runErrorTitle}>Run failed</div>
            <div className={styles.runErrorBody}>{runError}</div>
          </div>
        )}

        <div className={styles.statRow}>
          <div className={styles.stat}>
            <div className={styles.statLabel}>Outcome</div>
            <div
              className={`${styles.statValue} ${statusToClass(session.status)}`}
            >
              <span className={styles.statusDot} />
              {formatStatusLabel(session.status)}
            </div>
          </div>
          <div className={styles.stat}>
            <div className={styles.statLabel}>Exit</div>
            <div className={styles.statValue}>
              {exitCodeKnown ? formatExitCode(session.exit_code) : "—"}
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
            <div className={styles.statValue}>
              {telemetryKnown ? formatTokens(totalTokens) : "—"}
            </div>
          </div>
          <div className={styles.stat}>
            <div className={styles.statLabel}>Cost</div>
            <div className={styles.statValue}>
              {telemetryKnown ? formatCost(session.estimated_cost_usd) : "—"}
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
        {hasExecutionEvidence && (
          <div
            className={styles.statRow}
            data-testid="session-execution-evidence"
          >
            {session.runtime_strategy && (
              <div className={styles.stat}>
                <div className={styles.statLabel}>Runtime</div>
                <div
                  className={styles.statValue}
                  title={session.runtime_strategy}
                >
                  {evidenceLabel(session.runtime_strategy)}
                </div>
              </div>
            )}
            {session.delivery && (
              <div className={styles.stat}>
                <div className={styles.statLabel}>Delivery</div>
                <div className={styles.statValue} title={session.delivery}>
                  {evidenceLabel(session.delivery)}
                </div>
              </div>
            )}
            {session.patch_back_status && (
              <div className={styles.stat}>
                <div className={styles.statLabel}>Patch back</div>
                <div className={styles.statValue}>
                  {evidenceLabel(session.patch_back_status)}
                </div>
              </div>
            )}
            {executionBranch && (
              <div className={styles.stat}>
                <div className={styles.statLabel}>Branch</div>
                <div className={styles.statValue} title={executionBranch}>
                  {executionBranch}
                </div>
              </div>
            )}
            {session.head_sha && (
              <div className={styles.stat}>
                <div className={styles.statLabel}>Head</div>
                <div className={styles.statValue} title={session.head_sha}>
                  {session.head_sha.slice(0, 12)}
                </div>
              </div>
            )}
            {session.logs_ref && (
              <div className={styles.stat}>
                <div className={styles.statLabel}>Logs</div>
                <div className={styles.statValue} title={session.logs_ref}>
                  {session.logs_ref}
                </div>
              </div>
            )}
            {githubPRURL && (
              <div className={styles.stat}>
                <div className={styles.statLabel}>Pull request</div>
                <a
                  className={styles.statValue}
                  href={githubPRURL}
                  target="_blank"
                  rel="noreferrer"
                >
                  Open PR
                </a>
              </div>
            )}
          </div>
        )}
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
          {transcriptLoading &&
            !transcriptUnavailable &&
            entries.length === 0 && (
              <div className={styles.emptyState}>Loading transcript...</div>
            )}
          {transcriptUnavailable && entries.length === 0 && (
            <div
              className={styles.emptyState}
              data-testid="session-transcript-unavailable"
            >
              Transcript is unavailable for this session.
            </div>
          )}
          {transcriptError && (
            <div className={styles.errorText}>
              {formatTranscriptError(transcriptError.message)}
            </div>
          )}
          {!transcriptLoading &&
            !transcriptUnavailable &&
            !transcriptError &&
            entries.length === 0 && (
              <div className={styles.emptyState}>No transcript entries</div>
            )}

          {grouped.blocks.map((block) => {
            if (block.kind === "notice") {
              return (
                <div
                  key={`notice-${block.seq}`}
                  className={styles.transcriptNotice}
                  role="status"
                  data-testid="transcript-truncation-notice"
                >
                  Transcript truncated by Loom. Some transcript entries are not
                  shown.
                </div>
              );
            }
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
                  <MarkdownRenderer
                    content={block.text}
                    className={styles.msg}
                  />
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
                    <MarkdownRenderer
                      key={item.seq}
                      content={item.text}
                      className={styles.msg}
                    />
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
