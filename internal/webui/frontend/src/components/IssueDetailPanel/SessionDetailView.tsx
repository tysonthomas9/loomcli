/**
 * SessionDetailView component.
 * Renders agent session transcript entries including content and tool_input.
 * Tool inputs longer than TOOL_INPUT_MAX_LENGTH are truncated with a disclosure button.
 */

import { useState } from "react";

import styles from "./SessionDetailView.module.css";

/** Maximum character length for tool_input before truncation. */
const TOOL_INPUT_MAX_LENGTH = 2000;

/** A single transcript entry from an agent session. */
export interface TranscriptEntry {
  /** Sequence number for ordering and keying. */
  seq: number;
  /** Role of the message sender. */
  role: "user" | "assistant" | "tool";
  /** Text content of the entry (assistant/user messages). */
  content?: string;
  /** Tool name for tool-use entries. */
  tool_name?: string;
  /** Raw tool input (can be very large — file contents, JSON payloads). */
  tool_input?: string;
  /** Tool output / result. */
  tool_output?: string;
  /** Timestamp. */
  timestamp?: string;
}

/** Props for SessionDetailView. */
export interface SessionDetailViewProps {
  /** Session ID. */
  sessionId: string;
  /** Transcript entries to render. */
  entries: TranscriptEntry[];
  /** Whether the session is still active. */
  isActive?: boolean;
}

/**
 * SessionDetailView renders a session transcript with truncated tool_input.
 */
export function SessionDetailView({
  sessionId,
  entries,
  isActive = false,
}: SessionDetailViewProps) {
  // Track which entries have their tool_input expanded
  const [expandedInputs, setExpandedInputs] = useState<Set<number>>(
    () => new Set(),
  );

  const toggleExpanded = (seq: number) => {
    setExpandedInputs((prev) => {
      const next = new Set(prev);
      if (next.has(seq)) {
        next.delete(seq);
      } else {
        next.add(seq);
      }
      return next;
    });
  };

  return (
    <div className={styles.sessionDetail} data-testid="session-detail-view">
      <div className={styles.sessionHeader}>
        <span className={styles.sessionId}>{sessionId}</span>
        {isActive && <span className={styles.activeBadge}>Active</span>}
      </div>
      <div className={styles.transcript}>
        {entries.map((entry) => (
          <div
            key={entry.seq}
            className={styles.entry}
            data-role={entry.role}
            data-testid={`transcript-entry-${entry.seq}`}
          >
            <div className={styles.entryHeader}>
              <span className={styles.role}>{entry.role}</span>
              {entry.tool_name && (
                <span className={styles.toolName}>{entry.tool_name}</span>
              )}
              {entry.timestamp && (
                <span className={styles.timestamp}>{entry.timestamp}</span>
              )}
            </div>

            {/* Content (assistant/user text) — never truncated */}
            {entry.content && (
              <div className={styles.content}>{entry.content}</div>
            )}

            {/* Tool input — truncated above TOOL_INPUT_MAX_LENGTH */}
            {!entry.content && entry.tool_input && (
              <div className={styles.toolInput}>
                {entry.tool_input.length <= TOOL_INPUT_MAX_LENGTH ||
                expandedInputs.has(entry.seq) ? (
                  <>
                    <pre>{entry.tool_input}</pre>
                    {entry.tool_input.length > TOOL_INPUT_MAX_LENGTH && (
                      <button
                        className={styles.toolInputToggle}
                        data-testid="show-less-input"
                        onClick={() => toggleExpanded(entry.seq)}
                      >
                        Show less
                      </button>
                    )}
                  </>
                ) : (
                  <>
                    <pre>
                      {entry.tool_input.slice(0, TOOL_INPUT_MAX_LENGTH)}...
                    </pre>
                    <button
                      className={styles.toolInputToggle}
                      data-testid="show-full-input"
                      onClick={() => toggleExpanded(entry.seq)}
                    >
                      Show full input
                    </button>
                  </>
                )}
              </div>
            )}

            {/* Tool output */}
            {entry.tool_output && (
              <div className={styles.toolOutput}>
                <pre>{entry.tool_output}</pre>
              </div>
            )}
          </div>
        ))}
      </div>
    </div>
  );
}
