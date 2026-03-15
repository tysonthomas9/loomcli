/**
 * AgentDetailPanel component.
 * Slide-out side panel that displays detailed information about a selected agent.
 * Follows the same slide-out pattern as IssueDetailPanel.
 */

import { useEffect, useRef, useCallback, useState } from "react";

import { ErrorDisplay } from "@/components";
import { useAgentTerminalLogs } from "@/hooks";
import type { LoomAgentStatus, LoomTaskInfo } from "@/types";
import { parseLoomStatus } from "@/types";

import { LogViewer } from "../LogViewer";
import { OpenInEditor } from "../OpenInEditor";
import { RepoBadge } from "../RepoBadge";
import styles from "./AgentDetailPanel.module.css";
import { GitTab } from "./GitTab";
import { useExpandedCommits } from "./useExpandedCommits";
import {
  getAvatarColor,
  shouldUseWhiteText,
  getStatusDotColor,
  getStatusLabel,
  getPriorityLabel,
} from "./utils";

/**
 * Props for the AgentDetailPanel component.
 */
export interface AgentDetailPanelProps {
  /** Whether the panel is open */
  isOpen: boolean;
  /** Name of the selected agent (null when closed) */
  agentName: string | null;
  /** All agents from useAgents */
  agents: LoomAgentStatus[];
  /** Agent tasks map from useAgents */
  agentTasks: Record<string, LoomTaskInfo>;
  /** Callback when panel should close */
  onClose: () => void;
  /** Callback when task link is clicked (opens IssueDetailPanel) */
  onTaskClick?: (taskId: string) => void;
}

/**
 * AgentDetailPanel displays detailed agent information in a slide-out panel.
 */
type TabType = "info" | "logs" | "git";

