/**
 * TaskDrawer component displays a slide-out drawer with task list.
 * Opens from the right side, covering 50% of the screen.
 */

import { useEffect, useCallback } from 'react';

import type { LoomTaskInfo } from '@/types';

import styles from './TaskDrawer.module.css';

/**
 * Category type for work queue items.
 */
export type TaskCategory = 'plan' | 'impl' | 'review' | 'inProgress' | 'blocked' | 'done';

/**
 * Props for the TaskDrawer component.
 */
export interface TaskDrawerProps {
  /** Whether the drawer is open */
  isOpen: boolean;
  /** The category being displayed */
  category: TaskCategory | null;
  /** Title to display in the header */
  title: string;
  /** List of tasks to display */
  tasks: LoomTaskInfo[];
  /** Callback when drawer should close */
  onClose: () => void;
}

/**
 * Get priority badge color based on priority level.
 */
function getPriorityColor(priority: number): string {
  switch (priority) {
    case 0:
      return 'var(--color-priority-critical, #dc2626)';
    case 1:
      return 'var(--color-priority-high, #ea580c)';
    case 2:
      return 'var(--color-priority-medium, #ca8a04)';
    case 3:
      return 'var(--color-priority-low, #2563eb)';
    case 4:
      return 'var(--color-priority-backlog, #6b7280)';
    default:
      return 'var(--color-priority-medium, #ca8a04)';
  }
}

/**
 * TaskDrawer displays a slide-out panel with a list of tasks.
 */
export function TaskDrawer({
  isOpen,
  category,
  title,
  tasks,
  onClose,
}: TaskDrawerProps): JSX.Element | null {
  // Handle escape key to close drawer
  const handleKeyDown = useCallback(
    (e: KeyboardEvent) => {
      if (e.key === 'Escape' && isOpen) {
        onClose();
      }
    },
    [isOpen, onClose]
  );

  useEffect(() => {
    document.addEventListener('keydown', handleKeyDown);
    return () => document.removeEventListener('keydown', handleKeyDown);
  }, [handleKeyDown]);

  // Prevent body scroll when drawer is open
  useEffect(() => {
    if (isOpen) {
      document.body.style.overflow = 'hidden';
    } else {
      document.body.style.overflow = '';
    }
    return () => {
      document.body.style.overflow = '';
    };
  }, [isOpen]);

  if (!isOpen || !category) {
    return null;
  }

  return (
    <div className={styles.container} data-open={isOpen}>
      {/* Overlay */}
      <div className={styles.overlay} onClick={onClose} aria-hidden="true" />

      {/* Drawer */}
      <div className={styles.drawer} role="dialog" aria-modal="true" aria-labelledby="drawer-title">
        {/* Header */}
        <div className={styles.header}>
          <h2 id="drawer-title" className={styles.title}>
            {title}
            <span className={styles.count}>({tasks.length})</span>
          </h2>
          <button
            type="button"
            className={styles.closeButton}
            onClick={onClose}
            aria-label="Close drawer"
          >
            <span aria-hidden="true">&times;</span>
          </button>
        </div>

        {/* Task list */}
        <div className={styles.taskList}>
          {tasks.length === 0 ? (
            <div className={styles.emptyState}>No tasks in this category</div>
          ) : (
            tasks.map((task) => (
              <div key={task.id} className={styles.taskItem}>
                <span className={styles.taskId}>{task.id}</span>
                <span className={styles.taskTitle} title={task.title}>
                  {task.title}
                </span>
                <span
                  className={styles.priority}
                  style={{ backgroundColor: getPriorityColor(task.priority) }}
                >
                  P{task.priority}
                </span>
              </div>
            ))
          )}
        </div>
      </div>
    </div>
  );
}
