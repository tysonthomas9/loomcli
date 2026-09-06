/** @vitest-environment jsdom */
import React, { useContext } from "react";
import {
  QueryRecoveryContext,
  QueryRecoveryCoordinator,
} from "../queryRecovery";
import { act, cleanup, renderHook } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { EventProvider, useEventContext } from "../useEventProvider";
import { WorkspaceSSEClient } from "@/api/common/sse";
import { setAuthToken } from "@/api/common/client";
import {
  prepareIssueRecovery,
  type PreparedIssueRecovery,
} from "@/api/common/issueRecovery";
import type { RecoveryHandle } from "@/api/common/recoveryHandle";

const mocks = vi.hoisted(() => ({
  stream: vi.fn(),
  read: vi.fn(),
  workspace: "WS-A",
}));
vi.mock("@microsoft/fetch-event-source", () => ({
  EventStreamContentType: "text/event-stream",
  fetchEventSource: mocks.stream,
}));
vi.mock("@/api/common/readIssueRecovery", () => ({
  readIssueRecovery: mocks.read,
}));
vi.mock("@/api/common/client", async (original) => ({
  ...(await original<typeof import("@/api/common/client")>()),
  get: vi.fn().mockResolvedValue({ disabled: true }),
}));
vi.mock("@/hooks/workspace", () => ({
  useWorkspaceContext: () => ({ workspaceId: mocks.workspace }),
}));

type Stream = {
  headers: Record<string, string>;
  signal: AbortSignal;
  fetch: typeof fetch;
  onmessage: (message: { id: string; event: string; data: string }) => void;
};
let repos: string[] | undefined;
let streams: Stream[];
function wrapper({ children }: { children: React.ReactNode }) {
  return <EventProvider sourceRepos={repos}>{children}</EventProvider>;
}
function offer(): RecoveryHandle {
  return {
    handle: "A".repeat(43),
    source_identity: "s1.Zml4dHVyZQ",
    workspace: mocks.workspace,
    source_repos: repos ?? [],
    expires_at: new Date(Date.now() + 60_000).toISOString(),
    manifest: "fleet.issue-workspace.v2",
  };
}
function prepared(input: RecoveryHandle) {
  return prepareIssueRecovery(
    JSON.stringify({
      manifest: input.manifest,
      workspace: input.workspace,
      through: "c2.c25hcHNob3Q",
      issues: [],
      total: 0,
      ready: [],
      blocked: [],
      deferred: [],
    }),
    input,
    input.handle,
  );
}
async function flush() {
  await act(async () => {});
}
function send(stream: Stream, event: string, data: unknown, id?: string) {
  if (id !== undefined) {
    if (id) stream.headers["last-event-id"] = id;
    else delete stream.headers["last-event-id"];
  }
  stream.onmessage({ id: id ?? "", event, data: JSON.stringify(data) });
}

