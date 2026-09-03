/**
 * @vitest-environment jsdom
 *
 * Regression cover for PUPPET-428: the card used to count whatever issue array
 * the page had fetched, so a truncated or filtered board silently under-reported
 * the workspace by roughly half.
 */

import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import "@testing-library/jest-dom";

import {
  ThisWorkspaceCard,
  workspaceCountsFromStats,
} from "../ThisWorkspaceCard";
import type { Statistics } from "@/types";

/** The real PUPPET numbers from the ticket, including blocked 10 vs. 6. */
const STATS: Statistics = {
  total_issues: 408,
  open_issues: 52,
  in_progress_issues: 2,
  closed_issues: 340,
  blocked_issues: 6, // computed dependency-blocked view
  status_blocked_issues: 10, // status == blocked
  deferred_issues: 0,
  ready_issues: 30,
  review_issues: 4,
  tombstone_issues: 0,
  pinned_issues: 0,
  epics_eligible_for_closure: 0,
  average_lead_time_hours: 0,
};

function renderedRows(): string[] {
  return Array.from(
    screen.getByTestId("rail-this-workspace").querySelectorAll("span"),
  ).map((node) => node.textContent ?? "");
}

describe("ThisWorkspaceCard", () => {
  it("renders identical numbers regardless of what the page has fetched", () => {
    // The card takes no issue collection at all — reintroducing one would not
    // compile — so the "full page" and "truncated page" cases are the same
    // props by construction. Both render the workspace-wide stats.
    const counts = workspaceCountsFromStats(STATS);

    const full = render(
      <ThisWorkspaceCard counts={counts} workspaceId="PUPPET" />,
    );
    const fullRows = renderedRows();
    const fullRunline =
      screen.getByTestId("rail-this-workspace").textContent ?? "";
    full.unmount();

    render(<ThisWorkspaceCard counts={counts} workspaceId="PUPPET" />);

    expect(renderedRows()).toEqual(fullRows);
    expect(screen.getByTestId("rail-this-workspace").textContent).toBe(
      fullRunline,
    );
    // And they are the stats numbers, not a page's.
    expect(screen.getByTestId("rail-this-workspace")).toHaveTextContent(
      "408 issues · 340 closed",
    );
    expect(screen.getByText("open 52")).toBeInTheDocument();
    expect(screen.getByText("review 4")).toBeInTheDocument();
  });

  it("keeps segment widths within 100% when deferred pushes rows past total", () => {
    // total_issues excludes deferred, so the rows can sum past it.
    const counts = workspaceCountsFromStats({ ...STATS, deferred_issues: 92 });

    render(<ThisWorkspaceCard counts={counts} workspaceId="PUPPET" />);

    const widths = Array.from(
      screen
        .getByTestId("rail-this-workspace")
        .querySelectorAll<HTMLElement>("i[data-status]"),
    )
      .map((node) => node.style.width)
      .filter((width) => width !== "")
      .map((width) => Number.parseFloat(width));

    expect(widths.length).toBeGreaterThan(0);
    const sum = widths.reduce((acc, width) => acc + width, 0);
    expect(sum).toBeLessThanOrEqual(100.0001);
  });

  it("renders no segments and no NaN widths for an empty workspace", () => {
    const counts = workspaceCountsFromStats({
      ...STATS,
      total_issues: 0,
      open_issues: 0,
      in_progress_issues: 0,
      closed_issues: 0,
      status_blocked_issues: 0,
      review_issues: 0,
      deferred_issues: 0,
    });

    render(<ThisWorkspaceCard counts={counts} workspaceId="PUPPET" />);

    expect(
      screen
        .getByTestId("rail-this-workspace")
        .querySelectorAll("i[data-status][style]"),
    ).toHaveLength(0);
  });
});

describe("workspaceCountsFromStats", () => {
  it("maps blocked from status_blocked_issues, not blocked_issues", () => {
    const counts = workspaceCountsFromStats(STATS);

    expect(counts?.blocked).toBe(10);
    expect(counts?.blocked).not.toBe(STATS.blocked_issues);
  });

  it("maps every card field from the stats payload", () => {
    expect(workspaceCountsFromStats(STATS)).toEqual({
      total: 408,
      closed: 340,
      inProgress: 2,
      review: 4,
      open: 52,
      blocked: 10,
      deferred: 0,
    });
  });

  it("returns null when stats have not loaded", () => {
    expect(workspaceCountsFromStats(null)).toBeNull();
  });

  it("coerces the newest fields to 0 when an older server omits them", () => {
    const legacy = { ...STATS } as Partial<Statistics>;
    delete legacy.review_issues;
    delete legacy.status_blocked_issues;

    const counts = workspaceCountsFromStats(legacy as Statistics);

    expect(counts?.review).toBe(0);
    expect(counts?.blocked).toBe(0);
    expect(counts?.total).toBe(408);
  });
});
