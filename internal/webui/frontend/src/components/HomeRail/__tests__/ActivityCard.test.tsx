/**
 * @vitest-environment jsdom
 */

import { fireEvent, render, screen } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import "@testing-library/jest-dom";

import type { RecentActivityItem } from "@/hooks/workspace";

import { ActivityCard } from "../ActivityCard";

const mockWorkspaceContext = vi.hoisted(() => ({
  repos: [] as { name: string; source_repo_id?: string }[],
}));

vi.mock("@/hooks/workspace", () => ({
  SEED_ISSUE_COUNT: 5,
  useWorkspaceContext: () => mockWorkspaceContext,
}));

const activity: RecentActivityItem[] = [
  {
    id: "event-1",
    timestamp: "2026-08-21T15:48:02.000Z",
    actor: "agent-dev-1",
    issueId: "TASK-1",
    sourceRepo: "source-id",
    text: "updated",
    marker: "default",
  },
];

function activityItems(count: number): RecentActivityItem[] {
  return Array.from({ length: count }, (_, index) => ({
    ...activity[0],
    id: `event-${index + 1}`,
    issueId: `TASK-${index + 1}`,
  }));
}

beforeEach(() => {
  mockWorkspaceContext.repos = [];
});

describe("ActivityCard", () => {
  it("reveals activity in 15-event pages near the scroll boundary", () => {
    render(<ActivityCard activity={activityItems(38)} />);

    const scroll = screen.getByTestId("activity-scroll");
    Object.defineProperties(scroll, {
      clientHeight: { configurable: true, value: 420 },
      scrollHeight: { configurable: true, value: 900 },
    });

    expect(screen.getAllByTestId("activity-row")).toHaveLength(15);
    expect(screen.getByText(/Showing 15 of 38 events/)).toBeInTheDocument();

    fireEvent.scroll(scroll, { target: { scrollTop: 200 } });
    expect(screen.getAllByTestId("activity-row")).toHaveLength(15);

    fireEvent.scroll(scroll, { target: { scrollTop: 460 } });
    expect(screen.getAllByTestId("activity-row")).toHaveLength(30);
    expect(screen.getByText(/Showing 30 of 38 events/)).toBeInTheDocument();

    fireEvent.scroll(scroll, { target: { scrollTop: 480 } });
    expect(screen.getAllByTestId("activity-row")).toHaveLength(38);
    expect(screen.getByText(/Showing 38 of 38 events/)).toBeInTheDocument();
  });

  it("shows an issue repo chip only in a multi-repo workspace", () => {
    mockWorkspaceContext.repos = [
      { name: "source-repo", source_repo_id: "source-id" },
      { name: "web", source_repo_id: "web-id" },
    ];
    const { rerender } = render(<ActivityCard activity={activity} />);

    expect(screen.getByTestId("activity-repo")).toHaveTextContent(
      "source-repo",
    );

    mockWorkspaceContext.repos = [
      { name: "source-repo", source_repo_id: "source-id" },
    ];
    rerender(<ActivityCard activity={activity} />);

    expect(screen.queryByTestId("activity-repo")).not.toBeInTheDocument();
  });
});
