import { useMemo, useState, type ReactNode } from "react";

import { MarkdownRenderer } from "@/components/MarkdownRenderer";
import type { SessionRecord, TranscriptEntry } from "@/types/agent";
import { formatStatusLabel } from "@/utils/issue";

import styles from "./TranscriptView.module.css";

export interface TranscriptViewProps {
  entries: TranscriptEntry[];
  session?: SessionRecord | undefined;
  isLoading?: boolean | undefined;
  error?: Error | null | undefined;
  emptyMessage?: string | undefined;
  className?: string | undefined;
  toolbar?: ReactNode | undefined;
  footer?: ReactNode | undefined;
  showTranscript?: boolean | undefined;
}

export type ToolItem = {
  kind: "tool";
  seq: number;
  name: string;
  input: unknown;
  inputPreview: string;
  result?: string;
  resultTimestamp?: string;
};

export type TextItem = { kind: "text"; seq: number; text: string };

export type ReasoningItem = {
  kind: "reasoning";
  seq: number;
  text: string;
};

export type TurnItem = TextItem | ToolItem | ReasoningItem;

export type RenderBlock =
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

export interface GroupedEvents {
  prompt: { text: string; timestamp?: string } | null;
  blocks: RenderBlock[];
}

export function groupEvents(entries: TranscriptEntry[]): GroupedEvents {
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
      const shouldGroup =
        current && e.uuid && currentUuid && e.uuid === currentUuid;
      if (!shouldGroup) {
        flushCurrent();
        current = e.timestamp
          ? { kind: "turn", seq: e.seq, timestamp: e.timestamp, items: [] }
          : { kind: "turn", seq: e.seq, items: [] };
        currentUuid = e.uuid;
      }
      const turn = current;
      if (!turn) continue;

      if (e.type === "text") {
        const text = (e.text ?? "").trim();
        if (text) turn.items.push({ kind: "text", seq: e.seq, text });
      } else if (e.type === "reasoning") {
        const text = (e.text ?? "").trim();
        if (text) turn.items.push({ kind: "reasoning", seq: e.seq, text });
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
    }
  }
  flushCurrent();

  return { prompt, blocks };
}

