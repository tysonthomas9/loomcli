/**
 * WorkspaceTree component — v2 sidebar redesign.
 * Simplified layout (Aether V3): WorkspaceSelectorBar → AgentSection →
 * RunningSection → ReposSection (with the Add Repo entry at its bottom).
 * Collapsing the tree swaps in the
 * vertical CollapsedAgentRail (wireframe pin 24).
 */

import { useState, useCallback, useEffect, type CSSProperties } from "react";

import {
  useWorkspaceContext,
  WORKSPACE_TREE_MAX_WIDTH,
  WORKSPACE_TREE_MIN_WIDTH,
  useWorkspaceTreeWidth,
} from "@/hooks";
import { type ConnectionState } from "@/hooks/common";
import type { ViewMode } from "@/types";
import { wsGet, wsSet } from "@/utils/scopedStorage";
import { ONBOARDING_REPO_URL } from "@/utils/onboardingDefaults";
import { ErrorDisplay } from "@/components/ErrorDisplay";
import { AddRepoModal } from "@/components/AddRepoModal";

import { WorkspaceSelectorBar } from "./WorkspaceSelectorBar";
import { AgentSection } from "./AgentSection";
import { TerminalSection } from "./TerminalSection";
import { RunningSection } from "./RunningSection";
import { ReposSection } from "./ReposSection";
import { CollapsedAgentRail } from "./CollapsedAgentRail";
import { SidebarResizeHandle } from "./SidebarResizeHandle";
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
  /** Agent name highlighted in the sidebar (from route or agent panel). */
  selectedAgentName?: string | null;
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
  /** Current main view — terminal view swaps the agent list for terminals. */
  activeView?: ViewMode;
}

// Scoped key suffix for workspace-specific collapse state
const SK_COLLAPSED = "tree-collapsed";

function ChevronLeftIcon(): JSX.Element {
  return (
    <svg
      className={styles.chevronIcon}
      width="16"
      height="16"
      viewBox="0 0 16 16"
      fill="none"
      aria-hidden="true"
    >
      <path
        d="M10 3L5 8L10 13"
        stroke="currentColor"
        strokeWidth="1.75"
        strokeLinecap="round"
        strokeLinejoin="round"
      />
    </svg>
  );
}

function ChevronRightIcon(): JSX.Element {
  return (
    <svg
      className={styles.chevronIcon}
      width="16"
      height="16"
      viewBox="0 0 16 16"
      fill="none"
      aria-hidden="true"
    >
      <path
        d="M6 3L11 8L6 13"
        stroke="currentColor"
        strokeWidth="1.75"
        strokeLinecap="round"
        strokeLinejoin="round"
      />
    </svg>
  );
}

