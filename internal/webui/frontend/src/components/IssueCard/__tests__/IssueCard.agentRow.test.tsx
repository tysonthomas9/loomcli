/**
 * @vitest-environment jsdom
 */

/**
 * Integration tests for AgentRow rendering within IssueCard.
 * Tests that AgentRow appears for in_progress column with assignee,
 * and does not appear for other columns.
 */

import { render, screen } from "@testing-library/react";
import { describe, it, expect, vi } from "vitest";
import "@testing-library/jest-dom";

import type { Issue, LoomAgentStatus } from "@/types";

import { IssueCard } from "../IssueCard";

// Mutable mock state for agent store — tests update via setupAgentContext
// eslint-disable-next-line @typescript-eslint/no-explicit-any
let mockAgentStoreState: any = {
  agents: [] as LoomAgentStatus[],
};

// Mock zustand's useStore — apply selector to the mock agent store state
vi.mock("zustand", () => ({
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  useStore: (_store: unknown, selector: (s: any) => unknown) =>
    selector(mockAgentStoreState),
}));

// Mock hooks — replace useAgentStoreInstance with dummy
vi.mock("@/hooks", async (importOriginal) => {
  const orig = await importOriginal<typeof import("@/hooks")>();
  return {
    ...orig,
    useAgentStoreInstance: () => ({}),
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
    LAYER_ISSUE_PANEL: 10,
  };
});

// Mock AgentCard status helpers to return predictable values.
vi.mock("@/components/AgentCard", () => ({
  getStatusDotColor: vi.fn(() => "#22c55e"),
  getStatusLabel: vi.fn(() => "Working"),
}));

vi.mock("@/utils/colorUtils", () => ({
  getAvatarColor: vi.fn((name: string) => `#color-${name}`),
}));

/**
 * Create a minimal test issue with required fields.
 */
function createTestIssue(overrides: Partial<Issue> = {}): Issue {
  return {
    id: "test-issue-abc123",
    title: "Test Issue Title",
    priority: 2,
    created_at: "2024-01-15T10:30:00Z",
    updated_at: "2024-01-15T10:30:00Z",
    ...overrides,
  };
}

/**
 * Create a mock LoomAgentStatus. By default the agent claims the
 * test-issue-abc123 issue (matches createTestIssue()'s default id).
 * Tests that want the "orphaned task" path can pass current_task_id: ""
 * or a different id.
 */
function createMockAgent(
  overrides: Partial<LoomAgentStatus> = {},
): LoomAgentStatus {
  return {
    name: "nova",
    branch: "feature/loom-123",
    status: "working: loom-123 (5m)",
    ahead: 2,
    behind: 0,
    current_task_id: "test-issue-abc123",
    ...overrides,
  };
}

/**
 * Helper to configure the mock agent store state with a specific agent.
 */
function setupAgentContext(agent: LoomAgentStatus | undefined) {
  mockAgentStoreState = {
    agents: agent ? [agent] : [],
  };
}

