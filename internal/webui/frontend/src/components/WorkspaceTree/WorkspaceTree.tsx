/**
 * WorkspaceTree component — v2 sidebar redesign.
 * Simplified layout: WorkspaceSelectorBar → AgentSection → RunningSection →
 * EpicTaskTree → ReposSection, with QueueStatsBar pinned at the bottom.
 */

import { useState, useCallback, useEffect, type FormEvent } from "react";

import type { ConnectionState } from "@/api/common";
import { useStore } from "zustand";
import { useWorkspaceContext, useAgentStoreInstance } from "@/hooks";
import { wsGet, wsSet } from "@/utils/scopedStorage";
import { ErrorDisplay } from "@/components/ErrorDisplay";
import { addWorkspaceRepos } from "@/hooks/api";

import { WorkspaceSelectorBar } from "./WorkspaceSelectorBar";
import { AgentSection } from "./AgentSection";
import { RunningSection } from "./RunningSection";
import { ReposSection } from "./ReposSection";
import { QueueStatsBar, type WorkQueueCounts } from "./QueueStatsBar";
import { SidebarStatusBar, WorkQueueSection } from "./nav";
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
    refetch,
  } = useWorkspaceContext();
  const [repoPathInput, setRepoPathInput] = useState("");
  const [isAddingRepo, setIsAddingRepo] = useState(false);
  const [addRepoError, setAddRepoError] = useState<string | null>(null);

  // Load initial collapsed state from scoped localStorage
  const [isCollapsed, setIsCollapsed] = useState(() => {
    if (!workspaceId) return defaultCollapsed;
    const stored = wsGet(workspaceId, SK_COLLAPSED);
    return stored !== null ? stored === "true" : defaultCollapsed;
  });

  const wsConnectionState = error
    ? workspace
      ? "error_lost_connection"
      : "error_never_connected"
    : isLoading
      ? "loading"
      : "connected";
  const retryCountdown = null;
  const retryNow = refetch;

  const agentStore = useAgentStoreInstance();
  const agents = useStore(agentStore, (s) => s.agents);
  const agentHealth =
    agents.some((agent) => agent.status.startsWith("working")) ||
    agents.some((agent) => agent.status.startsWith("review"))
      ? "yellow"
      : agents.length > 0
        ? "green"
        : "none";

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

  const handleAddRepo = useCallback(
    async (e: FormEvent<HTMLFormElement>) => {
      e.preventDefault();
      const repoPath = repoPathInput.trim();
      if (!workspaceId || !repoPath || isAddingRepo) return;

      setIsAddingRepo(true);
      setAddRepoError(null);
      try {
        await addWorkspaceRepos(workspaceId, { repos: [repoPath] });
        setRepoPathInput("");
        await refetch();
      } catch (err) {
        setAddRepoError(
          err instanceof Error ? err.message : "Failed to add repository",
        );
      } finally {
        setIsAddingRepo(false);
      }
    },
    [workspaceId, repoPathInput, isAddingRepo, refetch],
  );

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
            onWorkspaceSwitch={onWorkspaceSwitch ?? (() => {})}
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
          <div className={styles.workspaceHeading}>Workspaces</div>

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

          {!isLoading && !error && repos.length === 0 && (
            <div className={styles.emptyState}>No repos in workspace</div>
          )}

          {!isLoading && !error && workspaceId && (
            <form className={styles.addRepoForm} onSubmit={handleAddRepo}>
              <input
                className={styles.addRepoInput}
                type="text"
                value={repoPathInput}
                onChange={(e) => setRepoPathInput(e.target.value)}
                placeholder="/path/to/repo"
                aria-label="Repository path"
                disabled={isAddingRepo}
              />
              <button
                type="submit"
                className={styles.addRepoButton}
                disabled={isAddingRepo || repoPathInput.trim() === ""}
              >
                {isAddingRepo ? "Adding..." : "Add Repo"}
              </button>
              {addRepoError && (
                <div className={styles.addRepoError} role="alert">
                  {addRepoError}
                </div>
              )}
            </form>
          )}

          {/* Flat agent list with repo·branch metadata */}
          <AgentSection
            onAgentClick={onAgentClick}
            agentTasks={agentTasks}
            onAddClick={onAddClick}
          />

          {workQueueCounts && <WorkQueueSection counts={workQueueCounts} />}

          {/* Running tasks grouped by epic — only shows when agents active */}
          <RunningSection onSelect={onTreeSelect} />

          {/* Static repo inventory */}
          <ReposSection repos={repos} />
        </div>
      )}

      {/* Compact queue stats pinned at bottom */}
      {!isCollapsed && workQueueCounts && (
        <QueueStatsBar counts={workQueueCounts} />
      )}
      {!isCollapsed && <SidebarStatusBar agents={agents} />}

      {!isCollapsed && connectionLost && (
        <div className={styles.daemonPrompt} role="alert">
          <span className={styles.daemonPromptIcon}>&#9888;</span>
          <div className={styles.daemonPromptText}>
            <span className={styles.daemonPromptTitle}>Connection lost</span>
            <span className={styles.daemonPromptDesc}>
              Check that the workspace service is running.
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
          data-disconnected={isDisconnected || undefined}
          data-health={agentHealth}
          title={
            connectionLost
              ? "Connection lost"
              : connectionState === "reconnecting"
                ? "Reconnecting..."
                : isDisconnected
                  ? "Disconnected"
                  : `${agents.length} agent${agents.length === 1 ? "" : "s"}`
          }
        >
          {isDisconnected ? "!" : agents.length}
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
