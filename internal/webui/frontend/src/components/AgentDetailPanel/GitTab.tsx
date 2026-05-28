/**
 * GitTab component for AgentDetailPanel.
 * Shows git action bar, branch header, commit log, working tree changes,
 * and conflict warnings.
 */

import { useState, useEffect } from "react";

import { fetchDiffCommits } from "@/hooks/api";
import type { DiffCommit } from "@/api/issues";
import {
  useGitStatus,
  useGitActions,
  useWorkspaceContext,
} from "@/hooks/workspace";
import type { LoomAgentStatus } from "@/types";
import { parseLoomStatus } from "@/types";
import { getAvatarColor } from "@/utils/colorUtils";

import panelStyles from "./AgentDetailPanel.module.css";
import { useCreatePRAction } from "./CreatePRAction";
import { TargetBranchSelector } from "./TargetBranchSelector";
import styles from "./GitTab.module.css";

interface GitTabProps {
  agent: LoomAgentStatus;
  isActive?: boolean;
}

const INITIAL_COMMIT_LIMIT = 10;

/** Format an ISO date string for commit metadata (e.g. "May 19, 2026 07:13"). */
function formatCommitDate(iso: string): string {
  const d = new Date(iso);
  if (isNaN(d.getTime())) return "";
  const datePart = new Intl.DateTimeFormat("en-US", {
    month: "short",
    day: "numeric",
    year: "numeric",
  }).format(d);
  const timePart = new Intl.DateTimeFormat("en-US", {
    hour: "2-digit",
    minute: "2-digit",
    hour12: false,
  }).format(d);
  return `${datePart} ${timePart}`;
}

type CommitEntry = DiffCommit | NonNullable<LoomAgentStatus["commits"]>[number];

interface DisplayCommit {
  key: string;
  shortHash: string;
  message: string;
  author: string;
  dateLabel: string;
  url?: string;
  dotColor: string;
  isUnpushed: boolean;
}

function toDisplayCommit(
  commit: CommitEntry,
  agentName: string,
  githubBaseUrl?: string,
): DisplayCommit {
  const isDiff = "short_hash" in commit;
  const shortHash = isDiff
    ? (commit as DiffCommit).short_hash
    : commit.hash.slice(0, 7);
  const fullHash = isDiff ? (commit as DiffCommit).hash : commit.hash;
  const message = isDiff ? (commit as DiffCommit).subject : commit.message;
  const author = isDiff
    ? (commit as DiffCommit).author || agentName
    : agentName;
  const date = isDiff ? (commit as DiffCommit).date : undefined;
  const isUnpushed = isDiff && (commit as DiffCommit).pushed === false;
  const url = isUnpushed
    ? undefined
    : isDiff
      ? githubBaseUrl && fullHash
        ? `${githubBaseUrl}/commit/${fullHash}`
        : undefined
      : commit.url;

  const display: DisplayCommit = {
    key: fullHash,
    shortHash,
    message,
    author,
    dateLabel: date ? formatCommitDate(date) : "",
    dotColor: getAvatarColor(author || shortHash),
    isUnpushed,
  };
  if (url) {
    display.url = url;
  }
  return display;
}

function GitBranchIcon(): JSX.Element {
  return (
    <svg
      width="16"
      height="16"
      viewBox="0 0 16 16"
      fill="none"
      aria-hidden="true"
    >
      <circle cx="4" cy="4" r="2" fill="currentColor" />
      <circle cx="4" cy="12" r="2" fill="currentColor" />
      <circle cx="12" cy="8" r="2" fill="currentColor" />
      <path
        d="M4 6v4M4 4c0 2.2 3.6 2.2 8 0"
        stroke="currentColor"
        strokeWidth="1.5"
        strokeLinecap="round"
      />
    </svg>
  );
}

