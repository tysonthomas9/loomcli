/**
 * EpicRow renders a collapsible row for an epic with nested TaskRow children.
 * Shows linked-rings icon, title, status dot, overflow menu button, and collapse chevron.
 */

import type React from "react";
import { useCallback } from "react";

import type { Issue } from "@/types";
import { createWorkspaceTask } from "@/hooks/api";
import { useToast } from "@/hooks/ui";
import { useWorkspaceContext } from "@/hooks/workspace";
import { useInlineCreate } from "@/hooks/issues";

import { TaskRow, type TaskRowProps } from "./TaskRow";
import { InlineAddInput } from "./InlineAddInput";
import styles from "./EpicTaskTree.module.css";

export interface EpicRowProps {
  epic: Issue;
  tasks: Issue[];
  isCollapsed: boolean;
  onToggle: () => void;
  selectedId?: string | undefined;
  onSelect?: ((issueId: string) => void) | undefined;
  onOverflowClick?: ((e: React.MouseEvent, epicId: string) => void) | undefined;
  isEditing?: boolean;
  draftTitle?: string | undefined;
  onDraftChange?: ((value: string) => void) | undefined;
  onSaveRename?: (() => void) | undefined;
  onCancelRename?: (() => void) | undefined;
  renameInputRef?: React.RefObject<HTMLInputElement | null> | undefined;
  renameError?: string | null | undefined;
  isSaving?: boolean | undefined;
  /** Pass-through task props for overflow/rename on child TaskRows. */
  taskOverflowClick?:
    | ((e: React.MouseEvent, taskId: string) => void)
    | undefined;
  editingTaskId?: string | undefined;
  taskDraftTitle?: string | undefined;
  onTaskDraftChange?: ((value: string) => void) | undefined;
  onTaskSaveRename?: (() => void) | undefined;
  onTaskCancelRename?: (() => void) | undefined;
  taskRenameInputRef?: React.RefObject<HTMLInputElement | null> | undefined;
  taskRenameError?: string | null | undefined;
  taskIsSaving?: boolean | undefined;
  /** Called after a task is successfully created inline. */
  onTaskCreated?: (() => void) | undefined;
  /** Callback when a task with an active agent is clicked for terminal. */
  onTaskTerminalOpen?:
    | ((issueId: string, agentName: string) => void)
    | undefined;
}

/** Map epic status to a CSS data-status value. */
function epicStatusAttr(status: string | undefined): string {
  switch (status) {
    case "in_progress":
      return "in_progress";
    case "open":
      return "open";
    case "closed":
      return "closed";
    default:
      return "open";
  }
}

