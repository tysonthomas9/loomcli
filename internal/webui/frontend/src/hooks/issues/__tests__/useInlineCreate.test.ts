/**
 * @vitest-environment jsdom
 */

/**
 * Unit tests for useInlineCreate hook.
 * Covers the idle → adding → submitting → idle/error state machine,
 * input validation, success/error callbacks, and double-submit prevention.
 */

import { renderHook, act } from "@testing-library/react";
import { describe, it, expect, vi } from "vitest";

import type { Issue } from "@/types";

import { useInlineCreate } from "../useInlineCreate";

/** Helper to create a test Issue with required fields. */
function createTestIssue(overrides: Partial<Issue> = {}): Issue {
  return {
    id: "new-issue-1",
    title: "Created Issue",
    priority: 2,
    created_at: "2026-01-01T00:00:00Z",
    updated_at: "2026-01-01T00:00:00Z",
    ...overrides,
  };
}

/** Helper to set up the hook with default mocks. */
function setupHook(
  overrides: {
    createFn?: (title: string) => Promise<Issue>;
    onSuccess?: (issue: Issue) => void;
    onError?: (error: string) => void;
  } = {},
) {
  const createFn =
    overrides.createFn ?? vi.fn(() => Promise.resolve(createTestIssue()));
  const onSuccess = overrides.onSuccess ?? vi.fn();
  const onError = overrides.onError ?? vi.fn();

  const hookResult = renderHook(() =>
    useInlineCreate({ createFn, onSuccess, onError }),
  );

  return { ...hookResult, createFn, onSuccess, onError };
}

