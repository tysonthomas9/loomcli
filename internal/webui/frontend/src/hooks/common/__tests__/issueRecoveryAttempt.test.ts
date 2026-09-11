import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { IssueRecoveryAttemptController } from "../issueRecoveryAttempt";
import { prepareIssueRecovery } from "@/api/common/issueRecovery";
import type { RecoveryHandle } from "@/api/common/recoveryHandle";
const now = Date.parse("2026-09-05T12:00:00Z");
function offer(): RecoveryHandle {
  return {
    handle: "A".repeat(43),
    source_identity: "s1.Zml4dHVyZQ",
    workspace: "WS",
    source_repos: [],
    expires_at: new Date(now + 60_000).toISOString(),
    manifest: "fleet.issue-workspace.v4",
  };
}
function prepared(input = offer()) {
  return prepareIssueRecovery(
    JSON.stringify({
      manifest: input.manifest,
      workspace: input.workspace,
      through: "c2.Zml4dHVyZQ",
      issues: [],
      total: 0,
      ready: [],
      blocked: [],
      deferred: [],
      dependencies: [],
      comments: [],
    }),
    input,
    input.handle,
  );
}
function deferred<T>() {
  let resolve!: (value: T) => void;
  let reject!: (error: Error) => void;
  const promise = new Promise<T>((yes, no) => {
    resolve = yes;
    reject = no;
  });
  return { promise, resolve, reject };
}
function lease() {
  const controller = new AbortController();
  return {
    controller,
    signal: controller.signal,
    isCurrent: vi.fn(() => true),
    retry: vi.fn(() => true),
  };
}
async function flush() {
  for (let i = 0; i < 8; i++) await Promise.resolve();
}
beforeEach(() => {
  vi.useFakeTimers();
  vi.setSystemTime(now);
});
afterEach(() => {
  vi.useRealTimers();
});
it("prepares privately, preserves immutable offer and never retries or publishes a checkpoint", async () => {
  const l = lease();
  const read = vi.fn(async () => prepared());
  const state = vi.fn();
  const c = new IssueRecoveryAttemptController(read);
  const input = offer();
  c.start(input, l, state);
  (input.source_repos as string[]).push("late");
  await flush();
  expect(read.mock.calls[0]?.[0]).toEqual(offer());
  expect(Object.isFrozen(read.mock.calls[0]?.[0])).toBe(true);
  expect(c.getStatus()).toBe("prepared");
  expect(state.mock.calls).toEqual([["reading"], ["prepared"]]);
  expect(l.retry).not.toHaveBeenCalled();
  c.cancel();
  expect(c.getStatus()).toBe("idle");
});
it("rejects ignored-abort late success and removes signal listener", async () => {
  const l = lease();
  const remove = vi.spyOn(l.signal, "removeEventListener");
  const pending = deferred<ReturnType<typeof prepared>>();
  const read = vi.fn(() => pending.promise);
  const state = vi.fn();
  const c = new IssueRecoveryAttemptController(read);
  c.start(offer(), l, state);
  await flush();
  l.controller.abort();
  pending.resolve(prepared());
  await flush();
  expect(c.getStatus()).toBe("idle");
  expect(state.mock.calls).toEqual([["reading"], ["idle"]]);
  expect(remove).toHaveBeenCalledWith("abort", expect.any(Function));
  expect(read.mock.calls[0]?.[1].aborted).toBe(true);
});
it("expires after preparation without restarting transport", async () => {
  const l = lease();
  const c = new IssueRecoveryAttemptController(async () => prepared());
  c.start(offer(), l, vi.fn());
  await flush();
  expect(c.getStatus()).toBe("prepared");
  await vi.advanceTimersByTimeAsync(60_000);
  expect(c.getStatus()).toBe("failed");
  expect(l.retry).not.toHaveBeenCalled();
  c.cancel();
});
it("checks absolute expiry even before queued timer fires", async () => {
  const pending = deferred<ReturnType<typeof prepared>>();
  const value = prepared();
  const c = new IssueRecoveryAttemptController(() => pending.promise);
  c.start(offer(), lease(), vi.fn());
  await flush();
  vi.setSystemTime(now + 60_001);
  pending.resolve(value);
  await flush();
  expect(c.getStatus()).toBe("failed");
  c.cancel();
});
it("supersession prevents prior success and error from changing replacement", async () => {
  for (const reject of [false, true]) {
    const first = deferred<ReturnType<typeof prepared>>();
    const read = vi
      .fn()
      .mockReturnValueOnce(first.promise)
      .mockResolvedValue(prepared());
    const c = new IssueRecoveryAttemptController(read);
    const old = vi.fn();
    c.start(offer(), lease(), old);
    await flush();
    c.start(offer(), lease(), vi.fn());
    await flush();
    if (reject) first.reject(new Error("late"));
    else first.resolve(prepared());
    await flush();
    expect(c.getStatus()).toBe("prepared");
    expect(old.mock.calls).toEqual([["reading"]]);
    c.cancel();
  }
});
it("reentrant reading cancellation never dispatches a request", async () => {
  const read = vi.fn(async () => prepared());
  const c = new IssueRecoveryAttemptController(read);
  c.start(offer(), lease(), (state) => {
    if (state === "reading") c.cancel();
  });
  await flush();
  expect(read).not.toHaveBeenCalled();
  expect(c.getStatus()).toBe("idle");
});
it("reentrant abort listener replacement wins over outer start", async () => {
  const first = deferred<ReturnType<typeof prepared>>();
  const read = vi
    .fn()
    .mockReturnValueOnce(first.promise)
    .mockResolvedValue(prepared());
  const c = new IssueRecoveryAttemptController(read);
  c.start(offer(), lease(), vi.fn());
  await flush();
  read.mock.calls[0]?.[1].addEventListener(
    "abort",
    () => c.start(offer(), lease(), vi.fn()),
    { once: true },
  );
  const outer = vi.fn();
  c.start(offer(), lease(), outer);
  await flush();
  expect(read).toHaveBeenCalledTimes(2);
  expect(outer).not.toHaveBeenCalled();
  expect(c.getStatus()).toBe("prepared");
  c.cancel();
  first.reject(new Error("retired"));
  await flush();
});
it("invalid offers and reader errors remain failed until explicit cancellation", async () => {
  const read = vi.fn(async () => {
    throw new Error("503");
  });
  const c = new IssueRecoveryAttemptController(read);
  c.start({ ...offer(), handle: "bad" }, lease(), vi.fn());
  expect(c.getStatus()).toBe("failed");
  expect(read).not.toHaveBeenCalled();
  c.start(offer(), lease(), vi.fn());
  await flush();
  expect(c.getStatus()).toBe("failed");
  c.cancel();
  expect(c.getStatus()).toBe("idle");
});
it("ownership loss before completion cannot prepare data", async () => {
  const l = lease();
  const pending = deferred<ReturnType<typeof prepared>>();
  const c = new IssueRecoveryAttemptController(() => pending.promise);
  c.start(offer(), l, vi.fn());
  await flush();
  l.isCurrent.mockReturnValue(false);
  pending.resolve(prepared());
  await flush();
  expect(c.getStatus()).toBe("idle");
});
it("late failure after silent lease retirement cannot report failed for old owner", async () => {
  const l = lease();
  const pending = deferred<ReturnType<typeof prepared>>();
  const c = new IssueRecoveryAttemptController(() => pending.promise);
  const status = vi.fn();
  c.start(offer(), l, status);
  await flush();
  l.isCurrent.mockReturnValue(false);
  pending.reject(new Error("late"));
  await flush();
  expect(c.getStatus()).toBe("idle");
  expect(status.mock.calls).toEqual([["reading"], ["idle"]]);
});
it("long-lived invalidly extended offer cannot overflow browser timers", async () => {
  const input = {
    ...offer(),
    expires_at: new Date(now + 3_000_000_000).toISOString(),
  };
  const c = new IssueRecoveryAttemptController(async () => prepared(input));
  c.start(input, lease(), vi.fn());
  await flush();
  await vi.advanceTimersByTimeAsync(100);
  expect(c.getStatus()).toBe("prepared");
  c.cancel();
  expect(vi.getTimerCount()).toBe(0);
});
