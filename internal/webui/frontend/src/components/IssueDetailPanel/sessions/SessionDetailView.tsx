/**
 * SessionDetailView — detail panel for a selected session.
 *
 * Renders the canonical transcript.Event stream as an agent worklog.
 * Assistant responses are turns with inline collapsible tool calls; the first
 * real user request is retained as the prompt and later user messages render as
 * interjections. Known backend-injected context is filtered explicitly.
 */

import { useMemo, useState } from "react";

import { CodeMirrorEditor } from "@/components/CodeMirrorEditor";
import { ToolPill } from "@/components/ToolPill";
import { useSessionTranscript, useSessionDiff } from "@/hooks/terminal";
import type { SessionRecord, TranscriptEntry } from "@/types/agent";
import { formatStatusLabel } from "@/utils/issue";
import { formatTokens, sessionTotalTokens } from "@/utils/sessionUsage";
import { argPreview } from "@/utils/toolPreview";

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

// Cost and duration keep detail-panel precision here (sub-cent cost to 4dp,
// fractional seconds) rather than the run rail's rounded summary formatting.
function formatCost(usd: number | null): string {
  if (usd == null) return "—";
  if (usd === 0) return "$0";
  if (usd < 0.01) return `$${usd.toFixed(4)}`;
  return `$${usd.toFixed(2)}`;
}

function formatDuration(s: number | undefined): string {
  if (!s || s <= 0) return "—";
  if (s < 60) return `${s.toFixed(1)}s`;
  const m = Math.floor(s / 60);
  const rem = s - m * 60;
  return `${m}m ${rem.toFixed(0)}s`;
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

type TextItem = { kind: "text"; seq: number; text: string; rawText?: string };

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

function isSyntheticUserContext(text: string): boolean {
  const normalized = text.trimStart();
  return (
    normalized.startsWith("<recommended_plugins>") ||
    normalized.startsWith("# AGENTS.md instructions for ") ||
    normalized.startsWith("<environment_context>") ||
    normalized.startsWith("<INSTRUCTIONS>")
  );
}

/**
 * Walk the event stream and produce render-ready blocks:
 *  - known backend-injected user context is omitted
 *  - the first real user text becomes the prompt
 *  - subsequent real user text messages become "interjection" blocks
 *  - assistant text + tool_use events are grouped into "turn" blocks
 *  - tool_result events are matched to their tool_use by tool_use_id and
 *    rendered inline inside the turn (never as their own block);
 *    when absent, tool_use.output is used (TS leaf / Codex embed path)
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
      if (isSyntheticUserContext(text)) continue;
      if (!prompt) {
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
        const decoded = decodeTranscriptText(e.text ?? "");
        if (decoded.text) {
          turn.items.push({ kind: "text", seq: e.seq, ...decoded });
        }
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
        const resultText = paired?.output || e.output;
        if (resultText) tool.result = resultText;
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

function decodeTranscriptText(rawText: string): {
  text: string;
  rawText?: string;
} {
  const trimmed = rawText.trim();
  if (!trimmed.startsWith("{") || !trimmed.endsWith("}")) {
    return { text: trimmed };
  }

  try {
    const parsed = JSON.parse(trimmed) as unknown;
    if (
      parsed &&
      typeof parsed === "object" &&
      "output" in parsed &&
      typeof (parsed as { output?: unknown }).output === "string"
    ) {
      return {
        text: (parsed as { output: string }).output.trim(),
        rawText: trimmed,
      };
    }
  } catch {
    return { text: trimmed };
  }

  return { text: trimmed };
}

function TranscriptText({ item }: { item: TextItem }): JSX.Element {
  if (!item.rawText) {
    return <MarkdownRenderer content={item.text} className={styles.msg} />;
  }

  return (
    <div className={styles.msgFrame} title={item.rawText}>
      <MarkdownRenderer content={item.text} className={styles.msg} />
      <details className={styles.rawTranscriptDetails}>
        <summary>Raw envelope</summary>
        <pre>{item.rawText}</pre>
      </details>
    </div>
  );
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
    session.evidence?.usage_status === "unavailable" ||
    (session.evidence == null &&
      session.input_tokens == null &&
      session.output_tokens == null)
      ? null
      : sessionTotalTokens(session);
  const runError = runErrorSummary(session);

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

        {session.evidence?.status === "conflict" && (
          <div
            className={styles.runErrorBanner}
            role="alert"
            data-testid="run-evidence-conflict"
          >
            <div className={styles.runErrorTitle}>Run evidence conflict</div>
            <div className={styles.runErrorBody}>
              {session.evidence.conflicts.map((conflict) => (
                <div key={`${conflict.field}-${conflict.incoming_source}`}>
                  {conflict.field}: {conflict.existing_source} reported{" "}
                  {conflict.existing_value}; {conflict.incoming_source} reported{" "}
                  {conflict.incoming_value}
                </div>
              ))}
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
              {formatStatusLabel(session.status)}
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
            <div className={styles.statValue}>
              {totalTokens == null ? "—" : formatTokens(totalTokens)}
            </div>
          </div>
          {(session.estimated_cost_usd ?? 0) > 0 && (
            <div className={styles.stat}>
              <div className={styles.statLabel}>Cost</div>
              <div className={styles.statValue}>
                {formatCost(session.estimated_cost_usd)}
              </div>
            </div>
          )}
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

          {grouped.prompt && (
            <details className={styles.promptBlock} open>
              <summary className={styles.promptSummary}>
                <span className={styles.promptLabel}>Prompt</span>
                {grouped.prompt.timestamp && (
                  <span className={styles.ts}>
                    {formatTimestamp(grouped.prompt.timestamp)}
                  </span>
                )}
              </summary>
              <MarkdownRenderer
                content={grouped.prompt.text}
                className={styles.promptBody}
              />
            </details>
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
                    <TranscriptText key={item.seq} item={item} />
                  ) : (
                    <ToolPill
                      key={item.seq}
                      name={item.name}
                      arg={argPreview(item.input)}
                      input={item.inputPreview}
                      result={item.result}
                      resultTimestamp={
                        item.resultTimestamp
                          ? formatTimestamp(item.resultTimestamp)
                          : undefined
                      }
                      expanded={expandedTools.has(item.seq)}
                      onToggle={() => toggleTool(item.seq)}
                      className={styles.toolBlock}
                      testId="transcript-event"
                      dataType="tool_use"
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
