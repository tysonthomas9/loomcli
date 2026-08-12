/**
 * @vitest-environment jsdom
 */

import { render, screen } from "@testing-library/react";
import { MemoryRouter, Route, Routes } from "react-router-dom";
import { beforeEach, describe, expect, it, vi } from "vitest";
import "@testing-library/jest-dom";

import {
  NO_WORKSPACE_VIEW_ACTIONS,
  NO_WORKSPACE_VIEW_DATA,
} from "@/contexts/WorkspaceViewContext";

const mockData = { ...NO_WORKSPACE_VIEW_DATA, activeView: "prs" as const };
const mockActions = { ...NO_WORKSPACE_VIEW_ACTIONS };

const pullRequestsMock = vi.hoisted(() => ({
  pullRequests: [] as unknown[],
  warnings: [] as string[],
  loading: true,
  error: null as Error | null,
}));

vi.mock("@/contexts/WorkspaceViewContext", async (importOriginal) => {
  const actual =
    await importOriginal<typeof import("@/contexts/WorkspaceViewContext")>();
  return {
    ...actual,
    useWorkspaceViewData: () => mockData,
    useWorkspaceViewActions: () => mockActions,
  };
});

vi.mock("@/hooks/workspace", () => ({
  usePullRequests: () => pullRequestsMock,
}));

vi.mock("../PRReviewWorkspace", () => ({
  PRReviewWorkspace: (props: {
    pullRequest?: { title?: string; number?: number; repo_name?: string };
    initialDiscussOpen?: boolean;
  }) => (
    <div
      data-testid="pr-review-workspace"
      data-title={props.pullRequest?.title ?? ""}
      data-number={String(props.pullRequest?.number ?? "")}
      data-repo={props.pullRequest?.repo_name ?? ""}
      data-discuss={props.initialDiscussOpen ? "1" : "0"}
    />
  ),
}));

import { PRsPage } from "../PRsPage";

function renderWithSearch(search: string): void {
  render(
    <MemoryRouter initialEntries={[`/ws/demo/prs${search}`]}>
      <Routes>
        <Route path="/ws/:workspaceId/prs" element={<PRsPage />} />
      </Routes>
    </MemoryRouter>,
  );
}

describe("PRsPage deep-link", () => {
  beforeEach(() => {
    pullRequestsMock.pullRequests = [];
    pullRequestsMock.warnings = [];
    pullRequestsMock.loading = true;
    pullRequestsMock.error = null;
  });

  it("mounts the review workspace from review-pr before the PR list loads", () => {
    renderWithSearch("?review-pr=tysonthomas9/loomcli%23220&discuss=1");

    const workspace = screen.getByTestId("pr-review-workspace");
    expect(workspace).toBeInTheDocument();
    expect(workspace).toHaveAttribute("data-title", "tysonthomas9/loomcli#220");
    expect(workspace).toHaveAttribute("data-number", "220");
    expect(workspace).toHaveAttribute("data-repo", "tysonthomas9/loomcli");
    expect(workspace).toHaveAttribute("data-discuss", "1");
    expect(screen.queryByText("Pull Requests")).not.toBeInTheDocument();
  });

  it("does not fall through to the list while an unparsable review-pr is loading", () => {
    renderWithSearch("?review-pr=not-a-valid-ref");

    expect(screen.getByTestId("pr-review-loading")).toBeInTheDocument();
    expect(screen.queryByTestId("pr-review-workspace")).not.toBeInTheDocument();
  });
});
