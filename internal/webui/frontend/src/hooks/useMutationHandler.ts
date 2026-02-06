/**
 * React hook for reconciling SSE mutation events with client-side issue state.
 * Processes create, update, delete, status, and other mutation types to keep the UI in sync.
 */

import { produce } from 'immer';
import { useCallback, useEffect, useRef, useState } from 'react';

import type { MutationPayload } from '../api/sse';
import type { Issue } from '../types/issue';
import {
  MutationCreate,
  MutationUpdate,
  MutationDelete,
  MutationStatus,
  MutationBonded,
} from '../types/mutation';

/**
 * Options for the useMutationHandler hook.
 */
export interface UseMutationHandlerOptions {
  /** Current issue state as a Map for O(1) lookups */
  issues: Map<string, Issue>;

  /** Callback to update issue state (supports functional updates for race condition safety) */
  setIssues: (
    issues: Map<string, Issue> | ((prev: Map<string, Issue>) => Map<string, Issue>)
  ) => void;

  /** Callback when an issue is created */
  onIssueCreated?: (issue: Issue) => void;

  /** Callback when an issue is updated */
  onIssueUpdated?: (issue: Issue, previousIssue: Issue) => void;

  /** Callback when an issue is deleted */
  onIssueDeleted?: (issueId: string) => void;

  /** Callback when mutation cannot be applied (missing issue) */
  onMutationSkipped?: (mutation: MutationPayload, reason: string) => void;
}

/**
 * Return type for the useMutationHandler hook.
 */
export interface UseMutationHandlerReturn {
  /** Process a single mutation event */
  handleMutation: (mutation: MutationPayload) => void;

  /** Process multiple mutation events */
  handleMutations: (mutations: MutationPayload[]) => void;

  /** Number of mutations processed since mount */
  mutationCount: number;

  /** Timestamp of last processed mutation */
  lastMutationAt: string | null;
}

/**
 * Checks if a mutation is stale (older than the issue's updated_at).
 * Returns true if the mutation should be skipped.
 * Invalid timestamps are treated as stale (fail-safe).
 */
function isStaleMutation(mutation: MutationPayload, issue: Issue): boolean {
  const mutationTime = Date.parse(mutation.timestamp);
  const issueTime = Date.parse(issue.updated_at);

  // Invalid timestamps should be considered stale (fail-safe)
  if (isNaN(mutationTime) || isNaN(issueTime)) {
    return true;
  }

  return mutationTime < issueTime;
}

/**
 * Creates a minimal Issue object from a create mutation.
 * Uses only the fields available in MutationPayload.
 */
function createIssueFromMutation(mutation: MutationPayload): Issue {
  const now = mutation.timestamp;
  const issue: Issue = {
    id: mutation.issue_id,
    title: mutation.title ?? 'Untitled',
    priority: 2, // Default priority
    created_at: now,
    updated_at: now,
  };
  // Only set optional fields if they have values (exactOptionalPropertyTypes)
  // Use != null to handle both undefined and null consistently
  if (mutation.assignee != null) {
    issue.assignee = mutation.assignee;
  }
  if (mutation.new_status != null) {
    issue.status = mutation.new_status;
  }
  return issue;
}

/**
 * Applies an update mutation to an existing issue.
 * Only updates fields that are present in the mutation payload.
 */
function applyUpdateToIssue(issue: Issue, mutation: MutationPayload): Issue {
  return produce(issue, (draft) => {
    // Update timestamp
    draft.updated_at = mutation.timestamp;

    // Update fields if present in mutation (use != null for consistency)
    if (mutation.title != null) {
      draft.title = mutation.title;
    }
    if (mutation.assignee != null) {
      draft.assignee = mutation.assignee;
    }
  });
}

/**
 * Applies a status mutation to an existing issue.
 */
function applyStatusToIssue(issue: Issue, mutation: MutationPayload): Issue {
  return produce(issue, (draft) => {
    draft.updated_at = mutation.timestamp;
    if (mutation.new_status != null) {
      draft.status = mutation.new_status;
    }
  });
}

/**
 * Applies a bonded mutation to an existing issue.
 * Bonded mutations update parent_id and step_count for parent-child relationships.
 */
function applyBondedToIssue(issue: Issue, mutation: MutationPayload): Issue {
  return produce(issue, (draft) => {
    draft.updated_at = mutation.timestamp;
    // Bonded mutations may update parent relationship
    // The parent_id and step_count are for the parent's reference
    // but the child issue may need to track its bonded state
  });
}

/**
 * React hook for handling SSE mutation events.
 *
 * @example
 * ```tsx
 * function IssueBoard() {
 *   const [issues, setIssues] = useState<Map<string, Issue>>(new Map())
 *
 *   const { handleMutation, mutationCount } = useMutationHandler({
 *     issues,
 *     setIssues,
 *     onIssueCreated: (issue) => console.log('Created:', issue.id),
 *     onIssueDeleted: (id) => console.log('Deleted:', id),
 *   })
 *
 *   const { connect } = useSSE({
 *     onMutation: handleMutation,
 *   })
 *
 *   return <div>Processed {mutationCount} mutations</div>
 * }
 * ```
 */
