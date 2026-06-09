// @vitest-environment jsdom

import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import type { ReactNode } from "react";
import { beforeEach, describe, expect, it, vi } from "vitest";

import { startWorkflowRun } from "@/api";

import { AgentsPage } from "../AgentsPage";

const mocks = vi.hoisted(() => {
  const fetchData = vi.fn();
  return {
    fetchData,
    navigate: vi.fn(),
    showToast: vi.fn(),
    startWorkflowRun: vi.fn(),
    agentStore: {
      getState: () => ({ fetchData }),
    },
  };
});

vi.mock("react-router-dom", async (importOriginal) => {
  const actual = await importOriginal<typeof import("react-router-dom")>();
  return {
    ...actual,
    useNavigate: () => mocks.navigate,
    useParams: () => ({ workspaceId: "DESKTOP-QA", agentName: "lead-1" }),
  };
});

vi.mock("zustand", () => ({
  useStore: <T,>(_: unknown, selector: (state: { agents: never[] }) => T) =>
    selector({ agents: [] }),
}));

vi.mock("@/api", () => ({
  EPIC_RUNNER_WORKFLOW_NAME: "epic-runner",
  startWorkflowRun: mocks.startWorkflowRun,
}));

vi.mock("@/hooks", () => ({
  useAgentStoreInstance: () => mocks.agentStore,
}));

vi.mock("@/hooks/ui/useToast", () => ({
  useToast: () => ({ showToast: mocks.showToast }),
}));

vi.mock("@/components", () => ({
  ErrorBoundary: ({ children }: { children: ReactNode }) => <>{children}</>,
  LoadingSkeleton: {
    Monitor: () => <div>loading</div>,
  },
}));

vi.mock("@/components/AgentDetailMain/AgentDetailMain", () => ({
  AgentDetailMain: () => <div data-testid="agent-detail" />,
}));

vi.mock("@/components/AgentWorkPanel/AgentWorkPanel", () => ({
  AgentWorkPanel: ({
    onRunEpic,
  }: {
    onRunEpic?: (epicId: string) => void | Promise<void>;
  }) => (
    <button type="button" onClick={() => void onRunEpic?.("EPIC-1")}>
      Run lead epic
    </button>
  ),
}));

vi.mock("@/components/IssueDetailPanel/IssueDetailPanel", () => ({
  IssueDetailPanel: () => <div data-testid="issue-detail" />,
}));

describe("AgentsPage", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    mocks.startWorkflowRun.mockResolvedValue({
      run_id: "run-1",
    });
  });

  it("queues the built-in epic runner workflow from the lead-panel Run button", async () => {
    render(<AgentsPage />);

    fireEvent.click(screen.getByRole("button", { name: "Run lead epic" }));

    await waitFor(() => {
      expect(startWorkflowRun).toHaveBeenCalledWith(
        "DESKTOP-QA",
        "epic-runner",
        {
          epicId: "EPIC-1",
          leadName: "lead-1",
          requestedBy: "ui",
        },
      );
    });
    expect(mocks.fetchData).toHaveBeenCalledTimes(1);
    expect(mocks.showToast).toHaveBeenCalledWith(
      "Epic runner queued for lead-1: run-1",
      { type: "success" },
    );
  });
});
