/**
 * Drag end handler factory for KanbanBoard.
 * Creates an onDragEnd handler that processes @dnd-kit DragEndEvents,
 * applies optimistic updates, and persists status changes via API.
 */

import type { DragEndEvent } from '@dnd-kit/core';
import { updateIssue } from '@/api';
import type { Issue, Status } from '@/types';

/**
 * Callback to update local state when an issue's status changes.
 * Called immediately (before API call) for optimistic update.
 */
export interface IssueStatusChangeCallback {
  (issueId: string, newStatus: Status): void;
}

/**
 * Options for creating the drag end handler.
 */
export interface HandleDragEndOptions {
  /** Callback to update local state optimistically */
  onIssueStatusChange: IssueStatusChangeCallback;
  /** Optional callback when API call succeeds */
  onSuccess?: (issue: Issue, newStatus: Status) => void;
  /** Optional callback when API call fails (for rollback in T035) */
  onError?: (error: Error, issue: Issue, previousStatus: Status) => void;
}

/**
 * Data attached to draggable items (DraggableIssueCard).
 * @internal
 */
interface DraggableData {
  issue: Issue;
  type: 'issue';
}

/**
 * Data attached to droppable targets (StatusColumn).
 * @internal
 */
interface DroppableData {
  status: Status;
}

/**
 * Type guard to check if drag data is valid DraggableData.
 * Validates the structure from DraggableIssueCard's useDraggable data prop.
 */
function isDraggableData(data: unknown): data is DraggableData {
  return (
    data != null &&
    typeof data === 'object' &&
    'issue' in data &&
    'type' in data &&
    (data as DraggableData).type === 'issue'
  );
}

/**
 * Type guard to check if drop target data is valid DroppableData.
 * Validates the structure from StatusColumn's useDroppable data prop.
 */
function isDroppableData(data: unknown): data is DroppableData {
  return (
    data != null &&
    typeof data === 'object' &&
    'status' in data &&
    typeof (data as DroppableData).status === 'string'
  );
}

/**
 * Creates a DragEndEvent handler for the KanbanBoard.
 *
 * The returned handler:
 * 1. Validates the drop target and dragged data
 * 2. Extracts issue and status information from @dnd-kit event
 * 3. Calls onIssueStatusChange for optimistic local state update
 * 4. Calls updateIssue API to persist the change
 * 5. Calls onSuccess or onError callbacks based on API result
 *
 * @example
 * ```ts
 * const handleDragEnd = createDragEndHandler({
 *   onIssueStatusChange: (issueId, newStatus) => {
 *     setIssues(prev => prev.map(issue =>
 *       issue.id === issueId ? { ...issue, status: newStatus } : issue
 *     ));
 *   },
 *   onSuccess: (issue, newStatus) => {
 *     console.log(`Issue ${issue.id} moved to ${newStatus}`);
 *   },
 *   onError: (error, issue, previousStatus) => {
 *     // Rollback optimistic update (T035)
 *     console.error('Failed to update status:', error);
 *   },
 * });
 *
 * <DndContext onDragEnd={handleDragEnd}>
 * ```
 */
export function createDragEndHandler(options: HandleDragEndOptions) {
  const { onIssueStatusChange, onSuccess, onError } = options;

  return async (event: DragEndEvent): Promise<void> => {
    const { active, over } = event;

    // No drop target - user dropped outside any column
    if (!over) return;

    // Validate dragged item has issue data
    const activeData = active.data.current;
    if (!isDraggableData(activeData)) return;

    // Validate drop target has status data
    const overData = over.data.current;
    if (!isDroppableData(overData)) return;

    const issue = activeData.issue;
    const previousStatus = issue.status ?? 'open';
    const newStatus = overData.status;

    // Skip if dropped on same column (no status change)
    if (previousStatus === newStatus) return;

    // Optimistic update - update local state immediately
    onIssueStatusChange(issue.id, newStatus);

    try {
      // Persist to backend
      await updateIssue(issue.id, { status: newStatus });
      onSuccess?.(issue, newStatus);
    } catch (error) {
      // Call error handler for potential rollback (T035)
      onError?.(error as Error, issue, previousStatus);
    }
  };
}

// Export type guards for testing
export { isDraggableData, isDroppableData };