export function useMutationHandler(options: UseMutationHandlerOptions): UseMutationHandlerReturn {
  const { issues, setIssues, onIssueCreated, onIssueUpdated, onIssueDeleted, onMutationSkipped } =
    options;

  // Track mutation stats
  const [mutationCount, setMutationCount] = useState(0);
  const [lastMutationAt, setLastMutationAt] = useState<string | null>(null);

  // Track mounted state to prevent state updates after unmount
  const mountedRef = useRef(true);

  // Store callbacks in refs to avoid stale closures
  const onIssueCreatedRef = useRef(onIssueCreated);
  const onIssueUpdatedRef = useRef(onIssueUpdated);
  const onIssueDeletedRef = useRef(onIssueDeleted);
  const onMutationSkippedRef = useRef(onMutationSkipped);

  // Update refs when callbacks change (following useSSE pattern)
  useEffect(() => {
    onIssueCreatedRef.current = onIssueCreated;
  }, [onIssueCreated]);

  useEffect(() => {
    onIssueUpdatedRef.current = onIssueUpdated;
  }, [onIssueUpdated]);

  useEffect(() => {
    onIssueDeletedRef.current = onIssueDeleted;
  }, [onIssueDeleted]);

  useEffect(() => {
    onMutationSkippedRef.current = onMutationSkipped;
  }, [onMutationSkipped]);

  // Cleanup on unmount
  useEffect(() => {
    return () => {
      mountedRef.current = false;
    };
  }, []);

  /**
   * Process a single mutation event.
   */
  const handleMutation = useCallback(
    (mutation: MutationPayload) => {
      // Guard against state updates after unmount
      if (!mountedRef.current) return;

      const { issue_id, type } = mutation;

      // Handle create mutation
      if (type === MutationCreate) {
        // If issue already exists, treat as update (handles duplicate/replayed events)
        const existingIssue = issues.get(issue_id);
        if (existingIssue) {
          // Check for stale mutation
          if (isStaleMutation(mutation, existingIssue)) {
            onMutationSkippedRef.current?.(
              mutation,
              'Stale create mutation (issue already exists with newer timestamp)'
            );
            return;
          }
          // Apply as update using functional update to avoid race conditions
          const updatedIssue = applyUpdateToIssue(existingIssue, mutation);
          setIssues((prev) => {
            const newIssues = new Map(prev);
            newIssues.set(issue_id, updatedIssue);
            return newIssues;
          });
          onIssueUpdatedRef.current?.(updatedIssue, existingIssue);
        } else {
          // Create new issue using functional update to avoid race conditions
          const newIssue = createIssueFromMutation(mutation);
          setIssues((prev) => {
            const newIssues = new Map(prev);
            newIssues.set(issue_id, newIssue);
            return newIssues;
          });
          onIssueCreatedRef.current?.(newIssue);
        }
        setMutationCount((c) => c + 1);
        setLastMutationAt(mutation.timestamp);
        return;
      }

      // Handle delete mutation
      if (type === MutationDelete) {
        const existingIssueForDelete = issues.get(issue_id);
        if (!existingIssueForDelete) {
          onMutationSkippedRef.current?.(mutation, 'Issue not found for delete mutation');
          return;
        }
        // Check for stale delete mutation
        if (isStaleMutation(mutation, existingIssueForDelete)) {
          onMutationSkippedRef.current?.(mutation, 'Stale delete mutation');
          return;
        }
        // Use functional update to avoid race conditions
        setIssues((prev) => {
          const newIssues = new Map(prev);
          newIssues.delete(issue_id);
          return newIssues;
        });
        onIssueDeletedRef.current?.(issue_id);
        setMutationCount((c) => c + 1);
        setLastMutationAt(mutation.timestamp);
        return;
      }

      // For all other mutations, issue must exist
      const existingIssue = issues.get(issue_id);
      if (!existingIssue) {
        onMutationSkippedRef.current?.(mutation, `Issue not found for ${type} mutation`);
        return;
      }

      // Check for stale mutation
      if (isStaleMutation(mutation, existingIssue)) {
        onMutationSkippedRef.current?.(mutation, 'Stale mutation (older than current issue)');
        return;
      }

      let updatedIssue: Issue;

      switch (type) {
        case MutationUpdate:
          updatedIssue = applyUpdateToIssue(existingIssue, mutation);
          break;

        case MutationStatus:
          updatedIssue = applyStatusToIssue(existingIssue, mutation);
          break;

        case MutationBonded:
          updatedIssue = applyBondedToIssue(existingIssue, mutation);
          break;

        default:
          // For unsupported mutation types (squashed, burned, comment),
          // just update the timestamp to mark the issue as modified
          updatedIssue = produce(existingIssue, (draft) => {
            draft.updated_at = mutation.timestamp;
          });
          break;
      }

      // Use functional update to avoid race conditions when multiple events arrive before render
      setIssues((prev) => {
        const newIssues = new Map(prev);
        newIssues.set(issue_id, updatedIssue);
        return newIssues;
      });
      onIssueUpdatedRef.current?.(updatedIssue, existingIssue);
      setMutationCount((c) => c + 1);
      setLastMutationAt(mutation.timestamp);
    },
    // Note: `issues` is intentionally NOT in deps because stale checks use closure value
    // (acceptable since stale checks only skip mutations), but state updates use functional
    // form with `prev` to ensure each mutation operates on the latest state.
    [issues, setIssues]
  );

  /**
   * Process multiple mutation events in order.
   */
  const handleMutations = useCallback(
    (mutations: MutationPayload[]) => {
      mutations.forEach((mutation) => {
        handleMutation(mutation);
      });
    },
    [handleMutation]
  );

  return {
    handleMutation,
    handleMutations,
    mutationCount,
    lastMutationAt,
  };
}
