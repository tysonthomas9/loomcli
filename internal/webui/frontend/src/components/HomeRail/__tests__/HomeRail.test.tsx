/**
 * @vitest-environment jsdom
 */

import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import "@testing-library/jest-dom";

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
});
