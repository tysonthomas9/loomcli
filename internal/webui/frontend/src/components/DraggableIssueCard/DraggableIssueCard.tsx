/**
 * DraggableIssueCard component for Kanban board.
 * Wraps IssueCard with @dnd-kit useDraggable hook to enable drag-and-drop.
 * Supports both normal rendering and overlay mode (for DragOverlay).
 */

import { useDraggable } from '@dnd-kit/core';

import { IssueCard, type IssueCardProps } from '../IssueCard';
import styles from './DraggableIssueCard.module.css';

/**
 * Drag handle icon (6 grip dots in 2x3 grid).
 * Provides visual affordance that cards are draggable.
 */
function DragHandleIcon({ className }: { className?: string | undefined }): JSX.Element {
  return (
    <svg
      className={className}
      width="12"
      height="16"
      viewBox="0 0 12 16"
      fill="currentColor"
      aria-hidden="true"
    >
      <circle cx="3" cy="3" r="1.5" />
      <circle cx="9" cy="3" r="1.5" />
      <circle cx="3" cy="8" r="1.5" />
      <circle cx="9" cy="8" r="1.5" />
      <circle cx="3" cy="13" r="1.5" />
      <circle cx="9" cy="13" r="1.5" />
    </svg>
  );
}

/**
 * Props for the DraggableIssueCard component.
 */
export interface DraggableIssueCardProps extends IssueCardProps {
  /** Whether this card is being rendered in a DragOverlay (no drag listeners) */
  isOverlay?: boolean;
  /** Column ID this card belongs to (for drag restrictions) */
  columnId?: string;
}

/**
 * DraggableIssueCard wraps IssueCard with drag-and-drop functionality.
 * Uses @dnd-kit useDraggable hook to enable dragging issues between columns.
 *
 * When rendered normally, it applies drag listeners and visual feedback.
 * When rendered in overlay mode (isOverlay=true), it renders without drag
 * functionality for use in DragOverlay.
 */
export function DraggableIssueCard({
  issue,
  onClick,
  className,
  isOverlay = false,
  blockedByCount,
  blockedBy,
  columnId,
  isBacklog,
  onApprove,
  onReject,
}: DraggableIssueCardProps): JSX.Element {
  const { attributes, listeners, setNodeRef, transform, isDragging } = useDraggable({
    id: issue.id,
    data: { issue, type: 'issue', columnId },
    disabled: isOverlay,
  });

  // Build IssueCard props, only including optional fields if defined
  // (required for exactOptionalPropertyTypes compatibility)
  const cardProps = {
    issue,
    ...(onClick !== undefined && { onClick }),
    ...(className !== undefined && { className }),
    ...(blockedByCount !== undefined && { blockedByCount }),
    ...(blockedBy !== undefined && { blockedBy }),
    ...(isBacklog !== undefined && { isBacklog }),
    ...(columnId !== undefined && { columnId }),
    ...(onApprove !== undefined && { onApprove }),
    ...(onReject !== undefined && { onReject }),
  };

  // In overlay mode, render without drag functionality
  if (isOverlay) {
    return (
      <div className={styles.overlay}>
        <IssueCard {...cardProps} />
      </div>
    );
  }

  // Apply transform using CSS translate3d for hardware acceleration
  const style: React.CSSProperties = {
    transform: transform ? `translate3d(${transform.x}px, ${transform.y}px, 0)` : undefined,
    opacity: isDragging ? 0.5 : 1,
  };

  return (
    <div
      ref={setNodeRef}
      style={style}
      className={styles.draggable}
      data-dragging={isDragging ? 'true' : undefined}
      {...listeners}
      {...attributes}
    >
      <DragHandleIcon className={styles.dragHandle} />
      <IssueCard {...cardProps} />
    </div>
  );
}
