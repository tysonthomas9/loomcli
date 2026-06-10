/**
 * WorkspaceTree component — v2 sidebar redesign.
 * Simplified layout (Aether V3): WorkspaceSelectorBar → AgentSection →
 * RunningSection → ReposSection (with the Add Repo entry at its bottom).
 */

import {
  useState,
  useCallback,
  useEffect,
} from "react";

import { useStore } from "zustand";
import { useWorkspaceContext, useAgentStoreInstance } from "@/hooks";
import { type ConnectionState } from "@/hooks/common";
import { wsGet, wsSet } from "@/utils/scopedStorage";
import { ONBOARDING_REPO_URL } from "@/utils/onboardingDefaults";
import { ErrorDisplay } from "@/components/ErrorDisplay";
import { AddRepoModal } from "@/components/AddRepoModal";

import { WorkspaceSelectorBar } from "./WorkspaceSelectorBar";
import { AgentSection } from "./AgentSection";
import { RunningSection } from "./RunningSection";
import { ReposSection } from "./ReposSection";
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
  /** Callback when the "+ Add agent" button is clicked */
  onAddClick?: () => void;
  /** Callback when "+ New Workspace" is clicked in the workspace switcher */
  onAddWorkspaceClick?: () => void;
  /** SSE connection state */
  connectionState?: ConnectionState;
  /** True when reconnection failed after max attempts */
  connectionLost?: boolean;
  /** Timestamp (ms) when disconnection started, null if connected */
  disconnectedSince?: number | null;
  /** Callback for retry button in daemon prompt */
  onRetryConnection?: () => void;
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
  onAddWorkspaceClick,
  connectionState,
  connectionLost,
  disconnectedSince,
  onRetryConnection,
  onTreeSelect,
}: WorkspaceTreeProps): JSX.Element {
  const workspaceContext = useWorkspaceContext();
  const {
    workspaceId,
    activeWorkspaceName,
    workspace,
    repos,
    isLoading,
    error,
    refetch,
  } = workspaceContext;
  const [addRepoOpen, setAddRepoOpen] = useState(false);

  // Load initial collapsed state from scoped localStorage
  const [isCollapsed, setIsCollapsed] = useState(() => {
    if (!workspaceId) return defaultCollapsed;
    const stored = wsGet(workspaceId, SK_COLLAPSED);
    return stored !== null ? stored === "true" : defaultCollapsed;
  });

  const workspaceConnection = workspaceContext as typeof workspaceContext & {
    connectionState?:
      | "loading"
      | "connected"
      | "error_never_connected"
      | "error_lost_connection";
    retryCountdown?: number | null;
    retryNow?: () => void;
  };
  const wsConnectionState =
    workspaceConnection.connectionState ??
    (error
      ? workspace
        ? "error_lost_connection"
        : "error_never_connected"
      : isLoading
        ? "loading"
        : "connected");
  const retryCountdown = workspaceConnection.retryCountdown ?? null;
  const retryNow = workspaceConnection.retryNow ?? refetch;

  const agentStore = useAgentStoreInstance();
  const agents = useStore(agentStore, (s) => s.agents);
  const workspaceRepos = repos ?? [];

  // One-click empty-workspace setup: seed the Add-Repo dialog with the sample
  // repo when the workspace has no repos yet.
  const onboardingRepoUrl =
    workspaceRepos.length === 0 && workspaceId && !isLoading && !error
      ? ONBOARDING_REPO_URL
      : "";

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
            onAddWorkspace={onAddWorkspaceClick}
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
          {isLoading && workspaceRepos.length === 0 && (
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

          {!isLoading && !error && workspaceRepos.length === 0 && (
            <div className={styles.emptyState}>No repos in workspace</div>
          )}

          <AddRepoModal
            isOpen={addRepoOpen}
            workspaceId={workspaceId ?? ""}
            initialUrl={onboardingRepoUrl}
            onClose={() => setAddRepoOpen(false)}
            onSuccess={() => {
              void refetch();
            }}
          />

          {/* Flat agent list with repo·branch metadata */}
          <AgentSection
            onAgentClick={onAgentClick}
            agentTasks={agentTasks}
            onAddClick={onAddClick}
          />

          {/* Running tasks grouped by epic — only shows when agents active */}
          <RunningSection onSelect={onTreeSelect} />

          {/* Repo inventory with the Add Repo entry at its bottom (Aether V3) */}
          <ReposSection
            repos={workspaceRepos}
            {...(workspaceId && { onAddRepo: () => setAddRepoOpen(true) })}
          />
        </div>
      )}

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
