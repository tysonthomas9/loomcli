/**
 * AgentsSidebar component displays a collapsible sidebar with agent status.
 * Shows all agents from the loom server with real-time updates.
 * Includes work queue summary, project stats, and sync status.
 */

import type { ReactNode } from 'react';
import { useState, useCallback, useEffect } from 'react';

import { useAgentContext } from '@/hooks';
import type { LoomTaskInfo } from '@/types';

import { AgentCard } from '../AgentCard';
import { TaskDrawer } from '../TaskDrawer';
import type { TaskCategory } from '../TaskDrawer';
import styles from './AgentsSidebar.module.css';

/**
 * Props for the AgentsSidebar component.
 */
export interface AgentsSidebarProps {
  /** Additional CSS class name */
  className?: string;
  /** Default collapsed state */
  defaultCollapsed?: boolean;
  /** Allow collapsing the sidebar (default: true) */
  collapsible?: boolean;
  /** Callback when an agent card is clicked */
  onAgentClick?: (agentName: string) => void;
  /** Optional content to render at the top of the sidebar (e.g., ViewSwitcher) */
  viewSwitcher?: ReactNode;
}

const COLLAPSE_STORAGE_KEY = 'agents-sidebar-collapsed';
const WORK_QUEUE_STORAGE_KEY = 'agents-sidebar-work-queue-expanded';

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
  // Load initial collapsed state from localStorage
  const [isCollapsed, setIsCollapsed] = useState(() => {
    try {
      const stored = localStorage.getItem(COLLAPSE_STORAGE_KEY);
      return stored !== null ? stored === 'true' : defaultCollapsed;
    } catch {
      return defaultCollapsed;
    }
  });

  // Load work queue expanded state from localStorage
  const [isWorkQueueExpanded, setIsWorkQueueExpanded] = useState(() => {
    try {
      const stored = localStorage.getItem(WORK_QUEUE_STORAGE_KEY);
      return stored !== null ? stored === 'true' : true;
    } catch {
      return true;
    }
  });

  // Track which category drawer is open
  const [selectedCategory, setSelectedCategory] = useState<TaskCategory | null>(null);

  const { agents, tasks, taskLists, agentTasks, sync, stats, isLoading, isConnected, lastUpdated } =
    useAgentContext();

  // Persist collapsed state
  useEffect(() => {
    try {
      localStorage.setItem(COLLAPSE_STORAGE_KEY, String(isCollapsed));
    } catch {
      // Ignore localStorage errors
    }
  }, [isCollapsed]);

  // Persist work queue expanded state
  useEffect(() => {
    try {
      localStorage.setItem(WORK_QUEUE_STORAGE_KEY, String(isWorkQueueExpanded));
    } catch {
      // Ignore localStorage errors
    }
  }, [isWorkQueueExpanded]);

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
  const getDrawerData = useCallback((): { title: string; tasks: LoomTaskInfo[] } => {
    switch (selectedCategory) {
      case 'plan':
        return { title: 'Backlog', tasks: taskLists.needsPlanning };
      case 'impl':
        return { title: 'Open', tasks: taskLists.readyToImplement };
      case 'review':
        return { title: 'Needs Review', tasks: taskLists.needsReview };
      case 'inProgress':
        return { title: 'In Progress', tasks: taskLists.inProgress };
      case 'blocked':
        return { title: 'Blocked', tasks: taskLists.blocked };
      case 'done':
        return { title: 'Done', tasks: [] };
      default:
        return { title: '', tasks: [] };
    }
  }, [selectedCategory, taskLists]);

  const drawerData = getDrawerData();

  const collapsed = collapsible ? isCollapsed : false;

  const rootClassName = [styles.sidebar, collapsed && styles.collapsed, className]
    .filter(Boolean)
    .join(' ');

  // Count active agents (working or planning)
  const activeCount = agents.filter(
    (a) => a.status.startsWith('working:') || a.status.startsWith('planning:')
  ).length;

  // Check if there are sync warnings
  const hasSyncWarning = sync.git_needs_push > 0 || sync.git_needs_pull > 0 || !sync.db_synced;

  return (
    <aside className={rootClassName} data-collapsed={isCollapsed}>
      {!collapsed && viewSwitcher && <div className={styles.viewSwitcherSlot}>{viewSwitcher}</div>}
      {collapsible ? (
        <button
          type="button"
          className={styles.toggleButton}
          onClick={handleToggle}
          aria-expanded={!isCollapsed}
          aria-label={isCollapsed ? 'Expand agents sidebar' : 'Collapse agents sidebar'}
        >
          {!collapsed && (
            <>
              <span className={styles.toggleText}>Agents</span>
              <span className={styles.sectionCount}>{agents.length}</span>
            </>
          )}
          <span className={styles.toggleIcon}>{collapsed ? '>' : '<'}</span>
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
            <div className={styles.disconnected}>
              <span className={styles.disconnectedIcon}>!</span>
              <span>Loom server not available</span>
            </div>
          )}

          {isLoading && agents.length === 0 && (
            <div className={styles.loading}>Loading agents...</div>
          )}

          {agents.length === 0 && isConnected && !isLoading && (
            <div className={styles.empty}>No agents found</div>
          )}

          {agents.length > 0 && (
            <div className={styles.agentList}>
              {agents.map((agent) => (
                <AgentCard
                  key={agent.name}
                  agent={agent}
                  taskTitle={agentTasks[agent.name]?.title}
                  {...(onAgentClick !== undefined && { onClick: () => onAgentClick(agent.name) })}
                />
              ))}
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
                onKeyDown={(e) => e.key === 'Enter' && handleWorkQueueToggle()}
              >
                <span className={styles.workQueueToggle}>{isWorkQueueExpanded ? 'v' : '>'}</span>
                <span>Work Queue</span>
              </div>
              {isWorkQueueExpanded && (
                <div className={styles.workQueueContent}>
                  <div className={styles.queueGrid}>
                    <button
                      type="button"
                      className={styles.queueItem}
                      onClick={() => handleCategoryClick('impl')}
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
                      onClick={() => handleCategoryClick('blocked')}
                      disabled={tasks.blocked === 0}
                    >
                      <span className={styles.queueLabel}>Blocked</span>
                      <span className={styles.queueCount}>{tasks.blocked}</span>
                    </button>
                    <button
                      type="button"
                      className={styles.queueItem}
                      onClick={() => handleCategoryClick('inProgress')}
                      disabled={(tasks.in_progress ?? 0) === 0}
                    >
                      <span className={styles.queueLabel}>In Progress</span>
                      <span
                        className={styles.queueCount}
                        data-highlight={(tasks.in_progress ?? 0) > 0}
                      >
                        {tasks.in_progress ?? 0}
                      </span>
                    </button>
                    <button
                      type="button"
                      className={styles.queueItem}
                      onClick={() => handleCategoryClick('review')}
                      disabled={tasks.need_review === 0}
                    >
                      <span className={styles.queueLabel}>Needs Review</span>
                      <span className={styles.queueCount} data-highlight={tasks.need_review > 0}>
                        {tasks.need_review}
                      </span>
                    </button>
                    <button
                      type="button"
                      className={styles.queueItem}
                      disabled
                      aria-label="Done count"
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
                  <span className={styles.statValue}>{stats.open}</span> open
                </span>
                <span className={styles.statSeparator}>·</span>
                <span className={styles.statItem}>
                  <span className={styles.statValue}>{stats.closed}</span> closed
                </span>
                <span className={styles.statSeparator}>·</span>
                <span className={styles.statItem}>
                  <span className={`${styles.statValue} ${styles.completion}`}>
                    {Math.round(stats.completion)}%
                  </span>
                </span>
              </div>
            )}

            {/* Sync status row */}
            {isConnected && hasSyncWarning && (
              <div className={styles.syncStatus}>
                {sync.git_needs_push > 0 && (
                  <span className={styles.syncWarning}>{sync.git_needs_push} need push</span>
                )}
                {sync.git_needs_push > 0 && sync.git_needs_pull > 0 && (
                  <span className={styles.syncSeparator}>·</span>
                )}
                {sync.git_needs_pull > 0 && (
                  <span className={styles.syncWarning}>{sync.git_needs_pull} need pull</span>
                )}
                {(sync.git_needs_push > 0 || sync.git_needs_pull > 0) && !sync.db_synced && (
                  <span className={styles.syncSeparator}>·</span>
                )}
                {!sync.db_synced && <span className={styles.syncWarning}>DB not synced</span>}
              </div>
            )}

            {/* Timestamp */}
            {lastUpdated && (
              <span className={styles.timestamp}>Updated {lastUpdated.toLocaleTimeString()}</span>
            )}
          </div>
        </div>
      )}

      {collapsed && activeCount > 0 && (
        <div className={styles.collapsedBadge} title={`${activeCount} active agent(s)`}>
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
