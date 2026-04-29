/**
 * WorkspaceTree component — v2 sidebar redesign.
 * Section order: WorkspaceSelectorBar → AgentSection → RunningSection →
 * ReposSection → OtherWorkspacesSection, with QueueStatsBar pinned at the bottom.
 */

import { useState, useCallback, useEffect } from "react";

import type { ConnectionState } from "@/api/common";
import { useWorkspaceContext } from "@/hooks";
import { wsGet, wsSet } from "@/utils/scopedStorage";
import { ErrorDisplay } from "@/components/ErrorDisplay";
import { updateRepoDefaultBranch } from "@/hooks/api";

import { WorkspaceSelectorBar } from "./WorkspaceSelectorBar";
import { AgentSection } from "./AgentSection";
import { RunningSection } from "./RunningSection";
import { ReposSection } from "./ReposSection";
import { OtherWorkspacesSection } from "./OtherWorkspacesSection";
import { QueueStatsBar, type WorkQueueCounts } from "./QueueStatsBar";
import styles from "./WorkspaceTree.module.css";

/**
 * Props for the WorkspaceTree component.
 */
export interface WorkspaceTreeProps {
  /** Additional CSS class name */
  className?: string;
  /** Default collapsed state */
  defaultCollapsed?: boolean;
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
}

// Scoped key suffix for workspace-specific collapse state
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
}: WorkspaceTreeProps): JSX.Element {
  const {
    workspaceId,
    activeWorkspaceName,
    workspace,
    repos,
    isLoading,
    error,
    connectionState: wsConnectionState,
    retryNow,
    refetch,
  } = useWorkspaceContext();

  // Workspace switch handler used by both the top selector and the other-workspaces list
  const handleWorkspaceSwitch = onWorkspaceSwitch ?? (() => {});

  const handleDefaultBranchChange = useCallback(
    (repoName: string, newBranch: string) => {
      if (!workspaceId) return;
      updateRepoDefaultBranch(workspaceId, repoName, newBranch)
        .then(() => refetch())
        .catch((err) => {
          console.error("Failed to update default branch", err);
          refetch();
        });
    },
    [workspaceId, refetch],
  );

  // Load initial collapsed state from scoped localStorage
  const [isCollapsed, setIsCollapsed] = useState(() => {
    if (!workspaceId) return defaultCollapsed;
    const stored = wsGet(workspaceId, SK_COLLAPSED);
    return stored !== null ? stored === "true" : defaultCollapsed;
  });

  // Re-read scoped state when workspace changes (SPA navigation)
  useEffect(() => {
    if (!workspaceId) return;
    const storedCollapsed = wsGet(workspaceId, SK_COLLAPSED);
    setIsCollapsed(
      storedCollapsed !== null ? storedCollapsed === "true" : defaultCollapsed,
    );
  }, [workspaceId, defaultCollapsed]);

  // Persist collapsed state to scoped storage
  useEffect(() => {
    if (workspaceId) wsSet(workspaceId, SK_COLLAPSED, String(isCollapsed));
  }, [isCollapsed, workspaceId]);

  const handleToggle = useCallback(() => {
    setIsCollapsed((prev) => !prev);
  }, []);

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
      {/* Top bar: workspace selector + collapse toggle */}
      <div className={styles.selectorRow}>
        {!isCollapsed && activeWorkspaceName && (
          <WorkspaceSelectorBar
            workspaceName={activeWorkspaceName}
            workspaces={workspaces}
            activeWorkspaceId={workspaceId}
            onWorkspaceSwitch={handleWorkspaceSwitch}
            onAddWorkspace={onAddClick}
          />
        )}
        <button
          type="button"
          className={styles.toggleButton}
          onClick={handleToggle}
          aria-expanded={!isCollapsed}
          aria-label={
            isCollapsed ? "Expand workspace tree" : "Collapse workspace tree"
          }
        >
          <span className={styles.toggleIcon}>{isCollapsed ? ">" : "<"}</span>
        </button>
      </div>

      {!isCollapsed && (
        <div className={styles.content}>
          {isLoading && repos.length === 0 && (
            <div className={styles.loading}>
              <div className={styles.skeletonRow} />
              <div className={styles.skeletonRow} />
              <div className={styles.skeletonRow} />
            </div>
          )}

          {wsConnectionState === "error_never_connected" && (
            <ErrorDisplay
              variant="connection-error"
              title="Workspace unavailable"
              description="Could not connect to workspace. The server may be starting up."
              onRetry={retryNow}
              isRetrying={isLoading}
              retryLabel="Retry now"
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
                Retry now
              </button>
            </div>
          )}

          {!isLoading && !error && repos.length === 0 && (
            <div className={styles.emptyState}>No repos in workspace</div>
          )}

          {/* Flat agent list with repo·branch metadata */}
          <AgentSection
            onAgentClick={onAgentClick}
            agentTasks={agentTasks}
            onAddClick={onAddClick}
          />

          {/* Running tasks grouped by epic — only shows when agents active */}
          <RunningSection onSelect={onTreeSelect} />

          {/* Static repo inventory */}
          <ReposSection
            repos={repos}
            workspaceId={workspaceId}
            onDefaultBranchChange={handleDefaultBranchChange}
          />

          {/* Non-active workspaces, drag-orderable */}
          <OtherWorkspacesSection
            workspaces={workspaces}
            activeWorkspaceName={activeWorkspaceName}
            onWorkspaceSwitch={handleWorkspaceSwitch}
            refetchWorkspaces={refetch}
          />
        </div>
      )}

      {/* Compact queue stats pinned at bottom */}
      {!isCollapsed && workQueueCounts && (
        <QueueStatsBar counts={workQueueCounts} />
      )}

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

      {isCollapsed && isDisconnected && (
        <div
          className={styles.collapsedBadge}
          data-disconnected={true}
          title={
            connectionLost
              ? "Connection lost"
              : connectionState === "reconnecting"
                ? "Reconnecting..."
                : "Disconnected"
          }
        >
          !
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
