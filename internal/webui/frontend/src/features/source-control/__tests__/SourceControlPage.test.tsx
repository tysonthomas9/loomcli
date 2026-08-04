// @vitest-environment jsdom

import { render, screen, waitFor } from "@testing-library/react";
import "@testing-library/jest-dom";
import { createMemoryRouter, Outlet, RouterProvider } from "react-router-dom";
import { beforeEach, describe, expect, it, vi } from "vitest";

import type { Issue } from "@/types";

import { SourceControlPage } from "../index";

const mocks = vi.hoisted(() => ({
  fetchPullRequests: vi.fn(),
}));

vi.mock("../api/pullRequests", () => ({
  fetchPullRequests: mocks.fetchPullRequests,
}));

function reviewIssue(): Issue {
  return {
    id: "TASK-7",
    title: "Review the Source Control boundary",
    status: "review",
    issue_type: "task",
    priority: 2,
    created_at: "2026-08-04T00:00:00Z",
    updated_at: "2026-08-04T00:00:00Z",
  } as Issue;
}

describe("SourceControlPage route composition", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    mocks.fetchPullRequests.mockResolvedValue({
      pullRequests: [],
      warnings: [],
    });
  });

  it("renders the Loom-backed review queue from the narrow outlet context", async () => {
    const router = createMemoryRouter(
      [
        {
          element: (
            <Outlet
              context={{
                sourceControl: {
                  workspaceId: "WS",
                  repos: [],
                  issues: [reviewIssue()],
                  agents: [],
                  refetchIssues: vi.fn(),
                  openIssue: vi.fn(),
                  showToast: vi.fn(() => "toast-1"),
                },
              }}
            />
          ),
          children: [{ path: "/prs", element: <SourceControlPage /> }],
        },
      ],
      { initialEntries: ["/prs"] },
    );

    render(<RouterProvider router={router} />);

    expect(
      await screen.findByText("Review the Source Control boundary"),
    ).toBeInTheDocument();
    await waitFor(() => {
      expect(mocks.fetchPullRequests).toHaveBeenCalledWith("WS", "all");
    });
  });
});
