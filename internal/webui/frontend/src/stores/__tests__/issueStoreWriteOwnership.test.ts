import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { QueryRecoveryCoordinator } from "../../hooks/common/queryRecovery";
import { createIssueStore } from "../issueStore";
import { AUTO_ROLLBACK_TIMEOUT_MS } from "../issueStoreHelpers";
import type { MutationPayload } from "../../api/common";
import type { Issue } from "@/types";
vi.mock(import("../../api/issues"), async (load) => ({
  ...(await load()),
  getReadyIssues: vi.fn(),
  getKanbanIssues: vi.fn(),
  fetchGraphIssues: vi.fn(),
  updateIssue: vi.fn(),
}));
import {
  getReadyIssues,
  getKanbanIssues,
  fetchGraphIssues,
  updateIssue,
} from "../../api/issues";
function deferred<T>() {
  let resolve!: (value: T) => void;
  let reject!: (error: Error) => void;
  const promise = new Promise<T>((yes, no) => {
    resolve = yes;
    reject = no;
  });
  return { promise, resolve, reject };
}
function issue(overrides: Partial<Issue> = {}): Issue {
  return {
    id: "same",
    title: "original",
    status: "open",
    priority: 2,
    repo: "repo-a",
    source_repo: "repo-a",
    created_at: "2026-01-01T00:00:00Z",
    updated_at: "2026-01-01T00:00:00Z",
    ...overrides,
  };
}
let store: ReturnType<typeof createIssueStore>;
async function configure(
  workspaceId = "A",
  sourceRepos = ["repo-a"],
  mode: "ready" | "kanban" | "graph" = "ready",
) {
  await store.getState().fetchIssues({ workspaceId, sourceRepos, mode });
}
function recover() {
  return store.getState().refreshForRecovery(new AbortController().signal, "A");
}
beforeEach(() => {
  vi.useFakeTimers();
  vi.setSystemTime(new Date("2026-09-05T12:00:00Z"));
  vi.clearAllMocks();
  store = createIssueStore();
  for (const fn of [getReadyIssues, getKanbanIssues, fetchGraphIssues])
    vi.mocked(fn).mockResolvedValue([issue()]);
});
afterEach(() => {
  store.getState().reset();
  vi.useRealTimers();
});
describe("issue store write ownership", () => {
  it("an already-aborted fetch cannot retire the valid in-flight read", async () => {
    await configure();
    const page = deferred<Issue[]>();
    vi.mocked(getReadyIssues).mockReturnValueOnce(page.promise);
    const active = store.getState().refetch();
    const abort = new AbortController();
    abort.abort();
    await store
      .getState()
      .fetchIssues({
        workspaceId: "A",
        mode: "ready",
        sourceRepos: ["repo-a"],
        signal: abort.signal,
      });
    page.resolve([issue({ title: "Valid new read" })]);
    await active;
    expect(store.getState().getIssue("same")?.title).toBe("Valid new read");
    expect(store.getState().isLoading).toBe(false);
  });

  it.each([
    { workspace_id: "B", source_repo: "repo-a" },
    { workspace_id: "A", source_repo: "repo-b" },
  ])(
    "filters foreign mutation before optimistic buffering %j",
    async (scope) => {
      await configure();
      const command = deferred<Issue>();
      vi.mocked(updateIssue).mockReturnValue(command.promise);
      const completion = store
        .getState()
        .updateIssueStatus("same", "in_progress", "A");
      store.getState().applyMutation({
        type: "update",
        issue_id: "same",
        title: "foreign",
        timestamp: "2099-01-01T00:00:00Z",
        ...scope,
      });
      command.resolve(issue({ status: "in_progress" }));
      await completion;
      expect(store.getState().getIssue("same")?.title).toBe("original");
    },
  );
  it.each([false, true])(
    "old timed-out command cannot retire newer command (failure=%s)",
    async (failure) => {
      await configure();
      const old = deferred<Issue>(),
        next = deferred<Issue>();
      vi.mocked(updateIssue)
        .mockReturnValueOnce(old.promise)
        .mockReturnValueOnce(next.promise);
      const first = store
        .getState()
        .updateIssueStatus("same", "in_progress", "A")
        .catch(() => {});
      await vi.advanceTimersByTimeAsync(AUTO_ROLLBACK_TIMEOUT_MS + 1);
      const second = store.getState().updateIssueStatus("same", "review", "A");
      if (failure) old.reject(new Error("old failure"));
      else old.resolve(issue());
      await first;
      expect(store.getState().pendingIds.has("same")).toBe(true);
      expect(store.getState().getIssue("same")?.status).toBe("review");
      next.resolve(issue({ status: "review" }));
      await second;
    },
  );
  it("rejects recovery while an API command remains unsettled after timeout and A-B-A reset", async () => {
    await configure();
    const pending = deferred<Issue>();
    vi.mocked(updateIssue).mockReturnValue(pending.promise);
    const command = store
      .getState()
      .updateIssueStatus("same", "in_progress", "A");
    await expect(recover()).rejects.toBeDefined();
    await vi.advanceTimersByTimeAsync(AUTO_ROLLBACK_TIMEOUT_MS + 1);
    expect(store.getState().pendingIds.size).toBe(0);
    await expect(recover()).rejects.toBeDefined();
    store.getState().reset();
    await configure("B");
    store.getState().reset();
    await configure("A");
    await expect(recover()).rejects.toBeDefined();
    pending.resolve(issue());
    await command;
    await expect(recover()).resolves.toBeUndefined();
  });
  it("rejects recovery if a command starts and settles while its read is pending", async () => {
    await configure();
    const page = deferred<Issue[]>();
    vi.mocked(getReadyIssues).mockReturnValueOnce(page.promise);
    const recovery = recover();
    const rejected = expect(recovery).rejects.toBeDefined();
    vi.mocked(updateIssue).mockResolvedValue(issue({ status: "review" }));
    await store.getState().updateIssueStatus("same", "review", "A");
    page.resolve([issue()]);
    await rejected;
  });
  it("rejects mismatched workspace before command API or optimistic write", async () => {
    await configure();
    const original = store.getState().getIssue("same");
    vi.mocked(updateIssue).mockResolvedValue(issue());
    await expect(
      store.getState().updateIssueStatus("same", "review", "B"),
    ).rejects.toBeDefined();
    expect(updateIssue).not.toHaveBeenCalled();
    expect(store.getState().getIssue("same")).toBe(original);
  });
  it.each(["workspace", "repo", "mode"])(
    "scope change retires old optimistic visibility (%s)",
    async (change) => {
      await configure();
      const pending = deferred<Issue>();
      vi.mocked(updateIssue).mockReturnValue(pending.promise);
      const command = store
        .getState()
        .updateIssueStatus("same", "review", "A")
        .catch(() => {});
      await configure(
        change === "workspace" ? "B" : "A",
        change === "repo" ? ["repo-b"] : ["repo-a"],
        change === "mode" ? "kanban" : "ready",
      );
      expect(store.getState().pendingIds.size).toBe(0);
      expect(store.getState().getIssue("same")?.status).toBe("open");
      pending.reject(new Error("retired failure"));
      await command;
      expect(store.getState().getIssue("same")?.status).toBe("open");
    },
  );
  it("a reset A-B-A old command cannot clear a new command with the same ID", async () => {
    await configure();
    const old = deferred<Issue>(),
      next = deferred<Issue>();
    vi.mocked(updateIssue)
      .mockReturnValueOnce(old.promise)
      .mockReturnValueOnce(next.promise);
    const first = store
      .getState()
      .updateIssueStatus("same", "in_progress", "A");
    store.getState().reset();
    await configure("B");
    store.getState().reset();
    await configure();
    const second = store.getState().updateIssueStatus("same", "review", "A");
    old.resolve(issue());
    await first;
    expect(store.getState().pendingIds.has("same")).toBe(true);
    expect(store.getState().getIssue("same")?.status).toBe("review");
    next.resolve(issue());
    await second;
  });
  it("rechecks an accepted mutation revision before the combined recovery can finish", async () => {
    await configure();
    const coordinator = new QueryRecoveryCoordinator("A");
    const other = deferred<void>();
    coordinator.register(
      "issues",
      (signal) => store.getState().refreshForRecovery(signal, "A"),
      () => store.getState().getRecoveryRevision(),
    );
    coordinator.register("other", () => other.promise);
    let completed = false;
    const recovery = coordinator.refresh().then(() => {
      completed = true;
    });
    for (let n = 0; n < 12; n++) await Promise.resolve();
    expect(getReadyIssues).toHaveBeenCalledTimes(2);
    const next = deferred<Issue[]>();
    vi.mocked(getReadyIssues).mockReturnValueOnce(next.promise);
    store.getState().applyMutation({
      type: "update",
      entity_type: "issue",
      workspace_id: "A",
      source_repo: "repo-a",
      issue_id: "same",
      title: "Changed",
      timestamp: "2026-09-05T12:00:01Z",
    });
    other.resolve();
    for (let n = 0; n < 12; n++) await Promise.resolve();
    expect(getReadyIssues).toHaveBeenCalledTimes(3);
    expect(completed).toBe(false);
    next.resolve([
      issue({ title: "Changed", updated_at: "2026-09-05T12:00:01Z" }),
    ]);
    await recovery;
    expect(completed).toBe(true);
  });
  it("rejects reentrant command admission at recovery publication without retiring that command", async () => {
    await configure();
    const initial = store.getState().issuesMap;
    const api = deferred<Issue>();
    vi.mocked(updateIssue).mockReturnValue(api.promise);
    let command: Promise<void> | undefined;
    let started = false;
    const unsubscribe = store.subscribe((state) => {
      if (
        started ||
        state.issuesMap === initial ||
        state.issuesMap.get("same")?.title !== "Recovered"
      )
        return;
      started = true;
      command = state.updateIssueStatus("same", "review", "A");
    });
    vi.mocked(getReadyIssues).mockResolvedValueOnce([
      issue({ title: "Recovered", updated_at: "2026-09-05T12:00:01Z" }),
    ]);
    await expect(recover()).rejects.toBeDefined();
    expect(started).toBe(true);
    expect(store.getState().pendingIds.has("same")).toBe(true);
    expect(store.getState().getIssue("same")?.status).toBe("review");
    api.resolve(issue({ status: "review" }));
    await command;
    unsubscribe();
    expect(store.getState().pendingIds.size).toBe(0);
  });
  it("ignores retired subscription callbacks and repeated old unsubscribe preserves the new source", async () => {
    await configure();
    let oldCallback!: (event: MutationPayload) => void,
      newCallback!: (event: MutationPayload) => void;
    const oldCleanup = vi.fn(),
      newCleanup = vi.fn();
    const disconnectOld = store.getState().connectToEvents((callback) => {
      oldCallback = callback;
      return oldCleanup;
    });
    disconnectOld();
    const disconnectNew = store.getState().connectToEvents((callback) => {
      newCallback = callback;
      return newCleanup;
    });
    const mutation: MutationPayload = {
      type: "update",
      issue_id: "same",
      workspace_id: "A",
      source_repo: "repo-a",
      title: "Old callback",
      timestamp: "2099-01-01T00:00:00Z",
    };
    oldCallback(mutation);
    expect(store.getState().getIssue("same")?.title).toBe("original");
    disconnectOld();
    expect(oldCleanup).toHaveBeenCalledTimes(1);
    expect(newCleanup).not.toHaveBeenCalled();
    newCallback({ ...mutation, title: "New callback" });
    expect(store.getState().getIssue("same")?.title).toBe("New callback");
    disconnectNew();
    expect(newCleanup).toHaveBeenCalledTimes(1);
  });
  it("refetches the saved filter and repository snapshot after caller mutation", async () => {
    const filter = {
      assignee: "alice",
      labels: ["initial"],
      source_repos: ["filter-repo"],
    };
    const repos = ["repo-a"];
    await store.getState().fetchIssues({
      workspaceId: "A",
      mode: "ready",
      filter,
      sourceRepos: repos,
    });
    filter.assignee = "mallory";
    filter.labels.push("later");
    filter.source_repos[0] = "changed";
    repos[0] = "repo-b";
    await store.getState().refetch();
    expect(vi.mocked(getReadyIssues).mock.calls.at(-1)?.[1]).toEqual({
      assignee: "alice",
      labels: ["initial"],
      source_repos: ["repo-a"],
    });
    const graphFilter = {
      status: "open" as const,
      source_repos: ["graph-original"],
    };
    await store
      .getState()
      .fetchIssues({ workspaceId: "A", mode: "graph", graphFilter });
    graphFilter.source_repos[0] = "graph-mutated";
    await store.getState().refetch();
    expect(vi.mocked(fetchGraphIssues).mock.calls.at(-1)?.[1]).toEqual({
      status: "open",
      source_repos: ["graph-original"],
    });
  });
  it("does not supersede a newer same-scope fetch started synchronously by scope clearing", async () => {
    await configure();
    const page = deferred<Issue[]>();
    vi.mocked(getReadyIssues).mockReturnValueOnce(page.promise);
    let inner: Promise<void> | undefined;
    let started = false;
    const unsubscribe = store.subscribe((state) => {
      if (started || state.issuesMap.size !== 0) return;
      started = true;
      inner = state.fetchIssues({
        workspaceId: "B",
        mode: "ready",
        sourceRepos: ["repo-a"],
      });
    });
    const outer = store.getState().fetchIssues({
      workspaceId: "B",
      mode: "ready",
      sourceRepos: ["repo-a"],
    });
    expect(started).toBe(true);
    expect(getReadyIssues).toHaveBeenCalledTimes(2);
    const signal = vi.mocked(getReadyIssues).mock.calls.at(-1)?.[2]?.signal;
    expect(signal?.aborted).toBe(false);
    page.resolve([issue({ title: "Newer owner result" })]);
    await inner;
    await outer;
    expect(store.getState().getIssue("same")?.title).toBe("Newer owner result");
    expect(signal?.aborted).toBe(false);
    unsubscribe();
  });
  it("scope retirement cancels an old optimistic rollback timer", async () => {
    await configure();
    const pending = deferred<Issue>();
    vi.mocked(updateIssue).mockReturnValue(pending.promise);
    const command = store.getState().updateIssueStatus("same", "review", "A");
    await configure("B");
    const map = store.getState().issuesMap;
    await vi.advanceTimersByTimeAsync(AUTO_ROLLBACK_TIMEOUT_MS + 1);
    expect(store.getState().issuesMap).toBe(map);
    expect(store.getState().pendingIds.size).toBe(0);
    pending.resolve(issue());
    await command;
    expect(store.getState().issuesMap).toBe(map);
  });
  it("does not schedule an old retry after its error subscriber installs a new workspace", async () => {
    await configure();
    const failure = deferred<Issue[]>();
    vi.mocked(getReadyIssues).mockReturnValueOnce(failure.promise);
    let newer: Promise<void> | undefined;
    let switched = false;
    const unsubscribe = store.subscribe((state) => {
      if (switched || state.error !== "old retryable failure") return;
      switched = true;
      newer = state.fetchIssues({
        workspaceId: "B",
        mode: "ready",
        sourceRepos: ["repo-a"],
      });
    });
    const older = store.getState().refetch();
    failure.reject(new Error("old retryable failure"));
    await older;
    await newer;
    expect(switched).toBe(true);
    const map = store.getState().issuesMap;
    expect(vi.mocked(getReadyIssues).mock.calls.map((call) => call[0])).toEqual(
      ["A", "A", "B"],
    );
    await vi.advanceTimersByTimeAsync(60_000);
    expect(vi.mocked(getReadyIssues).mock.calls.map((call) => call[0])).toEqual(
      ["A", "A", "B"],
    );
    expect(store.getState().issuesMap).toBe(map);
    expect(store.getState().nextRetryAt).toBeNull();
    unsubscribe();
  });
});
