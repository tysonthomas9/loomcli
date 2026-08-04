/**
 * @vitest-environment jsdom
 */

import { renderHook } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";

import {
  useWorkspaceViewData,
  useWorkspaceViewActions,
  NO_WORKSPACE_VIEW_DATA,
  NO_WORKSPACE_VIEW_ACTIONS,
  type WorkspaceViewData,
  type WorkspaceViewActions,
  type WorkspaceViewRouteContext,
} from "../WorkspaceViewContext";

const outlet = vi.hoisted(() => ({
  value: null as WorkspaceViewRouteContext | null,
}));

vi.mock("react-router-dom", async (importOriginal) => {
  const actual = await importOriginal<typeof import("react-router-dom")>();
  return {
    ...actual,
    useOutletContext: () => outlet.value,
  };
});

function buildTestData(
  overrides: Partial<WorkspaceViewData> = {},
): WorkspaceViewData {
  return {
    ...NO_WORKSPACE_VIEW_DATA,
    issues: [
      { id: "ISS-1", title: "Test issue" } as WorkspaceViewData["issues"][0],
    ],
    isLoading: true,
    error: "test error",
    connectionState: "connected",
    workspaceId: "ws-42",
    ...overrides,
  };
}

function buildTestActions(
  overrides: Partial<WorkspaceViewActions> = {},
): WorkspaceViewActions {
  return {
    ...NO_WORKSPACE_VIEW_ACTIONS,
    ...overrides,
  };
}

describe("WorkspaceView route context", () => {
  beforeEach(() => {
    outlet.value = null;
  });

  describe("safe defaults outside a composed route", () => {
    it("returns the canonical data defaults", () => {
      const { result } = renderHook(() => useWorkspaceViewData());

      expect(result.current).toBe(NO_WORKSPACE_VIEW_DATA);
      expect(result.current.issues).toEqual([]);
      expect(result.current.filteredIssues).toEqual([]);
      expect(result.current.isLoading).toBe(false);
      expect(result.current.error).toBeNull();
      expect(result.current.connectionState).toBe("disconnected");
      expect(result.current.activeView).toBe("kanban");
      expect(result.current.filters).toEqual({});
    });

    it("returns safe synchronous and asynchronous actions", async () => {
      const { result } = renderHook(() => useWorkspaceViewActions());

      expect(result.current).toBe(NO_WORKSPACE_VIEW_ACTIONS);
      expect(() => result.current.refetch()).not.toThrow();
      expect(() => result.current.fetchIssue("ISS-1")).not.toThrow();
      await expect(
        result.current.updateIssueStatus("ISS-1", "open" as never),
      ).resolves.toBeUndefined();
      await expect(result.current.handleCopyLink()).resolves.toBeUndefined();
      expect(result.current.showToast("test")).toBe("");
    });
  });

  describe("active Outlet composition", () => {
    it("returns the exact route-scoped data and actions", () => {
      const data = buildTestData();
      const refetch = vi.fn();
      const actions = buildTestActions({ refetch });
      outlet.value = { workspaceView: { data, actions } };

      const { result: dataResult } = renderHook(() => useWorkspaceViewData());
      const { result: actionResult } = renderHook(() =>
        useWorkspaceViewActions(),
      );

      expect(dataResult.current).toBe(data);
      expect(dataResult.current.workspaceId).toBe("ws-42");
      expect(actionResult.current).toBe(actions);
      expect(actionResult.current.refetch).toBe(refetch);
    });

    it("tracks replacement route composition without hidden provider state", () => {
      const actions = buildTestActions();
      outlet.value = {
        workspaceView: {
          data: buildTestData({ workspaceId: "ws-1" }),
          actions,
        },
      };
      const { result, rerender } = renderHook(() => useWorkspaceViewData());
      expect(result.current.workspaceId).toBe("ws-1");

      outlet.value = {
        workspaceView: {
          data: buildTestData({ workspaceId: "ws-2" }),
          actions,
        },
      };
      rerender();

      expect(result.current.workspaceId).toBe("ws-2");
    });

    it("does not confuse Source Control-only context with workspace views", () => {
      outlet.value = { sourceControl: {} } as WorkspaceViewRouteContext;

      const { result: dataResult } = renderHook(() => useWorkspaceViewData());
      const { result: actionResult } = renderHook(() =>
        useWorkspaceViewActions(),
      );

      expect(dataResult.current).toBe(NO_WORKSPACE_VIEW_DATA);
      expect(actionResult.current).toBe(NO_WORKSPACE_VIEW_ACTIONS);
    });
  });
});