export function EpicRow({
  epic,
  tasks,
  isCollapsed,
  onToggle,
  selectedId,
  onSelect,
  onOverflowClick,
  isEditing,
  draftTitle,
  onDraftChange,
  onSaveRename,
  onCancelRename,
  renameInputRef,
  renameError,
  taskOverflowClick,
  editingTaskId,
  taskDraftTitle,
  onTaskDraftChange,
  onTaskSaveRename,
  onTaskCancelRename,
  taskRenameInputRef,
  taskRenameError,
  isSaving,
  taskIsSaving,
  onTaskCreated,
  onTaskTerminalOpen,
}: EpicRowProps): JSX.Element {
  const { workspaceId } = useWorkspaceContext();
  const { showToast } = useToast();

  const handleCreateTask = useCallback(
    (title: string) => createWorkspaceTask(workspaceId, epic.id, title),
    [workspaceId, epic.id],
  );

  const addTask = useInlineCreate({
    createFn: handleCreateTask,
    onSuccess: (issue) => {
      showToast(`Task ${issue.id} created`, { type: "success" });
      onTaskCreated?.();
    },
    onError: (msg) => {
      showToast(msg, { type: "error" });
    },
  });
  const handleOverflowClick = (e: React.MouseEvent) => {
    e.stopPropagation();
    onOverflowClick?.(e, epic.id);
  };

  const handleRenameKeyDown = (e: React.KeyboardEvent<HTMLInputElement>) => {
    if (e.key === "Enter") {
      e.preventDefault();
      onSaveRename?.();
    } else if (e.key === "Escape") {
      e.preventDefault();
      onCancelRename?.();
    }
  };

  // Build task row props for child tasks
  const buildTaskProps = (task: Issue): Omit<TaskRowProps, "task"> => {
    const isTaskEditing = editingTaskId === task.id;
    return {
      isSelected: selectedId === task.id,
      onSelect,
      onOverflowClick: taskOverflowClick,
      isEditing: isTaskEditing,
      draftTitle: isTaskEditing ? taskDraftTitle : undefined,
      onDraftChange: isTaskEditing ? onTaskDraftChange : undefined,
      onSaveRename: isTaskEditing ? onTaskSaveRename : undefined,
      onCancelRename: isTaskEditing ? onTaskCancelRename : undefined,
      renameInputRef: isTaskEditing ? taskRenameInputRef : undefined,
      renameError: isTaskEditing ? taskRenameError : undefined,
      isSaving: isTaskEditing ? taskIsSaving : undefined,
      onTaskTerminalOpen,
    };
  };

  return (
    <div className={styles.epicGroup}>
      <div className={styles.epicRow}>
        <button
          type="button"
          className={styles.epicRowButton}
          onClick={onToggle}
          title={epic.title}
        >
          {/* Linked-rings icon */}
          <span className={styles.epicIcon}>
            <svg
              width="16"
              height="16"
              viewBox="0 0 16 16"
              fill="none"
              xmlns="http://www.w3.org/2000/svg"
            >
              <circle
                cx="5.5"
                cy="8"
                r="4"
                stroke="currentColor"
                strokeWidth="1.3"
              />
              <circle
                cx="10.5"
                cy="8"
                r="4"
                stroke="currentColor"
                strokeWidth="1.3"
              />
            </svg>
          </span>
          {isEditing ? (
            <div
              className={styles.renameContainer}
              onClick={(e) => e.stopPropagation()}
            >
              <input
                ref={renameInputRef as React.RefObject<HTMLInputElement>}
                type="text"
                className={styles.renameInput}
                value={draftTitle ?? ""}
                onChange={(e) => onDraftChange?.(e.target.value)}
                onBlur={onSaveRename}
                onKeyDown={handleRenameKeyDown}
                disabled={isSaving}
                aria-label="Rename epic"
                data-testid="epic-rename-input"
              />
              {renameError && (
                <span
                  className={styles.renameError}
                  role="alert"
                  data-testid="epic-rename-error"
                >
                  {renameError}
                </span>
              )}
            </div>
          ) : (
            <span className={styles.titleText}>{epic.title}</span>
          )}
          <span
            className={styles.statusDot}
            data-status={epicStatusAttr(epic.status)}
          />
        </button>
        {!isEditing && (
          <button
            type="button"
            className={styles.overflowButton}
            onClick={handleOverflowClick}
            aria-label={`More actions for ${epic.title}`}
            data-testid={`epic-overflow-${epic.id}`}
          >
            &#x2026;
          </button>
        )}
        <span
          className={styles.collapseChevron}
          data-expanded={!isCollapsed}
          role="img"
          aria-label={isCollapsed ? "Expand epic" : "Collapse epic"}
          onClick={onToggle}
        >
          &rsaquo;
        </span>
      </div>
      {!isCollapsed && (
        <div className={styles.epicChildren}>
          {tasks.map((task) => (
            <TaskRow key={task.id} task={task} {...buildTaskProps(task)} />
          ))}
          {addTask.isAdding ? (
            <InlineAddInput
              placeholder="New task name"
              onSubmit={addTask.submitTitle}
              onCancel={addTask.cancelAdding}
              isSubmitting={addTask.isSubmitting}
              error={addTask.error}
              className={styles.inlineAddInputTask}
            />
          ) : (
            <button
              type="button"
              className={styles.addButtonTask}
              onClick={addTask.startAdding}
              data-testid={`add-task-${epic.id}`}
            >
              + Add task
            </button>
          )}
        </div>
      )}
    </div>
  );
}