export function argPreview(input: unknown): string {
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

export function formatTimestamp(ts: string | undefined): string {
  if (!ts) return "";
  const d = new Date(ts);
  if (isNaN(d.getTime())) return ts;
  const hh = String(d.getHours()).padStart(2, "0");
  const mm = String(d.getMinutes()).padStart(2, "0");
  const ss = String(d.getSeconds()).padStart(2, "0");
  const ms = String(d.getMilliseconds()).padStart(3, "0");
  return `${hh}:${mm}:${ss}.${ms}`;
}

export function formatDuration(s: number | undefined): string {
  if (!s || s <= 0) return "-";
  if (s < 60) return `${s.toFixed(1)}s`;
  const m = Math.floor(s / 60);
  const rem = s - m * 60;
  return `${m}m ${rem.toFixed(0)}s`;
}

export function formatTokens(n: number): string {
  if (n < 1000) return String(n);
  if (n < 1000000) return `${(n / 1000).toFixed(1)}k`;
  return `${(n / 1000000).toFixed(1)}M`;
}

export function formatCost(usd: number): string {
  if (usd === 0) return "$0";
  if (usd < 0.01) return `$${usd.toFixed(4)}`;
  return `$${usd.toFixed(2)}`;
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

function truncate(s: string, n: number): string {
  return s.length > n ? `${s.slice(0, n - 1)}...` : s;
}

function formatExitCode(code: number): string {
  if (code === 0) return "0 (success)";
  return String(code);
}

function runErrorSummary(session: SessionRecord): string | null {
  if (session.last_error) return session.last_error;
  if (session.error_class) return session.error_class;
  if (session.status === "failed") return "Agent run failed.";
  if (session.status === "aborted") return "Agent run was aborted.";
  return null;
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
        <span className={styles.toolPillCaret}>{expanded ? "v" : ">"}</span>
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
                    {" "}
                    {formatTimestamp(item.resultTimestamp)}
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

function ReasoningBlock({
  item,
  expanded,
  onToggle,
}: {
  item: ReasoningItem;
  expanded: boolean;
  onToggle: () => void;
}): JSX.Element {
  const isLong = item.text.length > 420 || item.text.split("\n").length > 8;
  const body = isLong && !expanded ? truncate(item.text, 420) : item.text;

  return (
    <div className={styles.reasoningBlock} data-testid="transcript-reasoning">
      <button
        type="button"
        className={styles.reasoningHeader}
        onClick={isLong ? onToggle : undefined}
        aria-expanded={isLong ? expanded : undefined}
      >
        <span className={styles.reasoningLabel}>Reasoning</span>
        {isLong && (
          <span className={styles.reasoningCaret}>
            {expanded ? "Hide" : "Show"}
          </span>
        )}
      </button>
      <div
        className={
          isLong && !expanded ? styles.reasoningPreview : styles.reasoningBody
        }
      >
        {body}
      </div>
    </div>
  );
}

function SessionMasthead({
  session,
  prompt,
}: {
  session: SessionRecord;
  prompt: GroupedEvents["prompt"];
}): JSX.Element {
  const totalTokens =
    (session.input_tokens ?? 0) +
    (session.output_tokens ?? 0) +
    (session.cache_read_tokens ?? 0) +
    (session.cache_write_tokens ?? 0);
  const runError = runErrorSummary(session);

  return (
    <header className={styles.masthead}>
      <div className={styles.mastheadHeader}>
        <span className={styles.agentName}>{session.agent_name}</span>
        <span className={styles.sep}>/</span>
        <span className={styles.agentBackend}>{session.backend}</span>
        {session.model && (
          <>
            <span className={styles.sep}>/</span>
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

      {prompt && (
        <div className={styles.promptBlock}>
          <div className={styles.promptLabel}>Prompt</div>
          <MarkdownRenderer
            content={prompt.text}
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
  );
}

export function TranscriptView({
  entries,
  session,
  isLoading = false,
  error = null,
  emptyMessage = "No transcript entries",
  className,
  toolbar,
  footer,
  showTranscript = true,
}: TranscriptViewProps): JSX.Element {
  const [expandedTools, setExpandedTools] = useState<Set<number>>(
    () => new Set(),
  );
  const [expandedReasoning, setExpandedReasoning] = useState<Set<number>>(
    () => new Set(),
  );

  const grouped = useMemo(() => groupEvents(entries), [entries]);
  const rootClassName = [styles.root, className].filter(Boolean).join(" ");

  const toggleTool = (seq: number) => {
    setExpandedTools((prev) => {
      const next = new Set(prev);
      if (next.has(seq)) next.delete(seq);
      else next.add(seq);
      return next;
    });
  };

  const toggleReasoning = (seq: number) => {
    setExpandedReasoning((prev) => {
      const next = new Set(prev);
      if (next.has(seq)) next.delete(seq);
      else next.add(seq);
      return next;
    });
  };

  return (
    <div
      className={rootClassName}
      data-testid="transcript-view"
      data-body-hidden={showTranscript ? undefined : "true"}
    >
      {session && <SessionMasthead session={session} prompt={grouped.prompt} />}
      {toolbar && <div className={styles.toolbar}>{toolbar}</div>}

      {showTranscript && (
        <div
          className={styles.transcriptContainer}
          data-testid="session-transcript"
        >
          {isLoading && entries.length === 0 && (
            <div className={styles.emptyState}>Loading transcript...</div>
          )}
          {error && (
            <div className={styles.errorText}>
              Failed to load transcript: {error.message}
            </div>
          )}
          {!isLoading && !error && entries.length === 0 && (
            <div className={styles.emptyState}>{emptyMessage}</div>
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
                {block.items.map((item) => {
                  if (item.kind === "text") {
                    return (
                      <MarkdownRenderer
                        key={item.seq}
                        content={item.text}
                        className={styles.msg}
                      />
                    );
                  }
                  if (item.kind === "reasoning") {
                    return (
                      <ReasoningBlock
                        key={item.seq}
                        item={item}
                        expanded={expandedReasoning.has(item.seq)}
                        onToggle={() => toggleReasoning(item.seq)}
                      />
                    );
                  }
                  return (
                    <ToolBlock
                      key={item.seq}
                      item={item}
                      expanded={expandedTools.has(item.seq)}
                      onToggle={() => toggleTool(item.seq)}
                    />
                  );
                })}
              </article>
            );
          })}
          {footer}
        </div>
      )}
    </div>
  );
}
