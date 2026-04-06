/**
 * SessionDetailView - Detail panel for a selected session.
 * Shows metadata summary and inner tabs for Transcript and Diff.
 */

import { useState } from "react";

import { CodeMirrorEditor } from "@/components/CodeMirrorEditor";
import { useSessionTranscript } from "@/hooks/useSessionTranscript";
import { useSessionDiff } from "@/hooks/useSessionDiff";
import type { SessionRecord } from "@/types/session";

import styles from "./SessionsTab.module.css";

export interface SessionDetailViewProps {
  taskId: string;
  session: SessionRecord;
}

type InnerTab = "transcript" | "diff";

/** Format exit code for display */
function formatExitCode(code: number): string {
  if (code === 0) return "0 (success)";
  return String(code);
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

  return (
    <div className={styles.detail} data-testid="session-detail-view">
      {/* Metadata summary */}
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

      {/* Files touched */}
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

      {/* Inner tab bar */}
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

      {/* Transcript tab content */}
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
            <div key={entry.seq} className={styles.transcriptEntry}>
              <div className={styles.transcriptRole} data-role={entry.role}>
                {entry.role}
              </div>
              {entry.type === "tool_use" && entry.tool_name && (
                <div className={styles.transcriptToolName}>
                  Tool: {entry.tool_name}
                </div>
              )}
              {entry.content && (
                <div className={styles.transcriptContent}>{entry.content}</div>
              )}
              {!entry.content && entry.tool_input && (
                <div className={styles.transcriptContent}>
                  {entry.tool_input.length <= 2000 ||
                  expandedInputs.has(entry.seq) ? (
                    <>
                      {entry.tool_input}
                      {expandedInputs.has(entry.seq) && (
                        <button
                          type="button"
                          className={styles.toolInputToggle}
                          data-testid="show-less-input"
                          onClick={() =>
                            setExpandedInputs((prev) => {
                              const next = new Set(prev);
                              next.delete(entry.seq);
                              return next;
                            })
                          }
                        >
                          Show less
                        </button>
                      )}
                    </>
                  ) : (
                    <>
                      {entry.tool_input.slice(0, 2000)}...
                      <button
                        type="button"
                        className={styles.toolInputToggle}
                        data-testid="show-full-input"
                        onClick={() =>
                          setExpandedInputs((prev) => {
                            const next = new Set(prev);
                            next.add(entry.seq);
                            return next;
                          })
                        }
                      >
                        Show full input
                      </button>
                    </>
                  )}
                </div>
              )}
            </div>
          ))}
        </div>
      )}

      {/* Diff tab content */}
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
