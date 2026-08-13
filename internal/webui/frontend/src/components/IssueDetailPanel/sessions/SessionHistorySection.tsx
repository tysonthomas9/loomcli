import { useCallback, useEffect, useState } from "react";

import {
  listSessionHistory,
  getSessionScrollback,
  type SessionRecord,
} from "@/hooks/api";
import { useWorkspaceContext } from "@/hooks/workspace";

import styles from "./SessionHistorySection.module.css";

interface SessionHistorySectionProps {
  issueId: string;
  onJumpToSession?: (sessionName: string) => void;
}

function formatRelativeTime(dateString: string): string {
  const date = new Date(dateString);
  const now = Date.now();
  const diffMs = now - date.getTime();
  if (diffMs < 0) return "just now";

  const seconds = Math.floor(diffMs / 1000);
  if (seconds < 60) return `${seconds}s ago`;
  const minutes = Math.floor(seconds / 60);
  if (minutes < 60) return `${minutes}m ago`;
  const hours = Math.floor(minutes / 60);
  if (hours < 24) return `${hours}h ago`;
  const days = Math.floor(hours / 24);
  return `${days}d ago`;
}

function formatDuration(startStr: string, endStr?: string | null): string {
  const start = new Date(startStr).getTime();
  const end = endStr ? new Date(endStr).getTime() : Date.now();
  const diffMs = end - start;
  if (diffMs < 0) return "0s";

  const seconds = Math.floor(diffMs / 1000);
  if (seconds < 60) return `${seconds}s`;
  const minutes = Math.floor(seconds / 60);
  if (minutes < 60) return `${minutes}m`;
  const hours = Math.floor(minutes / 60);
  const remainingMinutes = minutes % 60;
  if (remainingMinutes === 0) return `${hours}h`;
  return `${hours}h ${remainingMinutes}m`;
}

function scrollbackEvidenceLabel(
  state: SessionRecord["scrollback_evidence_status"],
): string {
  switch (state) {
    case "pending":
      return "Scrollback pending";
    case "finalized":
      return "Scrollback ready";
    case "truncated":
      return "Scrollback ready (truncated)";
    case "capture_failed":
      return "Scrollback capture failed";
    case "content_unavailable":
      return "Scrollback unavailable";
    case "corrupt":
      return "Scrollback corrupt";
    default:
      return "Scrollback not captured";
  }
}

function canViewScrollback(record: SessionRecord): boolean {
  return (
    record.status === "completed" &&
    (record.scrollback_evidence_status === "finalized" ||
      record.scrollback_evidence_status === "truncated")
  );
}

export function SessionHistorySection({
  issueId,
  onJumpToSession,
}: SessionHistorySectionProps): JSX.Element {
  const { workspaceId } = useWorkspaceContext();
  const [records, setRecords] = useState<SessionRecord[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [viewingScrollback, setViewingScrollback] = useState<{
    recordId: string;
    sessionName: string;
  } | null>(null);
  const [scrollbackContent, setScrollbackContent] = useState<string>("");
  const [scrollbackLoading, setScrollbackLoading] = useState(false);

  useEffect(() => {
    let cancelled = false;
    setLoading(true);
    setError(null);

    listSessionHistory(workspaceId, issueId)
      .then((data) => {
        if (!cancelled) {
          setRecords(data);
          setLoading(false);
        }
      })
      .catch((err) => {
        if (!cancelled) {
          setError(err.message || "Failed to load session history");
          setLoading(false);
        }
      });

    return () => {
      cancelled = true;
    };
  }, [workspaceId, issueId]);

  const handleViewScrollback = useCallback(
    async (recordId: string, sessionName: string) => {
      setViewingScrollback({ recordId, sessionName });
      setScrollbackLoading(true);
      setScrollbackContent("");
      try {
        const result = await getSessionScrollback(
          workspaceId,
          issueId,
          recordId,
        );
        setScrollbackContent(result.content);
      } catch {
        setScrollbackContent("Failed to load scrollback content.");
      } finally {
        setScrollbackLoading(false);
      }
    },
    [workspaceId, issueId],
  );

  if (loading) {
    return (
      <div className={styles.sessionHistoryEmpty}>Loading sessions...</div>
    );
  }

  if (error) {
    return <div className={styles.sessionHistoryEmpty}>{error}</div>;
  }

  if (records.length === 0) {
    return (
      <div className={styles.sessionHistoryEmpty}>No terminal sessions yet</div>
    );
  }

  return (
    <div className={styles.sessionHistoryList}>
      {records.map((record) => (
        <div key={record.id} className={styles.sessionHistoryItem}>
          <div className={styles.sessionHistoryRow}>
            <span
              className={styles.sessionHistoryStatus}
              data-status={record.status}
            />
            <span className={styles.sessionHistoryBackend}>
              {record.backend}
            </span>
            <span className={styles.sessionHistoryTime}>
              {formatRelativeTime(record.started_at)}
            </span>
            <span className={styles.sessionHistoryDuration}>
              {formatDuration(record.started_at, record.ended_at)}
            </span>
            <span
              className={styles.sessionHistoryEvidence}
              data-evidence-state={record.scrollback_evidence_status}
              data-testid={`scrollback-evidence-status-${record.id}`}
            >
              {scrollbackEvidenceLabel(record.scrollback_evidence_status)}
            </span>
          </div>
          <div className={styles.sessionHistoryActions}>
            {record.status === "active" && onJumpToSession && (
              <button
                type="button"
                className={styles.sessionHistoryAction}
                onClick={() => onJumpToSession(record.session_name)}
              >
                Jump to tab
              </button>
            )}
            {canViewScrollback(record) && (
              <button
                type="button"
                className={styles.sessionHistoryAction}
                onClick={() =>
                  handleViewScrollback(record.id, record.session_name)
                }
              >
                View scrollback
              </button>
            )}
          </div>
        </div>
      ))}

      {viewingScrollback && (
        <div className={styles.scrollbackOverlay}>
          <div className={styles.scrollbackPanel}>
            <div className={styles.scrollbackHeader}>
              <span>Scrollback: {viewingScrollback.sessionName}</span>
              <button
                type="button"
                className={styles.scrollbackClose}
                onClick={() => setViewingScrollback(null)}
              >
                Close
              </button>
            </div>
            <pre className={styles.scrollbackContent}>
              {scrollbackLoading
                ? "Loading..."
                : scrollbackContent || "No content"}
            </pre>
          </div>
        </div>
      )}
    </div>
  );
}
