/**
 * AgentsSidebar component displays a collapsible sidebar with agent status.
 */
import type { ReactNode } from "react";
import { useState, useCallback, useEffect } from "react";

import { ErrorDisplay, LoadingSkeleton } from "@/components";
import { useStore } from "zustand";

import {
  useAgentStoreInstance,
  useRepoFilter,
  useWorkspaceContext,
} from "@/hooks";
import { wsGet, wsSet } from "@/utils/scopedStorage";
import type {
  LoomAgentStatus,
  LoomTaskInfo,
  WorktreeSyncDetail,
} from "@/types";
import { isAgentActive } from "@/types";

import { TaskDrawer } from "../TaskDrawer";
import type { TaskCategory } from "../TaskDrawer";
import styles from "./AgentsSidebar.module.css";
import { RepoGroupedList } from "./RepoGroupedList";
import { WorkspaceGroupedList } from "./WorkspaceGroupedList";

function buildSyncTooltip(
  details: WorktreeSyncDetail[] | undefined,
  direction: string,
): string {
  if (!details || details.length === 0) return "";
  return details
    .map(
      (d) =>
        `${d.name}: ${d.count} commit${d.count !== 1 ? "s" : ""} ${direction}`,
    )
    .join("\n");
}

export type AgentTaskMap = Record<string, LoomTaskInfo>;

/**
 * Group agents by repo affinity when repo filters are active.
 * Agents matching selected repos go into named groups; the rest go into "other".
 */
export function groupAgentsByRepo(
  agents: LoomAgentStatus[],
  selectedRepos: string[],
): { grouped: Map<string, LoomAgentStatus[]>; other: LoomAgentStatus[] } {
  const grouped = new Map<string, LoomAgentStatus[]>();
  const other: LoomAgentStatus[] = [];

  for (const repo of selectedRepos) {
    grouped.set(repo, []);
  }

  for (const agent of agents) {
    if (agent.repo && grouped.has(agent.repo)) {
      grouped.get(agent.repo)!.push(agent);
    } else {
      other.push(agent);
    }
  }

  return { grouped, other };
}

export interface AgentsSidebarProps {
  className?: string;
  defaultCollapsed?: boolean;
  collapsible?: boolean;
  onAgentClick?: (agentName: string) => void;
  viewSwitcher?: ReactNode;
}

// Scoped key suffixes
const SK_COLLAPSED = "agents-sidebar-collapsed";
const SK_WORK_QUEUE = "agents-sidebar-work-queue-expanded";

/**
 * AgentsSidebar displays a collapsible panel with agent status cards.
 */
