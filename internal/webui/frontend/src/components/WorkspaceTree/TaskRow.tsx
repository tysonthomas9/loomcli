/**
 * TaskRow renders a leaf row for a task within an epic.
 * Shows task icon, title, assignee chip, diff stats, overflow menu button, and status dot.
 */

import type React from "react";

import type { Issue } from "@/types";
import { useIssueDiffStat } from "@/hooks";

import styles from "./EpicTaskTree.module.css";

export interface TaskRowProps {
  task: Issue;
  isSelected: boolean;
  onSelect?: ((issueId: string) => void) | undefined;
  onOverflowClick?: ((e: React.MouseEvent, taskId: string) => void) | undefined;
  isEditing?: boolean;
  draftTitle?: string | undefined;
  onDraftChange?: ((value: string) => void) | undefined;
  onSaveRename?: (() => void) | undefined;
  onCancelRename?: (() => void) | undefined;
  renameInputRef?: React.RefObject<HTMLInputElement | null> | undefined;
  renameError?: string | null | undefined;
  isSaving?: boolean | undefined;
}

/** Map task status to a CSS data-status value for dot coloring. */
function statusToDataAttr(status: string | undefined): string {
  switch (status) {
    case "in_progress":
      return "in_progress";
    case "review":
      return "review";
    case "open":
      return "open";
    case "blocked":
      return "blocked";
    case "closed":
      return "closed";
    default:
      return "open";
  }
}

export function TaskRow({
  task,
  isSelected,
  onSelect,
  onOverflowClick,
  isEditing,
  draftTitle,
  onDraftChange,
  onSaveRename,
  onCancelRename,
  renameInputRef,
  renameError,
  isSaving,
}: TaskRowProps): JSX.Element {
  const { data: diffStat } = useIssueDiffStat({
    issueId: task.id,
    enabled: task.assignee !== undefined,
    pollInterval: 60000,
  });

  const handleOverflowClick = (e: React.MouseEvent) => {
    e.stopPropagation();
    onOverflowClick?.(e, task.id);
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

  return (
    <div
      className={`${styles.taskRow} ${isSelected ? styles.taskRowSelected : ""}`}
    >
      <button
        type="button"
        className={styles.taskRowButton}
        onClick={() => onSelect?.(task.id)}
        title={task.title}
      >
        {/* Task circle icon */}
        <span className={styles.taskIcon}>
          <svg
            width="14"
            height="14"
            viewBox="0 0 14 14"
            fill="none"
            xmlns="http://www.w3.org/2000/svg"
          >
            <circle
              cx="7"
              cy="7"
              r="5.5"
              stroke="currentColor"
              strokeWidth="1.2"
            />
            {task.status === "closed" && (
              <path
                d="M4.5 7L6.5 9L9.5 5"
                stroke="currentColor"
                strokeWidth="1.2"
                strokeLinecap="round"
                strokeLinejoin="round"
              />
            )}
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
              aria-label="Rename task"
              data-testid="task-rename-input"
            />
            {renameError && (
              <span
                className={styles.renameError}
                role="alert"
                data-testid="task-rename-error"
              >
                {renameError}
              </span>
            )}
          </div>
        ) : (
          <span className={styles.titleText}>{task.title}</span>
        )}
        {task.assignee && (
          <span className={styles.branchChip}>{task.assignee}</span>
        )}
        {diffStat && (diffStat.added > 0 || diffStat.removed > 0) && (
          <span className={styles.diffStats}>
            {diffStat.added > 0 && (
              <span className={styles.diffAdded}>+{diffStat.added}</span>
            )}
            {diffStat.removed > 0 && (
              <span className={styles.diffRemoved}>-{diffStat.removed}</span>
            )}
          </span>
        )}
        <span
          className={styles.statusDot}
          data-status={statusToDataAttr(task.status)}
        />
      </button>
      {!isEditing && (
        <button
          type="button"
          className={styles.overflowButton}
          onClick={handleOverflowClick}
          aria-label={`More actions for ${task.title}`}
          data-testid={`task-overflow-${task.id}`}
        >
          &#x2026;
        </button>
      )}
    </div>
  );
}
