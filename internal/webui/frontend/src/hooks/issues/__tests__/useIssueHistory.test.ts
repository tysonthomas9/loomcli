/** @vitest-environment jsdom */
import { act, render, renderHook } from "@testing-library/react";
import {
  createElement,
  Suspense,
  startTransition,
  useLayoutEffect,
  useState,
  type ReactNode,
} from "react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { getIssueEvents } from "@/api";
import {
  QueryRecoveryContext,
  QueryRecoveryCoordinator,
} from "@/hooks/common/queryRecovery";
import type { Event, MutationPayload } from "@/types";
import { useIssueHistory } from "../useIssueHistory";
const context = vi.hoisted(() => ({
  workspaceId: "WS",
  epoch: 0,
  listeners: new Set<(event: MutationPayload) => void>(),
}));
vi.mock("@/api", () => ({ getIssueEvents: vi.fn() }));
vi.mock("@/hooks/workspace", () => ({
  useWorkspaceContext: () => ({ workspaceId: context.workspaceId }),
}));
const subscribe = (callback: (event: MutationPayload) => void) => {
  context.listeners.add(callback);
  return () => {
    context.listeners.delete(callback);
  };
};
vi.mock("@/hooks/common/useEventProvider", () => ({
  useEventContext: () => ({ subscribe, connectionEpoch: context.epoch }),
}));
const read = vi.mocked(getIssueEvents);
function deferred<T>() {
  let resolve!: (value: T) => void;
  let reject!: (reason: Error) => void;
  const promise = new Promise<T>((yes, no) => {
    resolve = yes;
    reject = no;
  });
  return { promise, resolve, reject };
}
function row(issue_id = "I", id = "event-1"): Event {
  return {
    id,
    issue_id,
    event_type: "issue.comment",
    actor: "alice",
    created_at: "2026-09-05T00:00:00Z",
  };
}
function wrapper(coordinator: QueryRecoveryCoordinator) {
  return function Wrapper({ children }: { children: ReactNode }) {
    return createElement(
      QueryRecoveryContext.Provider,
      { value: coordinator },
      children,
    );
  };
}
beforeEach(() => {
  read.mockReset();
  context.workspaceId = "WS";
  context.epoch = 0;
  context.listeners.clear();
});
describe("useIssueHistory", () => {
  it("only enrolls enabled selections and treats genuine empty history as success", async () => {
    const coordinator = new QueryRecoveryCoordinator();
    read.mockResolvedValue([]);
    const { result, rerender } = renderHook(
      ({ id, enabled }) => useIssueHistory(id, undefined, enabled),
      {
        initialProps: { id: null as string | null, enabled: true },
        wrapper: wrapper(coordinator),
      },
    );
    await act(async () => {
      await coordinator.refresh();
    });
    expect(read).not.toHaveBeenCalled();
    rerender({ id: "I", enabled: false });
    await act(async () => {
      await coordinator.refresh();
    });
    expect(read).not.toHaveBeenCalled();
    await act(async () => {
      rerender({ id: "I", enabled: true });
    });
    expect(result.current.events).toEqual([]);
    expect(result.current.error).toBeNull();
    await act(async () => {
      await coordinator.refresh();
    });
    expect(read).toHaveBeenCalledTimes(2);
    expect(read).toHaveBeenLastCalledWith("WS", "I", 200, {
      signal: expect.any(AbortSignal),
    });
  });
  it("supersedes a pre-fence request and rejects failure without clearing same-scope rows", async () => {
    const coordinator = new QueryRecoveryCoordinator();
    const before = deferred<Event[]>(),
      fresh = deferred<Event[]>();
    read.mockReturnValueOnce(before.promise).mockReturnValueOnce(fresh.promise);
    const { result } = renderHook(() => useIssueHistory("I"), {
      wrapper: wrapper(coordinator),
    });
    await act(async () => {});
    let recovery!: Promise<void>;
    await act(async () => {
      recovery = coordinator.refresh();
    });
    expect(read.mock.calls[0]?.[3]?.signal?.aborted).toBe(true);
    await act(async () => {
      before.resolve([row("I", "old")]);
    });
    expect(result.current.events).toEqual([]);
    await act(async () => {
      fresh.resolve([row()]);
      await recovery;
    });
    expect(result.current.events).toEqual([row()]);
    read.mockRejectedValueOnce(new Error("history unavailable"));
    await act(async () => {
      await expect(coordinator.refresh()).rejects.toThrow(
        "history unavailable",
      );
    });
    expect(result.current.events).toEqual([row()]);
    expect(result.current.error?.message).toBe("history unavailable");
  });
  it.each([null, {}, [{ ...row(), issue_id: "foreign" }]])(
    "rejects malformed or foreign arrays",
    async (value) => {
      const coordinator = new QueryRecoveryCoordinator();
      read.mockResolvedValueOnce([]);
      const { result } = renderHook(() => useIssueHistory("I"), {
        wrapper: wrapper(coordinator),
      });
      await act(async () => {});
      read.mockResolvedValueOnce(value as Event[]);
      await act(async () => {
        await expect(coordinator.refresh()).rejects.toThrow(
          "Invalid selected issue history",
        );
      });
      expect(result.current.error).not.toBeNull();
    },
  );
  it("rereads equal-timestamp object revisions received during strict recovery", async () => {
    const coordinator = new QueryRecoveryCoordinator();
    read.mockResolvedValueOnce([row()]);
    const initial = { updated_at: "same" };
    const { result, rerender } = renderHook(
      ({ revision }) => useIssueHistory("I", revision),
      { initialProps: { revision: initial }, wrapper: wrapper(coordinator) },
    );
    await act(async () => {});
    const first = deferred<Event[]>(),
      next = deferred<Event[]>();
    read.mockReturnValueOnce(first.promise).mockReturnValueOnce(next.promise);
    let recovery!: Promise<void>;
    let complete = false;
    await act(async () => {
      recovery = coordinator.refresh();
      void recovery.then(() => {
        complete = true;
      });
    });
    await act(async () => {
      rerender({ revision: { updated_at: "same" } });
    });
    expect(read).toHaveBeenCalledTimes(2);
    await act(async () => {
      first.resolve([row("I", "pre-change")]);
    });
    expect(complete).toBe(false);
    expect(read).toHaveBeenCalledTimes(3);
    await act(async () => {
      next.resolve([row("I", "after-change")]);
      await recovery;
    });
    expect(result.current.events[0]?.id).toBe("after-change");
  });
  it("moves pending recovery to a new selected issue and ignores retired completion", async () => {
    const coordinator = new QueryRecoveryCoordinator();
    read.mockResolvedValueOnce([row()]);
    const { result, rerender } = renderHook(({ id }) => useIssueHistory(id), {
      initialProps: { id: "I" },
      wrapper: wrapper(coordinator),
    });
    await act(async () => {});
    const old = deferred<Event[]>(),
      fresh = deferred<Event[]>();
    read.mockImplementation((_ws, id) =>
      id === "I" ? old.promise : fresh.promise,
    );
    let recovery!: Promise<void>;
    let complete = false;
    await act(async () => {
      recovery = coordinator.refresh();
      void recovery.then(() => {
        complete = true;
      });
    });
    await act(async () => {
      rerender({ id: "J" });
      old.resolve([row("I", "late")]);
    });
    expect(result.current.events).toEqual([]);
    expect(complete).toBe(false);
    await act(async () => {
      fresh.resolve([row("J")]);
      await recovery;
    });
    expect(result.current.events[0]?.issue_id).toBe("J");
  });
  it("rejects workspace ABA and disabled late completions", async () => {
    const old = deferred<Event[]>();
    read.mockReturnValueOnce(old.promise).mockResolvedValue([]);
    const { result, rerender } = renderHook(
      ({ enabled }) => useIssueHistory("I", undefined, enabled),
      { initialProps: { enabled: true } },
    );
    await act(async () => {});
    context.workspaceId = "OTHER";
    await act(async () => {
      rerender({ enabled: true });
    });
    context.workspaceId = "WS";
    await act(async () => {
      rerender({ enabled: true });
    });
    await act(async () => {
      old.resolve([row("I", "retired")]);
    });
    expect(result.current.events).toEqual([]);
    const pending = deferred<Event[]>();
    read.mockReturnValueOnce(pending.promise);
    await act(async () => {
      void result.current.refetch();
    });
    await act(async () => {
      rerender({ enabled: false });
    });
    expect(read.mock.calls.at(-1)?.[3]?.signal?.aborted).toBe(true);
    await act(async () => {
      pending.resolve([row()]);
    });
    expect(result.current.events).toEqual([]);
  });
  it("invalidates on matching comments, dependency changes and epoch, not foreign workspace", async () => {
    read.mockResolvedValue([]);
    const { rerender } = renderHook(() => useIssueHistory("I"));
    await act(async () => {});
    async function emit(event: Partial<MutationPayload>) {
      await act(async () => {
        for (const listener of context.listeners)
          listener({ type: "comment", timestamp: "now", ...event });
      });
    }
    await emit({ workspace_id: "OTHER", issue_id: "I" });
    expect(read).toHaveBeenCalledTimes(1);
    await emit({ workspace_id: "WS", issue_id: "I" });
    expect(read).toHaveBeenCalledTimes(2);
    await emit({
      workspace_id: "WS",
      entity_type: "dependency",
      issue_id: "J",
    });
    expect(read).toHaveBeenCalledTimes(3);
    context.epoch++;
    await act(async () => {
      rerender();
    });
    expect(read).toHaveBeenCalledTimes(4);
  });
  it("leaves committed history callbacks active through a suspended speculative workspace", async () => {
    const never = new Promise<void>(() => {});
    let change!: (id: string) => void;
    let visible!: ReturnType<typeof useIssueHistory>;
    function History({ id }: { id: string }) {
      context.workspaceId = id;
      const history = useIssueHistory("I");
      useLayoutEffect(() => {
        visible = history;
      });
      if (id === "OTHER") throw never;
      return createElement("div", null, history.events.length);
    }
    function Harness() {
      const [id, setId] = useState("WS");
      change = setId;
      return createElement(
        Suspense,
        { fallback: "loading" },
        createElement(History, { id }),
      );
    }
    read.mockResolvedValue([row()]);
    const view = render(createElement(Harness));
    await act(async () => {});
    const refetch = visible.refetch;
    await act(async () => {
      startTransition(() => change("OTHER"));
    });
    await act(async () => {
      await refetch();
    });
    expect(read.mock.calls.at(-1)?.[0]).toBe("WS");
    expect(view.container.textContent).toBe("1");
    view.unmount();
  });
});
