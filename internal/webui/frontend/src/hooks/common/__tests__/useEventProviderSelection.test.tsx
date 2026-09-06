/** @vitest-environment jsdom */
import React, { useContext } from "react";
import { IssueRecoverySelectionContext } from "../issueRecoverySelection";
import { act, cleanup, renderHook } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { EventProvider, useEventContext } from "../useEventProvider";
import { WorkspaceSSEClient } from "@/api/common/sse";
import { setAuthToken } from "@/api/common/client";
import { prepareIssueRecovery } from "@/api/common/issueRecovery";
import { useIssueHistory } from "@/hooks/issues/useIssueHistory";
import type { RecoveryHandle } from "@/api/common/recoveryHandle";

vi.mock("@/api", () => ({ getIssueEvents: vi.fn().mockResolvedValue([]) }));

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
    manifest: "fleet.issue-workspace.v5",
  };
}
function prepared(input: RecoveryHandle, selected?: string) {
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
      dependencies: [],
      comments: [],
      history: selected
        ? { issue_id: selected, present: false, events: [], has_older: false }
        : null,
    }),
    input,
    input.handle,
    Date.now(),
    selected,
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

describe("EventProvider selected history ownership", () => {
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

  it("captures child layout registrations, fails conflicts closed, and fences selection changes", async () => {
    mocks.read.mockImplementation(async (input: RecoveryHandle) =>
      prepared(input),
    );
    const suspended = vi.spyOn(
      WorkspaceSSEClient.prototype,
      "suspendForRecovery",
    );
    function useOwners({ conflict }: { conflict: boolean }) {
      const registry = useContext(IssueRecoverySelectionContext)!;
      React.useLayoutEffect(() => {
        const removeA = registry.register(mocks.workspace, "A");
        const removeB = conflict
          ? registry.register(mocks.workspace, "B")
          : undefined;
        return () => {
          removeA();
          removeB?.();
        };
      }, [registry, conflict]);
      return useEventContext();
    }
    const hook = renderHook(useOwners, {
      wrapper,
      initialProps: { conflict: true },
    });
    await flush();
    const stream = streams[0];
    act(() => {
      send(stream, "checkpoint", {}, "c2.YWNjZXB0ZWQ");
      send(stream, "resync", { reason: "expired", recovery: offer() });
    });
    await flush();
    expect(hook.result.current.recoveryStatus).toBe("failed");
    expect(mocks.read).not.toHaveBeenCalled();
    expect(stream.signal.aborted).toBe(true);
    const client = suspended.mock.contexts[0];
    expect(client.getLastEventId()).toBe("c2.YWNjZXB0ZWQ");
    hook.rerender({ conflict: false });
    expect(mocks.read).not.toHaveBeenCalled();
    expect(streams).toHaveLength(1);
  });
  it("passes the exact committed selection and aborts an ABA change without resuming transport", async () => {
    let resolve!: (value: ReturnType<typeof prepared>) => void;
    mocks.read.mockReturnValue(
      new Promise((yes) => {
        resolve = yes;
      }),
    );
    const suspended = vi.spyOn(
      WorkspaceSSEClient.prototype,
      "suspendForRecovery",
    );
    const hook = renderHook(
      ({ id }) => {
        useIssueHistory(id);
        return useEventContext();
      },
      { wrapper, initialProps: { id: "A" } },
    );
    await flush();
    act(() => {
      send(streams[0], "checkpoint", {}, "c2.YWNjZXB0ZWQ");
      send(streams[0], "resync", { reason: "expired", recovery: offer() });
    });
    await flush();
    expect(hook.result.current.recoveryStatus).toBe("reading");
    expect(mocks.read).toHaveBeenCalledTimes(1);
    expect(mocks.read.mock.calls[0][2]).toBe("A");
    const signal = mocks.read.mock.calls[0][1] as AbortSignal;
    hook.rerender({ id: "B" });
    hook.rerender({ id: "A" });
    expect(signal.aborted).toBe(true);
    await act(async () => {
      resolve(prepared(offer(), "A"));
    });
    expect(hook.result.current.recoveryStatus).toBe("idle");
    expect(mocks.read).toHaveBeenCalledTimes(1);
    expect(streams).toHaveLength(1);
    expect(streams[0].signal.aborted).toBe(true);
    expect(suspended.mock.contexts[0].getLastEventId()).toBe("c2.YWNjZXB0ZWQ");
  });
});
