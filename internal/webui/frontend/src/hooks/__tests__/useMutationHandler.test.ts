/**
 * @vitest-environment jsdom
 */

/**
 * Unit tests for useMutationHandler hook — specifically for the extended
 * applyUpdateToIssue logic that handles new_status and priority fields
 * (UI flicker fix: loomcli-kzdf4).
 */

import { renderHook, act } from "@testing-library/react";
import { describe, it, expect, vi } from "vitest";

import { useMutationHandler } from "../useMutationHandler";
import type { MutationPayload } from "@/api/sse";
import type { Issue } from "@/types";

/**
 * Helper to create a test issue with required fields.
 */
function createTestIssue(overrides: Partial<Issue> = {}): Issue {
  return {
    id: "issue-1",
    title: "Test Issue",
    priority: 2,
    created_at: "2025-01-23T10:00:00Z",
    updated_at: "2025-01-23T10:00:00Z",
    ...overrides,
  };
}

/**
 * Helper to create a mutation payload.
 */
function createMutationPayload(
  overrides: Partial<MutationPayload> = {},
): MutationPayload {
  return {
    type: "update",
    issue_id: "issue-1",
    timestamp: "2025-01-23T12:00:00Z",
    ...overrides,
  };
}

/**
 * Helper to set up the hook with a pre-populated issues Map.
 */
function setupHookWithIssues(initialIssues: Issue[]) {
  const initialMap = new Map(initialIssues.map((i) => [i.id, i]));
  let currentMap = initialMap;

  const setIssues = vi.fn(
    (
      updater:
        | Map<string, Issue>
        | ((prev: Map<string, Issue>) => Map<string, Issue>),
    ) => {
      if (typeof updater === "function") {
        currentMap = updater(currentMap);
      } else {
        currentMap = updater;
      }
    },
  );

  const onRefreshRequired = vi.fn();
  const onIssueCreated = vi.fn();
  const onIssueUpdated = vi.fn();
  const onMutationSkipped = vi.fn();

  const hookResult = renderHook(() =>
    useMutationHandler({
      issues: currentMap,
      setIssues: setIssues as any,
      onRefreshRequired,
      onIssueCreated,
      onIssueUpdated,
      onMutationSkipped,
    }),
  );

  return {
    ...hookResult,
    setIssues,
    onRefreshRequired,
    onIssueCreated,
    onIssueUpdated,
    onMutationSkipped,
    getIssue: (id: string) => currentMap.get(id),
    getCurrentMap: () => currentMap,
  };
}

