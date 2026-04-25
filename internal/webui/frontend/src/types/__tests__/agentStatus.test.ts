/**
 * Unit tests for the AgentStatusEntry -> LoomAgentStatus adapter.
 *
 * These tests cover the mapping rules in mapToLoomAgentStatus and
 * mapAgentStatusResponse, focusing on:
 *   - field-by-field passthrough on a fully populated entry,
 *   - null/undefined handling for optional fields (must be ABSENT, not undefined,
 *     because LoomAgentStatus is compiled with exactOptionalPropertyTypes),
 *   - the workspace label coming from the response envelope (not the entry),
 *   - the entry.error -> collection_error rename,
 *   - propagation of workspace_name across all entries in the envelope.
 */

import { describe, it, expect } from "vitest";

import type {
  AgentStatusEntry,
  WorkspaceAgentStatusResponse,
} from "../agent/agentStatus";
import {
  mapToLoomAgentStatus,
  mapAgentStatusResponse,
} from "../agent/agentStatus";

/** Build a fully populated AgentStatusEntry for the happy-path test. */
function fullEntry(): AgentStatusEntry {
  return {
    worktree: "nova",
    worktree_path: "/home/user/repo/.worktrees/nova",
    path: "/home/user/repo/.worktrees/nova",
    role: "implement",
    repo: "api",
    workspace: "ignored-from-entry",
    cross_repo: true,
    pid: 12345,
    status: "working:bd-100",
    supervisor_status: "running",
    restart_count: 2,
    last_error_class: "TransientError",
    backoff_until: "2024-08-01T12:30:00Z",
    stop_reason: "user_requested",
    task_id: "bd-100",
    epic_id: "epic-7",
    current_backend: "claude",
    branch: "feature/nova",
    ahead: 3,
    behind: 1,
    changes: 5,
    remote_branch: "origin/feature/nova",
    yield_requested: true,
    yield_reason: "shutting down",
    yield_requested_at: "2024-08-01T12:00:00Z",
    error: "git status failed",
  };
}

