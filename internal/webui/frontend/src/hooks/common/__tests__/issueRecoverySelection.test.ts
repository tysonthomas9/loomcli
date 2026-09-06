import { describe, expect, it } from "vitest";
import { IssueRecoverySelectionRegistry } from "../issueRecoverySelection";

describe("committed history selection leases", () => {
  it("captures empty or duplicate identical owners without choosing a conflicting issue", () => {
    const registry = new IssueRecoverySelectionRegistry();
    const empty = registry.capture("WS");
    expect(empty.issueId).toBeUndefined();
    const removeA = registry.register("WS", "A");
    expect(empty.signal.aborted).toBe(true);
    const removeDuplicate = registry.register("WS", "A");
    registry.register("OTHER", "foreign");
    const same = registry.capture("WS");
    expect(same.issueId).toBe("A");
    const removeB = registry.register("WS", "B");
    expect(same.isCurrent()).toBe(false);
    expect(() => registry.capture("WS")).toThrow(/more than one/);
    removeB();
    expect(registry.capture("WS").issueId).toBe("A");
    removeA();
    expect(registry.capture("WS").issueId).toBe("A");
    removeDuplicate();
    expect(registry.capture("WS").issueId).toBeUndefined();
  });

  it("never revives ABA leases and ignores repeated old cleanup or release", () => {
    const registry = new IssueRecoverySelectionRegistry();
    const removeA = registry.register("WS", "A");
    const first = registry.capture("WS");
    removeA();
    registry.register("WS", "A");
    const current = registry.capture("WS");
    removeA();
    first.release?.();
    expect(first.signal.aborted).toBe(true);
    expect(first.isCurrent()).toBe(false);
    expect(current.isCurrent()).toBe(true);
    current.release?.();
    expect(current.signal.aborted).toBe(true);
  });

  it("lets abort observers capture the updated membership without an outer capture overwriting them", () => {
    const registry = new IssueRecoverySelectionRegistry();
    registry.register("WS", "A");
    const first = registry.capture("WS");
    let reentrant: ReturnType<typeof registry.capture> | undefined;
    first.signal.addEventListener("abort", () => {
      reentrant = registry.capture("WS");
    });
    expect(() => registry.capture("WS")).toThrow(/superseded/);
    expect(reentrant?.isCurrent()).toBe(true);
    expect(reentrant?.issueId).toBe("A");
  });

  it("exposes the changed set during synchronous invalidation and retains only one capture", () => {
    const registry = new IssueRecoverySelectionRegistry();
    const first = registry.capture("WS");
    let seen: string | undefined;
    first.signal.addEventListener("abort", () => {
      seen = registry.capture("WS").issueId;
    });
    registry.register("WS", "B");
    expect(seen).toBe("B");
    const next = registry.capture("WS");
    const last = registry.capture("WS");
    expect(next.signal.aborted).toBe(true);
    expect(last.isCurrent()).toBe(true);
  });
});
