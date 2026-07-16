/**
 * SessionTimelineRow - A single row in the run timeline.
 * Shows agent name, phase/outcome pills, duration · tokens · cost · files, and local time.
 */

import type { SessionRecord } from "@/types/agent";
import { sessionTotalTokens } from "@/utils/sessionUsage";

import styles from "./SessionsTab.module.css";

export interface SessionTimelineRowProps {
  session: SessionRecord;
  isSelected: boolean;
  onClick: () => void;
}

/** Format duration in seconds to "Xm Ys" */
function formatDuration(seconds: number | undefined): string {
  if (seconds == null || seconds <= 0) return "--";
  const m = Math.floor(seconds / 60);
  const s = Math.round(seconds % 60);
  if (m === 0) return `${s}s`;
  return `${m}m ${s}s`;
}

/** Format token count with K suffix for large numbers */
function formatTokens(count: number): string {
  if (count >= 10_000) return `${(count / 1000).toFixed(1)}K`;
  if (count >= 1_000) return `${(count / 1000).toFixed(1)}K`;
  return String(count);
}

/** Format USD cost */
function formatCost(usd: number): string {
  if (usd === 0) return "$0.00";
  if (usd < 0.01) return "<$0.01";
  return `$${usd.toFixed(2)}`;
}

function formatRunStatus(status: string): string {
  switch (status) {
    case "completed":
      return "Completed";
    case "failed":
      return "Failed";
    case "running":
      return "Running";
    case "aborted":
      return "Aborted";
    default:
      return status;
  }
}

function formatWhen(iso: string): string {
  const d = new Date(iso);
  if (isNaN(d.getTime())) return "";
  const hh = String(d.getHours()).padStart(2, "0");
  const mm = String(d.getMinutes()).padStart(2, "0");
  return `${hh}:${mm}`;
}

function runErrorSummary(session: SessionRecord): string | null {
  if (session.last_error) return session.last_error;
  if (session.error_class) return session.error_class;
  if (session.status === "failed") return "Failed";
  return null;
}

export function SessionTimelineRow({
  session,
  isSelected,
  onClick,
}: SessionTimelineRowProps): JSX.Element {
  const totalTokens = sessionTotalTokens(session);
  const errorSummary = runErrorSummary(session);
  const statusLabel = formatRunStatus(session.status);
  const when = formatWhen(session.started_at);
  const showFiles =
    session.files_changed > 0 ||
    session.lines_added > 0 ||
    session.lines_removed > 0;
  const showCost = (session.estimated_cost_usd ?? 0) > 0;

  return (
    <div
      className={`${styles.row} ${isSelected ? styles.selected : ""}`}
      onClick={onClick}
      role="button"
      tabIndex={0}
      onKeyDown={(e) => {
        if (e.key === "Enter" || e.key === " ") {
          e.preventDefault();
          onClick();
        }
      }}
      aria-label={`Run by ${session.agent_name}, ${statusLabel}${errorSummary ? `, ${errorSummary}` : ""}`}
      data-testid={`session-row-${session.session_id}`}
    >
      <div className={styles.rowMain}>
        <div className={styles.rowTop}>
          <span
            className={styles.statusDot}
            data-status={session.status}
            aria-label={session.status}
          />
          <span className={styles.agentName}>{session.agent_name}</span>
          {when && <span className={styles.rowWhen}>{when}</span>}
        </div>
        <div className={styles.rowPills}>
          {session.phase && (
            <span className={styles.phaseBadge} data-phase={session.phase}>
              {session.phase}
            </span>
          )}
          <span className={styles.statusPill} data-status={session.status}>
            {statusLabel}
          </span>
        </div>
        {errorSummary && (
          <div className={styles.errorSummary}>{errorSummary}</div>
        )}
        <div className={styles.rowBottom}>
          <span className={styles.duration}>
            {formatDuration(session.duration_s)}
          </span>
          <span aria-hidden="true">·</span>
          <span className={styles.tokens}>{formatTokens(totalTokens)}</span>
          {showCost && (
            <>
              <span aria-hidden="true">·</span>
              <span className={styles.cost}>
                {formatCost(session.estimated_cost_usd)}
              </span>
            </>
          )}
          {showFiles && (
            <>
              <span aria-hidden="true">·</span>
              <span className={styles.filesStat} data-testid="row-files-stat">
                {session.files_changed}
                {(session.lines_added > 0 || session.lines_removed > 0) && (
                  <span className={styles.filesDelta}>
                    {" "}
                    +{session.lines_added} −{session.lines_removed}
                  </span>
                )}
              </span>
            </>
          )}
        </div>
      </div>
    </div>
  );
}
