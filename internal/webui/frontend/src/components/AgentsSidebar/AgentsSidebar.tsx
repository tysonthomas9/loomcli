/**
 * AgentsSidebar component displays a collapsible sidebar with agent status.
 * Shows all agents from the loom server with real-time updates.
 * Includes work queue summary, project stats, and sync status.
 */

import { useState, useCallback, useEffect } from 'react';
import { useAgents } from '@/hooks';
import { AgentCard } from '../AgentCard';
import { TaskDrawer } from '../TaskDrawer';
import type { TaskCategory } from '../TaskDrawer';
import type { LoomTaskInfo } from '@/types';
import styles from './AgentsSidebar.module.css';

/**
 * Props for the AgentsSidebar component.
 */
export interface AgentsSidebarProps {
  /** Additional CSS class name */
  className?: string;
  /** Default collapsed state */
  defaultCollapsed?: boolean;
}

const COLLAPSE_STORAGE_KEY = 'agents-sidebar-collapsed';
const WORK_QUEUE_STORAGE_KEY = 'agents-sidebar-work-queue-expanded';

/**
 * AgentsSidebar displays a collapsible panel with agent status cards.
 */
export function AgentsSidebar({
  className,
  defaultCollapsed = false,
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
    useAgents({
      pollInterval: 5000,
    });

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
    setIsCollapsed((prev) => !prev);
  }, []);

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
        return { title: 'Needs Planning', tasks: taskLists.needsPlanning };
      case 'impl':
        return { title: 'Ready to Implement', tasks: taskLists.readyToImplement };
      case 'review':
        return { title: 'Needs Review', tasks: taskLists.needsReview };
      case 'inProgress':
        return { title: 'In Progress', tasks: taskLists.inProgress };
      case 'blocked':
        return { title: 'Blocked', tasks: taskLists.blocked };
      default:
        return { title: '', tasks: [] };
    }
  }, [selectedCategory, taskLists]);

  const drawerData = getDrawerData();

  const rootClassName = [styles.sidebar, isCollapsed && styles.collapsed, className]
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
      <button
        type="button"
        className={styles.toggleButton}
        onClick={handleToggle}
        aria-expanded={!isCollapsed}
        aria-label={isCollapsed ? 'Expand agents sidebar' : 'Collapse agents sidebar'}
      >
        {!isCollapsed && (
          <span className={styles.toggleText}>
            Agents
            {activeCount > 0 && <span className={styles.activeCount}>{activeCount}</span>}
          </span>
        )}
        <span className={styles.toggleIcon}>{isCollapsed ? '>' : '<'}</span>
      </button>

      {!isCollapsed && (
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
                      onClick={() => handleCategoryClick('plan')}
                      disabled={tasks.needs_planning === 0}
                    >
                      <span className={styles.queueLabel}>Plan</span>
                      <span className={styles.queueCount} data-highlight={tasks.needs_planning > 0}>
                        {tasks.needs_planning}
                      </span>
                    </button>
                    <button
                      type="button"
                      className={styles.queueItem}
                      onClick={() => handleCategoryClick('impl')}
                      disabled={tasks.ready_to_implement === 0}
                    >
                      <span className={styles.queueLabel}>Impl</span>
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
                      onClick={() => handleCategoryClick('review')}
                      disabled={tasks.need_review === 0}
                    >
                      <span className={styles.queueLabel}>Review</span>
                      <span className={styles.queueCount} data-highlight={tasks.need_review > 0}>
                        {tasks.need_review}
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

      {isCollapsed && activeCount > 0 && (
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
