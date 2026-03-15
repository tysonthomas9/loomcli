/**
 * WorkspaceTree component displays a collapsible sidebar with repo navigation.
 * Shows all repos in the workspace with per-repo agent counts and status.
 */

import { useState, useCallback, useEffect, useMemo } from "react";

import { useWorkspaceRepos, useAgentContext } from "@/hooks";

import styles from "./WorkspaceTree.module.css";

/**
 * Props for the WorkspaceTree component.
 */
export interface WorkspaceTreeProps {
  /** Additional CSS class name */
  className?: string;
  /** Default collapsed state */
  defaultCollapsed?: boolean;
  /** Currently active repo name, or null/undefined for "All Workspaces" */
  activeRepoName?: string | null | undefined;
  /** Callback when a workspace/repo is selected. null = "All Workspaces" */
  onWorkspaceSelect?: (repoName: string | null) => void;
}

const COLLAPSE_STORAGE_KEY = "workspace-tree-collapsed";

/**
 * WorkspaceTree displays a collapsible sidebar with repo navigation.
 * Consumes useWorkspaceRepos for repo list and useAgents for agent counts.
 */
export function WorkspaceTree({
  className,
  defaultCollapsed = true,
  activeRepoName,
  onWorkspaceSelect,
}: WorkspaceTreeProps): JSX.Element {
  // Load initial collapsed state from localStorage
  const [isCollapsed, setIsCollapsed] = useState(() => {
    try {
      const stored = localStorage.getItem(COLLAPSE_STORAGE_KEY);
      return stored !== null ? stored === "true" : defaultCollapsed;
    } catch {
      return defaultCollapsed;
    }
  });

  const { repos, isLoading, error, refetch } = useWorkspaceRepos();
  const { agents } = useAgentContext();

  // Persist collapsed state
  useEffect(() => {
    try {
      localStorage.setItem(COLLAPSE_STORAGE_KEY, String(isCollapsed));
    } catch {
      // Ignore localStorage errors
    }
  }, [isCollapsed]);

  const handleToggle = useCallback(() => {
    setIsCollapsed((prev) => !prev);
  }, []);

  // Compute per-repo agent counts
  const repoAgentCounts = useMemo(() => {
    const counts = new Map<string, number>();
    for (const repo of repos) {
      counts.set(repo.name, 0);
    }
    for (const agent of agents) {
      if (agent.repo) {
        for (const repo of repos) {
          if (agent.repo === repo.name || agent.repo === repo.path) {
            counts.set(repo.name, (counts.get(repo.name) ?? 0) + 1);
            break;
          }
        }
      }
    }
    return counts;
  }, [repos, agents]);

  // Count total active agents across workspace repos
  const totalActiveCount = useMemo(() => {
    let count = 0;
    for (const [, v] of repoAgentCounts) {
      count += v;
    }
    return count;
  }, [repoAgentCounts]);

  const rootClassName = [
    styles.sidebar,
    isCollapsed && styles.collapsed,
    className,
  ]
    .filter(Boolean)
    .join(" ");

  return (
    <aside className={rootClassName} data-collapsed={isCollapsed}>
      <button
        type="button"
        className={styles.toggleButton}
        onClick={handleToggle}
        aria-expanded={!isCollapsed}
        aria-label={
          isCollapsed ? "Expand workspace tree" : "Collapse workspace tree"
        }
      >
        {!isCollapsed && (
          <>
            <span className={styles.toggleText}>Workspace</span>
            <span className={styles.sectionCount}>{repos.length}</span>
          </>
        )}
        <span className={styles.toggleIcon}>{isCollapsed ? ">" : "<"}</span>
      </button>

      {!isCollapsed && (
        <div className={styles.content}>
          {isLoading && repos.length === 0 && (
            <div className={styles.loading}>
              <div className={styles.skeletonRow} />
              <div className={styles.skeletonRow} />
              <div className={styles.skeletonRow} />
            </div>
          )}

          {error && (
            <div className={styles.errorState}>
              <span className={styles.errorText}>{error}</span>
              <button
                type="button"
                className={styles.retryButton}
                onClick={refetch}
              >
                Retry
              </button>
            </div>
          )}

          {!isLoading && !error && repos.length === 0 && (
            <div className={styles.emptyState}>No repos in workspace</div>
          )}

          {repos.length > 0 && (
            <div
              className={styles.repoList}
              role="radiogroup"
              aria-label="Workspace selection"
            >
              {/* All Workspaces option */}
              <button
                type="button"
                className={styles.repoItem}
                onClick={() => onWorkspaceSelect?.(null)}
                role="radio"
                aria-checked={
                  activeRepoName === null || activeRepoName === undefined
                }
              >
                <span
                  className={styles.radioIndicator}
                  data-active={
                    activeRepoName === null || activeRepoName === undefined
                  }
                />
                <svg
                  className={styles.allWorkspacesIcon}
                  viewBox="0 0 16 16"
                  width="14"
                  height="14"
                >
                  <rect
                    x="1"
                    y="4"
                    width="10"
                    height="8"
                    rx="1.5"
                    fill="none"
                    stroke="currentColor"
                    strokeWidth="1.3"
                  />
                  <rect
                    x="5"
                    y="1"
                    width="10"
                    height="8"
                    rx="1.5"
                    fill="none"
                    stroke="currentColor"
                    strokeWidth="1.3"
                  />
                </svg>
                <span className={styles.allWorkspacesLabel}>
                  All Workspaces
                </span>
                <span
                  className={styles.agentCount}
                  data-active={totalActiveCount > 0}
                >
                  {totalActiveCount}
                </span>
              </button>

              {repos.map((repo) => {
                const agentCount = repoAgentCounts.get(repo.name) ?? 0;
                const isActive = agentCount > 0;
                const isSelected = activeRepoName === repo.name;

                return (
                  <button
                    key={repo.name}
                    type="button"
                    className={styles.repoItem}
                    onClick={() => onWorkspaceSelect?.(repo.name)}
                    title={repo.path}
                    role="radio"
                    aria-checked={isSelected}
                  >
                    <span
                      className={styles.radioIndicator}
                      data-active={isSelected}
                    />
                    <span className={styles.repoIcon}>
                      <svg
                        width="16"
                        height="16"
                        viewBox="0 0 16 16"
                        fill="none"
                        xmlns="http://www.w3.org/2000/svg"
                      >
                        <path
                          d="M1.5 2.5C1.5 1.95 1.95 1.5 2.5 1.5H6.29L8.29 3.5H13.5C14.05 3.5 14.5 3.95 14.5 4.5V12.5C14.5 13.05 14.05 13.5 13.5 13.5H2.5C1.95 13.5 1.5 13.05 1.5 12.5V2.5Z"
                          fill="currentColor"
                        />
                      </svg>
                    </span>
                    <span className={styles.repoName}>{repo.name}</span>
                    <span className={styles.repoMeta}>
                      {isActive && (
                        <span
                          className={styles.agentCount}
                          data-active={isActive}
                        >
                          {agentCount}
                        </span>
                      )}
                      <span
                        className={styles.statusDot}
                        data-active={isActive}
                      />
                    </span>
                  </button>
                );
              })}
            </div>
          )}
        </div>
      )}

      {isCollapsed && totalActiveCount > 0 && (
        <div
          className={styles.collapsedBadge}
          title={`${totalActiveCount} active agent(s)`}
        >
          {totalActiveCount}
        </div>
      )}
    </aside>
  );
}
