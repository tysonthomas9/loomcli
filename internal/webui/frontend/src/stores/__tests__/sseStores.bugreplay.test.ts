/**
 * @vitest-environment jsdom
 */
import { afterEach, expect, it, vi } from "vitest";

import type { Issue } from "../../types";

vi.mock("../../api/agents", () => ({
  fetchAgents: vi.fn(),
  fetchStatus: vi.fn(),
  fetchTasks: vi.fn(),
}));

vi.mock(import("../../api/issues"), async (importOriginal) => ({
  ...(await importOriginal()),
  getReadyIssues: vi.fn(),
  getKanbanIssues: vi.fn(),
  fetchGraphIssues: vi.fn(),
  updateIssue: vi.fn(),
}));

import { fetchStatus } from "../../api/agents";
import { getReadyIssues, updateIssue } from "../../api/issues";
import { createAgentStore } from "../agentStore";
import { createIssueStore } from "../issueStore";

function deferred<T>() {
  let resolve!: (value: T) => void;
  let reject!: (error: unknown) => void;
  const promise = new Promise<T>((res, rej) => {
    resolve = res;
    reject = rej;
  });
  return { promise, resolve, reject };
}

function statusFor(agentName: string) {
  return {
    agents: [{ name: agentName, branch: agentName, status: "ready" }],
    tasks: {},
    taskLists: {},
    agentTasks: {},
    sync: {},
    stats: {},
  } as unknown as Awaited<ReturnType<typeof fetchStatus>>;
}

afterEach(() => {
  vi.useRealTimers();
  vi.clearAllMocks();
});

it("#617 a late agent-status response from the old workspace cannot overwrite the new one", async () => {
  const oldWorkspace = deferred<Awaited<ReturnType<typeof fetchStatus>>>();
  const newWorkspace = deferred<Awaited<ReturnType<typeof fetchStatus>>>();
  vi.mocked(fetchStatus).mockImplementation((workspaceId?: string) =>
    workspaceId === "ws-a" ? oldWorkspace.promise : newWorkspace.promise,
  );
  const store = createAgentStore();

  store.getState().startPolling({ workspaceId: "ws-a", pollInterval: 0 });
  store.getState().reset();
  store.getState().startPolling({ workspaceId: "ws-b", pollInterval: 0 });
  expect(vi.mocked(fetchStatus)).toHaveBeenLastCalledWith("ws-b");

  newWorkspace.resolve(statusFor("agent-b"));
  await vi.waitFor(() =>
    expect(store.getState().agents.map((a) => a.name)).toEqual(["agent-b"]),
  );
  oldWorkspace.resolve(statusFor("agent-a"));
  await new Promise((resolve) => setTimeout(resolve, 0));

  expect(
    store.getState().agents.map((a) => a.name),
    "workspace A's late fetchStatus result was committed into workspace B",
  ).toEqual(["agent-b"]);
  store.getState().reset();
});

it("#667 an old workspace command failure cannot roll back a newer same-ID command", async () => {
  const issue = (status: Issue["status"]): Issue => ({
    id: "issue-1",
    title: "Shared ID",
    priority: 2,
    status,
    created_at: "2026-01-01T00:00:00Z",
    updated_at: "2026-01-01T00:00:00Z",
  });
  const oldCommand = deferred<never>();
  const newCommand = deferred<never>();
  vi.mocked(getReadyIssues).mockResolvedValue([issue("open")]);
  vi.mocked(updateIssue).mockImplementation(
    (workspaceId: string) =>
      (workspaceId === "ws-a"
        ? oldCommand.promise
        : newCommand.promise) as ReturnType<typeof updateIssue>,
  );
  const store = createIssueStore();

  await store.getState().fetchIssues({ workspaceId: "ws-a", mode: "ready" });
  const oldUpdate = store
    .getState()
    .updateIssueStatus("issue-1", "in_progress", "ws-a")
    .catch(() => undefined);

  store.getState().reset();
  await store.getState().fetchIssues({ workspaceId: "ws-b", mode: "ready" });
  const newUpdate = store
    .getState()
    .updateIssueStatus("issue-1", "closed", "ws-b")
    .catch(() => undefined);
  expect(store.getState().issuesMap.get("issue-1")?.status).toBe("closed");

  oldCommand.reject(new Error("workspace A command failed"));
  await oldUpdate;

  expect(
    store.getState().issuesMap.get("issue-1")?.status,
    "workspace A's failure rolled back workspace B's pending optimistic state",
  ).toBe("closed");
  expect(store.getState().pendingIds.has("issue-1")).toBe(true);

  newCommand.reject(new Error("cleanup"));
  await newUpdate;
  store.getState().reset();
});
