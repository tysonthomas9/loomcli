/**
 * SessionDetailView — detail panel for a selected session.
 *
 * Renders the canonical transcript.Event stream through the shared
 * TranscriptView while preserving the task-scoped run hooks and tabs.
 */

import { useState } from "react";

import { CodeMirrorEditor } from "@/components/CodeMirrorEditor";
import { TranscriptView } from "@/components/TranscriptView";
import { useSessionTranscript, useSessionDiff } from "@/hooks/terminal";
import type { SessionRecord } from "@/types/agent";

import styles from "./SessionsTab.module.css";

export interface SessionDetailViewProps {
  taskId: string;
  session: SessionRecord;
}

type InnerTab = "transcript" | "diff";

// ─── Main component ───────────────────────────────────────────────────

export function SessionDetailView({
  taskId,
  session,
}: SessionDetailViewProps): JSX.Element {
  const [innerTab, setInnerTab] = useState<InnerTab>("transcript");

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
      <TranscriptView
        entries={entries}
        session={session}
        isLoading={transcriptLoading}
        error={transcriptError}
        showTranscript={innerTab === "transcript"}
        toolbar={
          <>
            {session.files_touched && session.files_touched.length > 0 && (
              <details className={styles.filesTouchedSection}>
                <summary className={styles.filesTouchedSummary}>
                  Files Touched ({session.files_touched.length})
                </summary>
                <ul className={styles.filesTouchedList}>
                  {session.files_touched.map((path) => (
                    <li
                      key={path}
                      className={styles.fileTouchedItem}
                      title={path}
                    >
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
          </>
        }
      />

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