describe("useInlineCreate", () => {
  describe("initial state", () => {
    it("starts in idle state", () => {
      const { result } = setupHook();

      expect(result.current.isAdding).toBe(false);
      expect(result.current.isSubmitting).toBe(false);
      expect(result.current.error).toBeNull();
    });
  });

  describe("startAdding", () => {
    it("sets isAdding to true", () => {
      const { result } = setupHook();

      act(() => {
        result.current.startAdding();
      });

      expect(result.current.isAdding).toBe(true);
      expect(result.current.isSubmitting).toBe(false);
      expect(result.current.error).toBeNull();
    });

    it("clears any previous error when starting", async () => {
      const createFn = vi.fn(() => Promise.reject(new Error("fail")));
      const { result } = setupHook({ createFn });

      // Enter adding state and trigger an error
      act(() => {
        result.current.startAdding();
      });

      await act(async () => {
        await result.current.submitTitle("Test");
      });

      expect(result.current.error).toBe("fail");

      // Start adding again — error should be cleared
      act(() => {
        result.current.startAdding();
      });

      expect(result.current.error).toBeNull();
    });
  });

  describe("cancelAdding", () => {
    it("resets to idle state", () => {
      const { result } = setupHook();

      act(() => {
        result.current.startAdding();
      });

      expect(result.current.isAdding).toBe(true);

      act(() => {
        result.current.cancelAdding();
      });

      expect(result.current.isAdding).toBe(false);
      expect(result.current.isSubmitting).toBe(false);
      expect(result.current.error).toBeNull();
    });

    it("resets error state on cancel", async () => {
      const createFn = vi.fn(() => Promise.reject(new Error("fail")));
      const { result } = setupHook({ createFn });

      act(() => {
        result.current.startAdding();
      });

      await act(async () => {
        await result.current.submitTitle("Test");
      });

      expect(result.current.error).toBe("fail");

      act(() => {
        result.current.cancelAdding();
      });

      expect(result.current.isAdding).toBe(false);
      expect(result.current.error).toBeNull();
    });
  });

  describe("submitTitle validation", () => {
    it("does not call createFn with empty string", async () => {
      const createFn = vi.fn(() => Promise.resolve(createTestIssue()));
      const { result } = setupHook({ createFn });

      act(() => {
        result.current.startAdding();
      });

      await act(async () => {
        await result.current.submitTitle("");
      });

      expect(createFn).not.toHaveBeenCalled();
    });

    it("does not call createFn with whitespace-only string", async () => {
      const createFn = vi.fn(() => Promise.resolve(createTestIssue()));
      const { result } = setupHook({ createFn });

      act(() => {
        result.current.startAdding();
      });

      await act(async () => {
        await result.current.submitTitle("   ");
      });

      expect(createFn).not.toHaveBeenCalled();
    });

    it("does not call createFn with tab/newline whitespace", async () => {
      const createFn = vi.fn(() => Promise.resolve(createTestIssue()));
      const { result } = setupHook({ createFn });

      act(() => {
        result.current.startAdding();
      });

      await act(async () => {
        await result.current.submitTitle("\t\n ");
      });

      expect(createFn).not.toHaveBeenCalled();
    });
  });

  describe("submitTitle success", () => {
    it("calls createFn with trimmed title", async () => {
      const createFn = vi.fn(() => Promise.resolve(createTestIssue()));
      const { result } = setupHook({ createFn });

      act(() => {
        result.current.startAdding();
      });

      await act(async () => {
        await result.current.submitTitle("  Valid Title  ");
      });

      expect(createFn).toHaveBeenCalledWith("Valid Title");
    });

    it("enters submitting state while createFn is in progress", async () => {
      let resolveCreate: (issue: Issue) => void;
      const createFn = vi.fn(
        () =>
          new Promise<Issue>((resolve) => {
            resolveCreate = resolve;
          }),
      );
      const { result } = setupHook({ createFn });

      act(() => {
        result.current.startAdding();
      });

      // Start the submission without awaiting
      let submitPromise: Promise<void>;
      act(() => {
        submitPromise = result.current.submitTitle("Valid");
      });

      // Should be in submitting state
      expect(result.current.isSubmitting).toBe(true);
      expect(result.current.isAdding).toBe(true);

      // Resolve the promise
      await act(async () => {
        resolveCreate!(createTestIssue());
        await submitPromise;
      });

      // Should be back to idle
      expect(result.current.isSubmitting).toBe(false);
      expect(result.current.isAdding).toBe(false);
    });

    it("resets to idle on success", async () => {
      const { result } = setupHook();

      act(() => {
        result.current.startAdding();
      });

      await act(async () => {
        await result.current.submitTitle("Valid");
      });

      expect(result.current.isAdding).toBe(false);
      expect(result.current.isSubmitting).toBe(false);
      expect(result.current.error).toBeNull();
    });

    it("calls onSuccess callback with the created issue", async () => {
      const issue = createTestIssue({ id: "new-42", title: "New Task" });
      const createFn = vi.fn(() => Promise.resolve(issue));
      const onSuccess = vi.fn();
      const { result } = setupHook({ createFn, onSuccess });

      act(() => {
        result.current.startAdding();
      });

      await act(async () => {
        await result.current.submitTitle("New Task");
      });

      expect(onSuccess).toHaveBeenCalledTimes(1);
      expect(onSuccess).toHaveBeenCalledWith(issue);
    });
  });

  describe("submitTitle error", () => {
    it("stays in adding state with error message on failure", async () => {
      const createFn = vi.fn(() => Promise.reject(new Error("Network error")));
      const { result } = setupHook({ createFn });

      act(() => {
        result.current.startAdding();
      });

      await act(async () => {
        await result.current.submitTitle("Valid");
      });

      expect(result.current.isAdding).toBe(true);
      expect(result.current.isSubmitting).toBe(false);
      expect(result.current.error).toBe("Network error");
    });

    it("calls onError callback with error message", async () => {
      const createFn = vi.fn(() => Promise.reject(new Error("Server error")));
      const onError = vi.fn();
      const { result } = setupHook({ createFn, onError });

      act(() => {
        result.current.startAdding();
      });

      await act(async () => {
        await result.current.submitTitle("Valid");
      });

      expect(onError).toHaveBeenCalledTimes(1);
      expect(onError).toHaveBeenCalledWith("Server error");
    });

    it("uses fallback message for non-Error exceptions", async () => {
      const createFn = vi.fn(() => Promise.reject("string error"));
      const onError = vi.fn();
      const { result } = setupHook({ createFn, onError });

      act(() => {
        result.current.startAdding();
      });

      await act(async () => {
        await result.current.submitTitle("Valid");
      });

      expect(result.current.error).toBe("Failed to create");
      expect(onError).toHaveBeenCalledWith("Failed to create");
    });
  });

  describe("double-submit prevention", () => {
    it("prevents double-submit while submitting", async () => {
      let resolveCreate: (issue: Issue) => void;
      const createFn = vi.fn(
        () =>
          new Promise<Issue>((resolve) => {
            resolveCreate = resolve;
          }),
      );
      const { result } = setupHook({ createFn });

      act(() => {
        result.current.startAdding();
      });

      // Start first submission
      let submitPromise1: Promise<void>;
      act(() => {
        submitPromise1 = result.current.submitTitle("First");
      });

      expect(result.current.isSubmitting).toBe(true);

      // Attempt second submission while first is in progress
      await act(async () => {
        await result.current.submitTitle("Second");
      });

      // createFn should have been called only once
      expect(createFn).toHaveBeenCalledTimes(1);
      expect(createFn).toHaveBeenCalledWith("First");

      // Resolve the first submission
      await act(async () => {
        resolveCreate!(createTestIssue());
        await submitPromise1!;
      });
    });
  });

  describe("unmount safety", () => {
    it("does not update state after unmount on success", async () => {
      let resolveCreate: (issue: Issue) => void;
      const createFn = vi.fn(
        () =>
          new Promise<Issue>((resolve) => {
            resolveCreate = resolve;
          }),
      );
      const onSuccess = vi.fn();
      const { result, unmount } = setupHook({ createFn, onSuccess });

      act(() => {
        result.current.startAdding();
      });

      let submitPromise: Promise<void>;
      act(() => {
        submitPromise = result.current.submitTitle("Valid");
      });

      // Unmount before resolving
      unmount();

      // Resolve after unmount — should not throw or call onSuccess
      await act(async () => {
        resolveCreate!(createTestIssue());
        await submitPromise;
      });

      expect(onSuccess).not.toHaveBeenCalled();
    });

    it("does not update state after unmount on error", async () => {
      let rejectCreate: (err: Error) => void;
      const createFn = vi.fn(
        () =>
          new Promise<Issue>((_, reject) => {
            rejectCreate = reject;
          }),
      );
      const onError = vi.fn();
      const { result, unmount } = setupHook({ createFn, onError });

      act(() => {
        result.current.startAdding();
      });

      let submitPromise: Promise<void>;
      act(() => {
        submitPromise = result.current.submitTitle("Valid");
      });

      // Unmount before rejecting
      unmount();

      // Reject after unmount — should not throw or call onError
      await act(async () => {
        rejectCreate!(new Error("Network error"));
        await submitPromise;
      });

      expect(onError).not.toHaveBeenCalled();
    });
  });
});