export function GitTab({ agent, isActive }: GitTabProps): JSX.Element {
  const { workspaceId } = useWorkspaceContext();
  const {
    status: gitStatus,
    error: gitError,
    refetch,
  } = useGitStatus({
    agentName: agent.name,
    enabled: isActive ?? true,
  });

  const parsedStatus = parseLoomStatus(agent.status);

  const actions = useGitActions({
    agentName: agent.name,
    taskId: parsedStatus.taskId || agent.task_id || null,
    onStatusChange: refetch,
  });

  // Derive GitHub base URL from agent's monitor commit URLs (e.g., ".../commit/abc123" → "...")
  const githubBaseUrl = (() => {
    const url = agent.commits?.[0]?.url;
    if (!url) return undefined;
    const idx = url.lastIndexOf("/commit/");
    return idx > 0 ? url.slice(0, idx) : undefined;
  })();

  // Rich commit data from diff endpoint
  const [diffCommits, setDiffCommits] = useState<DiffCommit[] | null>(null);
  const [showAllCommits, setShowAllCommits] = useState(false);

  // Fetch rich commit data when agent changes
  useEffect(() => {
    let cancelled = false;
    setDiffCommits(null);
    setShowAllCommits(false);

    fetchDiffCommits(workspaceId, agent.name)
      .then((commits) => {
        if (!cancelled) setDiffCommits(commits);
      })
      .catch(() => {
        // Fall back to agent.commits
      });

    return () => {
      cancelled = true;
    };
  }, [agent.name, workspaceId]);

  // Determine data sources — prefer gitStatus, fall back to agent data
  const branch = gitStatus?.branch ?? agent.branch;
  const targetBranch = gitStatus?.target_branch ?? "main";
  const ahead = gitStatus?.ahead ?? agent.ahead;
  const behind = gitStatus?.behind ?? agent.behind;
  const hasConflicts = gitStatus?.has_conflicts ?? false;
  const conflictedFiles = gitStatus?.conflicted_files ?? [];
  const usingFallback = gitError !== null && gitStatus === null;

  // Build commit list — prefer DiffCommit for richer data
  const commits = diffCommits ?? agent.commits ?? [];
  const visibleCommits = showAllCommits
    ? commits
    : commits.slice(0, INITIAL_COMMIT_LIMIT);
  const hasMoreCommits = commits.length > INITIAL_COMMIT_LIMIT;
  const displayCommits = visibleCommits.map((commit) =>
    toDisplayCommit(commit, agent.name, githubBaseUrl),
  );

  const createPR = useCreatePRAction({
    targetBranch,
    ahead,
    agentStatus: parsedStatus,
    actions,
  });

  return (
    <div className={styles.gitTab}>
      {hasConflicts && conflictedFiles.length > 0 && (
        <div className={styles.conflictSection}>
          <div className={styles.conflictBanner}>
            <p className={styles.conflictTitle}>Merge conflicts detected</p>
            {conflictedFiles.map((file) => (
              <div key={file} className={styles.conflictFile}>
                {file}
              </div>
            ))}
          </div>
        </div>
      )}

      <div className={styles.historyCard}>
        <header className={styles.historyHeader}>
          <span className={styles.historyIcon}>
            <GitBranchIcon />
          </span>
          <div className={styles.historyHeaderMain}>
            <h3 className={styles.historyTitle}>Git history</h3>
            <div className={styles.branchLine}>
              <span className={styles.branchName}>{branch}</span>
              <span className={styles.branchArrow}>&rarr;</span>
              <TargetBranchSelector
                currentTarget={targetBranch}
                isWorkspace={false}
                onUpdate={actions.updateTarget}
                loading={actions.targetState.isLoading}
              />
            </div>
            <div className={styles.badgeRow}>
              {ahead > 0 && (
                <span className={panelStyles.commitBadge} data-type="ahead">
                  +{ahead} ahead
                </span>
              )}
              {behind > 0 && (
                <span className={panelStyles.commitBadge} data-type="behind">
                  -{behind} behind
                </span>
              )}
              {ahead === 0 && behind === 0 && (
                <span className={panelStyles.commitBadge} data-type="synced">
                  In sync
                </span>
              )}
            </div>
            {usingFallback && (
              <p className={styles.fallbackNote}>
                Git status unavailable — showing cached data.
              </p>
            )}
          </div>
          <div className={styles.historyHeaderAction}>{createPR.button}</div>
        </header>

        {createPR.form}

        <div className={styles.historyBody}>
          {commits.length > 0 ? (
            <>
              <ul className={styles.historyList}>
                {displayCommits.map((commit) => (
                  <li key={commit.key} className={styles.historyRow}>
                    <span
                      className={styles.commitDot}
                      style={{ backgroundColor: commit.dotColor }}
                      aria-hidden="true"
                    />
                    <div className={styles.commitBody}>
                      <span className={styles.commitSubject}>
                        {commit.message}
                      </span>
                      {(commit.author || commit.dateLabel) && (
                        <span className={styles.commitMeta}>
                          {commit.author}
                          {commit.author && commit.dateLabel && (
                            <span className={styles.metaSep}> · </span>
                          )}
                          {commit.dateLabel}
                        </span>
                      )}
                    </div>
                    <span
                      className={styles.hashPill}
                      title={
                        commit.isUnpushed
                          ? "Not yet pushed to remote"
                          : undefined
                      }
                    >
                      {commit.url ? (
                        <a
                          href={commit.url}
                          target="_blank"
                          rel="noopener noreferrer"
                        >
                          {commit.shortHash}
                        </a>
                      ) : (
                        commit.shortHash
                      )}
                    </span>
                  </li>
                ))}
              </ul>
              {hasMoreCommits && !showAllCommits && (
                <button
                  type="button"
                  className={styles.commitExpandBtn}
                  onClick={() => setShowAllCommits(true)}
                >
                  Show all {commits.length} commits
                </button>
              )}
            </>
          ) : ahead === 0 ? (
            <p className={styles.emptyState}>In sync with {targetBranch}</p>
          ) : (
            <p className={styles.emptyState}>No commit data</p>
          )}
        </div>
      </div>
    </div>
  );
}