export function AgentsSidebar({
  className,
  defaultCollapsed = false,
  collapsible = true,
  onAgentClick,
  viewSwitcher,
}: AgentsSidebarProps): JSX.Element {
  const { workspaceId } = useWorkspaceContext();

  // Load initial collapsed state from scoped localStorage
  const [isCollapsed, setIsCollapsed] = useState(() => {
    if (!workspaceId) return defaultCollapsed;
    const stored = wsGet(workspaceId, SK_COLLAPSED);
    return stored !== null ? stored === "true" : defaultCollapsed;
  });

  // Load work queue expanded state from scoped localStorage
  const [isWorkQueueExpanded, setIsWorkQueueExpanded] = useState(() => {
    if (!workspaceId) return true;
    const stored = wsGet(workspaceId, SK_WORK_QUEUE);
    return stored !== null ? stored === "true" : true;
  });

  // Re-read scoped state when workspace changes (SPA navigation)
  useEffect(() => {
    if (!workspaceId) return;
    const storedCollapsed = wsGet(workspaceId, SK_COLLAPSED);
    setIsCollapsed(
      storedCollapsed !== null ? storedCollapsed === "true" : defaultCollapsed,
    );
    const storedWQ = wsGet(workspaceId, SK_WORK_QUEUE);
    setIsWorkQueueExpanded(storedWQ !== null ? storedWQ === "true" : true);
  }, [workspaceId, defaultCollapsed]);

  // Track which category drawer is open
  const [selectedCategory, setSelectedCategory] = useState<TaskCategory | null>(
    null,
  );

  // Publication details list. Publication itself is explicit CLI/PR work.
  const [isPushListExpanded, setIsPushListExpanded] = useState(false);

  const agentStore = useAgentStoreInstance();
  const agents = useStore(agentStore, (s) => s.agents);
  const tasks = useStore(agentStore, (s) => s.tasks);
  const taskLists = useStore(agentStore, (s) => s.taskLists);
  const agentTasks = useStore(agentStore, (s) => s.agentTasks);
  const sync = useStore(agentStore, (s) => s.sync);
  const stats = useStore(agentStore, (s) => s.stats);
  const isLoading = useStore(agentStore, (s) => s.isLoading);
  const isConnected = useStore(agentStore, (s) => s.isConnected);
  const wasEverConnected = useStore(agentStore, (s) => s.wasEverConnected);
  const lastUpdated = useStore(agentStore, (s) =>
    s.lastUpdated !== null ? new Date(s.lastUpdated) : null,
  );

  const [selectedRepos] = useRepoFilter();
  const isGrouped = selectedRepos.length > 0;

  // Persist collapsed state to scoped storage
  useEffect(() => {
    if (workspaceId) wsSet(workspaceId, SK_COLLAPSED, String(isCollapsed));
  }, [isCollapsed, workspaceId]);

  // Persist work queue expanded state to scoped storage
  useEffect(() => {
    if (workspaceId)
      wsSet(workspaceId, SK_WORK_QUEUE, String(isWorkQueueExpanded));
  }, [isWorkQueueExpanded, workspaceId]);

  // Auto-collapse at <1024px viewport width
  useEffect(() => {
    if (typeof window.matchMedia !== "function") return;
    const mql = window.matchMedia("(max-width: 1024px)");
    if (mql.matches) setIsCollapsed(true);
    const onChange = (e: MediaQueryListEvent) => {
      if (e.matches) setIsCollapsed(true);
    };
    mql.addEventListener("change", onChange);
    return () => mql.removeEventListener("change", onChange);
  }, []);

  // Auto-collapse publication details when no agents are ahead.
  useEffect(() => {
    if (sync.git_needs_push === 0) {
      setIsPushListExpanded(false);
    }
  }, [sync.git_needs_push]);

  const handleToggle = useCallback(() => {
    if (!collapsible) return;
    setIsCollapsed((prev) => !prev);
  }, [collapsible]);

  const handleWorkQueueToggle = useCallback(() => {
    setIsWorkQueueExpanded((prev) => !prev);
  }, []);

  const handleCategoryClick = useCallback((category: TaskCategory) => {
    setSelectedCategory(category);
  }, []);

  const handleDrawerClose = useCallback(() => {
    setSelectedCategory(null);
  }, []);

  // Get tasks and title for the selected category
  const getDrawerData = useCallback((): {
    title: string;
    tasks: LoomTaskInfo[];
  } => {
    switch (selectedCategory) {
      case "plan":
        return { title: "Backlog", tasks: taskLists.needsPlanning };
      case "impl":
        return { title: "Open", tasks: taskLists.readyToImplement };
      case "review":
        return { title: "Needs Review", tasks: taskLists.needsReview };
      case "inProgress":
        return { title: "In Progress", tasks: taskLists.inProgress };
      case "blocked":
        return { title: "Blocked", tasks: taskLists.backlog };
      case "done":
        return { title: "Done", tasks: taskLists.done };
      default:
        return { title: "", tasks: [] };
    }
  }, [selectedCategory, taskLists]);

  const drawerData = getDrawerData();

  const collapsed = collapsible ? isCollapsed : false;

  const rootClassName = [
    styles.sidebar,
    collapsed && styles.collapsed,
    className,
  ]
    .filter(Boolean)
    .join(" ");

  // Count active agents (working or planning) via the shared isAgentActive helper,
  // which parses the same effective status the AgentCard badge renders (preferring
  // fleet-db's live_status). Sharing the predicate keeps the count and the badges in
  // lockstep — including a bare "working" with no task id, which earlier slipped the
  // colon-prefix check here while still showing a "Working" badge.
  const activeCount = agents.filter(isAgentActive).length;

  // Check if there are sync warnings for the footer (push count moved to banner)
  const hasSyncWarning = sync.git_needs_pull > 0 || !sync.db_synced;

  return (
    <aside className={rootClassName} data-collapsed={isCollapsed}>
      {!collapsed && viewSwitcher && (
        <div className={styles.viewSwitcherSlot}>{viewSwitcher}</div>
      )}
      {collapsible ? (
        <button
          type="button"
          className={styles.toggleButton}
          onClick={handleToggle}
          aria-expanded={!isCollapsed}
          aria-label={
            isCollapsed ? "Expand agents sidebar" : "Collapse agents sidebar"
          }
        >
          {!collapsed && (
            <>
              <span className={styles.toggleText}>Agents</span>
              <span className={styles.sectionCount}>{agents.length}</span>
            </>
          )}
          <span className={styles.toggleIcon}>{collapsed ? ">" : "<"}</span>
        </button>
      ) : (
        <div className={styles.headerRow}>
          <span className={styles.toggleText}>Agents</span>
          <span className={styles.sectionCount}>{agents.length}</span>
        </div>
      )}

      {!collapsed && (
        <div className={styles.content}>
          {!isConnected && !isLoading && (
            <ErrorDisplay
              variant="connection-error"
              title="Loom server not available"
              description="Unable to connect to the agent server."
              className={styles.sidebarError ?? ""}
            />
          )}

          {isLoading && agents.length === 0 && !wasEverConnected && (
            <div className={styles.loadingCards}>
              <LoadingSkeleton.Card />
              <LoadingSkeleton.Card />
              <LoadingSkeleton.Card />
            </div>
          )}

          {agents.length === 0 && isConnected && !isLoading && (
            <div className={styles.emptyGuide}>
              <p className={styles.emptyHeadline}>No agents configured</p>
              <p className={styles.emptyHint}>
                Add agents in your loom config to start automating work
              </p>
            </div>
          )}

          {agents.length > 0 && (
            <div className={styles.agentList}>
              {isGrouped ? (
                <RepoGroupedList
                  agents={agents}
                  selectedRepos={selectedRepos}
                  agentTasks={agentTasks}
                  {...(onAgentClick !== undefined && {
                    onAgentClick,
                  })}
                />
              ) : (
                <WorkspaceGroupedList
                  agents={agents}
                  agentTasks={agentTasks}
                  {...(onAgentClick !== undefined && {
                    onAgentClick,
                  })}
                />
              )}
            </div>
          )}

          {/* Work Queue Section */}
          {isConnected && (
            <div className={styles.workQueue}>
              <div
                className={styles.workQueueHeader}
                onClick={handleWorkQueueToggle}
                role="button"
                tabIndex={0}
                onKeyDown={(e) => e.key === "Enter" && handleWorkQueueToggle()}
              >
                <span className={styles.workQueueToggle}>
                  {isWorkQueueExpanded ? "v" : ">"}
                </span>
                <span>Work Queue</span>
              </div>
              {isWorkQueueExpanded && (
                <div className={styles.workQueueContent}>
                  {sync.git_needs_push > 0 && (
                    <>
                      <div className={styles.syncBanner}>
                        {sync.git_push_details &&
                        sync.git_push_details.length > 0 ? (
                          <button
                            type="button"
                            className={styles.syncBannerText}
                            onClick={() =>
                              setIsPushListExpanded((prev) => !prev)
                            }
                            aria-expanded={isPushListExpanded}
                            aria-label={`${isPushListExpanded ? "Collapse" : "Expand"} push details`}
                            title={buildSyncTooltip(
                              sync.git_push_details,
                              "ahead",
                            )}
                          >
                            <span className={styles.syncBannerToggle}>
                              {isPushListExpanded ? "v" : ">"}
                            </span>
                            {sync.git_needs_push} need publication
                          </button>
                        ) : (
                          <span
                            className={styles.syncBannerTextStatic}
                            title={buildSyncTooltip(
                              sync.git_push_details,
                              "ahead",
                            )}
                          >
                            {sync.git_needs_push} need publication
                          </span>
                        )}
                      </div>
                      {isPushListExpanded &&
                        sync.git_push_details &&
                        sync.git_push_details.length > 0 && (
                          <div className={styles.pushDetailsList}>
                            {sync.git_push_details.map((detail) => (
                              <div
                                key={detail.name}
                                className={styles.pushDetailRow}
                              >
                                <span className={styles.pushDetailName}>
                                  {detail.name}
                                </span>
                                <span className={styles.pushDetailCount}>
                                  {detail.count} commit
                                  {detail.count !== 1 ? "s" : ""}
                                </span>
                              </div>
                            ))}
                          </div>
                        )}
                    </>
                  )}
                  <div className={styles.queueGrid}>
                    <button
                      type="button"
                      className={styles.queueItem}
                      onClick={() => handleCategoryClick("plan")}
                      disabled={tasks.needs_planning === 0}
                    >
                      <span className={styles.queueLabel}>Backlog</span>
                      <span
                        className={styles.queueCount}
                        data-highlight={tasks.needs_planning > 0}
                      >
                        {tasks.needs_planning}
                      </span>
                    </button>
                    <button
                      type="button"
                      className={styles.queueItem}
                      onClick={() => handleCategoryClick("impl")}
                      disabled={tasks.ready_to_implement === 0}
                    >
                      <span className={styles.queueLabel}>Open</span>
                      <span
                        className={styles.queueCount}
                        data-highlight={tasks.ready_to_implement > 0}
                      >
                        {tasks.ready_to_implement}
                      </span>
                    </button>
                    <button
                      type="button"
                      className={styles.queueItem}
                      onClick={() => handleCategoryClick("blocked")}
                      disabled={tasks.backlog === 0}
                    >
                      <span className={styles.queueLabel}>Blocked</span>
                      <span className={styles.queueCount}>{tasks.backlog}</span>
                    </button>
                    <button
                      type="button"
                      className={styles.queueItem}
                      onClick={() => handleCategoryClick("inProgress")}
                      disabled={tasks.in_progress === 0}
                    >
                      <span className={styles.queueLabel}>In Progress</span>
                      <span
                        className={styles.queueCount}
                        data-highlight={tasks.in_progress > 0}
                      >
                        {tasks.in_progress}
                      </span>
                    </button>
                    <button
                      type="button"
                      className={styles.queueItem}
                      onClick={() => handleCategoryClick("review")}
                      disabled={tasks.need_review === 0}
                    >
                      <span className={styles.queueLabel}>Needs Review</span>
                      <span
                        className={styles.queueCount}
                        data-highlight={tasks.need_review > 0}
                      >
                        {tasks.need_review}
                      </span>
                    </button>
                    <button
                      type="button"
                      className={styles.queueItem}
                      onClick={() => handleCategoryClick("done")}
                      disabled={stats.closed === 0}
                    >
                      <span className={styles.queueLabel}>Done</span>
                      <span className={styles.queueCount}>{stats.closed}</span>
                    </button>
                  </div>
                </div>
              )}
            </div>
          )}

          {/* Footer with stats, sync status, and timestamp */}
          <div className={styles.footer}>
            {/* Stats row */}
            {isConnected && stats.total > 0 && (
              <div className={styles.statsRow}>
                <span className={styles.statItem}>
                  <span className={styles.statValue}>{stats.remaining}</span>{" "}
                  remaining
                </span>
                <span className={styles.statSeparator}>·</span>
                <span className={styles.statItem}>
                  <span className={styles.statValue}>{stats.closed}</span>{" "}
                  closed
                </span>
                <span className={styles.statSeparator}>·</span>
                <span className={styles.statItem}>
                  <span className={`${styles.statValue} ${styles.completion}`}>
                    {Math.round(stats.completion)}%
                  </span>
                </span>
              </div>
            )}

            {/* Sync status row (push count moved to Work Queue banner) */}
            {isConnected && hasSyncWarning && (
              <div className={styles.syncStatus}>
                {sync.git_needs_pull > 0 && (
                  <span
                    className={styles.syncWarning}
                    title={buildSyncTooltip(sync.git_pull_details, "behind")}
                  >
                    {sync.git_needs_pull} unpulled
                  </span>
                )}
                {sync.git_needs_pull > 0 && !sync.db_synced && (
                  <span className={styles.syncSeparator}>·</span>
                )}
                {!sync.db_synced && (
                  <span className={styles.syncWarning}>DB not synced</span>
                )}
              </div>
            )}

            {/* Timestamp */}
            {lastUpdated && (
              <span className={styles.timestamp}>
                Updated {lastUpdated.toLocaleTimeString()}
              </span>
            )}
          </div>
        </div>
      )}

      {collapsed && activeCount > 0 && (
        <div
          className={styles.collapsedBadge}
          title={`${activeCount} active agent(s)`}
        >
          {activeCount}
        </div>
      )}

      {/* Task Drawer */}
      <TaskDrawer
        isOpen={selectedCategory !== null}
        category={selectedCategory}
        title={drawerData.title}
        tasks={drawerData.tasks}
        onClose={handleDrawerClose}
      />
    </aside>
  );
}