describe("EventProvider recovery preparation ownership", () => {
  beforeEach(() => {
    mocks.workspace = "WS-A";
    repos = undefined;
    streams = [];
    mocks.read.mockReset();
    mocks.stream.mockReset();
    setAuthToken("credential-a");
    vi.spyOn(globalThis, "fetch").mockResolvedValue(
      new Response(null, { headers: { "content-type": "text/event-stream" } }),
    );
    mocks.stream.mockImplementation((url: string, options: Stream) => {
      streams.push(options);
      void options
        .fetch(url, { headers: options.headers, signal: options.signal })
        .catch(() => {});
      return new Promise<void>((resolve) =>
        options.signal.addEventListener("abort", () => resolve(), {
          once: true,
        }),
      );
    });
  });
  afterEach(() => {
    cleanup();
    setAuthToken(null);
    vi.restoreAllMocks();
  });

  async function start() {
    let resolve!: (value: PreparedIssueRecovery) => void;
    let reject!: (error: Error) => void;
    mocks.read.mockReturnValue(
      new Promise<PreparedIssueRecovery>((yes, no) => {
        resolve = yes;
        reject = no;
      }),
    );
    const suspended = vi.spyOn(
      WorkspaceSSEClient.prototype,
      "suspendForRecovery",
    );
    const connect = vi.spyOn(WorkspaceSSEClient.prototype, "connect");
    const hook = renderHook(() => useEventContext(), { wrapper });
    await flush();
    const stream = streams[0];
    expect(stream).toBeDefined();
    const input = offer();
    act(() => {
      send(stream, "checkpoint", {}, "c2.YWNjZXB0ZWQ");
      send(stream, "resync", { reason: "expired", recovery: input });
    });
    await flush();
    expect(hook.result.current.recoveryStatus).toBe("reading");
    expect(suspended).toHaveBeenCalledTimes(1);
    expect(stream.signal.aborted).toBe(true);
    expect(mocks.read).toHaveBeenCalledTimes(1);
    const client = suspended.mock.contexts[0];
    expect(client.getLastEventId()).toBe("c2.YWNjZXB0ZWQ");
    const signal = mocks.read.mock.calls[0][1] as AbortSignal;
    return { ...hook, resolve, reject, input, client, signal, connect };
  }

  it("prepares while suspended without accepting through, reconnecting or publishing mutations", async () => {
    const run = await start();
    const mutation = vi.fn();
    const stop = run.result.current.subscribe(mutation);
    const calls = run.connect.mock.calls.length;
    await act(async () => run.resolve(prepared(run.input)));
    expect(run.result.current.recoveryStatus).toBe("prepared");
    expect(run.result.current.state).toBe("disconnected");
    expect(run.client.getLastEventId()).toBe("c2.YWNjZXB0ZWQ");
    expect(run.connect).toHaveBeenCalledTimes(calls);
    expect(mutation).not.toHaveBeenCalled();
    expect(run.result.current).not.toHaveProperty("prepared");
    stop();
  });

  it.each([
    "workspace ABA",
    "repo change",
    "credential replacement",
    "sign out",
    "sign out event",
    "manual retry",
    "disconnect",
  ])("discards ignored-abort late success after %s", async (change) => {
    const run = await start();
    if (change === "workspace ABA") {
      mocks.workspace = "WS-B";
      run.rerender();
      mocks.workspace = "WS-A";
      run.rerender();
    } else if (change === "repo change") {
      repos = ["repo-b"];
      run.rerender();
    } else if (change === "credential replacement") {
      act(() => setAuthToken("credential-b"));
    } else if (change === "sign out") {
      act(() => setAuthToken(null));
    } else if (change === "sign out event") {
      act(() => window.dispatchEvent(new Event("auth-sign-out")));
    } else if (change === "manual retry") {
      act(() => run.result.current.retryNow());
    } else {
      act(() => run.result.current.disconnect());
    }
    await flush();
    expect(run.signal.aborted).toBe(true);
    const calls = run.connect.mock.calls.length;
    await act(async () => run.resolve(prepared(run.input)));
    expect(run.result.current.recoveryStatus).toBe("idle");
    expect(run.client.getLastEventId()).toBe("c2.YWNjZXB0ZWQ");
    expect(run.connect).toHaveBeenCalledTimes(calls);
    if (change === "sign out") expect(streams).toHaveLength(1);
  });

  it.each(["retry", "sign out"])(
    "stops refresh fanout when the first subscriber performs %s",
    async (action) => {
      const hook = renderHook(
        () => ({
          events: useEventContext(),
          recovery: useContext(QueryRecoveryContext)!,
        }),
        { wrapper },
      );
      await flush();
      const later = vi.fn();
      const resync = vi.fn();
      const refresh = vi.fn().mockResolvedValue(undefined);
      const clean = [
        hook.result.current.events.subscribe(() => {
          if (action === "retry") hook.result.current.events.retryNow();
          else window.dispatchEvent(new Event("auth-sign-out"));
        }),
        hook.result.current.events.subscribe(later),
        hook.result.current.events.onResync(resync),
        hook.result.current.recovery.register("observer", refresh),
      ];
      act(() =>
        send(streams[0], "resync", { reason: "expired", recovery: offer() }),
      );
      await flush();
      expect(later).not.toHaveBeenCalled();
      expect(resync).not.toHaveBeenCalled();
      expect(refresh).not.toHaveBeenCalled();
      expect(mocks.read).not.toHaveBeenCalled();
      for (const stop of clean) stop();
    },
  );

  it("cancels an already running ordinary query recovery on standalone sign-out", async () => {
    const hook = renderHook(() => useContext(QueryRecoveryContext)!, {
      wrapper,
    });
    await flush();
    let signal: AbortSignal | undefined;
    const stop = hook.result.current.register("pending query", (current) => {
      signal = current;
      return new Promise<void>(() => {});
    });
    act(() => send(streams[0], "resync", { reason: "error" }));
    await flush();
    expect(signal?.aborted).toBe(false);
    act(() => window.dispatchEvent(new Event("auth-sign-out")));
    expect(signal?.aborted).toBe(true);
    await flush();
    stop();
  });

  it("routes synchronous resync during repo rebind to the newly committed coordinator", async () => {
    const hook = renderHook(() => useContext(QueryRecoveryContext)!, {
      wrapper,
    });
    await flush();
    const old = hook.result.current;
    const refresh = vi.spyOn(QueryRecoveryCoordinator.prototype, "refresh");
    const update = WorkspaceSSEClient.prototype.updateSourceRepos;
    vi.spyOn(
      WorkspaceSSEClient.prototype,
      "updateSourceRepos",
    ).mockImplementation(function (next) {
      update.call(this, next);
      // Arrives inside the rebind layout effect, before passive effects can run.
      send(streams.at(-1)!, "resync", { reason: "error" });
    });
    repos = ["repo-b"];
    hook.rerender();
    await flush();
    expect(hook.result.current).not.toBe(old);
    expect(refresh).toHaveBeenCalledTimes(1);
    expect(refresh.mock.contexts[0]).toBe(hook.result.current);
  });

  it("reports failed read without advancing the accepted cursor or automatically retrying", async () => {
    const run = await start();
    const calls = run.connect.mock.calls.length;
    await act(async () => run.reject(new Error("recovery read rejected")));
    expect(run.result.current.recoveryStatus).toBe("failed");
    expect(run.client.getLastEventId()).toBe("c2.YWNjZXB0ZWQ");
    expect(run.connect).toHaveBeenCalledTimes(calls);
  });
});
