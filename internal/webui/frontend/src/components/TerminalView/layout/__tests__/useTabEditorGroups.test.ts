// @vitest-environment jsdom

import { act, renderHook } from "@testing-library/react";
import { describe, expect, it } from "vitest";

import {
  reconcileTabEditorGroups,
  useTabEditorGroups,
} from "../useTabEditorGroups";

describe("reconcileTabEditorGroups", () => {
  it("adds new tab ids to the first group", () => {
    const next = reconcileTabEditorGroups(
      [{ tabIds: ["a"], activeTabId: "a" }],
      ["a", "b"],
      "a",
    );
    expect(next).toEqual([{ tabIds: ["a", "b"], activeTabId: "a" }]);
  });

  it("removes closed tab ids from all groups", () => {
    const next = reconcileTabEditorGroups(
      [
        { tabIds: ["a", "b"], activeTabId: "a" },
        { tabIds: ["c"], activeTabId: "c" },
      ],
      ["a", "c"],
      "a",
    );
    expect(next).toEqual([
      { tabIds: ["a"], activeTabId: "a" },
      { tabIds: ["c"], activeTabId: "c" },
    ]);
  });

  it("adopts the live active tab within the group that contains it", () => {
    // Single-group mode never calls activateInGroup, so the group active
    // must follow the live active id or splitting moves the wrong tab.
    const next = reconcileTabEditorGroups(
      [{ tabIds: ["a", "b"], activeTabId: "a" }],
      ["a", "b"],
      "b",
    );
    expect(next).toEqual([{ tabIds: ["a", "b"], activeTabId: "b" }]);
  });

  it("activates a newly created tab in the group it joins", () => {
    // A new terminal created in split mode is appended to group 0 and must
    // become visible there, not stay hidden behind the previous active tab.
    const next = reconcileTabEditorGroups(
      [
        { tabIds: ["a"], activeTabId: "a" },
        { tabIds: ["b"], activeTabId: "b" },
      ],
      ["a", "b", "new"],
      "new",
    );
    expect(next).toEqual([
      { tabIds: ["a", "new"], activeTabId: "new" },
      { tabIds: ["b"], activeTabId: "b" },
    ]);
  });

  it("leaves other groups' active tabs untouched", () => {
    const next = reconcileTabEditorGroups(
      [
        { tabIds: ["a", "b"], activeTabId: "b" },
        { tabIds: ["c", "d"], activeTabId: "c" },
      ],
      ["a", "b", "c", "d"],
      "a",
    );
    expect(next).toEqual([
      { tabIds: ["a", "b"], activeTabId: "a" },
      { tabIds: ["c", "d"], activeTabId: "c" },
    ]);
  });
});

describe("useTabEditorGroups", () => {
  it("splits the currently selected tab after a tab switch (no stale active)", () => {
    const { result, rerender } = renderHook(
      ({ active }: { active: string }) =>
        useTabEditorGroups(["a", "b"], active, "ws-1"),
      { initialProps: { active: "a" } },
    );

    // User switches to tab b (non-split mode: only the live active changes).
    rerender({ active: "b" });

    act(() => {
      result.current.splitActiveTab();
    });

    expect(result.current.groups).toEqual([
      { tabIds: ["a"], activeTabId: "a" },
      { tabIds: ["b"], activeTabId: "b" },
    ]);
  });

  it("moves the active tab into a right group when split is requested", () => {
    const { result } = renderHook(() =>
      useTabEditorGroups(["a", "b", "c"], "b", "ws-1"),
    );

    act(() => {
      result.current.splitActiveTab();
    });

    expect(result.current.isSplit).toBe(true);
    expect(result.current.groups).toEqual([
      { tabIds: ["a", "c"], activeTabId: "a" },
      { tabIds: ["b"], activeTabId: "b" },
    ]);
  });

  it("moves a tab into another editor group on drop", () => {
    const { result } = renderHook(() =>
      useTabEditorGroups(["a", "b", "c"], "b", "ws-1"),
    );

    act(() => {
      result.current.splitActiveTab();
    });

    act(() => {
      result.current.handleGroupDragStart(1, "b");
      result.current.moveTabToGroup(0);
    });

    expect(result.current.isSplit).toBe(false);
    expect(result.current.groups).toEqual([
      { tabIds: ["a", "c", "b"], activeTabId: "b" },
    ]);
  });
});
