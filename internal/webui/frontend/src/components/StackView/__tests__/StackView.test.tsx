/**
 * @vitest-environment jsdom
 */
import "@testing-library/jest-dom";
import { fireEvent, render, screen, within } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { beforeEach, describe, expect, it, vi } from "vitest";

import {
  fetchStacks,
  queueMergeUpTo,
  type StackCard,
} from "@/api/workspace/git";
import type { Issue } from "@/types";

import { StackView, canMergeUpTo, stackNote } from "../StackView";

vi.mock("@/api/workspace/git", () => ({
  fetchStacks: vi.fn(),
  queueMergeUpTo: vi.fn(),
}));

function card(states: StackCard["layers"][number]["state"][]): StackCard {
  return {
    stack_id: "feature",
    repo: "octo/loomcli",
    backend: "native",
    layers: states.map((state, index) => ({
      change: `task-${index + 1}`,
      pr_number: 140 + index,
      pr_url: `https://github.com/octo/loomcli/pull/${140 + index}`,
      state,
    })),
  };
}

const issues = [
  { id: "task-3", title: "Carry max_run_duration", parent_title: "Hardening" },
] as Issue[];

function renderView() {
  return render(
    <MemoryRouter>
      <StackView workspaceId="W" issues={issues} />
    </MemoryRouter>,
  );
}

describe("StackView", () => {
  beforeEach(() => {
    vi.mocked(fetchStacks).mockReset();
    vi.mocked(queueMergeUpTo).mockReset();
  });

  it("shows the rows bottom up with one state and folds merged rows", async () => {
    vi.mocked(fetchStacks).mockResolvedValue([
      card(["merged", "merged", "ready", "needs_review", "draft"]),
    ]);
    renderView();
    const view = await screen.findByTestId("stack-card");
    expect(view).toHaveAttribute("data-publisher", "native");
    expect(within(view).getByText("Hardening")).toBeInTheDocument();
    expect(within(view).getByText("GitHub stack")).toBeInTheDocument();
    expect(within(view).getByText("5 changes")).toBeInTheDocument();
    const summary = within(view).getByTestId("stack-merged-summary");
    expect(summary).toHaveTextContent("2 merged changes · Show history");
    let rows = within(view).getAllByTestId("stack-row");
    expect(rows.map((row) => row.textContent)).toEqual([
      "3Carry max_run_durationloomcli#142Merge up to hereReady to merge",
      "4task-4loomcli#143Needs review",
      "5task-5loomcli#144Draft",
    ]);
    expect(within(rows[0]!).getByTestId("stack-row-changes")).toHaveAttribute(
      "href",
      "/ws/W/issues/task-3?tab=changes",
    );
    fireEvent.click(summary);
    rows = within(view).getAllByTestId("stack-row");
    expect(rows).toHaveLength(5);
    expect(rows[0]).toHaveAttribute("data-state", "merged");
  });

  it("queues Merge up to here and shows a refusal plainly", async () => {
    vi.mocked(fetchStacks).mockResolvedValue([card(["approved", "ready"])]);
    vi.mocked(queueMergeUpTo).mockRejectedValue(
      new Error("a merge up to task-1 is already running for this stack"),
    );
    renderView();
    const buttons = await screen.findAllByTestId("merge-up-to-here");
    expect(buttons).toHaveLength(2);
    fireEvent.click(buttons[1]!);
    expect(await screen.findByTestId("stack-merge-error")).toHaveTextContent(
      "already running",
    );
    expect(queueMergeUpTo).toHaveBeenCalledWith("W", "task-2");
    expect(fetchStacks).toHaveBeenCalledTimes(2);
  });
});

describe("canMergeUpTo", () => {
  it("needs every unmerged PR at or below the row approved", () => {
    const stack = card(["merged", "approved", "checks_failing", "ready"]);
    expect([0, 1, 2, 3].map((index) => canMergeUpTo(stack, index))).toEqual([
      true,
      true,
      false,
      false,
    ]);
  });

  it("is not offered while a merge runs, and is again once it is blocked", () => {
    const merge = {
      stack_id: "feature",
      target: "task-1",
      backend: "loom",
      phase: "ready",
    };
    expect(canMergeUpTo({ ...card(["approved"]), merge }, 0)).toBe(false);
    expect(
      canMergeUpTo({ ...card(["approved"]), merge: { ...merge, phase: "blocked" } }, 0),
    ).toBe(true);
  });
});

describe("stackNote", () => {
  it("says who is merging without phases or stack IDs", () => {
    expect(
      stackNote({
        ...card(["merging"]),
        merge: {
          stack_id: "feature",
          target: "task-1",
          pr_number: 140,
          backend: "native",
          phase: "dispatching",
          queued_by: "lead",
        },
      }),
    ).toBe("Merging up to #140 (queued by the lead)");
    expect(stackNote({ ...card(["ready"]), note: "Merge stopped: x" })).toBe(
      "Merge stopped: x",
    );
  });
});
