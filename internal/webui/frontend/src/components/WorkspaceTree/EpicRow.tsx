/**
 * EpicRow renders a collapsible row for an epic with nested TaskRow children.
 * Shows linked-rings icon, title, status dot, and collapse chevron.
 */

import type { Issue } from "@/types";

import { TaskRow } from "./TaskRow";
import styles from "./EpicTaskTree.module.css";

export interface EpicRowProps {
  epic: Issue;
  tasks: Issue[];
  isCollapsed: boolean;
  onToggle: () => void;
  selectedId?: string | undefined;
  onSelect?: ((issueId: string) => void) | undefined;
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
}: EpicRowProps): JSX.Element {
  return (
    <div className={styles.epicGroup}>
      <button
        type="button"
        className={styles.epicRow}
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
        <span className={styles.titleText}>{epic.title}</span>
        <span
          className={styles.statusDot}
          data-status={epicStatusAttr(epic.status)}
        />
        <span
          className={styles.collapseChevron}
          data-expanded={!isCollapsed}
          role="img"
          aria-label={isCollapsed ? "Expand epic" : "Collapse epic"}
        >
          &rsaquo;
        </span>
      </button>
      {!isCollapsed && tasks.length > 0 && (
        <div className={styles.epicChildren}>
          {tasks.map((task) => (
            <TaskRow
              key={task.id}
              task={task}
              isSelected={selectedId === task.id}
              onSelect={onSelect}
            />
          ))}
        </div>
      )}
    </div>
  );
}
