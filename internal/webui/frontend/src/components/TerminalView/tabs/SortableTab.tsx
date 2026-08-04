/**
 * SortableTab — wraps a tab element with dnd-kit sortable behavior.
 * Renders the tab content with drag-and-drop reordering support.
 */

import type React from "react";
import { useSortable } from "@dnd-kit/sortable";

import styles from "./TerminalTabBar.module.css";

export interface SortableTabProps {
  id: string;
  children: React.ReactNode;
  className: string;
  isPinned: boolean;
  isActive: boolean;
  onClick: () => void;
  onContextMenu: (e: React.MouseEvent) => void;
  onKeyDown: (e: React.KeyboardEvent) => void;
  "data-testid": string;
  /** Native drag between editor groups (split view); disables dnd-kit reorder. */
  groupDrag?: {
    onDragStart: () => void;
    onDragEnd: () => void;
  };
}

export function SortableTab({
  id,
  children,
  className,
  isPinned,
  isActive,
  onClick,
  onContextMenu,
  onKeyDown,
  "data-testid": testId,
  groupDrag,
}: SortableTabProps) {
  const {
    attributes,
    listeners,
    setNodeRef,
    transform,
    transition,
    isDragging,
  } = useSortable({ id, disabled: groupDrag != null });

  const style: React.CSSProperties = groupDrag
    ? {}
    : {
        transform: transform
          ? `translate3d(${transform.x}px, 0, 0)`
          : undefined,
        transition: transition ?? undefined,
        opacity: isDragging ? 0.5 : 1,
        zIndex: isDragging ? 10 : undefined,
      };

  const tabClassName = [
    className,
    isPinned && styles.pinned,
    !groupDrag && isDragging && styles.dragging,
    groupDrag && styles.groupDraggable,
  ]
    .filter(Boolean)
    .join(" ");

  return (
    <div
      {...(groupDrag ? {} : attributes)}
      {...(groupDrag ? {} : listeners)}
      ref={setNodeRef}
      style={style}
      id={`terminal-tab-${id}`}
      role="tab"
      aria-selected={isActive}
      aria-controls={`terminal-panel-${id}`}
      tabIndex={isActive ? 0 : -1}
      className={tabClassName}
      draggable={groupDrag != null}
      onDragStart={
        groupDrag
          ? (event) => {
              event.dataTransfer.effectAllowed = "move";
              groupDrag.onDragStart();
            }
          : undefined
      }
      onDragEnd={groupDrag ? () => groupDrag.onDragEnd() : undefined}
      onClick={onClick}
      onContextMenu={onContextMenu}
      onKeyDown={onKeyDown}
      data-testid={testId}
    >
      {children}
    </div>
  );
}
