/**
 * WorkPipelinePanel displays the task flow through pipeline stages.
 * Shows Plan → Ready → In Progress → Review → Done with Backlog as branch.
 */

import { useCallback, useState } from 'react';
import type { LoomTaskSummary, LoomTaskLists, LoomTaskInfo } from '@/types';
import { TaskDrawer } from '../TaskDrawer';
import type { TaskCategory } from '../TaskDrawer';
import { PipelineStage } from './PipelineStage';
import styles from './WorkPipelinePanel.module.css';

export interface WorkPipelinePanelProps {
  /** Task counts for each stage */
  tasks: LoomTaskSummary;
  /** Full task lists */
  taskLists: LoomTaskLists;
  /** Additional CSS class name */
  className?: string;
}

// Map pipeline stages to TaskDrawer categories
const STAGE_TO_CATEGORY: Record<string, TaskCategory> = {
  plan: 'plan',
  ready: 'impl',
  inProgress: 'inProgress',
  review: 'review',
  blocked: 'blocked',
};

// Stage configuration with labels and keys
interface StageConfig {
  id: string;
  label: string;
  countKey: keyof LoomTaskSummary;
  listKey: keyof LoomTaskLists;
  icon?: string;
}

const MAIN_STAGES: StageConfig[] = [
  { id: 'plan', label: 'Plan', countKey: 'needs_planning', listKey: 'needsPlanning', icon: '📝' },
  {
    id: 'ready',
    label: 'Ready',
    countKey: 'ready_to_implement',
    listKey: 'readyToImplement',
    icon: '✅',
  },
  {
    id: 'inProgress',
    label: 'In Progress',
    countKey: 'in_progress',
    listKey: 'inProgress',
    icon: '🔄',
  },
  { id: 'review', label: 'Review', countKey: 'need_review', listKey: 'needsReview', icon: '👀' },
];

const BLOCKED_STAGE: StageConfig = {
  id: 'blocked',
  label: 'Backlog',
  countKey: 'blocked',
  listKey: 'blocked',
  icon: '📦',
};

export function WorkPipelinePanel({
  tasks,
  taskLists,
  className,
}: WorkPipelinePanelProps): JSX.Element {
  const [selectedCategory, setSelectedCategory] = useState<TaskCategory | null>(null);

  const handleStageClick = useCallback((stageId: string) => {
    const category = STAGE_TO_CATEGORY[stageId];
    if (category) {
      setSelectedCategory(category);
    }
  }, []);

  const handleDrawerClose = useCallback(() => {
    setSelectedCategory(null);
  }, []);

  // Get tasks and title for selected category
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
        return { title: 'Backlog', tasks: taskLists.blocked };
      default:
        return { title: '', tasks: [] };
    }
  }, [selectedCategory, taskLists]);

  const drawerData = getDrawerData();

  const rootClassName = className ? `${styles.panel} ${className}` : styles.panel;

  return (
    <div className={rootClassName} data-testid="work-pipeline-panel">
      {/* Main pipeline flow */}
      <div className={styles.pipeline}>
        {MAIN_STAGES.map((stage, index) => (
          <div key={stage.id} className={styles.stageWrapper}>
            <PipelineStage
              id={stage.id}
              label={stage.label}
              icon={stage.icon}
              count={tasks[stage.countKey]}
              oldestItem={taskLists[stage.listKey][0]}
              onClick={handleStageClick}
            />
            {index < MAIN_STAGES.length - 1 && (
              <span className={styles.arrow} aria-hidden="true">
                →
              </span>
            )}
          </div>
        ))}
        {/* Done stage (no tasks to show, just visual end) */}
        <div className={styles.stageWrapper}>
          <span className={styles.arrow} aria-hidden="true">
            →
          </span>
          <div className={styles.doneStage}>
            <span className={styles.doneIcon}>✓</span>
            <span className={styles.doneLabel}>Done</span>
          </div>
        </div>
      </div>

      {/* Blocked branch */}
      {tasks.blocked > 0 && (
        <div className={styles.blockedBranch}>
          <span className={styles.branchLine} aria-hidden="true">
            ↳
          </span>
          <PipelineStage
            id={BLOCKED_STAGE.id}
            label={BLOCKED_STAGE.label}
            icon={BLOCKED_STAGE.icon}
            count={tasks.blocked}
            oldestItem={taskLists.blocked[0]}
            onClick={handleStageClick}
            variant="blocked"
          />
        </div>
      )}

      {/* Oldest items table */}
      <div className={styles.oldestItems}>
        <h4 className={styles.oldestTitle}>Oldest in Each Stage</h4>
        <table className={styles.oldestTable}>
          <thead>
            <tr>
              <th>Stage</th>
              <th>Task</th>
              <th>Priority</th>
            </tr>
          </thead>
          <tbody>
            {MAIN_STAGES.map((stage) => {
              const oldest = taskLists[stage.listKey][0];
              if (!oldest) return null;
              return (
                <tr key={stage.id}>
                  <td className={styles.stageCell}>{stage.label}</td>
                  <td className={styles.taskCell}>
                    <span className={styles.taskId}>{oldest.id}</span>
                    <span className={styles.taskTitle} title={oldest.title}>
                      {oldest.title}
                    </span>
                  </td>
                  <td className={styles.priorityCell}>P{oldest.priority}</td>
                </tr>
              );
            })}
          </tbody>
        </table>
      </div>

      {/* Task Drawer */}
      <TaskDrawer
        isOpen={selectedCategory !== null}
        category={selectedCategory}
        title={drawerData.title}
        tasks={drawerData.tasks}
        onClose={handleDrawerClose}
      />
    </div>
  );
}
