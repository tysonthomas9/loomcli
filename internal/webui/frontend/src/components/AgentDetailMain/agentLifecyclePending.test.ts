/**
 * @vitest-environment jsdom
 */

import { beforeEach, describe, expect, it, vi } from "vitest";

import {
  acquireAgentLifecycleSubmission,
  clearPendingAgentLifecycleCommand,
  isAgentLifecycleSubmissionLocked,
  loadPendingAgentLifecycleCommand,
  markPendingAgentLifecycleWarningShown,
  pendingAgentLifecycleStorageKey,
  releaseAgentLifecycleSubmission,
  savePendingAgentLifecycleCommand,
  subscribeAgentLifecyclePending,
  type PendingAgentLifecycleCommand,
} from "./agentLifecyclePending";

const pending: PendingAgentLifecycleCommand = {
  workspace: "TEAM A",
  agent: "review/one",
  action: "restart",
  commandId: "agent-lifecycle-123",
  acceptedAt: 1_720_000_000_000,
  warningShown: false,
};

describe("pending agent lifecycle storage", () => {
  beforeEach(() => {
    window.localStorage.clear();
  });

  it("round-trips one workspace and agent without colliding with another", () => {
    expect(savePendingAgentLifecycleCommand(pending)).toBe(true);

    expect(
      loadPendingAgentLifecycleCommand(pending.workspace, pending.agent),
    ).toEqual(pending);
    expect(
      loadPendingAgentLifecycleCommand(pending.workspace, "review-two"),
    ).toBeNull();
  });

  it("persists the one-time delayed warning marker", () => {
    expect(
      savePendingAgentLifecycleCommand({ ...pending, warningShown: true }),
    ).toBe(true);

    expect(
      loadPendingAgentLifecycleCommand(pending.workspace, pending.agent),
    ).toMatchObject({ warningShown: true });
  });

  it("discards malformed or identity-forged records without throwing", () => {
    const storage = {
      getItem: () =>
        JSON.stringify({
          ...pending,
          workspace: "OTHER",
          acceptedAt: "yesterday",
        }),
      removeItem: () => undefined,
      setItem: () => undefined,
      clear: () => undefined,
      key: () => null,
      length: 1,
    } satisfies Storage;

    expect(
      loadPendingAgentLifecycleCommand(
        pending.workspace,
        pending.agent,
        storage,
      ),
    ).toBeNull();
  });

  it("treats unavailable storage as non-fatal", () => {
    const storage = {
      getItem: () => {
        throw new DOMException("denied", "SecurityError");
      },
      removeItem: () => {
        throw new DOMException("denied", "SecurityError");
      },
      setItem: () => {
        throw new DOMException("denied", "SecurityError");
      },
      clear: () => undefined,
      key: () => null,
      length: 0,
    } satisfies Storage;

    expect(
      loadPendingAgentLifecycleCommand(
        pending.workspace,
        pending.agent,
        storage,
      ),
    ).toBeNull();
    expect(savePendingAgentLifecycleCommand(pending, storage)).toBe(false);
    expect(() =>
      clearPendingAgentLifecycleCommand(
        pending.workspace,
        pending.agent,
        pending.commandId,
        storage,
      ),
    ).not.toThrow();
  });

  it("does not let stale warning or cleanup work mutate a newer command", () => {
    const newer = {
      ...pending,
      action: "start" as const,
      commandId: "agent-lifecycle-456",
      acceptedAt: pending.acceptedAt + 1,
    };
    expect(savePendingAgentLifecycleCommand(pending)).toBe(true);
    expect(savePendingAgentLifecycleCommand(newer)).toBe(true);

    expect(
      markPendingAgentLifecycleWarningShown(
        pending.workspace,
        pending.agent,
        pending.commandId,
      ),
    ).toBeNull();
    expect(
      clearPendingAgentLifecycleCommand(
        pending.workspace,
        pending.agent,
        pending.commandId,
      ),
    ).toBe(false);
    expect(
      loadPendingAgentLifecycleCommand(pending.workspace, pending.agent),
    ).toEqual(newer);
  });

  it("retains the newest accepted response when an older response arrives late", () => {
    const newer = {
      ...pending,
      commandId: "agent-lifecycle-newer",
      acceptedAt: pending.acceptedAt + 10,
    };
    expect(savePendingAgentLifecycleCommand(newer)).toBe(true);
    expect(savePendingAgentLifecycleCommand(pending)).toBe(true);

    expect(
      loadPendingAgentLifecycleCommand(pending.workspace, pending.agent),
    ).toEqual(newer);
  });

  it("notifies same-tab subscribers and exact-key synthetic storage events", () => {
    const listener = vi.fn();
    const unsubscribe = subscribeAgentLifecyclePending(
      pending.workspace,
      pending.agent,
      listener,
    );

    savePendingAgentLifecycleCommand(pending);
    expect(listener).toHaveBeenCalledTimes(1);

    window.dispatchEvent(
      new StorageEvent("storage", {
        key: "loom.agent-lifecycle.pending.v1:OTHER:agent",
        newValue: JSON.stringify(pending),
        storageArea: window.localStorage,
      }),
    );
    expect(listener).toHaveBeenCalledTimes(1);

    const newer = {
      ...pending,
      commandId: "agent-lifecycle-storage-event",
      acceptedAt: pending.acceptedAt + 1,
    };
    window.localStorage.setItem(
      pendingAgentLifecycleStorageKey(pending.workspace, pending.agent),
      JSON.stringify(newer),
    );
    window.dispatchEvent(
      new StorageEvent("storage", {
        key: pendingAgentLifecycleStorageKey(pending.workspace, pending.agent),
        oldValue: JSON.stringify(pending),
        newValue: JSON.stringify(newer),
        storageArea: window.localStorage,
      }),
    );
    expect(listener).toHaveBeenCalledTimes(2);

    unsubscribe();
  });

  it("acquires one compare-before-write submission lease per identity", () => {
    const first = acquireAgentLifecycleSubmission(
      pending.workspace,
      pending.agent,
    );
    expect(first).not.toBeNull();
    expect(
      isAgentLifecycleSubmissionLocked(pending.workspace, pending.agent),
    ).toBe(true);
    expect(
      acquireAgentLifecycleSubmission(pending.workspace, pending.agent),
    ).toBeNull();

    releaseAgentLifecycleSubmission(pending.workspace, pending.agent, first!);
    expect(
      isAgentLifecycleSubmissionLocked(pending.workspace, pending.agent),
    ).toBe(false);
  });
});
