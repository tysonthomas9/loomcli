/**
 * @vitest-environment jsdom
 */
import { renderHook, act } from "@testing-library/react";
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import React from "react";

import { useSessionManagement } from "../useCloseAllSessions";
import * as terminalApi from "@/api/terminal";

vi.mock("@/api/terminal", () => ({
  closeAllSessions: vi.fn(),
}));

function createArgs(
  overrides: Partial<Parameters<typeof useSessionManagement>[0]> = {},
) {
  return {
    setTabs: vi.fn() as React.Dispatch<React.SetStateAction<never[]>>,
    setActiveTabId: vi.fn() as React.Dispatch<React.SetStateAction<string>>,
    instanceRefs: { current: new Map() } as React.MutableRefObject<
      Map<string, unknown>
    >,
    initializedRef: { current: true } as React.MutableRefObject<boolean>,
    isActive: true,
    tabs: [],
    createTab: vi.fn().mockResolvedValue(undefined),
    linkToIssue: vi.fn().mockResolvedValue(undefined),
    backendName: "claude",
    ...overrides,
  };
}

describe("useSessionManagement", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    // Mock window.confirm
    vi.spyOn(window, "confirm").mockReturnValue(true);
  });

  afterEach(() => {
    vi.restoreAllMocks();
  });

  describe("handleCloseAll", () => {
    it("calls closeAllSessions API and clears state on confirm", async () => {
      vi.mocked(terminalApi.closeAllSessions).mockResolvedValue(undefined);
      const setTabs = vi.fn();
      const setActiveTabId = vi.fn();
      const instanceRefs = { current: new Map([["tab-1", {}]]) };
      const initializedRef = { current: true };

      const args = createArgs({
        setTabs: setTabs as unknown as React.Dispatch<
          React.SetStateAction<never[]>
        >,
        setActiveTabId: setActiveTabId as unknown as React.Dispatch<
          React.SetStateAction<string>
        >,
        instanceRefs: instanceRefs as React.MutableRefObject<
          Map<string, unknown>
        >,
        initializedRef: initializedRef as React.MutableRefObject<boolean>,
      });

      const { result } = renderHook(() => useSessionManagement(args));

      await act(async () => {
        result.current();
        // Wait for promise resolution
        await vi.runAllTimersAsync().catch(() => {});
        await new Promise((r) => setTimeout(r, 0));
      });

      expect(window.confirm).toHaveBeenCalledWith(
        "Close all terminal sessions? This cannot be undone.",
      );
      expect(terminalApi.closeAllSessions).toHaveBeenCalledTimes(1);
      expect(setTabs).toHaveBeenCalledWith([]);
      expect(setActiveTabId).toHaveBeenCalledWith("");
      expect(instanceRefs.current.size).toBe(0);
      expect(initializedRef.current).toBe(false);
    });

    it("does not call API when confirm is cancelled", () => {
      vi.spyOn(window, "confirm").mockReturnValue(false);

      const args = createArgs();

      const { result } = renderHook(() => useSessionManagement(args));

      act(() => {
        result.current();
      });

      expect(terminalApi.closeAllSessions).not.toHaveBeenCalled();
    });

    it("handles API error gracefully", async () => {
      const consoleSpy = vi
        .spyOn(console, "error")
        .mockImplementation(() => {});
      vi.mocked(terminalApi.closeAllSessions).mockRejectedValue(
        new Error("API error"),
      );

      const setTabs = vi.fn();
      const args = createArgs({
        setTabs: setTabs as unknown as React.Dispatch<
          React.SetStateAction<never[]>
        >,
      });

      const { result } = renderHook(() => useSessionManagement(args));

      await act(async () => {
        result.current();
        await new Promise((r) => setTimeout(r, 0));
      });

      expect(terminalApi.closeAllSessions).toHaveBeenCalledTimes(1);
      // setTabs should NOT be called on error
      expect(setTabs).not.toHaveBeenCalled();
      expect(consoleSpy).toHaveBeenCalled();

      consoleSpy.mockRestore();
    });
  });

  describe("issue-id auto-switch", () => {
    it("creates and switches to issue tab when issueId is provided", () => {
      const setTabs = vi.fn();
      const setActiveTabId = vi.fn();
      const createTab = vi.fn().mockResolvedValue(undefined);

      const args = createArgs({
        issueId: "PROJ-42",
        tabs: [],
        setTabs: setTabs as unknown as React.Dispatch<
          React.SetStateAction<never[]>
        >,
        setActiveTabId: setActiveTabId as unknown as React.Dispatch<
          React.SetStateAction<string>
        >,
        createTab,
        initializedRef: { current: true } as React.MutableRefObject<boolean>,
      });

      renderHook(() => useSessionManagement(args));

      // Should create a new tab with sanitized session name
      expect(setTabs).toHaveBeenCalled();
      expect(setActiveTabId).toHaveBeenCalledWith("issue-PROJ-42");
      expect(createTab).toHaveBeenCalledWith(
        "issue-PROJ-42",
        "issue-PROJ-42",
        0,
      );
    });

    it("switches to existing tab when issue tab already exists", () => {
      const setTabs = vi.fn();
      const setActiveTabId = vi.fn();
      const createTab = vi.fn().mockResolvedValue(undefined);

      const args = createArgs({
        issueId: "PROJ-42",
        tabs: [
          {
            id: "issue-PROJ-42",
            label: "issue-PROJ-42",
            sessionName: "issue-PROJ-42",
            connectionState: "disconnected" as const,
            backendName: "claude",
          },
        ],
        setTabs: setTabs as unknown as React.Dispatch<
          React.SetStateAction<never[]>
        >,
        setActiveTabId: setActiveTabId as unknown as React.Dispatch<
          React.SetStateAction<string>
        >,
        createTab,
        initializedRef: { current: true } as React.MutableRefObject<boolean>,
      });

      renderHook(() => useSessionManagement(args));

      expect(setActiveTabId).toHaveBeenCalledWith("issue-PROJ-42");
      // Should NOT create a new tab
      expect(createTab).not.toHaveBeenCalled();
    });

    it("does nothing when issueId is undefined", () => {
      const setTabs = vi.fn();
      const setActiveTabId = vi.fn();

      const args = createArgs({
        setTabs: setTabs as unknown as React.Dispatch<
          React.SetStateAction<never[]>
        >,
        setActiveTabId: setActiveTabId as unknown as React.Dispatch<
          React.SetStateAction<string>
        >,
        initializedRef: { current: true } as React.MutableRefObject<boolean>,
      });

      renderHook(() => useSessionManagement(args));

      expect(setTabs).not.toHaveBeenCalled();
      expect(setActiveTabId).not.toHaveBeenCalled();
    });

    it("does nothing when not initialized", () => {
      const setTabs = vi.fn();
      const setActiveTabId = vi.fn();

      const args = createArgs({
        issueId: "PROJ-42",
        setTabs: setTabs as unknown as React.Dispatch<
          React.SetStateAction<never[]>
        >,
        setActiveTabId: setActiveTabId as unknown as React.Dispatch<
          React.SetStateAction<string>
        >,
        initializedRef: { current: false } as React.MutableRefObject<boolean>,
      });

      renderHook(() => useSessionManagement(args));

      expect(setTabs).not.toHaveBeenCalled();
      expect(setActiveTabId).not.toHaveBeenCalled();
    });

    it("sanitizes issue ID with dots in session name", () => {
      const setActiveTabId = vi.fn();
      const createTab = vi.fn().mockResolvedValue(undefined);

      const args = createArgs({
        issueId: "proj.sub.123",
        setActiveTabId: setActiveTabId as unknown as React.Dispatch<
          React.SetStateAction<string>
        >,
        createTab,
        initializedRef: { current: true } as React.MutableRefObject<boolean>,
      });

      renderHook(() => useSessionManagement(args));

      // Dots should be replaced with dashes
      expect(setActiveTabId).toHaveBeenCalledWith("issue-proj-sub-123");
    });

    it("calls linkToIssue after createTab succeeds for new issue tab", async () => {
      const createTab = vi.fn().mockResolvedValue(undefined);
      const linkToIssue = vi.fn().mockResolvedValue(undefined);

      const args = createArgs({
        issueId: "PROJ-99",
        tabs: [],
        createTab,
        linkToIssue,
        initializedRef: { current: true } as React.MutableRefObject<boolean>,
      });

      renderHook(() => useSessionManagement(args));

      // Wait for promise chain (createTab -> linkToIssue) to resolve
      await act(async () => {
        await new Promise((r) => setTimeout(r, 0));
      });

      expect(createTab).toHaveBeenCalledWith(
        "issue-PROJ-99",
        "issue-PROJ-99",
        0,
      );
      expect(linkToIssue).toHaveBeenCalledWith("issue-PROJ-99", "PROJ-99");
    });

    it("calls linkToIssue after createTab for sanitized issue ID", async () => {
      const createTab = vi.fn().mockResolvedValue(undefined);
      const linkToIssue = vi.fn().mockResolvedValue(undefined);

      const args = createArgs({
        issueId: "proj.sub.42",
        tabs: [],
        createTab,
        linkToIssue,
        initializedRef: { current: true } as React.MutableRefObject<boolean>,
      });

      renderHook(() => useSessionManagement(args));

      await act(async () => {
        await new Promise((r) => setTimeout(r, 0));
      });

      expect(createTab).toHaveBeenCalledWith(
        "issue-proj-sub-42",
        "issue-proj-sub-42",
        0,
      );
      expect(linkToIssue).toHaveBeenCalledWith(
        "issue-proj-sub-42",
        "proj.sub.42",
      );
    });

    it("does not call linkToIssue when switching to existing tab", async () => {
      const linkToIssue = vi.fn().mockResolvedValue(undefined);
      const createTab = vi.fn().mockResolvedValue(undefined);

      const args = createArgs({
        issueId: "PROJ-42",
        tabs: [
          {
            id: "issue-PROJ-42",
            label: "issue-PROJ-42",
            sessionName: "issue-PROJ-42",
            connectionState: "disconnected" as const,
            backendName: "claude",
          },
        ],
        createTab,
        linkToIssue,
        initializedRef: { current: true } as React.MutableRefObject<boolean>,
      });

      renderHook(() => useSessionManagement(args));

      await act(async () => {
        await new Promise((r) => setTimeout(r, 0));
      });

      expect(createTab).not.toHaveBeenCalled();
      expect(linkToIssue).not.toHaveBeenCalled();
    });

    it("logs error when linkToIssue fails after createTab", async () => {
      const consoleSpy = vi
        .spyOn(console, "error")
        .mockImplementation(() => {});
      const createTab = vi.fn().mockResolvedValue(undefined);
      const linkToIssue = vi.fn().mockRejectedValue(new Error("link failed"));

      const args = createArgs({
        issueId: "PROJ-50",
        tabs: [],
        createTab,
        linkToIssue,
        initializedRef: { current: true } as React.MutableRefObject<boolean>,
      });

      renderHook(() => useSessionManagement(args));

      await act(async () => {
        await new Promise((r) => setTimeout(r, 0));
      });

      expect(createTab).toHaveBeenCalled();
      // The .catch handler logs the error
      expect(consoleSpy).toHaveBeenCalled();

      consoleSpy.mockRestore();
    });

    it("does not call linkToIssue when createTab fails", async () => {
      const consoleSpy = vi
        .spyOn(console, "error")
        .mockImplementation(() => {});
      const createTab = vi.fn().mockRejectedValue(new Error("create failed"));
      const linkToIssue = vi.fn().mockResolvedValue(undefined);

      const args = createArgs({
        issueId: "PROJ-60",
        tabs: [],
        createTab,
        linkToIssue,
        initializedRef: { current: true } as React.MutableRefObject<boolean>,
      });

      renderHook(() => useSessionManagement(args));

      await act(async () => {
        await new Promise((r) => setTimeout(r, 0));
      });

      expect(createTab).toHaveBeenCalled();
      // linkToIssue should NOT be called if createTab failed
      expect(linkToIssue).not.toHaveBeenCalled();
      expect(consoleSpy).toHaveBeenCalled();

      consoleSpy.mockRestore();
    });
  });
});
