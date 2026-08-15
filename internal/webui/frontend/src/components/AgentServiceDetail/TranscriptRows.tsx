import { useMemo, useState } from "react";

import type { TranscriptEntry } from "@/types/agent";

import styles from "./AgentServiceDetail.module.css";

function recordValue(value: unknown): Record<string, unknown> | null {
  return typeof value === "object" && value !== null && !Array.isArray(value)
    ? (value as Record<string, unknown>)
    : null;
}

function prettyJson(value: unknown): string | null {
  try {
    const parsed = typeof value === "string" ? JSON.parse(value) : value;
    return JSON.stringify(parsed, null, 2);
  } catch {
    return null;
  }
}

function outputStatus(
  entry: TranscriptEntry,
  output: string,
): { label: string; tone: "success" | "danger" | "neutral" } {
  const exit = /^\[exit\s+(\d+)\]/.exec(output);
  if (entry.tool_name === "shell") {
    const code = exit ? Number(exit[1]) : 0;
    return {
      label: `exit ${code}`,
      tone: code === 0 ? "success" : "danger",
    };
  }
  if (/^\[error\]/i.test(output)) return { label: "failed", tone: "danger" };
  return { label: "completed", tone: "success" };
}

function ToolRow({
  entry,
  output,
}: {
  entry: TranscriptEntry;
  output: string;
}): JSX.Element {
  const [expanded, setExpanded] = useState(false);
  const input = recordValue(entry.tool_input);
  const command = typeof input?.command === "string" ? input.command : "";
  const status = outputStatus(entry, output);
  const visibleOutput =
    entry.tool_name === "shell" ? output.replace(/^\[exit\s+\d+\]\n?/, "") : output;
  const outputKilobytes = Math.max(1, Math.round(visibleOutput.length / 1024));
  const inputText = command ? null : prettyJson(entry.tool_input);

  return (
    <div className={styles.transcriptRow} data-testid="transcript-command">
      <div className={styles.transcriptCommandHeader}>
        <code className={styles.transcriptCommand}>
          {command ? `$ ${command}` : entry.tool_name || "Unknown tool"}
        </code>
        <span className={styles.transcriptBadge} data-tone={status.tone}>
          {status.label}
        </span>
      </div>
      {inputText && inputText !== "undefined" ? (
        <pre className={styles.transcriptMessageJson}>{inputText}</pre>
      ) : null}
      {visibleOutput ? (
        <>
          <button
            type="button"
            className={styles.transcriptDisclosure}
            data-testid="transcript-output-toggle"
            aria-expanded={expanded}
            onClick={() => setExpanded((value) => !value)}
          >
            {expanded ? "Hide output" : "Show output"} ({outputKilobytes} KB)
          </button>
          {expanded ? (
            <pre className={styles.transcriptOutput}>{visibleOutput}</pre>
          ) : null}
        </>
      ) : null}
    </div>
  );
}

function TextRow({ entry }: { entry: TranscriptEntry }): JSX.Element {
  const text = entry.text ?? "";
  if (entry.role === "system") {
    return (
      <div
        className={`${styles.transcriptPlain} ${styles.transcriptOther}`}
        data-testid="transcript-system-text"
      >
        {text}
      </div>
    );
  }
  const formattedJson = prettyJson(text);
  return (
    <div className={styles.transcriptRow} data-testid="transcript-message">
      <strong className={styles.transcriptSectionLabel}>
        {entry.role === "user" ? "User" : "Assistant"}
      </strong>
      {formattedJson !== null ? (
        <pre className={styles.transcriptMessageJson}>{formattedJson}</pre>
      ) : (
        <div className={styles.transcriptMessageText}>{text}</div>
      )}
    </div>
  );
}

