/**
 * @vitest-environment jsdom
 */

import "@testing-library/jest-dom";
import { render, screen, fireEvent } from "@testing-library/react";
import { describe, it, expect, vi } from "vitest";

import type { Issue } from "@/types";

import { EpicTicketsSection } from "../EpicTicketsSection";

function issue(overrides: Partial<Issue> & Pick<Issue, "id">): Issue {
  return {
    id: overrides.id,
    title: overrides.title ?? `${overrides.id} title`,
    priority: 2,
    status: "open",
    issue_type: "task",
    created_at: "2026-01-01T00:00:00Z",
    updated_at: "2026-01-01T00:00:00Z",
    ...overrides,
  } as Issue;
}

describe("EpicTicketsSection", () => {
  it("renders nothing when the epic has no children", () => {
    const { container } = render(<EpicTicketsSection childIssues={[]} />);
    expect(container).toBeEmptyDOMElement();
  });

  it("shows progress, claim badge, and tickets sorted by status", () => {
    render(
      <EpicTicketsSection
        childIssues={[
          issue({ id: "T-DONE", status: "closed" }),
          issue({ id: "T-ACTIVE", status: "in_progress" }),
          issue({ id: "T-OPEN", status: "open" }),
        ]}
        claimedBy="atlas"
      />,
    );

    expect(screen.getByText("Tickets (3)")).toBeInTheDocument();
    expect(screen.getByText("1 of 3 done")).toBeInTheDocument();
    expect(screen.getByText("atlas")).toBeInTheDocument();
    expect(screen.queryByText("Unclaimed")).not.toBeInTheDocument();

    const rows = screen.getAllByRole("button");
    expect(rows.map((row) => row.textContent)).toEqual([
      expect.stringContaining("T-ACTIVE"),
      expect.stringContaining("T-OPEN"),
      expect.stringContaining("T-DONE"),
    ]);
  });

  it("shows Unclaimed when no lead runs the epic", () => {
    render(
      <EpicTicketsSection childIssues={[issue({ id: "T-1" })]} />,
    );
    expect(screen.getByText("Unclaimed")).toBeInTheDocument();
  });

  it("navigates to a child ticket on click", () => {
    const onNavigateToIssue = vi.fn();
    render(
      <EpicTicketsSection
        childIssues={[issue({ id: "T-1", title: "Build the thing" })]}
        onNavigateToIssue={onNavigateToIssue}
      />,
    );

    fireEvent.click(
      screen.getByRole("button", { name: "Open ticket T-1: Build the thing" }),
    );
    expect(onNavigateToIssue).toHaveBeenCalledWith(
      expect.objectContaining({ id: "T-1" }),
    );
  });
});
