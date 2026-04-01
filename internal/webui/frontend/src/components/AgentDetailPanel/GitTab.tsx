/**
 * GitTab component for AgentDetailPanel.
 * Shows git action bar, branch header, commit log, working tree changes,
 * and conflict warnings.
 */

import { useState, useEffect } from "react";

import { fetchDiffCommits } from "@/api/diff";
import type { DiffCommit } from "@/api/diff";
import { useGitStatus } from "@/hooks/useGitStatus";
import { useGitActions } from "@/hooks/useGitActions";
import { useWorkspaceContext } from "@/hooks/useWorkspaceContext";
import type { LoomAgentStatus } from "@/types";
import { parseLoomStatus } from "@/types";

import panelStyles from "./AgentDetailPanel.module.css";
import { GitActionBar } from "./GitActionBar";
import { TargetBranchSelector } from "./TargetBranchSelector";
import styles from "./GitTab.module.css";

interface GitTabProps {
  agent: LoomAgentStatus;
  isActive?: boolean;
}

const INITIAL_COMMIT_LIMIT = 10;

/** Format an ISO date string into a relative time label. */
function relativeTime(iso: string): string {
  const now = Date.now();
  const then = new Date(iso).getTime();
  if (isNaN(then)) return "";
  const diffSec = Math.floor((now - then) / 1000);
  if (diffSec < 60) return "just now";
  const diffMin = Math.floor(diffSec / 60);
  if (diffMin < 60) return `${diffMin}m ago`;
  const diffHr = Math.floor(diffMin / 60);
  if (diffHr < 24) return `${diffHr}h ago`;
  const diffDay = Math.floor(diffHr / 24);
  return `${diffDay}d ago`;
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

  const actions = useGitActions({
    agentName: agent.name,
    onStatusChange: refetch,
  });

  const parsedStatus = parseLoomStatus(agent.status);

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
  }, [agent.name]);

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

  // Build change list — prefer gitStatus changed_files, fall back to agent.changes
  const agentChanges = agent.changes ?? [];
  const gitChangedFiles = gitStatus?.changed_files ?? [];

  return (
    <>
      {/* Git Action Bar */}
      <GitActionBar
        agentName={agent.name}
        gitStatus={gitStatus}
        agentStatus={parsedStatus}
        actions={actions}
      />

      {/* Branch Header */}
      <div className={panelStyles.section}>
        <h3 className={panelStyles.sectionTitle}>Branch</h3>
        <div className={styles.branchHeader}>
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

      {/* Conflict Warning */}
      {hasConflicts && conflictedFiles.length > 0 && (
        <div className={panelStyles.section}>
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

      {/* Commit Log */}
      <div className={panelStyles.section}>
        <h3 className={panelStyles.sectionTitle}>Commits</h3>
        {commits.length > 0 ? (
          <>
            <div className={panelStyles.commitList}>
              {visibleCommits.map((commit) => {
                // Handle both DiffCommit and LoomCommitDetail shapes
                const isDiff = "short_hash" in commit;
                const hash = isDiff
                  ? (commit as DiffCommit).short_hash
                  : commit.hash;
                const message = isDiff
                  ? (commit as DiffCommit).subject
                  : commit.message;
                const url = isDiff ? undefined : commit.url;
                const date = isDiff ? (commit as DiffCommit).date : undefined;

                return (
                  <div
                    key={isDiff ? (commit as DiffCommit).hash : commit.hash}
                    className={panelStyles.commitItem}
                  >
                    {url ? (
                      <a
                        href={url}
                        target="_blank"
                        rel="noopener noreferrer"
                        className={panelStyles.commitHashLink}
                      >
                        {hash}
                      </a>
                    ) : (
                      <span className={panelStyles.commitHash}>{hash}</span>
                    )}
                    <span className={panelStyles.commitMessage}>{message}</span>
                    {date && (
                      <span className={styles.commitTime}>
                        {relativeTime(date)}
                      </span>
                    )}
                  </div>
                );
              })}
            </div>
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
          <span className={panelStyles.emptyState}>
            In sync with {targetBranch}
          </span>
        ) : (
          <span className={panelStyles.emptyState}>No commit data</span>
        )}
      </div>

      {/* Working Tree Changes */}
      <div className={panelStyles.section}>
        <h3 className={panelStyles.sectionTitle}>Working Tree</h3>
        {agentChanges.length > 0 ? (
          <div className={panelStyles.changesList}>
            {agentChanges.map((change) => (
              <div key={change.path} className={panelStyles.changeItem}>
                <span
                  className={panelStyles.changeStatus}
                  data-status={change.status}
                >
                  {change.status === "M"
                    ? "M"
                    : change.status === "A"
                      ? "+"
                      : change.status === "D"
                        ? "-"
                        : change.status === "??"
                          ? "?"
                          : change.status}
                </span>
                <span className={panelStyles.changePath}>{change.path}</span>
              </div>
            ))}
          </div>
        ) : gitChangedFiles.length > 0 ? (
          // Fallback: git status returns flat file list without status type
          <div className={panelStyles.changesList}>
            {gitChangedFiles.map((file) => (
              <div key={file} className={panelStyles.changeItem}>
                <span className={panelStyles.changeStatus} data-status="M">
                  M
                </span>
                <span className={panelStyles.changePath}>{file}</span>
              </div>
            ))}
          </div>
        ) : (
          <span className={panelStyles.emptyState}>Clean working tree</span>
        )}
      </div>
    </>
  );
}