describe("IssueCard AgentRow integration", () => {
  describe("in_progress column with assignee", () => {
    it("renders AgentRow when columnId is in_progress and assignee matches an agent", () => {
      const agent = createMockAgent({ name: "nova" });
      setupAgentContext(agent);

      const issue = createTestIssue({ assignee: "nova" });
      const { container } = render(
        <IssueCard issue={issue} columnId="in_progress" />,
      );

      // AgentRow renders the agent name
      expect(screen.getByText("nova")).toBeInTheDocument();
      // AgentRow renders the avatar initial
      expect(screen.getByText("N")).toBeInTheDocument();
      // AgentRow renders the activity text
      expect(screen.getByText("Working")).toBeInTheDocument();
      // Status dot should be present
      const dot = container.querySelector('[class*="statusDot"]');
      expect(dot).toBeInTheDocument();
    });

    it('resolves the live agent via the derived active_task_id (not "agent missing") when current_task_id is empty', () => {
      // Store-backed serve path: the lock-derived current_task_id stays empty
      // for a provably-working agent; the claim is in fleet-db's derived
      // active_task_id. The card must still match it to the live agent.
      const agent = createMockAgent({
        name: "jack-worker",
        current_task_id: "",
        active_task_id: "test-issue-abc123",
        live_status: "working",
      });
      setupAgentContext(agent);

      // Mirrors the real board: the claim's assignee is the human actor.
      const issue = createTestIssue({ assignee: "oleh" });
      render(<IssueCard issue={issue} columnId="in_progress" />);

      // The agent is provably on this task, so no false "agent missing".
      expect(screen.queryByText("agent missing")).not.toBeInTheDocument();
      expect(screen.getByText("oleh")).toBeInTheDocument();
    });

    it('renders AgentRow with the saved assignee name and "agent missing" when no live agent has current_task_id === issue.id', () => {
      // No agent claims this issue right now (orphaned in_progress).
      setupAgentContext(undefined);

      const issue = createTestIssue({ assignee: "unknown-agent" });
      render(<IssueCard issue={issue} columnId="in_progress" />);

      // The saved assignee name still appears
      expect(screen.getByText("unknown-agent")).toBeInTheDocument();
      // And the activity slot calls out the missing agent in red.
      expect(screen.getByText("agent missing")).toBeInTheDocument();
    });

    it("strips [H] prefix from human assignee display name", () => {
      setupAgentContext(undefined);

      const issue = createTestIssue({ assignee: "[H] Alice" });
      render(<IssueCard issue={issue} columnId="in_progress" />);

      expect(screen.getByText("Alice")).toBeInTheDocument();
      expect(screen.queryByText("[H] Alice")).not.toBeInTheDocument();
    });
  });

  describe("AgentRow not shown in other columns", () => {
    it.each(["open", "done", "backlog", "blocked"])(
      'does not render AgentRow for columnId="%s"',
      (columnId) => {
        const agent = createMockAgent({ name: "nova" });
        setupAgentContext(agent);

        const issue = createTestIssue({ assignee: "nova" });
        const { container } = render(
          <IssueCard issue={issue} columnId={columnId} />,
        );

        // AgentRow specific elements should not be present
        const agentRow = container.querySelector('[class*="agentRow"]');
        expect(agentRow).not.toBeInTheDocument();
      },
    );

    it("does not render AgentRow when columnId is undefined", () => {
      const agent = createMockAgent({ name: "nova" });
      setupAgentContext(agent);

      const issue = createTestIssue({ assignee: "nova" });
      const { container } = render(<IssueCard issue={issue} />);

      const agentRow = container.querySelector('[class*="agentRow"]');
      expect(agentRow).not.toBeInTheDocument();
    });
  });

  describe("AgentRow not shown without assignee", () => {
    it("does not render AgentRow when issue has no assignee", () => {
      setupAgentContext(createMockAgent());

      const issue = createTestIssue({ assignee: undefined });
      const { container } = render(
        <IssueCard issue={issue} columnId="in_progress" />,
      );

      const agentRow = container.querySelector('[class*="agentRow"]');
      expect(agentRow).not.toBeInTheDocument();
    });

    it("does not render AgentRow when assignee is empty string", () => {
      setupAgentContext(createMockAgent());

      const issue = createTestIssue({ assignee: "" });
      const { container } = render(
        <IssueCard issue={issue} columnId="in_progress" />,
      );

      const agentRow = container.querySelector('[class*="agentRow"]');
      expect(agentRow).not.toBeInTheDocument();
    });
  });

  describe("review column with assignee", () => {
    it("renders AgentRow when columnId is review and assignee exists", () => {
      setupAgentContext(undefined);
      const issue = createTestIssue({ assignee: "nova" });
      const { container } = render(
        <IssueCard issue={issue} columnId="review" />,
      );
      // AgentRow renders
      expect(screen.getByText("nova")).toBeInTheDocument();
      expect(screen.getByText("N")).toBeInTheDocument();
      // Shows static activity text, not live status
      expect(screen.getByText("Submitted for review")).toBeInTheDocument();
      // No status dot (agent is no longer actively working this task)
      const dot = container.querySelector('[class*="statusDot"]');
      expect(dot).not.toBeInTheDocument();
    });

    it("does not render AgentRow on review card without assignee", () => {
      setupAgentContext(undefined);
      const issue = createTestIssue({ assignee: undefined });
      const { container } = render(
        <IssueCard issue={issue} columnId="review" />,
      );
      const agentRow = container.querySelector('[class*="agentRow"]');
      expect(agentRow).not.toBeInTheDocument();
    });

    it("does not show live agent status on review cards even when agent is found", () => {
      const agent = createMockAgent({ name: "nova" });
      setupAgentContext(agent);
      const issue = createTestIssue({ assignee: "nova" });
      render(<IssueCard issue={issue} columnId="review" />);
      // Should show static text, not the live "Working" label
      expect(screen.getByText("Submitted for review")).toBeInTheDocument();
      expect(screen.queryByText("Working")).not.toBeInTheDocument();
    });
  });

  describe("does not affect other IssueCard functionality", () => {
    it("still renders title and priority when AgentRow is shown", () => {
      const agent = createMockAgent({ name: "nova" });
      setupAgentContext(agent);

      const issue = createTestIssue({
        title: "Important Task",
        priority: 1,
        assignee: "nova",
      });
      render(<IssueCard issue={issue} columnId="in_progress" />);

      expect(
        screen.getByRole("heading", { name: "Important Task" }),
      ).toBeInTheDocument();
      expect(screen.getByText("P1")).toBeInTheDocument();
    });

    it("still renders blocked badge alongside AgentRow", () => {
      const agent = createMockAgent({ name: "nova" });
      setupAgentContext(agent);

      const issue = createTestIssue({ assignee: "nova" });
      render(
        <IssueCard issue={issue} columnId="in_progress" blockedByCount={2} />,
      );

      expect(screen.getByLabelText("Blocked by 2 issues")).toBeInTheDocument();
      // AgentRow should still be present
      expect(screen.getByText("nova")).toBeInTheDocument();
    });
  });
});
