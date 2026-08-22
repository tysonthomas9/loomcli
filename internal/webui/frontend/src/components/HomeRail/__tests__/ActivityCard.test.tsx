/**
 * @vitest-environment jsdom
 */

import { render, screen } from "@testing-library/react";
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

beforeEach(() => {
  mockWorkspaceContext.repos = [];
});

describe("ActivityCard", () => {
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