function ReasoningRow({ entry }: { entry: TranscriptEntry }): JSX.Element {
  const text = entry.text ?? "";
  const isLong = text.length > 400;
  const [expanded, setExpanded] = useState(false);
  return (
    <div
      className={`${styles.transcriptRow} ${styles.transcriptReasoning}`}
      data-testid="transcript-reasoning"
    >
      <strong className={styles.transcriptSectionLabel}>Reasoning</strong>
      {isLong ? (
        <button
          type="button"
          className={styles.transcriptDisclosure}
          aria-expanded={expanded}
          onClick={() => setExpanded((value) => !value)}
        >
          {expanded ? "Hide reasoning" : "Show reasoning"}
        </button>
      ) : null}
      {!isLong || expanded ? (
        <div className={styles.transcriptMessageText}>{text}</div>
      ) : null}
    </div>
  );
}

function usageNumber(usage: Record<string, unknown> | null, key: string): number {
  const value = usage?.[key];
  return typeof value === "number" && Number.isFinite(value) ? value : 0;
}

function ResultRow({ entry }: { entry: TranscriptEntry }): JSX.Element {
  const failed = /^failed\b/i.test(entry.text ?? "");
  const parsedUsage = (() => {
    if (!entry.output) return null;
    try {
      return recordValue(JSON.parse(entry.output));
    } catch {
      return null;
    }
  })();
  const input = usageNumber(parsedUsage, "input_tokens");
  const cached = usageNumber(parsedUsage, "cache_read_tokens");
  const output = usageNumber(parsedUsage, "output_tokens");
  if (failed) {
    return (
      <div
        className={styles.transcriptError}
        data-testid="transcript-failed"
        role="alert"
      >
        Failed{entry.text && entry.text.toLowerCase() !== "failed" ? ` · ${entry.text}` : ""}
      </div>
    );
  }
  return (
    <div
      className={styles.transcriptTurnCompleted}
      data-testid="transcript-turn-completed"
    >
      {input || cached || output
        ? `Turn completed · ${input.toLocaleString("en-US")} input tokens (${cached.toLocaleString("en-US")} cached) · ${output.toLocaleString("en-US")} output`
        : entry.text || "Turn completed"}
    </div>
  );
}

function UnknownRow({ entry }: { entry: TranscriptEntry }): JSX.Element {
  const label = String(entry.type || "unknown").replace(/[._-]+/g, " ");
  return (
    <div
      className={`${styles.transcriptRow} ${styles.transcriptOther}`}
      data-testid="transcript-unknown"
    >
      {label}
      {entry.text ? ` · ${entry.text}` : ""}
    </div>
  );
}

export function TranscriptRows({
  entries,
}: {
  entries: TranscriptEntry[];
}): JSX.Element {
  const toolResults = useMemo(() => {
    const byID = new Map<string, TranscriptEntry>();
    for (const entry of entries) {
      if (entry.type === "tool_result" && entry.tool_use_id) {
        byID.set(entry.tool_use_id, entry);
      }
    }
    return byID;
  }, [entries]);

  return (
    <div className={styles.transcriptView} data-testid="transcript-view">
      {entries.map((entry) => {
        if (entry.type === "tool_result" && entry.tool_use_id) return null;
        if (entry.type === "tool_use") {
          const output = toolResults.get(entry.tool_use_id ?? "")?.output;
          return (
            <ToolRow
              key={entry.seq}
              entry={entry}
              output={output || entry.output || ""}
            />
          );
        }
        if (entry.type === "text") return <TextRow key={entry.seq} entry={entry} />;
        if (entry.type === "reasoning") {
          return <ReasoningRow key={entry.seq} entry={entry} />;
        }
        if (entry.type === "result") return <ResultRow key={entry.seq} entry={entry} />;
        if (entry.type === "session_meta") {
          return (
            <div
              key={entry.seq}
              className={`${styles.transcriptPlain} ${styles.transcriptOther}`}
            >
              {entry.text || "Session started"}
            </div>
          );
        }
        return <UnknownRow key={entry.seq} entry={entry} />;
      })}
    </div>
  );
}
