import { useEffect, useState } from "react";

import type { FileHistoryEntry } from "@/api/workspace";
import { historyScopedFile } from "@/hooks/api";
import { useWorkspaceContext } from "@/hooks";
import { checkoutLabel, type CheckoutRef } from "@/utils/fileExplorerRefs";

import styles from "./FileExplorer.module.css";

export interface HistorySubject {
  ref: CheckoutRef;
  path: string;
}

export interface HistoryOpenDiffRequest {
  ref: CheckoutRef;
  path: string;
  source?: "branch" | undefined;
  from?: string | undefined;
  to?: string | undefined;
  title?: string | undefined;
  patch?: string | undefined;
  canOpenFile?: boolean | undefined;
}

export interface HistoryOpenRevisionRequest {
  ref: CheckoutRef;
  path: string;
  rev: string;
  title: string;
}

function basename(path: string): string {
  return path.split("/").pop() || path;
}

function formatEntryMeta(entry: FileHistoryEntry): string {
  const date = new Date(entry.time);
  const time = Number.isNaN(date.getTime())
    ? entry.time
    : date.toLocaleString();
  return `${entry.author} · ${time}`;
}

function CommitItem({
  entry,
  subject,
  onOpenDiff,
  onOpenRevision,
}: {
  entry: FileHistoryEntry;
  subject: HistorySubject;
  onOpenDiff: (request: HistoryOpenDiffRequest) => void;
  onOpenRevision: (request: HistoryOpenRevisionRequest) => void;
}) {
  const shortSha = entry.sha.slice(0, 8);
  const summary = entry.summary.trim() || shortSha;
  return (
    <li className={styles.historyItem}>
      <span
        className={styles.historyDot}
        data-kind="commit"
        aria-hidden="true"
      />
      <div className={styles.historyEntryCard}>
        <div className={styles.historySummary}>{summary}</div>
        <div className={styles.historyMeta}>{formatEntryMeta(entry)}</div>
        <div className={styles.historyActions}>
          <button
            type="button"
            className={styles.historyAction}
            onClick={() =>
              onOpenDiff({
                ref: subject.ref,
                path: subject.path,
                from: `${entry.sha}^`,
                to: entry.sha,
                title: `${checkoutLabel(subject.ref)} · ${summary}`,
              })
            }
          >
            View diff
          </button>
          <button
            type="button"
            className={styles.historyAction}
            onClick={() =>
              onOpenRevision({
                ref: subject.ref,
                path: subject.path,
                rev: entry.sha,
                title: `${checkoutLabel(subject.ref)} · ${shortSha}`,
              })
            }
          >
            Open at commit
          </button>
        </div>
      </div>
    </li>
  );
}

export function FileHistoryPanel({
  subject,
  refreshKey,
  onClose,
  onOpenDiff,
  onOpenRevision,
}: {
  subject: HistorySubject | null;
  refreshKey: number;
  onClose: () => void;
  onOpenDiff: (request: HistoryOpenDiffRequest) => void;
  onOpenRevision: (request: HistoryOpenRevisionRequest) => void;
}) {
  const { workspaceId } = useWorkspaceContext();
  const [entries, setEntries] = useState<FileHistoryEntry[]>([]);
  const [isLoading, setIsLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const subjectKey = subject
    ? `${checkoutLabel(subject.ref)}:${subject.path}`
    : "";

  useEffect(() => {
    let canceled = false;
    if (!subject) {
      setEntries([]);
      setError(null);
      setIsLoading(false);
      return () => {
        canceled = true;
      };
    }
    setIsLoading(true);
    setError(null);
    historyScopedFile(workspaceId, subject.ref, subject.path)
      .then((history) => {
        if (!canceled) setEntries(history.entries);
      })
      .catch((err) => {
        if (!canceled) {
          setEntries([]);
          setError(err instanceof Error ? err.message : String(err));
        }
      })
      .finally(() => {
        if (!canceled) setIsLoading(false);
      });
    return () => {
      canceled = true;
    };
  }, [refreshKey, subject, subjectKey, workspaceId]);

  return (
    <aside className={styles.historyPanel} aria-label="File history">
      <div className={styles.historyHeader}>
        <div className={styles.historyHeaderTitle}>
          <span>History</span>
          {subject && (
            <code className={styles.historyHeaderPath}>
              {basename(subject.path)}
            </code>
          )}
        </div>
        <button
          type="button"
          className={styles.iconButton}
          aria-label="Close history"
          title="Close history"
          onClick={onClose}
        >
          <svg viewBox="0 0 16 16" aria-hidden="true">
            <path
              d="M4 4l8 8M12 4l-8 8"
              fill="none"
              stroke="currentColor"
              strokeWidth="1.5"
              strokeLinecap="round"
            />
          </svg>
        </button>
      </div>
      <div className={styles.historyBody}>
        {!subject ? (
          <div className={styles.changesEmpty}>No file selected.</div>
        ) : isLoading && entries.length === 0 ? (
          <div className={styles.changesLoading}>Loading history...</div>
        ) : error ? (
          <div className={styles.historyError}>{error}</div>
        ) : entries.length === 0 ? (
          <div className={styles.changesEmpty}>
            No commit history for this file.
          </div>
        ) : (
          <ol className={styles.historyTimeline}>
            {entries.map((entry) => (
              <CommitItem
                key={entry.sha}
                entry={entry}
                subject={subject}
                onOpenDiff={onOpenDiff}
                onOpenRevision={onOpenRevision}
              />
            ))}
          </ol>
        )}
      </div>
    </aside>
  );
}
