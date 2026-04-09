/**
 * WorkspaceTree is the sidebar orchestrator.
 * Renders sections in order: workspace selector, agents, running tasks,
 * repos, queue stats, other workspaces, and the status bar.
 */

import { useState, useCallback, useEffect, useMemo } from "react";

import type { ConnectionState } from "@/api/sse";
import {
  useWorkspaceRepos,
  useAgentContext,
  useWorkspaceContext,
} from "@/hooks";
import { wsGet, wsSet } from "@/utils/scopedStorage";
import {
  computeRepoHealth,
  worstHealthColor,
} from "@/utils/workspaceHealth";
import { ErrorDisplay } from "@/components/ErrorDisplay";

import { AgentSection } from "./AgentSection";
import { QueueStatsBar } from "./QueueStatsBar";
import type { WorkQueueCounts } from "./QueueStatsBar";
import { ReposSection } from "./ReposSection";
import { RunningSection } from "./RunningSection";
import { SidebarStatusBar } from "./SidebarStatusBar";
import { WorkspaceSelectorBar } from "./WorkspaceSelectorBar";
import styles from "./WorkspaceTree.module.css";

/**
 * Props for the WorkspaceTree component.
 */
export interface WorkspaceTreeProps {
  /** Additional CSS class name */
  className?: string;
  /** Default collapsed state */
  defaultCollapsed?: boolean;
  /** Currently active repo name (unused in new layout, kept for API compat) */
  activeRepoName?: string | null | undefined;
  /** Callback when a workspace/repo is selected. null = "All Workspaces" */
  onWorkspaceSelect?: (repoName: string | null) => void;
  /** Callback when a workspace entry is clicked to switch to it */
  onWorkspaceSwitch?: (workspaceName: string) => void;
  /** Callback when an agent card is clicked */
  onAgentClick?: (agentName: string) => void;
  /** Map of agent name to task info for display in AgentCards */
  agentTasks?: Record<string, { title: string }>;
  /** Callback when the "+" button is clicked */
  onAddClick?: () => void;
  /** SSE connection state */
  connectionState?: ConnectionState;
  /** True when reconnection failed after max attempts */
  connectionLost?: boolean;
  /** Timestamp (ms) when disconnection started, null if connected */
  disconnectedSince?: number | null;
  /** Callback for retry button in daemon prompt */
  onRetryConnection?: () => void;
  /** Work Queue counts derived from workspace-scoped issues */
  workQueueCounts?: WorkQueueCounts;
  /** Callback when a task is selected in the tree */
  onTreeSelect?: (issueId: string) => void;
  /** Callback when a task with an active agent is clicked for terminal */
  onTaskTerminalOpen?: (issueId: string, agentName: string) => void;
}

// Scoped key suffix for sidebar collapsed state
const SK_COLLAPSED = "tree-collapsed";

