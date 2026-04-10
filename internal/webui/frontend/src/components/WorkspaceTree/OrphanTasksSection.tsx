/**
 * OrphanTasksSection - Collapsible "Ungrouped" section shown by EpicTaskTree
 * for tasks that have no parent epic.
 */

import type React from "react";
import { useState } from "react";

import type { Issue } from "@/types";

import { TaskRow } from "./TaskRow";
import styles from "./EpicTaskTree.module.css";

export interface OrphanRenameState {
  editingId: string | null;
  editingType: "epic" | "task" | null;
  draftTitle: string;
  setDraftTitle: (value: string) => void;
  onSaveRename: () => void | Promise<void>;
  onCancelRename: () => void;
  renameInputRef: React.RefObject<HTMLInputElement | null>;
  renameError: string | null;
  isSaving: boolean;
}

export interface OrphanTasksSectionProps {
  orphanTasks: Issue[];
  selectedId?: string | undefined;
  onSelect?: ((issueId: string) => void) | undefined;
  onOverflowClick: (e: React.MouseEvent, taskId: string) => void;
  onTaskTerminalOpen?:
    | ((issueId: string, agentName: string) => void)
    | undefined;
  renameState: OrphanRenameState;
}

export function OrphanTasksSection({
  orphanTasks,
  selectedId,
  onSelect,
  onOverflowClick,
  onTaskTerminalOpen,
  renameState,
}: OrphanTasksSectionProps): JSX.Element | null {
  const [collapsed, setCollapsed] = useState(false);

  if (orphanTasks.length === 0) return null;

  const {
    editingId,
    editingType,
    draftTitle,
    setDraftTitle,
    onSaveRename,
    onCancelRename,
    renameInputRef,
    renameError,
    isSaving,
  } = renameState;

  return (
    <div className={styles.epicGroup}>
      <div className={styles.epicRow}>
        <button
          type="button"
          className={styles.epicRowButton}
          onClick={() => setCollapsed((p) => !p)}
          title="Ungrouped tasks"
        >
          <span className={styles.epicIcon}>
            <svg
              width="16"
              height="16"
              viewBox="0 0 16 16"
              fill="none"
              xmlns="http://www.w3.org/2000/svg"
            >
              <rect
                x="2"
                y="3"
                width="12"
                height="10"
                rx="1.5"
                stroke="currentColor"
                strokeWidth="1.3"
                strokeDasharray="2 1.5"
              />
            </svg>
          </span>
          <span className={styles.titleText}>Ungrouped</span>
        </button>
        <span
          className={styles.collapseChevron}
          data-expanded={!collapsed}
          role="img"
          aria-label={collapsed ? "Expand ungrouped" : "Collapse ungrouped"}
          onClick={() => setCollapsed((p) => !p)}
        >
          &rsaquo;
        </span>
      </div>
      {!collapsed && (
        <div className={styles.epicChildren}>
          {orphanTasks.map((task) => {
            const isEditingThis =
              editingId === task.id && editingType === "task";
            return (
              <TaskRow
                key={task.id}
                task={task}
                isSelected={selectedId === task.id}
                onSelect={onSelect}
                onOverflowClick={onOverflowClick}
                onTaskTerminalOpen={onTaskTerminalOpen}
                isEditing={isEditingThis}
                draftTitle={isEditingThis ? draftTitle : undefined}
                onDraftChange={isEditingThis ? setDraftTitle : undefined}
                onSaveRename={isEditingThis ? onSaveRename : undefined}
                onCancelRename={isEditingThis ? onCancelRename : undefined}
                renameInputRef={isEditingThis ? renameInputRef : undefined}
                renameError={isEditingThis ? renameError : undefined}
                isSaving={isEditingThis ? isSaving : undefined}
              />
            );
          })}
        </div>
      )}
    </div>
  );
}
