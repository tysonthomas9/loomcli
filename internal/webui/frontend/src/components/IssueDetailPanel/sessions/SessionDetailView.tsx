/**
 * SessionDetailView — detail panel for a selected session.
 *
 * Renders the canonical transcript.Event stream as an agent worklog.
 * Assistant responses are turns with inline collapsible tool calls; the first
 * real user request is retained as the prompt and later user messages render as
 * interjections. Known backend-injected context is filtered explicitly.
 */

import { useState } from "react";

import { CodeMirrorEditor } from "@/components/CodeMirrorEditor";
import {
  useAgentSessionDiff,
  useAgentSessionTranscript,
  useSessionTranscript,
  useSessionDiff,
} from "@/hooks/terminal";
import type { SessionRecord } from "@/types/agent";
import { formatStatusLabel } from "@/utils/issue";
import { formatTokens, sessionTotalTokens } from "@/utils/sessionUsage";

import styles from "./SessionsTab.module.css";
import { TranscriptWorklog } from "./TranscriptWorklog";
import type { SessionRowLabel } from "./SessionTimelineRow";

export interface SessionDetailViewProps {
  taskId?: string;
  agentName?: string;
  session: SessionRecord;
  contextLabel?: SessionRowLabel;
}

type InnerTab = "transcript" | "diff";

function formatExitCode(code: number): string {
  if (code === 0) return "0 (success)";
  return String(code);
}

// Cost and duration keep detail-panel precision here (sub-cent cost to 4dp,
// fractional seconds) rather than the run rail's rounded summary formatting.
function formatCost(usd: number): string {
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

export function SessionDetailView({
  taskId,
  agentName,
  session,
  contextLabel,
}: SessionDetailViewProps): JSX.Element {
  const [innerTab, setInnerTab] = useState<InnerTab>("transcript");

  const {
    entries,
    isLoading: transcriptLoading,
    error: transcriptError,
  } = useSessionTranscript(
    taskId ?? null,
    session.session_id,
    session.is_active,
  );
  const agentTranscript = useAgentSessionTranscript(
    agentName ?? null,
    session.session_id,
    session.is_active,
  );

  const {
    diff,
    isLoading: diffLoading,
    error: diffError,
  } = useSessionDiff(
    taskId ?? null,
    session.session_id,
    innerTab === "diff" && session.has_diff,
  );
  const agentDiff = useAgentSessionDiff(
    agentName ?? null,
    session.session_id,
    innerTab === "diff" && session.has_diff,
  );

  const transcriptState = agentName
    ? agentTranscript
    : { entries, isLoading: transcriptLoading, error: transcriptError };
  const diffState = agentName
    ? agentDiff
    : { diff, isLoading: diffLoading, error: diffError };

  const totalTokens = sessionTotalTokens(session);
  const runError = runErrorSummary(session);

  return (
    <div className={styles.detail} data-testid="session-detail-view">
      {/* ── Masthead ── */}
      <header className={styles.masthead}>
        <div className={styles.mastheadHeader}>
          <span className={styles.agentName}>
            {contextLabel?.primary ?? session.agent_name}
          </span>
          {contextLabel?.secondary && (
            <>
              <span className={styles.sep}>·</span>
              <span className={styles.agentBackend}>
                {contextLabel.secondary}
              </span>
            </>
          )}
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
          {transcriptState.isLoading &&
            transcriptState.entries.length === 0 && (
              <div className={styles.emptyState}>Loading transcript...</div>
            )}
          {transcriptState.error && (
            <div className={styles.errorText}>
              Failed to load transcript: {transcriptState.error.message}
            </div>
          )}
          {!transcriptState.isLoading &&
            !transcriptState.error &&
            transcriptState.entries.length === 0 && (
              <div className={styles.emptyState}>No transcript entries</div>
            )}

          {transcriptState.entries.length > 0 ? (
            <TranscriptWorklog entries={transcriptState.entries} />
          ) : null}
        </div>
      )}

      {innerTab === "diff" && (
        <div className={styles.diffContainer} data-testid="session-diff">
          {diffState.isLoading && (
            <div className={styles.emptyState}>Loading diff...</div>
          )}
          {diffState.error && (
            <div className={styles.errorText}>
              Failed to load diff: {diffState.error.message}
            </div>
          )}
          {!diffState.isLoading && !diffState.error && diffState.diff && (
            <div className={styles.diffCodeMirror}>
              <CodeMirrorEditor
                value={diffState.diff}
                language="diff"
                readOnly
                hideLineNumbers
              />
            </div>
          )}
          {!diffState.isLoading && !diffState.error && !diffState.diff && (
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