describe("useMutationHandler - applyUpdateToIssue extended fields", () => {
  describe("MutationUpdate with new_status", () => {
    it("applies new_status field to issue when present", () => {
      const issue = createTestIssue({ status: "open" });
      const hook = setupHookWithIssues([issue]);

      act(() => {
        hook.result.current.handleMutation(
          createMutationPayload({
            type: "update",
            issue_id: "issue-1",
            new_status: "in_progress",
            timestamp: "2025-01-23T12:00:00Z",
          }),
        );
      });

      const updated = hook.getIssue("issue-1");
      expect(updated).toBeDefined();
      expect(updated!.status).toBe("in_progress");
    });

    it("does not overwrite status when new_status is absent", () => {
      const issue = createTestIssue({ status: "open" });
      const hook = setupHookWithIssues([issue]);

      act(() => {
        hook.result.current.handleMutation(
          createMutationPayload({
            type: "update",
            issue_id: "issue-1",
            title: "Updated Title",
            timestamp: "2025-01-23T12:00:00Z",
          }),
        );
      });

      const updated = hook.getIssue("issue-1");
      expect(updated).toBeDefined();
      expect(updated!.status).toBe("open");
      expect(updated!.title).toBe("Updated Title");
    });
  });

  describe("MutationUpdate with priority", () => {
    it("applies priority field to issue when present", () => {
      const issue = createTestIssue({ priority: 2 });
      const hook = setupHookWithIssues([issue]);

      act(() => {
        hook.result.current.handleMutation(
          createMutationPayload({
            type: "update",
            issue_id: "issue-1",
            priority: 0,
            timestamp: "2025-01-23T12:00:00Z",
          }),
        );
      });

      const updated = hook.getIssue("issue-1");
      expect(updated).toBeDefined();
      expect(updated!.priority).toBe(0);
    });

    it("does not overwrite priority when priority is absent", () => {
      const issue = createTestIssue({ priority: 3 });
      const hook = setupHookWithIssues([issue]);

      act(() => {
        hook.result.current.handleMutation(
          createMutationPayload({
            type: "update",
            issue_id: "issue-1",
            title: "New Title",
            timestamp: "2025-01-23T12:00:00Z",
          }),
        );
      });

      const updated = hook.getIssue("issue-1");
      expect(updated).toBeDefined();
      expect(updated!.priority).toBe(3);
    });

    it("applies priority value of 0 (P0/critical) correctly", () => {
      const issue = createTestIssue({ priority: 4 });
      const hook = setupHookWithIssues([issue]);

      act(() => {
        hook.result.current.handleMutation(
          createMutationPayload({
            type: "update",
            issue_id: "issue-1",
            priority: 0,
            timestamp: "2025-01-23T12:00:00Z",
          }),
        );
      });

      const updated = hook.getIssue("issue-1");
      expect(updated).toBeDefined();
      // Priority 0 is valid (P0/critical) and must not be treated as falsy
      expect(updated!.priority).toBe(0);
    });
  });

  describe("MutationUpdate with both new_status and priority", () => {
    it("applies both new_status and priority in a single update", () => {
      const issue = createTestIssue({ status: "open", priority: 2 });
      const hook = setupHookWithIssues([issue]);

      act(() => {
        hook.result.current.handleMutation(
          createMutationPayload({
            type: "update",
            issue_id: "issue-1",
            new_status: "completed",
            priority: 1,
            timestamp: "2025-01-23T12:00:00Z",
          }),
        );
      });

      const updated = hook.getIssue("issue-1");
      expect(updated).toBeDefined();
      expect(updated!.status).toBe("completed");
      expect(updated!.priority).toBe(1);
    });
  });

  describe("MutationUpdate with title and assignee (pre-existing fields)", () => {
    it("still applies title and assignee correctly", () => {
      const issue = createTestIssue({
        title: "Old Title",
        assignee: "alice",
      });
      const hook = setupHookWithIssues([issue]);

      act(() => {
        hook.result.current.handleMutation(
          createMutationPayload({
            type: "update",
            issue_id: "issue-1",
            title: "New Title",
            assignee: "bob",
            timestamp: "2025-01-23T12:00:00Z",
          }),
        );
      });

      const updated = hook.getIssue("issue-1");
      expect(updated).toBeDefined();
      expect(updated!.title).toBe("New Title");
      expect(updated!.assignee).toBe("bob");
    });
  });

  describe("MutationStatus applies new_status", () => {
    it("applies new_status from a status mutation event", () => {
      const issue = createTestIssue({ status: "open" });
      const hook = setupHookWithIssues([issue]);

      act(() => {
        hook.result.current.handleMutation(
          createMutationPayload({
            type: "status",
            issue_id: "issue-1",
            old_status: "open",
            new_status: "in_progress",
            timestamp: "2025-01-23T12:00:00Z",
          }),
        );
      });

      const updated = hook.getIssue("issue-1");
      expect(updated).toBeDefined();
      expect(updated!.status).toBe("in_progress");
    });
  });

  describe("MutationCreate with new_status and priority", () => {
    it("creates new issue with status from new_status field", () => {
      const hook = setupHookWithIssues([]);

      act(() => {
        hook.result.current.handleMutation(
          createMutationPayload({
            type: "create",
            issue_id: "issue-new",
            title: "New Issue",
            new_status: "in_progress",
            timestamp: "2025-01-23T12:00:00Z",
          }),
        );
      });

      const created = hook.getIssue("issue-new");
      expect(created).toBeDefined();
      expect(created!.status).toBe("in_progress");
      expect(created!.title).toBe("New Issue");
    });

    it("applies priority when create mutation is treated as update for existing issue", () => {
      const existingIssue = createTestIssue({
        id: "issue-dup",
        priority: 2,
        status: "open",
      });
      const hook = setupHookWithIssues([existingIssue]);

      act(() => {
        hook.result.current.handleMutation(
          createMutationPayload({
            type: "create",
            issue_id: "issue-dup",
            title: "Duplicate Create",
            priority: 1,
            new_status: "in_progress",
            timestamp: "2025-01-23T12:00:00Z",
          }),
        );
      });

      const updated = hook.getIssue("issue-dup");
      expect(updated).toBeDefined();
      // When a create arrives for an existing issue, it's treated as update
      expect(updated!.priority).toBe(1);
      expect(updated!.status).toBe("in_progress");
    });
  });

  describe("MutationRefresh triggers onRefreshRequired", () => {
    it("calls onRefreshRequired for refresh mutations", () => {
      const hook = setupHookWithIssues([]);

      act(() => {
        hook.result.current.handleMutation(
          createMutationPayload({
            type: "refresh",
            issue_id: "",
            timestamp: "2025-01-23T12:00:00Z",
          }),
        );
      });

      expect(hook.onRefreshRequired).toHaveBeenCalledTimes(1);
    });
  });

  describe("update timestamp is always applied", () => {
    it("updates the updated_at timestamp on update mutations", () => {
      const issue = createTestIssue({
        updated_at: "2025-01-23T10:00:00Z",
      });
      const hook = setupHookWithIssues([issue]);

      act(() => {
        hook.result.current.handleMutation(
          createMutationPayload({
            type: "update",
            issue_id: "issue-1",
            timestamp: "2025-01-23T14:00:00Z",
          }),
        );
      });

      const updated = hook.getIssue("issue-1");
      expect(updated).toBeDefined();
      expect(updated!.updated_at).toBe("2025-01-23T14:00:00Z");
    });
  });
});
