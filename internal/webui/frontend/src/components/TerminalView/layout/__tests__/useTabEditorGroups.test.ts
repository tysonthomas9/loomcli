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
});

describe("useTabEditorGroups", () => {
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
