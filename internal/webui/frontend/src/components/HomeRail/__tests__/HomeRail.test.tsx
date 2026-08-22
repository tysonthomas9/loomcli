/**
 * @vitest-environment jsdom
 */

import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import "@testing-library/jest-dom";

import { PipelineCard } from "../PipelineCard";
import { ThisWorkspaceCard } from "../ThisWorkspaceCard";
import type { Issue } from "@/types";

function issue(overrides: Partial<Issue>): Issue {
  return {
    id: "TASK-1",
    title: "Rail task",
    priority: 2,
    issue_type: "task",
    status: "open",
    created_at: "2026-08-21T15:00:00.000Z",
    updated_at: "2026-08-21T15:00:00.000Z",
    ...overrides,
  } as Issue;
}

describe("Home rail cards", () => {
  it("counts non-epic workspace statuses", () => {
    render(
      <ThisWorkspaceCard
        issues={[
          issue({ id: "CLOSED", status: "closed" }),
          issue({ id: "BLOCKED", status: "blocked" }),
          issue({ id: "EPIC", issue_type: "epic", status: "closed" }),
        ]}
        workspaceId="workspace-1"
      />,
    );

    expect(screen.getByTestId("rail-this-workspace")).toHaveTextContent(
      "2 tasks · 1 closed",
    );
    expect(screen.getByText("blocked 1")).toBeInTheDocument();
  });

  it("renders all pipeline stages with their stable identifiers", () => {
    render(
      <PipelineCard
        counts={{
          backlog: 0,
          designing: 1,
          awaitingApproval: 2,
          building: 3,
          deferred: 0,
          awaitingMerge: [{ branch: "localmode", count: 1 }],
          merged: 4,
          taskCount: 10,
        }}
      />,
    );

    expect(screen.getAllByTestId("pipeline-row")).toHaveLength(7);
    expect(
      screen.getByText("Awaiting merge · 1 branch ahead of localmode"),
    ).toBeInTheDocument();
    const building = screen
      .getAllByTestId("pipeline-row")
      .find((row) => row.dataset.row === "building");
    expect(building).toHaveAttribute("title", "in progress + blocked");
  });
});