export function WorkspaceTree({
  className,
  defaultCollapsed = false,
  onWorkspaceSwitch,
  onAgentClick,
  selectedAgentName = null,
  agentTasks,
  onAddClick,
  onAddWorkspaceClick,
  connectionState,
  connectionLost,
  disconnectedSince,
  onRetryConnection,
  onTreeSelect,
  activeView = "kanban",
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
  const [isResizing, setIsResizing] = useState(false);
  const {
    width: sidebarWidth,
    applyDelta,
    resetWidth,
  } = useWorkspaceTreeWidth(workspaceId);

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

  const workspaceRepos = repos ?? [];

  // One-click empty-workspace setup: seed the Add-Repo dialog with the sample
  // repo when the workspace has no repos yet.
  const onboardingRepoUrl =
    workspaceRepos.length === 0 && workspaceId && !isLoading && !error
      ? ONBOARDING_REPO_URL
      : "";

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

  // Keep maximized issue panels aligned to the right of the workspace tree.
  useEffect(() => {
    const root = document.documentElement;
    const width = isCollapsed ? "56px" : `${sidebarWidth}px`;
    root.style.setProperty("--workspace-tree-active-width", width);
    return () => {
      root.style.removeProperty("--workspace-tree-active-width");
    };
  }, [isCollapsed, sidebarWidth]);

  const handleToggle = useCallback(() => {
    setIsCollapsed((prev) => !prev);
  }, []);

  const workspaces = workspace?.workspaces ?? [];
  const showTerminalSidebar = activeView === "terminal";

  const isDisconnected =
    connectionState !== undefined &&
    connectionState !== "connected" &&
    connectionState !== "connecting" &&
    disconnectedSince != null;

  const rootClassName = [
    styles.sidebar,
    isCollapsed && styles.collapsed,
    isResizing && styles.resizing,
    className,
  ]
    .filter(Boolean)
    .join(" ");

  const sidebarStyle = isCollapsed
    ? undefined
    : ({
        "--workspace-tree-sidebar-width": `${sidebarWidth}px`,
      } as CSSProperties);

  return (
    <aside
      className={rootClassName}
      data-collapsed={isCollapsed}
      style={sidebarStyle}
    >
      {isCollapsed ? (
        <div className={styles.collapsedChrome}>
          {activeWorkspaceName ? (
            <WorkspaceSelectorBar
              variant="collapsed"
              workspaceName={activeWorkspaceName}
              workspaces={workspaces}
              activeWorkspaceId={workspaceId}
              onWorkspaceSwitch={onWorkspaceSwitch ?? (() => {})}
              onAddWorkspace={onAddWorkspaceClick}
            />
          ) : null}
          <button
            type="button"
            className={styles.expandButton}
            onClick={handleToggle}
            aria-expanded={false}
            title="Expand sidebar"
            aria-label="Expand workspace tree"
          >
            <ChevronRightIcon />
          </button>
          <div className={styles.railDivider} aria-hidden="true" />
        </div>
      ) : (
        <div className={styles.selectorRow}>
          {activeWorkspaceName ? (
            <WorkspaceSelectorBar
              workspaceName={activeWorkspaceName}
              workspaces={workspaces}
              activeWorkspaceId={workspaceId}
              onWorkspaceSwitch={onWorkspaceSwitch ?? (() => {})}
              onAddWorkspace={onAddWorkspaceClick}
            />
          ) : null}
          <button
            type="button"
            className={`${styles.toggleButton} ${styles.collapseButton}`}
            onClick={handleToggle}
            aria-expanded={true}
            title="Collapse sidebar"
            aria-label="Collapse workspace tree"
          >
            <ChevronLeftIcon />
          </button>
        </div>
      )}

      {isCollapsed ? (
        <CollapsedAgentRail
          onAgentClick={onAgentClick}
          selectedAgentName={selectedAgentName}
          onAddClick={onAddClick}
        />
      ) : null}

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

          {/* Agents in board views; terminal sessions in Terminal view */}
          {showTerminalSidebar ? (
            <TerminalSection />
          ) : (
            <AgentSection
              onAgentClick={onAgentClick}
              selectedAgentName={selectedAgentName}
              agentTasks={agentTasks}
              onAddClick={onAddClick}
            />
          )}

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

      {isCollapsed && isDisconnected ? (
        <div
          className={styles.collapsedConnectionBadge}
          data-disconnected="true"
          title={
            connectionLost
              ? "Connection lost"
              : connectionState === "reconnecting"
                ? "Reconnecting..."
                : "Disconnected"
          }
          aria-label="Connection issue"
        >
          !
        </div>
      ) : null}

      {isCollapsed &&
        (wsConnectionState === "error_never_connected" ||
          wsConnectionState === "error_lost_connection") && (
          <div className={styles.errorBadge} title="Workspace connection error">
            !
          </div>
        )}

      {!isCollapsed && (
        <SidebarResizeHandle
          width={sidebarWidth}
          onDelta={applyDelta}
          onReset={resetWidth}
          onDragStart={() => setIsResizing(true)}
          onDragEnd={() => setIsResizing(false)}
          minWidth={WORKSPACE_TREE_MIN_WIDTH}
          maxWidth={WORKSPACE_TREE_MAX_WIDTH}
        />
      )}
    </aside>
  );
}
