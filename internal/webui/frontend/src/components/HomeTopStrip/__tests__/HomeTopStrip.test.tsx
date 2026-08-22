/**
 * @vitest-environment jsdom
 */

import { render, screen } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import "@testing-library/jest-dom";

import type { LoomAgentStatus } from "@/types";

import { HomeTopStrip } from "../HomeTopStrip";

const mockWorkspaceContext = vi.hoisted(() => ({
  workspace: { name: "LOCALMODE" } as { name: string } | null,
  repos: [] as { name: string; default_branch: string }[],
  isLoading: false,
}));

vi.mock("@/hooks/workspace", () => ({
  useWorkspaceContext: () => mockWorkspaceContext,
}));

const agent = (overrides: Partial<LoomAgentStatus> = {}): LoomAgentStatus =>
  ({ name: "agent-dev-1", status: "idle", ...overrides }) as LoomAgentStatus;

beforeEach(() => {
  mockWorkspaceContext.workspace = { name: "LOCALMODE" };
  mockWorkspaceContext.repos = [];
  mockWorkspaceContext.isLoading = false;
});

describe("HomeTopStrip", () => {
  it("shows the sole workspace repo without selecting it", () => {
    mockWorkspaceContext.repos = [
      { name: "source-repo", default_branch: "localmode" },
    ];

    render(<HomeTopStrip agents={[agent()]} workspaceId="LOCALMODE" />);

    expect(screen.getByTestId("strip-repos")).toHaveTextContent("source-repo");
    expect(screen.queryByText("localmode")).not.toBeInTheDocument();
  });

  it("shows the repository count for a multi-repo workspace", () => {
    mockWorkspaceContext.repos = [
      { name: "source-repo", default_branch: "localmode" },
      { name: "web", default_branch: "develop" },
    ];

    render(<HomeTopStrip agents={[agent()]} workspaceId="LOCALMODE" />);

    expect(screen.getByTestId("strip-repos")).toHaveTextContent("2 repos");
  });

  it("shows loading repos while workspace metadata is loading", () => {
    mockWorkspaceContext.isLoading = true;

    render(<HomeTopStrip agents={[]} workspaceId="LOCALMODE" />);

    expect(screen.getByTestId("strip-repos")).toHaveTextContent(
      "loading repos…",
    );
  });
});
