import { useEffect, useMemo, useState } from "react";

import type { FileHistoryEntry } from "@/api/workspace";
import { historyScopedFile, readScopedFile } from "@/hooks/api";
import { useWorkspaceContext } from "@/hooks";
import { checkoutLabel, type CheckoutRef } from "@/utils/fileExplorerRefs";

import {
  buildHistoryTimeline,
  saveClusterRangeLabel,
  type HistoryTimelineNode,
} from "./historyTimeline";
import { buildUnifiedPatchFromContents } from "./gitGutter";
import styles from "./FileExplorer.module.css";

export interface HistorySubject {
  ref: CheckoutRef;
  path: string;
}

export interface HistoryOpenDiffRequest {
  ref: CheckoutRef;
  path: string;
  from?: string | undefined;
  to?: string | undefined;
  title?: string | undefined;
  patch?: string | undefined;
  restoreContent?: string | undefined;
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

function formatAbsoluteTime(value: string): string {
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return value;
  return date.toLocaleString();
}

function formatRelativeTime(value: string): string {
  const date = new Date(value);
  const time = date.getTime();
  if (Number.isNaN(time)) return value;
  const deltaSeconds = Math.round((time - Date.now()) / 1000);
  const absSeconds = Math.abs(deltaSeconds);
  const units: Array<[Intl.RelativeTimeFormatUnit, number]> = [
    ["year", 60 * 60 * 24 * 365],
    ["month", 60 * 60 * 24 * 30],
    ["day", 60 * 60 * 24],
    ["hour", 60 * 60],
    ["minute", 60],
  ];
  const formatter = new Intl.RelativeTimeFormat(undefined, {
    numeric: "auto",
  });
  for (const [unit, seconds] of units) {
    if (absSeconds >= seconds) {
      return formatter.format(Math.round(deltaSeconds / seconds), unit);
    }
  }
  return formatter.format(deltaSeconds, "second");
}

function formatEntryMeta(entry: FileHistoryEntry): string {
  const timeLabel = `${formatRelativeTime(entry.time)} (${formatAbsoluteTime(entry.time)})`;
  return entry.author ? `${entry.author} · ${timeLabel}` : timeLabel;
}

function entrySubject(entry: FileHistoryEntry): string {
  if (entry.summary.trim()) return entry.summary;
  if (entry.kind === "commit" && entry.sha) return entry.sha.slice(0, 8);
  return "Browser save";
}

function clusterLabel(entries: FileHistoryEntry[]): string {
  const count = entries.length;
  return `${count} saves · ${saveClusterRangeLabel(entries)}`;
}

function Chevron({ expanded }: { expanded: boolean }) {
  return (
    <span
      className={`${styles.chevron} ${expanded ? styles.chevronExpanded : ""}`}
      aria-hidden="true"
    >
      <svg viewBox="0 0 16 16">
        <path
          d="M6 4l4 4-4 4"
          fill="none"
          stroke="currentColor"
          strokeWidth="1.5"
        />
      </svg>
    </span>
  );
}

function HistoryDot({ kind }: { kind: "commit" | "save" }) {
  return (
    <span className={styles.historyDot} data-kind={kind} aria-hidden="true" />
  );
}

function SaveActions({
  entry,
  subject,
  onOpenDiff,
  onRestoreSnapshot,
  onActionError,
}: {
  entry: FileHistoryEntry;
  subject: HistorySubject;
  onOpenDiff: (request: HistoryOpenDiffRequest) => void;
  onRestoreSnapshot: (
    ref: CheckoutRef,
    path: string,
    content: string,
  ) => Promise<void>;
  onActionError: (message: string | null) => void;
}) {
  const { workspaceId } = useWorkspaceContext();
  const content = entry.content;

  const viewSave = async () => {
    if (content === undefined) {
      onActionError("Snapshot content is unavailable for this save.");
      return;
    }
    try {
      const current = await readScopedFile(
        workspaceId,
        subject.ref,
        subject.path,
      );
      if (current.binary || current.truncated) {
        onActionError(
          "Cannot diff this snapshot against a binary or truncated current file.",
        );
        return;
      }
      onActionError(null);
      onOpenDiff({
        ref: subject.ref,
        path: subject.path,
        title: `${checkoutLabel(subject.ref)} · Browser save`,
        patch: buildUnifiedPatchFromContents(
          subject.path,
          content,
          current.content ?? "",
        ),
        restoreContent: content,
        canOpenFile: true,
      });
    } catch (err) {
      onActionError(err instanceof Error ? err.message : String(err));
    }
  };

  return (
    <div className={styles.historyActions}>
      <button
        type="button"
        className={styles.historyAction}
        disabled={content === undefined}
        onClick={() => void viewSave()}
      >
        View
      </button>
      <button
        type="button"
        className={styles.historyAction}
        disabled={content === undefined}
        aria-label={`Restore save from ${formatAbsoluteTime(entry.time)}`}
        onClick={() => {
          if (content !== undefined) {
            void onRestoreSnapshot(subject.ref, subject.path, content);
          }
        }}
      >
        Restore
      </button>
    </div>
  );
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
  const sha = entry.sha ?? "";
  const shortSha = sha.slice(0, 8);
  return (
    <li className={styles.historyItem}>
      <HistoryDot kind="commit" />
      <div className={styles.historyEntryCard}>
        <div className={styles.historySummary}>{entrySubject(entry)}</div>
        <div className={styles.historyMeta}>{formatEntryMeta(entry)}</div>
        <div className={styles.historyActions}>
          <button
            type="button"
            className={styles.historyAction}
            disabled={!sha}
            onClick={() =>
              onOpenDiff({
                ref: subject.ref,
                path: subject.path,
                from: `${sha}^`,
                to: sha,
                title: `${checkoutLabel(subject.ref)} · ${entrySubject(entry)}`,
              })
            }
          >
            View diff
          </button>
          <button
            type="button"
            className={styles.historyAction}
            disabled={!sha}
            onClick={() =>
              onOpenRevision({
                ref: subject.ref,
                path: subject.path,
                rev: sha,
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

function SaveItem({
  entry,
  subject,
  onOpenDiff,
  onRestoreSnapshot,
  onActionError,
}: {
  entry: FileHistoryEntry;
  subject: HistorySubject;
  onOpenDiff: (request: HistoryOpenDiffRequest) => void;
  onRestoreSnapshot: (
    ref: CheckoutRef,
    path: string,
    content: string,
  ) => Promise<void>;
  onActionError: (message: string | null) => void;
}) {
  return (
    <li className={styles.historyItem}>
      <HistoryDot kind="save" />
      <div className={styles.historyEntryCard}>
        <div className={styles.historySummary}>Browser save</div>
        <div className={styles.historyMeta}>{formatEntryMeta(entry)}</div>
        <SaveActions
          entry={entry}
          subject={subject}
          onOpenDiff={onOpenDiff}
          onRestoreSnapshot={onRestoreSnapshot}
          onActionError={onActionError}
        />
      </div>
    </li>
  );
}

function SaveClusterItem({
  node,
  subject,
  expanded,
  onToggle,
  onOpenDiff,
  onRestoreSnapshot,
  onActionError,
}: {
  node: Extract<HistoryTimelineNode, { kind: "save-cluster" }>;
  subject: HistorySubject;
  expanded: boolean;
  onToggle: () => void;
  onOpenDiff: (request: HistoryOpenDiffRequest) => void;
  onRestoreSnapshot: (
    ref: CheckoutRef,
    path: string,
    content: string,
  ) => Promise<void>;
  onActionError: (message: string | null) => void;
}) {
  return (
    <li className={styles.historyItem}>
      <HistoryDot kind="save" />
      <div className={styles.historyEntryCard}>
        <button
          type="button"
          className={styles.historyClusterButton}
          aria-expanded={expanded}
          onClick={onToggle}
        >
          <Chevron expanded={expanded} />
          <span>{clusterLabel(node.entries)}</span>
        </button>
        {expanded && (
          <div className={styles.historySaveList}>
            {node.entries.map((entry) => (
              <div
                key={entry.id ?? entry.time}
                className={styles.historySaveRow}
              >
                <div className={styles.historySaveTime}>
                  {formatAbsoluteTime(entry.time)}
                </div>
                <SaveActions
                  entry={entry}
                  subject={subject}
                  onOpenDiff={onOpenDiff}
                  onRestoreSnapshot={onRestoreSnapshot}
                  onActionError={onActionError}
                />
              </div>
            ))}
          </div>
        )}
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
  onRestoreSnapshot,
}: {
  subject: HistorySubject | null;
  refreshKey: number;
  onClose: () => void;
  onOpenDiff: (request: HistoryOpenDiffRequest) => void;
  onOpenRevision: (request: HistoryOpenRevisionRequest) => void;
  onRestoreSnapshot: (
    ref: CheckoutRef,
    path: string,
    content: string,
  ) => Promise<void>;
}) {
  const { workspaceId } = useWorkspaceContext();
  const [entries, setEntries] = useState<FileHistoryEntry[]>([]);
  const [isLoading, setIsLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [actionError, setActionError] = useState<string | null>(null);
  const [expandedClusters, setExpandedClusters] = useState<Set<string>>(
    () => new Set(),
  );
  const subjectKey = subject
    ? `${checkoutLabel(subject.ref)}:${subject.path}`
    : "";
  const nodes = useMemo(() => buildHistoryTimeline(entries), [entries]);

  useEffect(() => {
    let canceled = false;
    setActionError(null);
    setExpandedClusters(new Set());
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

  const toggleCluster = (key: string) => {
    setExpandedClusters((prev) => {
      const next = new Set(prev);
      if (next.has(key)) next.delete(key);
      else next.add(key);
      return next;
    });
  };

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
          <div className={styles.changesEmpty}>No history for this file.</div>
        ) : (
          <>
            {actionError && (
              <div className={styles.historyError}>{actionError}</div>
            )}
            <ol className={styles.historyTimeline}>
              {nodes.map((node) => {
                if (node.kind === "commit") {
                  return (
                    <CommitItem
                      key={node.key}
                      entry={node.entry}
                      subject={subject}
                      onOpenDiff={onOpenDiff}
                      onOpenRevision={onOpenRevision}
                    />
                  );
                }
                if (node.kind === "save") {
                  return (
                    <SaveItem
                      key={node.key}
                      entry={node.entry}
                      subject={subject}
                      onOpenDiff={onOpenDiff}
                      onRestoreSnapshot={onRestoreSnapshot}
                      onActionError={setActionError}
                    />
                  );
                }
                return (
                  <SaveClusterItem
                    key={node.key}
                    node={node}
                    subject={subject}
                    expanded={expandedClusters.has(node.key)}
                    onToggle={() => toggleCluster(node.key)}
                    onOpenDiff={onOpenDiff}
                    onRestoreSnapshot={onRestoreSnapshot}
                    onActionError={setActionError}
                  />
                );
              })}
            </ol>
          </>
        )}
      </div>
    </aside>
  );
}
