import { useState } from "react";

import type { ParsedTranscript, TranscriptRow } from "@/utils/transcript";

import styles from "./AgentServiceDetail.module.css";

function prettyJson(text: string): string | null {
  try {
    return JSON.stringify(JSON.parse(text), null, 2);
  } catch {
    return null;
  }
}

function CommandRow({
  row,
}: {
  row: Extract<TranscriptRow, { kind: "command" }>;
}): JSX.Element {
  const [expanded, setExpanded] = useState(false);
  const outputKilobytes = Math.max(1, Math.round(row.output.length / 1024));
  const badge =
    row.exitCode === 0
      ? { label: "exit 0", tone: "success" }
      : row.exitCode !== null
        ? { label: `exit ${row.exitCode}`, tone: "danger" }
        : { label: row.status || "unknown", tone: "neutral" };

  return (
    <div className={styles.transcriptRow} data-testid="transcript-command">
      <div className={styles.transcriptCommandHeader}>
        <code className={styles.transcriptCommand}>$ {row.command}</code>
        <span className={styles.transcriptBadge} data-tone={badge.tone}>
          {badge.label}
        </span>
      </div>
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
        <pre className={styles.transcriptOutput}>{row.output}</pre>
      ) : null}
    </div>
  );
}

function FileChangeRow({
  row,
}: {
  row: Extract<TranscriptRow, { kind: "fileChange" }>;
}): JSX.Element {
  return (
    <div className={styles.transcriptRow}>
      <div className={styles.transcriptSectionHeader}>
        <strong>Files changed</strong>
        <span className={styles.transcriptBadge} data-tone="neutral">
          {row.status}
        </span>
      </div>
      {row.changes.length > 0 ? (
        <ul className={styles.transcriptFileList}>
          {row.changes.map((change, index) => (
            <li key={`${change.kind}-${change.path}-${index}`}>
              <span className={styles.transcriptChangeBadge}>
                {change.kind}
              </span>
              <code>{change.path}</code>
            </li>
          ))}
        </ul>
      ) : (
        <p className={styles.transcriptMuted}>No file paths reported.</p>
      )}
    </div>
  );
}

function MessageRow({
  row,
}: {
  row: Extract<TranscriptRow, { kind: "message" }>;
}): JSX.Element {
  const formattedJson = prettyJson(row.text);
  return (
    <div className={styles.transcriptRow} data-testid="transcript-message">
      <strong className={styles.transcriptSectionLabel}>Assistant</strong>
      {formattedJson !== null ? (
        <pre className={styles.transcriptMessageJson}>{formattedJson}</pre>
      ) : (
        <div className={styles.transcriptMessageText}>{row.text}</div>
      )}
    </div>
  );
}

function ReasoningRow({
  row,
}: {
  row: Extract<TranscriptRow, { kind: "reasoning" }>;
}): JSX.Element {
  const isLong = row.text.length > 400;
  const [expanded, setExpanded] = useState(false);

  return (
    <div className={`${styles.transcriptRow} ${styles.transcriptReasoning}`}>
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
        <div className={styles.transcriptMessageText}>{row.text}</div>
      ) : null}
    </div>
  );
}

function OtherRow({
  row,
}: {
  row: Extract<TranscriptRow, { kind: "other" }>;
}): JSX.Element {
  const [expanded, setExpanded] = useState(false);
  return (
    <div className={`${styles.transcriptRow} ${styles.transcriptOther}`}>
      <span>{row.label}</span>
      <button
        type="button"
        className={styles.transcriptDisclosure}
        aria-expanded={expanded}
        onClick={() => setExpanded((value) => !value)}
      >
        {expanded ? "Hide event" : "Show event"}
      </button>
      {expanded ? <pre>{row.raw}</pre> : null}
    </div>
  );
}

function TurnCompletedRow({
  row,
}: {
  row: Extract<TranscriptRow, { kind: "turnCompleted" }>;
}): JSX.Element {
  const { usage } = row;
  return (
    <div
      className={styles.transcriptTurnCompleted}
      data-testid="transcript-turn-completed"
    >
      Turn completed · {usage.inputTokens.toLocaleString("en-US")} input tokens
      ({usage.cachedInputTokens.toLocaleString("en-US")} cached) ·{" "}
      {usage.outputTokens.toLocaleString("en-US")} output
    </div>
  );
}

function renderRow(row: TranscriptRow, index: number): JSX.Element {
  switch (row.kind) {
    case "plain":
      return (
        <div className={styles.transcriptPlain} key={index}>
          {row.text}
        </div>
      );
    case "unparsed":
      return (
        <div
          className={styles.transcriptUnparsed}
          data-testid="transcript-unparsed"
          title={row.text}
          key={index}
        >
          {row.text}
        </div>
      );
    case "command":
      return <CommandRow row={row} key={index} />;
    case "fileChange":
      return <FileChangeRow row={row} key={index} />;
    case "message":
      return <MessageRow row={row} key={index} />;
    case "reasoning":
      return <ReasoningRow row={row} key={index} />;
    case "turnCompleted":
      return <TurnCompletedRow row={row} key={index} />;
    case "turnFailed":
      return (
        <div className={styles.transcriptError} role="alert" key={index}>
          {row.message}
        </div>
      );
    case "other":
      return <OtherRow row={row} key={index} />;
  }
}

export function TranscriptView({
  transcript,
}: {
  transcript: ParsedTranscript;
}): JSX.Element {
  return (
    <div className={styles.transcriptView} data-testid="transcript-view">
      {transcript.rows.map(renderRow)}
    </div>
  );
}