export function AgentDetailPanel({
  isOpen,
  agentName,
  agents,
  agentTasks,
  onClose,
  onTaskClick,
}: AgentDetailPanelProps): JSX.Element {
  const panelRef = useRef<HTMLElement>(null);
  const [activeTab, setActiveTab] = useState<TabType>("info");

  const {
    mode: logMode,
    chunks: logChunks,
    state: logConnectionState,
    error: logError,
    resetVersion: logResetVersion,
    refresh: refreshLogs,
    resize: resizeLogs,
    sendInput: sendLogInput,
    loadOlderLogs,
    hasMoreLines,
    isLoadingMore,
  } = useAgentTerminalLogs({
    agentName,
    enabled: isOpen && activeTab === "logs" && agentName !== null,
  });

  // Reset to info tab when agent changes
  useEffect(() => {
    setActiveTab("info");
  }, [agentName]);

  // Handle Escape key
  useEffect(() => {
    if (!isOpen) return;

    const handleKeyDown = (event: KeyboardEvent) => {
      if (event.key === "Escape") {
        onClose();
      }
    };

    document.addEventListener("keydown", handleKeyDown);
    return () => document.removeEventListener("keydown", handleKeyDown);
  }, [isOpen, onClose]);

  // Lock body scroll when open
  useEffect(() => {
    if (isOpen) {
      const previousOverflow = document.body.style.overflow;
      document.body.style.overflow = "hidden";
      return () => {
        document.body.style.overflow = previousOverflow;
      };
    }
  }, [isOpen]);

  // Focus management
  useEffect(() => {
    if (isOpen && panelRef.current) {
      const previouslyFocused = document.activeElement as HTMLElement | null;
      panelRef.current.focus();
      return () => {
        if (
          previouslyFocused &&
          document.contains(previouslyFocused) &&
          previouslyFocused.focus
        ) {
          previouslyFocused.focus();
        }
      };
    }
  }, [isOpen]);

  const handleTaskClick = useCallback(
    (taskId: string) => {
      onTaskClick?.(taskId);
    },
    [onTaskClick],
  );

  const {
    expandedCommits,
    loadingCommits,
    handleShowAll: handleShowAllCommits,
    handleShowLess,
  } = useExpandedCommits(agentName);

  // Find the agent from the array
  const agent = agentName
    ? agents.find((a) => a.name === agentName)
    : undefined;
  const parsed = agent ? parseLoomStatus(agent.status) : undefined;
  const task = agentName ? agentTasks[agentName] : undefined;
  const currentTaskId = parsed?.taskId;
  const isActive = parsed?.type === "working" || parsed?.type === "planning";

  const rootClassName = [styles.overlay, isOpen && styles.open]
    .filter(Boolean)
    .join(" ");

  return (
    <div
      className={rootClassName}
      onClick={onClose}
      data-testid="agent-detail-overlay"
      aria-hidden={!isOpen}
    >
      <aside
        ref={panelRef}
        className={styles.panel}
        onClick={(e) => e.stopPropagation()}
        role="dialog"
        aria-modal="true"
        aria-label={agent ? `Details for agent ${agent.name}` : "Agent details"}
        tabIndex={-1}
        data-testid="agent-detail-panel"
        data-state={isOpen ? "open" : "closed"}
      >
        {agent && parsed ? (
          <>
            <div className={styles.stickyTop}>
              {/* Sticky Header */}
              <div className={styles.stickyHeaderWrapper}>
                <div
                  className={styles.agentAvatar}
                  style={{
                    backgroundColor: getAvatarColor(agent.name),
                    color: shouldUseWhiteText(getAvatarColor(agent.name))
                      ? "#fff"
                      : "#1f2937",
                  }}
                >
                  {agent.name.charAt(0).toUpperCase()}
                </div>
                <div className={styles.headerInfo}>
                  <h2 className={styles.agentName}>{agent.name}</h2>
                  <div className={styles.statusRow}>
                    <span
                      className={styles.statusDot}
                      style={{
                        backgroundColor: getStatusDotColor(parsed.type),
                      }}
                      data-active={isActive}
                      aria-hidden="true"
                    />
                    <span className={styles.statusLabel}>
                      {getStatusLabel(parsed.type)}
                      {parsed.duration && ` (${parsed.duration})`}
                    </span>
                  </div>
                </div>
                <button
                  type="button"
                  className={styles.closeButton}
                  onClick={onClose}
                  aria-label="Close panel"
                >
                  <svg width="20" height="20" viewBox="0 0 16 16" fill="none">
                    <path
                      d="M4 4l8 8M12 4l-8 8"
                      stroke="currentColor"
                      strokeWidth="2"
                      strokeLinecap="round"
                    />
                  </svg>
                </button>
              </div>

              {/* Metadata Bar (hide when branch matches agent name to avoid duplicate label) */}
              {agent.branch && agent.branch !== agent.name && (
                <div className={styles.metadataBar}>
                  <span className={styles.metadataItem}>
                    <span className={styles.branchName}>{agent.branch}</span>
                  </span>
                </div>
              )}

              {/* Tab Bar */}
              <div
                className={styles.tabBar}
                role="tablist"
                aria-label="Agent detail tabs"
              >
                <button
                  type="button"
                  className={`${styles.tab} ${activeTab === "info" ? styles.activeTab : ""}`}
                  onClick={() => setActiveTab("info")}
                  aria-selected={activeTab === "info"}
                  role="tab"
                  id="agent-panel-tab-info"
                  aria-controls="agent-panel-tabpanel-info"
                >
                  Info
                </button>
                <button
                  type="button"
                  className={`${styles.tab} ${activeTab === "logs" ? styles.activeTab : ""}`}
                  onClick={() => setActiveTab("logs")}
                  aria-selected={activeTab === "logs"}
                  role="tab"
                  id="agent-panel-tab-logs"
                  aria-controls="agent-panel-tabpanel-logs"
                >
                  Logs
                </button>
                <button
                  type="button"
                  className={`${styles.tab} ${activeTab === "git" ? styles.activeTab : ""}`}
                  onClick={() => setActiveTab("git")}
                  aria-selected={activeTab === "git"}
                  role="tab"
                  id="agent-panel-tab-git"
                  aria-controls="agent-panel-tabpanel-git"
                >
                  Git
                </button>
              </div>
            </div>

            {/* Tab Content */}
            {activeTab === "info" ? (
              /* Info Tab - Scrollable Content */
              <div
                className={styles.scrollableContent}
                id="agent-panel-tabpanel-info"
                role="tabpanel"
                aria-labelledby="agent-panel-tab-info"
              >
                {/* Current Task Section */}
                <div className={styles.section}>
                  <h3 className={styles.sectionTitle}>Current Task</h3>
                  {task && currentTaskId ? (
                    <button
                      type="button"
                      className={styles.taskLink}
                      onClick={() => handleTaskClick(currentTaskId)}
                    >
                      <span className={styles.taskId}>{task.id}</span>
                      <div className={styles.taskInfo}>
                        <p className={styles.taskTitle}>{task.title}</p>
                        <div className={styles.taskMeta}>
                          <span
                            className={styles.priorityBadge}
                            data-priority={task.priority}
                          >
                            {getPriorityLabel(task.priority)}
                          </span>
                        </div>
                      </div>
                    </button>
                  ) : (
                    <span className={styles.emptyState}>No active task</span>
                  )}
                </div>

                {/* Commit Status Section */}
                <div className={styles.section}>
                  <h3 className={styles.sectionTitle}>Commit Status</h3>
                  <div className={styles.commitRow}>
                    {agent.ahead > 0 ? (
                      <span className={styles.commitBadge} data-type="ahead">
                        +{agent.ahead} ahead
                      </span>
                    ) : null}
                    {agent.behind > 0 ? (
                      <span className={styles.commitBadge} data-type="behind">
                        -{agent.behind} behind
                      </span>
                    ) : null}
                    {agent.ahead === 0 && agent.behind === 0 && (
                      <span className={styles.commitBadge} data-type="synced">
                        In sync
                      </span>
                    )}
                  </div>
                  {agent.commits && agent.commits.length > 0 && (
                    <div className={styles.commitList}>
                      {(expandedCommits ?? agent.commits).map((commit) => (
                        <div key={commit.hash} className={styles.commitItem}>
                          {commit.url ? (
                            <a
                              href={commit.url}
                              target="_blank"
                              rel="noopener noreferrer"
                              className={styles.commitHashLink}
                            >
                              {commit.hash}
                            </a>
                          ) : (
                            <span className={styles.commitHash}>
                              {commit.hash}
                            </span>
                          )}
                          <span className={styles.commitMessage}>
                            {commit.message}
                          </span>
                        </div>
                      ))}
                      {agent.commits &&
                        agent.commits.length < agent.ahead &&
                        !expandedCommits && (
                          <button
                            type="button"
                            className={styles.showAllCommitsBtn}
                            onClick={handleShowAllCommits}
                            disabled={loadingCommits}
                          >
                            {loadingCommits
                              ? "Loading..."
                              : `Show all ${agent.ahead} commits`}
                          </button>
                        )}
                      {expandedCommits && (
                        <button
                          type="button"
                          className={styles.showAllCommitsBtn}
                          onClick={handleShowLess}
                        >
                          Show less
                        </button>
                      )}
                    </div>
                  )}
                </div>

                {/* Changes Section */}
                <div className={styles.section}>
                  <h3 className={styles.sectionTitle}>Changes</h3>
                  {agent.changes && agent.changes.length > 0 ? (
                    <div className={styles.changesList}>
                      {agent.changes.map((change) => (
                        <div key={change.path} className={styles.changeItem}>
                          <span
                            className={styles.changeStatus}
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
                          <span className={styles.changePath}>
                            {change.path}
                          </span>
                        </div>
                      ))}
                    </div>
                  ) : (
                    <span className={styles.emptyState}>
                      Clean working tree
                    </span>
                  )}
                </div>

                {/* Agent Info Section */}
                <div className={styles.section}>
                  <h3 className={styles.sectionTitle}>Agent Info</h3>
                  <dl className={styles.infoGrid}>
                    {agent.path && (
                      <>
                        <dt>Path</dt>
                        <dd>{agent.path}</dd>
                      </>
                    )}
                    <dt>Branch</dt>
                    <dd>{agent.branch}</dd>
                    {agent.repo && (
                      <>
                        <dt>Repos</dt>
                        <dd className={styles.repoDetail}>
                          <RepoBadge repoName={agent.repo} />
                          {agent.cross_repo && (
                            <span className={styles.crossRepoLabel}>
                              All repos
                            </span>
                          )}
                        </dd>
                      </>
                    )}
                    <dt>Status</dt>
                    <dd>{agent.status}</dd>
                    {parsed.taskId && (
                      <>
                        <dt>Task ID</dt>
                        <dd>{parsed.taskId}</dd>
                      </>
                    )}
                    {parsed.duration && (
                      <>
                        <dt>Duration</dt>
                        <dd>{parsed.duration}</dd>
                      </>
                    )}
                  </dl>
                  {agent.worktree_path && (
                    <div className={styles.openInSection}>
                      <OpenInEditor path={agent.worktree_path} />
                    </div>
                  )}
                </div>
              </div>
            ) : activeTab === "logs" ? (
              /* Logs Tab */
              <div
                className={styles.logsContainer}
                id="agent-panel-tabpanel-logs"
                role="tabpanel"
                aria-labelledby="agent-panel-tab-logs"
              >
                <div className={styles.logsMetaRow}>
                  <span className={styles.logsModeBadge} data-mode={logMode}>
                    {logMode === "tmux"
                      ? "Live (tmux)"
                      : logMode === "archive"
                        ? "Archive snapshot"
                        : logMode === "loading"
                          ? "Loading logs..."
                          : "Idle"}
                  </span>
                  {logMode === "archive" && (
                    <button
                      type="button"
                      className={styles.logsRefreshButton}
                      onClick={refreshLogs}
                    >
                      Refresh
                    </button>
                  )}
                </div>
                <LogViewer
                  chunks={logChunks}
                  connectionState={logConnectionState}
                  error={logError}
                  height="100%"
                  autoScroll={logMode !== "tmux"}
                  resetVersion={logResetVersion}
                  mode={logMode === "tmux" ? "interactive" : "static"}
                  onTerminalResize={resizeLogs}
                  onScrollToTop={
                    logMode === "archive" ? loadOlderLogs : undefined
                  }
                  isLoadingMore={isLoadingMore}
                  hasMoreOlder={logMode === "archive" ? hasMoreLines : false}
                  {...(logMode === "tmux"
                    ? { onTerminalData: sendLogInput }
                    : {})}
                />
              </div>
            ) : (
              /* Git Tab */
              <div
                className={styles.scrollableContent}
                id="agent-panel-tabpanel-git"
                role="tabpanel"
                aria-labelledby="agent-panel-tab-git"
              >
                <GitTab agent={agent} isActive={activeTab === "git"} />
              </div>
            )}
          </>
        ) : agentName ? (
          /* Agent not found state */
          <ErrorDisplay
            variant="connection-error"
            title="Agent disconnected"
            description="This agent is no longer connected or could not be found."
          />
        ) : null}
      </aside>
    </div>
  );
}