describe("mapToLoomAgentStatus", () => {
  it("full mapping: every populated field is propagated correctly", () => {
    const entry = fullEntry();
    const result = mapToLoomAgentStatus(entry, "ws-from-envelope");

    expect(result.name).toBe(entry.worktree);
    expect(result.branch).toBe(entry.branch);
    expect(result.status).toBe(entry.status);
    expect(result.ahead).toBe(entry.ahead);
    expect(result.behind).toBe(entry.behind);
    expect(result.role).toBe(entry.role);
    expect(result.repo).toBe(entry.repo);
    expect(result.pid).toBe(entry.pid);
    expect(result.supervisor_status).toBe(entry.supervisor_status);
    expect(result.restart_count).toBe(entry.restart_count);
    expect(result.last_error_class).toBe(entry.last_error_class);
    expect(result.backoff_until).toBe(entry.backoff_until);
    expect(result.stop_reason).toBe(entry.stop_reason);
    expect(result.task_id).toBe(entry.task_id);
    expect(result.epic_id).toBe(entry.epic_id);
    expect(result.current_backend).toBe(entry.current_backend);
    expect(result.remote_branch).toBe(entry.remote_branch);
    expect(result.yield_requested).toBe(entry.yield_requested);
    expect(result.yield_reason).toBe(entry.yield_reason);
    expect(result.yield_requested_at).toBe(entry.yield_requested_at);
    expect(result.changes_count).toBe(entry.changes);

    expect(result.daemon_managed).toBe(true);
    expect(result.workspace).toBe("ws-from-envelope");
    expect(result.path).toBe(entry.worktree_path);
    expect(result.worktree_path).toBe(entry.worktree_path);
    expect(result.cross_repo).toBe(entry.cross_repo);
  });

  it("null timestamps are dropped (backoff_until, yield_requested_at)", () => {
    const entry: AgentStatusEntry = {
      ...fullEntry(),
      backoff_until: null,
      yield_requested_at: null,
    };

    const result = mapToLoomAgentStatus(entry, "ws-1");

    expect(result).not.toHaveProperty("backoff_until");
    expect(result).not.toHaveProperty("yield_requested_at");
  });

  it("undefined optional fields are absent on result", () => {
    // Build an entry with only the required fields populated.
    const entry: AgentStatusEntry = {
      worktree: "ember",
      worktree_path: "/home/user/repo/.worktrees/ember",
      path: "/home/user/repo/.worktrees/ember",
      workspace: "ws-1",
      cross_repo: false,
      pid: 0,
      status: "ready",
      supervisor_status: "stopped",
      restart_count: 0,
      branch: "main",
      ahead: 0,
      behind: 0,
      changes: 0,
      yield_requested: false,
      // Omit role, repo, last_error_class, backoff_until, stop_reason,
      // task_id, epic_id, current_backend, remote_branch, yield_reason,
      // yield_requested_at, error.
    };

    const result = mapToLoomAgentStatus(entry, "ws-1");

    expect(result).not.toHaveProperty("role");
    expect(result).not.toHaveProperty("repo");
    expect(result).not.toHaveProperty("last_error_class");
    expect(result).not.toHaveProperty("backoff_until");
    expect(result).not.toHaveProperty("stop_reason");
    expect(result).not.toHaveProperty("task_id");
    expect(result).not.toHaveProperty("epic_id");
    expect(result).not.toHaveProperty("current_backend");
    expect(result).not.toHaveProperty("remote_branch");
    expect(result).not.toHaveProperty("yield_reason");
    expect(result).not.toHaveProperty("yield_requested_at");
    expect(result).not.toHaveProperty("collection_error");
  });

  it("workspace comes from envelope arg, not the entry's workspace field", () => {
    const entry: AgentStatusEntry = {
      ...fullEntry(),
      workspace: "from-entry-should-not-leak",
    };

    const result = mapToLoomAgentStatus(entry, "alpha");

    expect(result.workspace).toBe("alpha");
  });

  it("empty workspace name is preserved as empty string", () => {
    const entry = fullEntry();
    const result = mapToLoomAgentStatus(entry, "");

    expect(result.workspace).toBe("");
  });

  it("entry.error is renamed to collection_error", () => {
    const entry: AgentStatusEntry = {
      ...fullEntry(),
      error: "git failed",
    };

    const result = mapToLoomAgentStatus(entry, "ws-1");

    expect(result.collection_error).toBe("git failed");
    // The raw `error` field is not part of LoomAgentStatus; it should not be
    // copied verbatim.
    expect(result).not.toHaveProperty("error");
  });
});

describe("mapAgentStatusResponse", () => {
  it("returns [] for an empty agents array", () => {
    const response: WorkspaceAgentStatusResponse = {
      agents: [],
      ipc_socket_active: true,
      daemon_pid: 999,
      daemon_started_at: "2024-08-01T00:00:00Z",
      workspace_name: "ws-empty",
      timestamp: "2024-08-01T12:00:00Z",
    };

    const result = mapAgentStatusResponse(response);

    expect(result).toEqual([]);
  });

  it("propagates workspace_name to every mapped agent", () => {
    const baseEntry = fullEntry();
    const response: WorkspaceAgentStatusResponse = {
      agents: [
        { ...baseEntry, worktree: "nova" },
        { ...baseEntry, worktree: "ember" },
        { ...baseEntry, worktree: "falcon" },
      ],
      ipc_socket_active: true,
      daemon_pid: 999,
      daemon_started_at: "2024-08-01T00:00:00Z",
      workspace_name: "ws1",
      timestamp: "2024-08-01T12:00:00Z",
    };

    const result = mapAgentStatusResponse(response);

    expect(result).toHaveLength(3);
    for (const agent of result) {
      expect(agent.workspace).toBe("ws1");
    }
    expect(result.map((a) => a.name)).toEqual(["nova", "ember", "falcon"]);
  });
});
