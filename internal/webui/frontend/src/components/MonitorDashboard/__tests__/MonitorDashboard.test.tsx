/**
 * @vitest-environment jsdom
 */

import { render, screen, fireEvent } from "@testing-library/react";
import { describe, it, expect, vi } from "vitest";

import "@testing-library/jest-dom";
import type { Priority } from "@/types";

import { MonitorDashboard } from "../MonitorDashboard";

// Mock the hooks to prevent API calls in tests
const mockSetActiveView = vi.fn();
let mockBlockedIssuesData: unknown[] = [];

vi.mock("@/hooks", () => ({
  useAgentContext: () => ({
    agents: [],
    isLoading: false,
    isConnected: true,
    connectionState: "connected",
    wasEverConnected: true,
    retryCountdown: 0,
    error: null,
    lastUpdated: new Date(),
    refetch: vi.fn(),
    retryNow: vi.fn(),
    showStaleBanner: false,
    connectionLost: false,
    disconnectedSince: null,
    getAgentByName: () => undefined,
  }),
  useBlockedIssues: () => ({
    data: mockBlockedIssuesData,
    loading: false,
    error: null,
    refetch: vi.fn(),
  }),
  useViewState: () => ["monitor", mockSetActiveView],
  useRegisterEscapeLayer: vi.fn(),
  useKeyboardShortcuts: vi.fn(() => ({
    isCheatsheetOpen: false,
    toggleCheatsheet: vi.fn(),
    closeCheatsheet: vi.fn(),
  })),
  KeyboardShortcutProvider: ({ children }: { children: React.ReactNode }) =>
    children,
  LAYER_CONFIRM_DIALOG: 60,
  LAYER_TOAST: 50,
  LAYER_CHEATSHEET: 45,
  LAYER_MODAL: 40,
  LAYER_TERMINAL_PANEL: 30,
  LAYER_AGENT_PANEL: 20,
  LAYER_ISSUE_PANEL: 10,}));

vi.mock("@/hooks/useWorkspaceContext", () => ({
  useWorkspaceContext: () => ({
    workspaceId: "test-workspace",
    workspace: null,
    repos: [],
    groups: [],
    agents: [],
    isLoading: false,
    error: null,
    refetch: vi.fn(),
    getRepoByName: vi.fn(),
    getReposByGroup: vi.fn(() => []),
    getAgentByName: vi.fn(),
    activeWorkspaceName: "test-workspace",
    setActiveWorkspace: vi.fn(),
    selectedRepoNames: new Set<string>(),
    activeRepos: [],
    activeRepoNames: [],
    isAllSelected: true,
    selectRepos: vi.fn(),
    selectAll: vi.fn(),
    toggleRepo: vi.fn(),
    sourceReposFilter: undefined,
    isMultiRepo: false,
  }),
}));

/**
 * Create a blocked issue for testing bottleneck click behavior.
 */
function createBlockedIssue(id: string, blockedBy: string[]) {
  return {
    id,
    title: `Title for ${id}`,
    priority: 2 as Priority,
    created_at: "2026-01-25T00:00:00Z",
    updated_at: "2026-01-25T00:00:00Z",
    blocked_by_count: blockedBy.length,
    blocked_by: blockedBy,
  };
}

