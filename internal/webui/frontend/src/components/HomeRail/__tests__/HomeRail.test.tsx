/**
 * @vitest-environment jsdom
 */

import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import "@testing-library/jest-dom";

import { ThisWorkspaceCard } from "../ThisWorkspaceCard";
import type { ThisWorkspaceCounts } from "../ThisWorkspaceCard";

function counts(overrides: Partial<ThisWorkspaceCounts> = {}) {
  return {
    total: 408,
    closed: 340,
    inProgress: 2,
    review: 4,
    open: 52,
    blocked: 10,
    deferred: 0,
    ...overrides,
  } satisfies ThisWorkspaceCounts;
}

describe("Home rail cards", () => {
  it("renders the workspace-wide counts it is handed", () => {
    render(<ThisWorkspaceCard counts={counts()} workspaceId="workspace-1" />);

    // "issues", not "tasks": the counts include epics by decision.
    expect(screen.getByTestId("rail-this-workspace")).toHaveTextContent(
      "408 issues · 340 closed",
    );
    expect(screen.getByText("blocked 10")).toBeInTheDocument();
    expect(screen.getByText("review 4")).toBeInTheDocument();
    expect(screen.getByText("open 52")).toBeInTheDocument();
    expect(screen.getByText("in progress 2")).toBeInTheDocument();
    expect(screen.getByText("deferred 0")).toBeInTheDocument();
  });

  it("renders a zero state while counts are still null", () => {
    render(<ThisWorkspaceCard counts={null} workspaceId="workspace-1" />);

    expect(screen.getByTestId("rail-this-workspace")).toHaveTextContent(
      "0 issues · 0 closed",
    );
    expect(screen.getByText("open 0")).toBeInTheDocument();
  });
});
