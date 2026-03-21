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
}: SortableTabProps) {
  const {
    attributes,
    listeners,
    setNodeRef,
    transform,
    transition,
    isDragging,
  } = useSortable({ id });

  const style: React.CSSProperties = {
    transform: transform ? `translate3d(${transform.x}px, 0, 0)` : undefined,
    transition: transition ?? undefined,
    opacity: isDragging ? 0.5 : 1,
    zIndex: isDragging ? 10 : undefined,
  };

  const tabClassName = [
    className,
    isPinned && styles.pinned,
    isDragging && styles.dragging,
  ]
    .filter(Boolean)
    .join(" ");

  return (
    <div
      {...attributes}
      {...listeners}
      ref={setNodeRef}
      style={style}
      id={`terminal-tab-${id}`}
      role="tab"
      aria-selected={isActive}
      aria-controls={`terminal-panel-${id}`}
      tabIndex={isActive ? 0 : -1}
      className={tabClassName}
      onClick={onClick}
      onContextMenu={onContextMenu}
      onKeyDown={onKeyDown}
      data-testid={testId}
    >
      {children}
    </div>
  );
}
