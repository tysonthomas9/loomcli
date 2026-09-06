// @vitest-environment jsdom
import { act, renderHook } from "@testing-library/react";
import { describe, it, expect } from "vitest";
import type { Comment } from "@/types";
import { useDetailComments } from "../useDetailComments";
const comment = (id: number, issue_id = "a"): Comment => ({
  id,
  issue_id,
  author: "me",
  text: "Confirmed",
  created_at: "2026-01-01T00:00:00Z",
});
describe("useDetailComments", () => {
  it("keeps a confirmed comment across a pre-write read and deduplicates its authoritative appearance", () => {
    const { result, rerender } = renderHook(
      ({ base }) => useDetailComments("w", "a", base),
      { initialProps: { base: [] as Comment[] } },
    );
    act(() => {
      result.current.add(comment(1));
    });
    rerender({ base: [] });
    expect(result.current.comments).toEqual([comment(1)]);
    rerender({ base: [comment(1)] });
    expect(result.current.comments).toHaveLength(1);
    // Once observed, a later authoritative deletion is no longer masked.
    rerender({ base: [] });
    expect(result.current.comments).toEqual([]);
  });
  it("deduplicates a completion delivered after the read already included it", () => {
    const { result } = renderHook(() =>
      useDetailComments("w", "a", [comment(1)]),
    );
    act(() => {
      result.current.add(comment(1));
      result.current.add(comment(1));
    });
    expect(result.current.comments).toHaveLength(1);
  });
  it("rejects old callbacks across issue and workspace ABA and unmount", () => {
    const { result, rerender, unmount } = renderHook(
      ({ ws, id }) => useDetailComments(ws, id, []),
      { initialProps: { ws: "w", id: "a" } },
    );
    const old = result.current.add;
    rerender({ ws: "w", id: "b" });
    act(() => {
      expect(old(comment(1))).toBe(false);
    });
    expect(result.current.comments).toEqual([]);
    rerender({ ws: "w", id: "a" });
    act(() => {
      expect(old(comment(1))).toBe(false);
    });
    const oldWorkspace = result.current.add;
    rerender({ ws: "other", id: "a" });
    rerender({ ws: "w", id: "a" });
    act(() => {
      expect(oldWorkspace(comment(1))).toBe(false);
      expect(result.current.add(comment(2, "foreign"))).toBe(false);
    });
    const last = result.current.add;
    unmount();
    expect(last(comment(1))).toBe(false);
  });
});
