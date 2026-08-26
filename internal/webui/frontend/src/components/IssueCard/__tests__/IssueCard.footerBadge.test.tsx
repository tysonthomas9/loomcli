/**
 * @vitest-environment jsdom
 */

import { render, screen } from "@testing-library/react";
import { describe, it, expect, vi } from "vitest";
import "@testing-library/jest-dom";

import { createAgentStore } from "@/stores/agentStore";
import type { Issue, LoomAgentStatus } from "@/types";

import { IssueCard } from "../IssueCard";

const mocks = vi.hoisted(() => ({
  useAgentStoreInstance: vi.fn(),
}));

vi.mock("@/hooks/common", async (importOriginal) => {
  const orig = await importOriginal<typeof import("@/hooks/common")>();
  return {
    ...orig,
    useAgentStoreInstance: mocks.useAgentStoreInstance,
  };
});

function createTestIssue(overrides: Partial<Issue> = {}): Issue {
  return {
    id: "LOCALMODE-5",
    title: "Test Issue",
    priority: 2,
    created_at: "2024-01-15T10:30:00Z",
    updated_at: "2024-01-15T10:30:00Z",
    ...overrides,
  };
}

function agent(overrides: Partial<LoomAgentStatus> = {}): LoomAgentStatus {
  return {
    name: "lead-a",
    branch: "feature/x",
    status: "working: LOCALMODE-5 (5m)",
    ahead: 0,
    behind: 0,
    ...overrides,
  };
}

function renderWithAgents(agents: LoomAgentStatus[], issue: Issue) {
  const store = createAgentStore();
  store.setState({ agents });
  mocks.useAgentStoreInstance.mockReturnValue(store);
  return render(<IssueCard issue={issue} columnId="in_progress" />);
}

describe("IssueCard footer badge", () => {
  it("shows working agent badge when an agent claims the task", () => {
    renderWithAgents(
      [agent({ name: "lead-a", active_task_id: "LOCALMODE-5" })],
      createTestIssue({ owner: "tyson", assignee: "[H] tyson" }),
    );

    const badge = screen.getByTestId("issue-card-agent");
    expect(badge).toHaveAttribute("title", "Working: lead-a");
    expect(badge).toHaveAttribute("data-variant", "agent");
    expect(badge).toHaveTextContent("LA");
    expect(screen.getByLabelText("lead-a avatar")).toBeInTheDocument();
  });

  it("shows owner badge when no agent is working or assigned", () => {
    renderWithAgents([], createTestIssue({ owner: "tyson" }));

    const badge = screen.getByTestId("issue-card-owner");
    expect(badge).toHaveAttribute("title", "Owner: tyson");
    expect(badge).toHaveAttribute("data-variant", "owner");
    expect(badge).toHaveTextContent("TY");
  });

  it("shows agent badge from assignee when owner is set but assignee is an agent", () => {
    renderWithAgents([], createTestIssue({ owner: "tyson", assignee: "nova" }));

    const badge = screen.getByTestId("issue-card-agent");
    expect(badge).toHaveAttribute("title", "Working: nova");
    expect(badge).toHaveTextContent("NO");
  });

  it("uses the shared two-letter agent avatar on Kanban cards", () => {
    renderWithAgents(
      [agent({ name: "codex-coder", active_task_id: "LOCALMODE-5" })],
      createTestIssue({ assignee: "codex-coder" }),
    );

    const badge = screen.getByTestId("issue-card-agent");
    expect(badge).toHaveTextContent("CC");
    expect(screen.getByLabelText("codex-coder avatar")).toHaveStyle({
      backgroundColor: expect.any(String),
    });
  });
});
