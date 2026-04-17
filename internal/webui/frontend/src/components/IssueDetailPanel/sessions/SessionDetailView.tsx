/**
 * SessionDetailView - Detail panel for a selected session.
 *
 * Renders the canonical transcript.Event stream returned by the backend.
 * Each event is one of:
 *   - { role: "user"/"assistant", type: "text", text }
 *   - { role: "assistant",       type: "tool_use", tool_name, tool_input }
 *   - { role: "tool",            type: "tool_result", output, tool_use_id }
 * Events are grouped visually by role; tool_use blocks can be expanded to
 * show their JSON argument payload.
 */

import { useState } from "react";

import { CodeMirrorEditor } from "@/components/CodeMirrorEditor";
import { useSessionTranscript, useSessionDiff } from "@/hooks/terminal";
import type { SessionRecord, TranscriptEntry } from "@/types/agent";

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

function firstLinePreview(text: string, max = 200): string {
  const firstLine = text.split("\n", 1)[0] ?? "";
  if (firstLine.length <= max) return firstLine;
  return firstLine.slice(0, max) + "…";
}

const INLINE_LIMIT = 2_000;

function TranscriptEvent({
  entry,
  expanded,
  onToggle,
}: {
  entry: TranscriptEntry;
  expanded: boolean;
  onToggle: () => void;
}): JSX.Element | null {
  const key = entry.seq;

  if (entry.type === "text") {
    const text = entry.text ?? "";
    if (!text) return null;
    return (
      <div
        key={key}
        className={styles.transcriptEntry}
        data-testid="transcript-event"
        data-role={entry.role}
        data-type="text"
      >
        <div className={styles.transcriptRole} data-role={entry.role}>
          {entry.role}
        </div>
        <div className={styles.transcriptContent}>{text}</div>
      </div>
    );
  }

  if (entry.type === "tool_use") {
    const payload = formatToolInput(entry.tool_input);
    const isLarge = payload.length > INLINE_LIMIT;
    return (
      <div
        key={key}
        className={styles.transcriptEntry}
        data-testid="transcript-event"
        data-role={entry.role}
        data-type="tool_use"
      >
        <div className={styles.transcriptRole} data-role={entry.role}>
          {entry.role}
        </div>
        <div className={styles.transcriptToolName}>
          Tool: {entry.tool_name ?? "(unknown)"}
        </div>
        {payload && (
          <div className={styles.transcriptContent}>
            {!isLarge || expanded ? (
              <>
                <pre>{payload}</pre>
                {isLarge && expanded && (
                  <button
                    type="button"
                    className={styles.toolInputToggle}
                    data-testid="show-less-input"
                    onClick={onToggle}
                  >
                    Show less
                  </button>
                )}
              </>
            ) : (
              <>
                <pre>{payload.slice(0, INLINE_LIMIT)}…</pre>
                <button
                  type="button"
                  className={styles.toolInputToggle}
                  data-testid="show-full-input"
                  onClick={onToggle}
                >
                  Show full input
                </button>
              </>
            )}
          </div>
        )}
      </div>
    );
  }

  if (entry.type === "tool_result") {
    const output = entry.output ?? "";
    if (!output) return null;
    const isLarge = output.length > INLINE_LIMIT;
    return (
      <div
        key={key}
        className={styles.transcriptEntry}
        data-testid="transcript-event"
        data-role={entry.role}
        data-type="tool_result"
      >
        <div className={styles.transcriptRole} data-role={entry.role}>
          tool result
        </div>
        {!expanded && isLarge ? (
          <div className={styles.transcriptContent}>
            <div>{firstLinePreview(output)}</div>
            <button
              type="button"
              className={styles.toolInputToggle}
              data-testid="show-full-input"
              onClick={onToggle}
            >
              Show full output
            </button>
          </div>
        ) : (
          <div className={styles.transcriptContent}>
            <pre>{output}</pre>
            {isLarge && (
              <button
                type="button"
                className={styles.toolInputToggle}
                data-testid="show-less-input"
                onClick={onToggle}
              >
                Show less
              </button>
            )}
          </div>
        )}
      </div>
    );
  }

  return null;
}

export function SessionDetailView({
  taskId,
  session,
}: SessionDetailViewProps): JSX.Element {
  const [innerTab, setInnerTab] = useState<InnerTab>("transcript");
  const [expandedInputs, setExpandedInputs] = useState<Set<number>>(
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

  const toggleExpanded = (seq: number) => {
    setExpandedInputs((prev) => {
      const next = new Set(prev);
      if (next.has(seq)) next.delete(seq);
      else next.add(seq);
      return next;
    });
  };

  return (
    <div className={styles.detail} data-testid="session-detail-view">
      <div className={styles.metaSummary}>
        {session.model && (
          <span className={styles.metaItem}>
            <span className={styles.metaLabel}>Model:</span>
            <span className={styles.metaValue}>{session.model}</span>
          </span>
        )}
        <span className={styles.metaItem}>
          <span className={styles.metaLabel}>Exit:</span>
          <span className={styles.metaValue}>
            {formatExitCode(session.exit_code)}
          </span>
        </span>
        {session.files_changed > 0 && (
          <span className={styles.metaItem}>
            <span className={styles.metaLabel}>Files:</span>
            <span className={styles.metaValue}>{session.files_changed}</span>
          </span>
        )}
        {(session.lines_added > 0 || session.lines_removed > 0) && (
          <span className={styles.metaItem}>
            <span className={styles.metaValue}>
              +{session.lines_added} -{session.lines_removed}
            </span>
          </span>
        )}
      </div>

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
          {entries.map((entry) => (
            <TranscriptEvent
              key={entry.seq}
              entry={entry}
              expanded={expandedInputs.has(entry.seq)}
              onToggle={() => toggleExpanded(entry.seq)}
            />
          ))}
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
