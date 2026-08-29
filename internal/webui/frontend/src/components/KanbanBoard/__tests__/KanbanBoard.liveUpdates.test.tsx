/**
 * @vitest-environment jsdom
 */

import { act, cleanup, render, screen, within } from "@testing-library/react";
import { useMemo } from "react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { useStore } from "zustand";
import type { StoreApi } from "zustand/vanilla";
import "@testing-library/jest-dom";

import type { MutationPayload } from "@/api/common";
import { getKanbanIssues } from "@/api/issues";
import { createIssueStore, type IssueStore } from "@/stores/issueStore";
import type { Issue, Status } from "@/types";

import { KanbanBoard } from "../KanbanBoard";

vi.mock(import("../../../api/issues"), async (importOriginal) => {
  const actual = await importOriginal();
  return {
    ...actual,
    getKanbanIssues: vi.fn(),
  };
});

const mockGetKanbanIssues = vi.mocked(getKanbanIssues);

const COLUMN_NAMES: Record<Status, string> = {
  open: "Open issues",
  in_progress: "In Progress issues",
  blocked: "Blocked issues",
  deferred: "Backlog issues",
  review: "Review issues",
  closed: "Done issues",
  tombstone: "Done issues",
  pinned: "Open issues",
  hooked: "Open issues",
};

function makeIssue(
  status: Status,
  isDeferred: boolean | null,
  updatedAt: string,
): Issue {
  return {
    id: "task-12",
    title: "Card follows live status",
    priority: 2,
    status,
    issue_type: "task",
    created_at: "2026-08-20T18:00:00.000Z",
    updated_at: updatedAt,
    // The live list endpoint returns null when the derived flag is clear.
    is_deferred: isDeferred,
  } as Issue;
}

function StoreBackedBoard({
  store,
}: {
  store: StoreApi<IssueStore>;
}): JSX.Element {
  const issuesMap = useStore(store, (state) => state.issuesMap);
  const issues = useMemo(() => [...issuesMap.values()], [issuesMap]);
  return <KanbanBoard issues={issues} />;
}

function expectCardInColumn(status: Status): void {
  const column = screen.getByRole("region", { name: COLUMN_NAMES[status] });
  expect(
    within(column).getByText("Card follows live status"),
  ).toBeInTheDocument();
}

describe("KanbanBoard live issue updates", () => {
  let store: StoreApi<IssueStore>;
  let emitMutation: ((mutation: MutationPayload) => void) | null;
  let disconnect: (() => void) | null;

  beforeEach(() => {
    vi.useFakeTimers();
    vi.clearAllMocks();
    store = createIssueStore();
    emitMutation = null;
    disconnect = null;
  });

  afterEach(() => {
    cleanup();
    disconnect?.();
    store.getState().reset();
    vi.useRealTimers();
  });

  it("moves a card through deferred and every later SSE status", async () => {
    mockGetKanbanIssues.mockResolvedValueOnce([
      makeIssue("open", null, "2026-08-20T18:00:00.000Z"),
    ]);

    render(<StoreBackedBoard store={store} />);
    await act(async () => {
      await store.getState().fetchIssues({
        workspaceId: "workspace-1",
        mode: "kanban",
      });
    });
    expectCardInColumn("open");

    disconnect = store.getState().connectToEvents((callback) => {
      emitMutation = callback;
      return () => {
        emitMutation = null;
      };
    });

    // Fleet's issue.defer action can omit a field-level changes payload. The
    // store therefore invalidates and reloads the authoritative projection.
    mockGetKanbanIssues.mockResolvedValueOnce([
      makeIssue("deferred", true, "2026-08-20T18:01:00.000Z"),
    ]);
    await act(async () => {
      emitMutation?.({
        type: "update",
        entity_type: "issue",
        entity_id: "task-12",
        action: "issue.defer",
        issue_id: "task-12",
        workspace_id: "workspace-1",
        timestamp: "2026-08-20T18:01:00.000Z",
      });
      await vi.advanceTimersByTimeAsync(1_000);
    });
    expectCardInColumn("deferred");

    const transitions: Array<{
      status: Status;
      serverTimestamp: string;
      eventTimestamp: string;
    }> = [
      {
        status: "in_progress",
        serverTimestamp: "2026-08-20T18:02:00.000Z",
        eventTimestamp: "2026-08-20T18:02:00.500Z",
      },
      {
        status: "review",
        serverTimestamp: "2026-08-20T18:03:00.000Z",
        eventTimestamp: "2026-08-20T18:03:00.500Z",
      },
      {
        status: "blocked",
        serverTimestamp: "2026-08-20T18:04:00.000Z",
        eventTimestamp: "2026-08-20T18:04:00.500Z",
      },
      {
        status: "review",
        serverTimestamp: "2026-08-20T18:05:00.000Z",
        eventTimestamp: "2026-08-20T18:05:00.500Z",
      },
      {
        status: "blocked",
        serverTimestamp: "2026-08-20T18:06:00.000Z",
        eventTimestamp: "2026-08-20T18:06:00.500Z",
      },
    ];

    let previousStatus: Status = "deferred";
    for (const transition of transitions) {
      mockGetKanbanIssues.mockResolvedValueOnce([
        makeIssue(transition.status, null, transition.serverTimestamp),
      ]);

      await act(async () => {
        emitMutation?.({
          type: "update",
          entity_type: "issue",
          entity_id: "task-12",
          action: "issue.update",
          issue_id: "task-12",
          workspace_id: "workspace-1",
          old_status: previousStatus,
          new_status: transition.status,
          timestamp: transition.eventTimestamp,
        });
        await vi.advanceTimersByTimeAsync(1_000);
      });

      expectCardInColumn(transition.status);
      expect(screen.queryByLabelText("Deferred")).not.toBeInTheDocument();
      previousStatus = transition.status;
    }
  });
});