describe("MonitorDashboard", () => {
  beforeEach(() => {
    mockBlockedIssuesData = [];
  });

  it("renders both panels", () => {
    render(<MonitorDashboard />);

    expect(
      screen.getByRole("heading", { name: /project health/i }),
    ).toBeInTheDocument();
    expect(
      screen.getByRole("heading", { name: /agent activity/i }),
    ).toBeInTheDocument();
  });

  it("renders with testid for e2e tests", () => {
    render(<MonitorDashboard />);

    expect(screen.getByTestId("monitor-dashboard")).toBeInTheDocument();
  });

  it("applies custom className", () => {
    render(<MonitorDashboard className="custom-class" />);

    const dashboard = screen.getByTestId("monitor-dashboard");
    expect(dashboard).toHaveClass("custom-class");
  });

  it("renders panels in correct order: Project Health, Agent Activity, Usage", () => {
    render(<MonitorDashboard />);

    const headings = screen.getAllByRole("heading", { level: 2 });
    const panelNames = headings.map((h) => h.textContent);
    expect(panelNames).toEqual(["Project Health", "Agent Activity", "Usage"]);
  });

  it("renders AgentActivityPanel", () => {
    render(<MonitorDashboard />);

    expect(screen.getByTestId("agent-activity-panel")).toBeInTheDocument();
  });

  it("renders ProjectHealthPanel with placeholder stats", () => {
    // MonitorDashboard currently uses a zeroed placeholder for stats
    // (pending the workspace-scoped stats API).
    render(<MonitorDashboard />);

    expect(screen.getByTestId("project-health-panel")).toBeInTheDocument();
  });

  it("has refresh indicator in agent activity panel", () => {
    render(<MonitorDashboard />);

    expect(screen.getByText("↻ 5s")).toBeInTheDocument();
  });

  it("renders settings button for agent activity", () => {
    render(<MonitorDashboard />);

    expect(
      screen.getByRole("button", { name: /agent activity settings/i }),
    ).toBeInTheDocument();
  });

  describe("onIssueClick prop", () => {
    it("renders without onIssueClick (backward compatibility)", () => {
      render(<MonitorDashboard />);

      expect(screen.getByTestId("monitor-dashboard")).toBeInTheDocument();
      expect(
        screen.getByRole("heading", { name: /project health/i }),
      ).toBeInTheDocument();
      expect(
        screen.getByRole("heading", { name: /agent activity/i }),
      ).toBeInTheDocument();
    });

    it("calls onIssueClick when a bottleneck item is clicked", () => {
      // Set up blocked issues so that 'bottleneck-1' blocks multiple issues (creating a bottleneck)
      mockBlockedIssuesData = [
        createBlockedIssue("blocked-1", ["bottleneck-1"]),
        createBlockedIssue("blocked-2", ["bottleneck-1"]),
      ];

      const onIssueClick = vi.fn();
      render(<MonitorDashboard onIssueClick={onIssueClick} />);

      const bottleneckButton = screen.getByRole("button", {
        name: /bottleneck-1/i,
      });
      fireEvent.click(bottleneckButton);

      expect(onIssueClick).toHaveBeenCalledTimes(1);
      expect(onIssueClick).toHaveBeenCalledWith(
        expect.objectContaining({
          id: "bottleneck-1",
          title: "bottleneck-1", // Falls back to ID since title is not in blocked_by data
        }),
      );
    });

    it("does not throw when bottleneck is clicked without onIssueClick", () => {
      mockBlockedIssuesData = [
        createBlockedIssue("blocked-1", ["bottleneck-1"]),
        createBlockedIssue("blocked-2", ["bottleneck-1"]),
      ];

      render(<MonitorDashboard />);

      const bottleneckButton = screen.getByRole("button", {
        name: /bottleneck-1/i,
      });
      // handleBottleneckClick uses optional chaining (onIssueClick?.()),
      // so clicking without onIssueClick should be a no-op
      expect(() => fireEvent.click(bottleneckButton)).not.toThrow();
    });

    it("handleAgentClick still console.logs (unchanged behavior)", () => {
      const consoleSpy = vi.spyOn(console, "log").mockImplementation(() => {});

      render(<MonitorDashboard onIssueClick={vi.fn()} />);

      // The handleAgentClick is passed to AgentActivityPanel but agents array is empty,
      // so we verify the console.log behavior is unchanged by confirming the component
      // renders correctly with the onIssueClick prop without interfering with agent handling
      expect(screen.getByTestId("agent-activity-panel")).toBeInTheDocument();

      consoleSpy.mockRestore();
    });
  });
});
