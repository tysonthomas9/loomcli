/**
 * SortableWorkspaceEntry — a draggable workspace entry for the sidebar.
 * Supports drag-and-drop reorder via @dnd-kit and Alt+Up/Down keyboard shortcuts.
 */

import type React from "react";
import { useSortable } from "@dnd-kit/sortable";

import type { WorkspaceSummary } from "@/api/workspace";

import styles from "../WorkspaceTree.module.css";

export interface SortableWorkspaceEntryProps {
  ws: WorkspaceSummary;
  isActive: boolean;
  isEditing: boolean;
  draftName: string;
  isSaving: boolean;
  renameError: string | null;
  renameInputRef: React.RefObject<HTMLInputElement | null>;
  onClick?: (name: string) => void;
  onDraftChange: (value: string) => void;
  onSaveRename: () => void;
  onRenameKeyDown: (e: React.KeyboardEvent<HTMLInputElement>) => void;
  onContextMenu: (e: React.MouseEvent, name: string) => void;
  onOverflowClick: (e: React.MouseEvent, name: string) => void;
  onMoveUp: (() => void) | undefined;
  onMoveDown: (() => void) | undefined;
}

export function SortableWorkspaceEntry({
  ws,
  isActive,
  isEditing,
  draftName,
  isSaving,
  renameError,
  renameInputRef,
  onClick,
  onDraftChange,
  onSaveRename,
  onRenameKeyDown,
  onContextMenu,
  onOverflowClick,
  onMoveUp,
  onMoveDown,
}: SortableWorkspaceEntryProps) {
  const {
    attributes,
    listeners,
    setNodeRef,
    transform,
    transition,
    isDragging,
  } = useSortable({ id: ws.name });

  const style: React.CSSProperties = {
    transform: transform
      ? `translate3d(${transform.x}px, ${transform.y}px, 0)`
      : undefined,
    transition: transition ?? undefined,
    opacity: isDragging ? 0.5 : 1,
  };

  const handleKeyDown = (e: React.KeyboardEvent) => {
    if (e.altKey && e.key === "ArrowUp") {
      e.preventDefault();
      onMoveUp?.();
    } else if (e.altKey && e.key === "ArrowDown") {
      e.preventDefault();
      onMoveDown?.();
    }
  };

  const handleClick = () => {
    if (!isEditing && onClick) {
      onClick(ws.name);
    }
  };

  return (
    <div
      ref={setNodeRef}
      style={style}
      className={`${styles.workspaceEntry}${isActive ? ` ${styles.workspaceEntryActive}` : ""}`}
      data-active={ws.active}
      data-current={isActive}
      data-dragging={isDragging}
      role="button"
      tabIndex={0}
      onClick={handleClick}
      onContextMenu={(e) => onContextMenu(e, ws.name)}
      onKeyDown={(e) => {
        if (e.key === "Enter" || e.key === " ") {
          e.preventDefault();
          handleClick();
        }
        handleKeyDown(e);
      }}
    >
      <span
        className={styles.dragHandle}
        {...attributes}
        {...listeners}
        aria-label={`Drag to reorder ${ws.name}`}
      >
        <svg width="8" height="14" viewBox="0 0 8 14" fill="currentColor">
          <circle cx="2" cy="2" r="1.2" />
          <circle cx="6" cy="2" r="1.2" />
          <circle cx="2" cy="7" r="1.2" />
          <circle cx="6" cy="7" r="1.2" />
          <circle cx="2" cy="12" r="1.2" />
          <circle cx="6" cy="12" r="1.2" />
        </svg>
      </span>
      <svg
        className={styles.workspaceEntryIcon}
        viewBox="0 0 16 16"
        width="14"
        height="14"
      >
        <rect
          x="1"
          y="4"
          width="10"
          height="8"
          rx="1.5"
          fill="none"
          stroke="currentColor"
          strokeWidth="1.3"
        />
        <rect
          x="5"
          y="1"
          width="10"
          height="8"
          rx="1.5"
          fill="none"
          stroke="currentColor"
          strokeWidth="1.3"
        />
      </svg>
      {isEditing ? (
        <div className={styles.renameContainer}>
          <input
            ref={renameInputRef as React.RefObject<HTMLInputElement>}
            type="text"
            className={styles.renameInput}
            value={draftName}
            onChange={(e) => onDraftChange(e.target.value)}
            onBlur={onSaveRename}
            onKeyDown={onRenameKeyDown}
            disabled={isSaving}
            aria-label="Rename workspace"
            data-testid="workspace-rename-input"
          />
          {renameError && (
            <span
              className={styles.renameError}
              role="alert"
              data-testid="workspace-rename-error"
            >
              {renameError}
            </span>
          )}
        </div>
      ) : (
        <a
          className={styles.workspaceEntryName}
          href={`/ws/${ws.id}/`}
          onClick={(e) => {
            e.preventDefault();
            e.stopPropagation();
            // Route through the onClick handler (setActiveWorkspace) so
            // the workspace switch uses a single code path with proper
            // API header sync and clean URL construction, instead of
            // relying on default <a> navigation which can race with
            // React URL-sync effects.
            if (onClick) {
              onClick(ws.name);
            }
          }}
          aria-label={`Switch to workspace ${ws.name}`}
        >
          {ws.name}
        </a>
      )}
      {ws.state === "error" && (
        <span className={styles.errorDot} title={ws.error_message ?? "Error"} />
      )}
      {ws.state && ws.state !== "ready" && ws.state !== "error" && (
        <span className={styles.stateSpinner} title={`${ws.state}...`} />
      )}
      <span className={styles.workspaceEntryMeta}>
        <span className={styles.workspaceRepoCount}>{ws.repo_count}</span>
        {ws.active && (
          <span className={styles.workspaceActiveBadge}>active</span>
        )}
      </span>
      {!isEditing && (
        <button
          type="button"
          className={styles.overflowButton}
          onClick={(e) => onOverflowClick(e, ws.name)}
          aria-label={`More actions for ${ws.name}`}
          data-testid={`workspace-overflow-${ws.name}`}
        >
          &#x2026;
        </button>
      )}
    </div>
  );
}