export function WorkspaceTree({
  className,
  defaultCollapsed = false,
  onWorkspaceSwitch,
  onAgentClick,
  agentTasks,
  onAddClick,
  connectionState,
  connectionLost,
  disconnectedSince,
  onRetryConnection,
  workQueueCounts,
  onTreeSelect,
  onTaskTerminalOpen,
}: WorkspaceTreeProps): JSX.Element {
  const { workspaceId, activeWorkspaceName } = useWorkspaceContext();
  const {
    workspace,
    repos,
    isLoading,
    error,
    refetch: _refetch,
    connectionState: wsConnectionState,
    retryCountdown,
    retryNow,
  } = useWorkspaceRepos();
  const { agents } = useAgentContext();

  // Sidebar collapsed state persisted to scoped localStorage
  const [isCollapsed, setIsCollapsed] = useState(() => {
    if (!workspaceId) return defaultCollapsed;
    const stored = wsGet(workspaceId, SK_COLLAPSED);
    return stored !== null ? stored === "true" : defaultCollapsed;
  });

  // Re-read scoped state when workspace changes
  useEffect(() => {
    if (!workspaceId) return;
    const storedCollapsed = wsGet(workspaceId, SK_COLLAPSED);
    setIsCollapsed(
      storedCollapsed !== null ? storedCollapsed === "true" : defaultCollapsed,
    );
  }, [workspaceId, defaultCollapsed]);

  // Persist collapsed state
  useEffect(() => {
    if (workspaceId) wsSet(workspaceId, SK_COLLAPSED, String(isCollapsed));
  }, [isCollapsed, workspaceId]);

  const handleToggle = useCallback(() => {
    setIsCollapsed((prev) => !prev);
  }, []);

  // Health totals for collapsed badge
  const { totalActiveCount, worstHealth } = useMemo(() => {
    const colors: Array<"green" | "yellow" | "red"> = [];
    for (const repo of repos) {
      const repoAgents = agents.filter((a) => a.repo === repo.name);
      const health = computeRepoHealth(repoAgents);
      colors.push(health.healthColor);
    }
    // Include unassigned agents
    const unassigned = agents.filter(
      (a) => !a.repo || !repos.some((r) => r.name === a.repo),
    );
    if (unassigned.length > 0) {
      colors.push(computeRepoHealth(unassigned).healthColor);
    }
    const totalCount = agents.length;
    return {
      totalActiveCount: totalCount,
      worstHealth: worstHealthColor(colors),
    };
  }, [repos, agents]);

  const workspaces = workspace?.workspaces ?? [];

  const isDisconnected =
    connectionState !== undefined &&
    connectionState !== "connected" &&
    connectionState !== "connecting" &&
    disconnectedSince != null;

  const rootClassName = [
    styles.sidebar,
    isCollapsed && styles.collapsed,
    className,
  ]
    .filter(Boolean)
    .join(" ");

  return (
    <aside className={rootClassName} data-collapsed={isCollapsed}>
      {/* Collapse toggle — shown when collapsed */}
      {isCollapsed && (
        <button
          type="button"
          className={styles.toggleButton}
          onClick={handleToggle}
          aria-expanded={false}
          aria-label="Expand workspace tree"
        >
          <span className={styles.toggleIcon}>&gt;</span>
        </button>
      )}

      {!isCollapsed && (
        <div className={styles.content}>
          {/* Workspace selector at top with collapse button */}
          <div className={styles.selectorRow}>
            <WorkspaceSelectorBar
            workspaceName={activeWorkspaceName ?? workspace?.name ?? ""}
            workspaces={workspaces}
            activeWorkspaceId={workspaceId}
            onWorkspaceSwitch={onWorkspaceSwitch ?? (() => {})}
            onAddWorkspace={onAddClick}
          />
            <button
              type="button"
              className={styles.toggleButton}
              onClick={handleToggle}
              aria-expanded={true}
              aria-label="Collapse workspace tree"
            >
              <span className={styles.toggleIcon}>&lt;</span>
            </button>
          </div>

          {/* Connection error states */}
          {wsConnectionState === "error_never_connected" && (
            <ErrorDisplay
              variant="connection-error"
              title="Workspace unavailable"
              description="Could not connect to workspace. The server may be starting up."
              onRetry={retryNow}
              isRetrying={isLoading}
              retryLabel={
                retryCountdown != null
                  ? `Retry in ${retryCountdown}s`
                  : "Retry now"
              }
            />
          )}

          {wsConnectionState === "error_lost_connection" && (
            <div className={styles.staleBanner}>
              <span>Connection lost — showing last known state</span>
              <button
                type="button"
                onClick={retryNow}
                className={styles.retryButton}
              >
                {retryCountdown != null
                  ? `Retry in ${retryCountdown}s`
                  : "Retry now"}
              </button>
            </div>
          )}

          {/* Loading skeleton */}
          {isLoading && repos.length === 0 && agents.length === 0 && (
            <div className={styles.loading}>
              <div className={styles.skeletonRow} />
              <div className={styles.skeletonRow} />
              <div className={styles.skeletonRow} />
            </div>
          )}

          {/* Empty state */}
          {!isLoading &&
            !error &&
            repos.length === 0 &&
            agents.length === 0 && (
              <div className={styles.emptyState}>No repos in workspace</div>
            )}

          {/* Agents — flat list */}
          <AgentSection
            onAgentClick={onAgentClick}
            agentTasks={agentTasks}
            onAddClick={onAddClick}
          />

          {/* Running — conditional, only when in-progress tasks exist */}
          <RunningSection
            onSelect={onTreeSelect}
            onTaskTerminalOpen={onTaskTerminalOpen}
          />

          {/* Repos — static inventory */}
          <ReposSection repos={repos} />
        </div>
      )}

      {/* Stats pinned to bottom when expanded */}
      {!isCollapsed && workQueueCounts && <QueueStatsBar counts={workQueueCounts} />}
      {!isCollapsed && <SidebarStatusBar agents={agents} />}

      {/* Daemon disconnection prompt */}
      {!isCollapsed && connectionLost && (
        <div className={styles.daemonPrompt} role="alert">
          <span className={styles.daemonPromptIcon}>&#9888;</span>
          <div className={styles.daemonPromptText}>
            <span className={styles.daemonPromptTitle}>Connection lost</span>
            <span className={styles.daemonPromptDesc}>
              Check that the daemon is running.
            </span>
          </div>
          {onRetryConnection && (
            <button
              type="button"
              className={styles.retryButton}
              onClick={onRetryConnection}
            >
              Retry
            </button>
          )}
        </div>
      )}

      {/* Collapsed badge */}
      {isCollapsed && (totalActiveCount > 0 || isDisconnected) && (
        <div
          className={styles.collapsedBadge}
          data-disconnected={isDisconnected}
          data-health={worstHealth}
          title={
            connectionLost
              ? "Connection lost"
              : connectionState === "reconnecting"
                ? "Reconnecting..."
                : isDisconnected
                  ? "Disconnected"
                  : `${totalActiveCount} agent(s)`
          }
        >
          {isDisconnected ? "!" : totalActiveCount}
        </div>
      )}

      {isCollapsed &&
        (wsConnectionState === "error_never_connected" ||
          wsConnectionState === "error_lost_connection") && (
          <div className={styles.errorBadge} title="Workspace connection error">
            !
          </div>
        )}
    </aside>
  );
}
