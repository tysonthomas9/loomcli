/**
 * @vitest-environment jsdom
 */

/**
 * Unit tests for IssueDetailPanel component.
 */

import {
  render,
  screen,
  fireEvent,
  within,
  waitFor,
} from "@testing-library/react";
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import "@testing-library/jest-dom";

import type { Issue, IssueDetails, IssueWithDependencyMetadata } from "@/types";
import type { SessionRecord } from "@/types/agent";
import { updateIssue, startAgent } from "@/api";
import { createAgentStore } from "@/stores/agentStore";

import { IssueDetailPanel } from "../IssueDetailPanel";

// Create hoisted mocks
const {
  mockUseRegisterEscapeLayer,
  mockDeleteTabMetadata,
  mockScheduleSessionKill,
  mockUseIssueTabPersistence,
  mockUseWorkspaceContext,
  mockUseAgentStoreInstance,
  mockGetTaskSessions,
} = vi.hoisted(() => ({
  mockUseRegisterEscapeLayer: vi.fn(),
  mockDeleteTabMetadata: vi.fn(() => Promise.resolve()),
  mockScheduleSessionKill: vi.fn(() => Promise.resolve()),
  mockUseIssueTabPersistence: vi.fn(() => ({
    savedState: null,
    isLoading: true,
    saveTabs: vi.fn(),
    clearTabs: vi.fn(),
  })),
  mockUseWorkspaceContext: vi.fn(() => ({
    workspace: null,
    repos: [],
    groups: [],
    agents: [],
    isLoading: false,
    error: null,
    refetch: () => {},
    getRepoByName: () => undefined,
    getReposByGroup: () => [],
    getAgentByName: () => undefined,
    workspaceId: "",
    activeWorkspaceName: null,
    setActiveWorkspace: () => {},
    defaultWorkspaceName: null,
    setDefaultWorkspace: () => Promise.resolve(),
  })),
  mockUseAgentStoreInstance: vi.fn(),
  mockGetTaskSessions: vi.fn(() => Promise.resolve([])),
}));

// Mock the API module
vi.mock("@/api", () => ({
  updateIssue: vi.fn(),
  startAgent: vi.fn().mockResolvedValue(undefined),
  addDependency: vi.fn(),
  removeDependency: vi.fn(),
  getIssueEvents: vi.fn().mockImplementation(() => new Promise(() => {})),
  getTaskLogPhases: vi.fn().mockResolvedValue([]),
}));

// Mock terminal API for cleanup verification
vi.mock("@/api/terminal", () => ({
  deleteTabMetadata: mockDeleteTabMetadata,
  scheduleSessionKill: mockScheduleSessionKill,
  getTaskSessions: mockGetTaskSessions,
  listIssueSessions: vi.fn().mockImplementation(() => new Promise(() => {})),
}));

// Mock tab persistence hook for terminal tab restoration tests
vi.mock("@/hooks/issues", async () => {
  const actual =
    await vi.importActual<typeof import("@/hooks/issues")>("@/hooks/issues");
  return { ...actual, useIssueTabPersistence: mockUseIssueTabPersistence };
});

// Mock workspace context for cleanup tests needing workspace ID
vi.mock("@/hooks/workspace", async () => {
  const actual =
    await vi.importActual<typeof import("@/hooks/workspace")>(
      "@/hooks/workspace",
    );
  return { ...actual, useWorkspaceContext: mockUseWorkspaceContext };
});

vi.mock("@/hooks/common", async () => {
  const actual =
    await vi.importActual<typeof import("@/hooks/common")>("@/hooks/common");
  return { ...actual, useAgentStoreInstance: mockUseAgentStoreInstance };
});

vi.mock("@/hooks", async (importOriginal) => {
  const orig = await importOriginal<typeof import("@/hooks")>();
  return {
    ...orig,
    useRegisterEscapeLayer: mockUseRegisterEscapeLayer,
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

/**
 * Create a minimal test issue with required fields.
 */
function createTestIssue(overrides: Partial<Issue> = {}): Issue {
  return {
    id: "test-123",
    title: "Test Issue",
    priority: 2,
    created_at: "2026-01-23T00:00:00Z",
    updated_at: "2026-01-23T00:00:00Z",
    ...overrides,
  };
}

/**
 * Create a test issue with full details (IssueDetails type).
 */
function createTestIssueDetails(
  overrides: Partial<IssueDetails> = {},
): IssueDetails {
  return {
    id: "test-123",
    title: "Test Issue",
    priority: 2,
    created_at: "2026-01-23T00:00:00Z",
    updated_at: "2026-01-23T00:00:00Z",
    comments: [],
    dependencies: [],
    dependents: [],
    ...overrides,
  };
}

/**
 * Create a test dependency issue.
 */
function createTestDependency(
  overrides: Partial<IssueWithDependencyMetadata> = {},
): IssueWithDependencyMetadata {
  return {
    id: "dep-456",
    title: "Dependency Issue",
    priority: 2,
    created_at: "2026-01-23T00:00:00Z",
    updated_at: "2026-01-23T00:00:00Z",
    status: "open",
    dependency_type: "blocks",
    ...overrides,
  };
}

function createTestSession(
  overrides: Partial<SessionRecord> = {},
): SessionRecord {
  return {
    session_id: "sess-1",
    task_id: "test-123",
    agent_name: "planner",
    backend: "codex",
    status: "failed",
    started_at: "2026-01-23T00:00:00Z",
    ended_at: "2026-01-23T00:00:15Z",
    duration_s: 15,
    input_tokens: 0,
    output_tokens: 0,
    cache_read_tokens: 0,
    cache_write_tokens: 0,
    estimated_cost_usd: 0,
    exit_code: 1,
    files_changed: 0,
    lines_added: 0,
    lines_removed: 0,
    attempt_num: 1,
    has_transcript: true,
    has_diff: false,
    is_active: false,
    error_class: "AuthFailure",
    ...overrides,
  };
}

function createWorkspaceContext(overrides: Record<string, unknown> = {}) {
  return {
    workspace: null,
    repos: [],
    groups: [],
    agents: [],
    isLoading: false,
    error: null,
    refetch: () => {},
    getRepoByName: () => undefined,
    getReposByGroup: () => [],
    getAgentByName: () => undefined,
    workspaceId: "",
    activeWorkspaceName: null,
    setActiveWorkspace: () => {},
    defaultWorkspaceName: null,
    setDefaultWorkspace: () => Promise.resolve(),
    ...overrides,
  };
}

describe("IssueDetailPanel", () => {
  beforeEach(() => {
    const agentStore = createAgentStore();
    mockUseAgentStoreInstance.mockReset();
    mockUseAgentStoreInstance.mockReturnValue(agentStore);
    mockUseWorkspaceContext.mockReset();
    mockUseWorkspaceContext.mockImplementation(() => createWorkspaceContext());
    mockGetTaskSessions.mockReset();
    mockGetTaskSessions.mockResolvedValue([]);
  });

  // Reset body overflow after each test
  afterEach(() => {
    document.body.style.overflow = "";
  });

  describe("rendering", () => {
    it("renders when open", () => {
      const mockIssue = createTestIssue();
      render(
        <IssueDetailPanel isOpen={true} issue={mockIssue} onClose={() => {}} />,
      );
      expect(screen.getByTestId("issue-detail-panel")).toBeInTheDocument();
    });

    it("renders children in content area", () => {
      const mockIssue = createTestIssue();
      render(
        <IssueDetailPanel isOpen={true} issue={mockIssue} onClose={() => {}}>
          <div data-testid="child-content">Child Content</div>
        </IssueDetailPanel>,
      );
      expect(screen.getByTestId("child-content")).toBeInTheDocument();
    });

    it("shows latest failed task run and links to Runs tab", async () => {
      mockGetTaskSessions.mockResolvedValue([
        createTestSession({ error_class: "AuthFailure" }),
      ]);
      const mockIssue = createTestIssue({
        issue_type: "task",
        id: "test-123",
      });
      render(
        <IssueDetailPanel isOpen={true} issue={mockIssue} onClose={() => {}} />,
      );

      await waitFor(() => {
        expect(
          screen.getByTestId("latest-run-failure-banner"),
        ).toHaveTextContent("AuthFailure");
      });

      fireEvent.click(screen.getByRole("button", { name: "View run" }));
      expect(screen.getByRole("tab", { name: "Runs" })).toHaveAttribute(
        "aria-selected",
        "true",
      );
    });

    it("does not show failed-run banner when a newer run succeeded", async () => {
      mockGetTaskSessions.mockResolvedValue([
        createTestSession({
          session_id: "old-failure",
          status: "failed",
          error_class: "AuthFailure",
          started_at: "2026-01-23T00:00:00Z",
        }),
        createTestSession({
          session_id: "new-success",
          status: "completed",
          error_class: undefined,
          exit_code: 0,
          started_at: "2026-01-23T01:00:00Z",
        }),
      ]);
      const mockIssue = createTestIssue({
        issue_type: "task",
        id: "test-123",
      });
      render(
        <IssueDetailPanel isOpen={true} issue={mockIssue} onClose={() => {}} />,
      );

      await waitFor(() => {
        expect(mockGetTaskSessions).toHaveBeenCalledWith("", "test-123");
      });
      expect(
        screen.queryByTestId("latest-run-failure-banner"),
      ).not.toBeInTheDocument();
    });

    it("applies open class when isOpen is true", () => {
      const mockIssue = createTestIssue();
      render(
        <IssueDetailPanel isOpen={true} issue={mockIssue} onClose={() => {}} />,
      );
      const overlay = screen.getByTestId("issue-detail-overlay");
      // CSS modules mangle class names, so check for pattern containing 'open'
      expect(overlay.className).toMatch(/open/i);
    });

    it("does not apply open class when isOpen is false", () => {
      render(
        <IssueDetailPanel isOpen={false} issue={null} onClose={() => {}} />,
      );
      const overlay = screen.getByTestId("issue-detail-overlay");
      // CSS modules mangle class names, so check that 'open' pattern is not present
      expect(overlay.className).not.toMatch(/_open_/);
    });

    it("renders even when closed (for animation)", () => {
      render(
        <IssueDetailPanel isOpen={false} issue={null} onClose={() => {}} />,
      );
      expect(screen.getByTestId("issue-detail-panel")).toBeInTheDocument();
    });
  });

  describe("close interactions", () => {
    it("calls onClose when clicking overlay", () => {
      const mockIssue = createTestIssue();
      const onClose = vi.fn();
      render(
        <IssueDetailPanel isOpen={true} issue={mockIssue} onClose={onClose} />,
      );
      fireEvent.click(screen.getByTestId("issue-detail-overlay"));
      expect(onClose).toHaveBeenCalledTimes(1);
    });

    it("does not call onClose when clicking panel", () => {
      const mockIssue = createTestIssue();
      const onClose = vi.fn();
      render(
        <IssueDetailPanel isOpen={true} issue={mockIssue} onClose={onClose} />,
      );
      fireEvent.click(screen.getByTestId("issue-detail-panel"));
      expect(onClose).not.toHaveBeenCalled();
    });

    it("calls onClose when pressing Escape", () => {
      mockUseRegisterEscapeLayer.mockClear();
      const mockIssue = createTestIssue();
      const onClose = vi.fn();
      render(
        <IssueDetailPanel isOpen={true} issue={mockIssue} onClose={onClose} />,
      );
      // useRegisterEscapeLayer is mocked; verify it was called with the right args
      // and manually invoke the registered handler to simulate Escape
      const call = mockUseRegisterEscapeLayer.mock.calls.find(
        (c: unknown[]) => c[2] === true, // active=true
      );
      expect(call).toBeDefined();
      const handler = call![1] as () => void;
      handler();
      expect(onClose).toHaveBeenCalledTimes(1);
    });

    it("does not call onClose on Escape when closed", () => {
      const onClose = vi.fn();
      render(
        <IssueDetailPanel isOpen={false} issue={null} onClose={onClose} />,
      );
      fireEvent.keyDown(document, { key: "Escape" });
      expect(onClose).not.toHaveBeenCalled();
    });

    it("does not call onClose on other keys", () => {
      const mockIssue = createTestIssue();
      const onClose = vi.fn();
      render(
        <IssueDetailPanel isOpen={true} issue={mockIssue} onClose={onClose} />,
      );
      fireEvent.keyDown(document, { key: "Enter" });
      fireEvent.keyDown(document, { key: "Tab" });
      expect(onClose).not.toHaveBeenCalled();
    });
  });

  describe("accessibility", () => {
    it("has correct ARIA attributes when open with issue", () => {
      const mockIssue = createTestIssue({ title: "Test Issue" });
      render(
        <IssueDetailPanel isOpen={true} issue={mockIssue} onClose={() => {}} />,
      );
      const panel = screen.getByTestId("issue-detail-panel");
      expect(panel).toHaveAttribute("role", "dialog");
      expect(panel).toHaveAttribute("aria-modal", "true");
      expect(panel).toHaveAttribute("aria-label", "Details for Test Issue");
    });

    it("has default aria-label when issue is null", () => {
      render(
        <IssueDetailPanel isOpen={true} issue={null} onClose={() => {}} />,
      );
      const panel = screen.getByTestId("issue-detail-panel");
      expect(panel).toHaveAttribute("aria-label", "Issue details");
    });

    it("sets aria-hidden on overlay when closed", () => {
      render(
        <IssueDetailPanel isOpen={false} issue={null} onClose={() => {}} />,
      );
      const overlay = screen.getByTestId("issue-detail-overlay");
      expect(overlay).toHaveAttribute("aria-hidden", "true");
    });

    it("clears aria-hidden on overlay when open", () => {
      const mockIssue = createTestIssue();
      render(
        <IssueDetailPanel isOpen={true} issue={mockIssue} onClose={() => {}} />,
      );
      const overlay = screen.getByTestId("issue-detail-overlay");
      expect(overlay).toHaveAttribute("aria-hidden", "false");
    });
  });

  describe("body scroll lock", () => {
    it("locks body scroll when open", () => {
      const mockIssue = createTestIssue();
      render(
        <IssueDetailPanel isOpen={true} issue={mockIssue} onClose={() => {}} />,
      );
      expect(document.body.style.overflow).toBe("hidden");
    });

    it("restores body scroll when closed", () => {
      const mockIssue = createTestIssue();
      const { rerender } = render(
        <IssueDetailPanel isOpen={true} issue={mockIssue} onClose={() => {}} />,
      );
      expect(document.body.style.overflow).toBe("hidden");

      rerender(
        <IssueDetailPanel isOpen={false} issue={null} onClose={() => {}} />,
      );
      expect(document.body.style.overflow).toBe("");
    });

    it("does not lock body scroll when initially closed", () => {
      render(
        <IssueDetailPanel isOpen={false} issue={null} onClose={() => {}} />,
      );
      expect(document.body.style.overflow).toBe("");
    });
  });

  describe("className prop", () => {
    it("applies custom className to overlay", () => {
      const mockIssue = createTestIssue();
      render(
        <IssueDetailPanel
          isOpen={true}
          issue={mockIssue}
          onClose={() => {}}
          className="custom-class"
        />,
      );
      const overlay = screen.getByTestId("issue-detail-overlay");
      expect(overlay).toHaveClass("custom-class");
    });

    it("combines custom className with open class", () => {
      const mockIssue = createTestIssue();
      render(
        <IssueDetailPanel
          isOpen={true}
          issue={mockIssue}
          onClose={() => {}}
          className="custom-class"
        />,
      );
      const overlay = screen.getByTestId("issue-detail-overlay");
      // CSS modules mangle class names, so check for pattern containing 'open'
      expect(overlay.className).toMatch(/open/i);
      expect(overlay).toHaveClass("custom-class");
    });
  });

  describe("cleanup", () => {
    it("removes keydown listener on unmount when open", () => {
      const onClose = vi.fn();
      const mockIssue = createTestIssue();
      const { unmount } = render(
        <IssueDetailPanel isOpen={true} issue={mockIssue} onClose={onClose} />,
      );

      unmount();

      // Escape key should not trigger onClose after unmount
      fireEvent.keyDown(document, { key: "Escape" });
      expect(onClose).not.toHaveBeenCalled();
    });

    it("restores body scroll on unmount when open", () => {
      const mockIssue = createTestIssue();
      const { unmount } = render(
        <IssueDetailPanel isOpen={true} issue={mockIssue} onClose={() => {}} />,
      );
      expect(document.body.style.overflow).toBe("hidden");

      unmount();
      expect(document.body.style.overflow).toBe("");
    });
  });

  describe("edge cases", () => {
    it("handles null issue when open", () => {
      render(
        <IssueDetailPanel isOpen={true} issue={null} onClose={() => {}} />,
      );
      expect(screen.getByTestId("issue-detail-panel")).toBeInTheDocument();
    });

    it("handles rapid open/close", () => {
      const mockIssue = createTestIssue();
      const { rerender } = render(
        <IssueDetailPanel isOpen={false} issue={null} onClose={() => {}} />,
      );

      // Rapidly toggle
      rerender(
        <IssueDetailPanel isOpen={true} issue={mockIssue} onClose={() => {}} />,
      );
      rerender(
        <IssueDetailPanel isOpen={false} issue={null} onClose={() => {}} />,
      );
      rerender(
        <IssueDetailPanel isOpen={true} issue={mockIssue} onClose={() => {}} />,
      );

      // CSS modules mangle class names, so check for pattern containing 'open'
      expect(screen.getByTestId("issue-detail-overlay").className).toMatch(
        /open/i,
      );
      expect(document.body.style.overflow).toBe("hidden");
    });

    it("stops propagation on panel click to prevent overlay close", () => {
      const mockIssue = createTestIssue();
      const onClose = vi.fn();
      render(
        <IssueDetailPanel isOpen={true} issue={mockIssue} onClose={onClose}>
          <button data-testid="inner-button">Click me</button>
        </IssueDetailPanel>,
      );

      // Click on the inner button - should not close
      fireEvent.click(screen.getByTestId("inner-button"));
      expect(onClose).not.toHaveBeenCalled();

      // Click on the panel itself - should not close
      fireEvent.click(screen.getByTestId("issue-detail-panel"));
      expect(onClose).not.toHaveBeenCalled();
    });
  });

  describe("CollapsibleSection", () => {
    it("renders design section always visible (not collapsible at section level)", () => {
      const mockIssue = createTestIssueDetails({
        design: "Short design text",
      });
      render(
        <IssueDetailPanel isOpen={true} issue={mockIssue} onClose={() => {}} />,
      );
      const designSection = screen.getByTestId("design-section");
      expect(designSection).toBeInTheDocument();
      // DesignPanel has a fullscreen button but no collapsible toggle for the section itself
      expect(
        within(designSection).getByLabelText("Enter fullscreen"),
      ).toBeInTheDocument();
    });

    it("renders design in right column with heading", () => {
      const mockIssue = createTestIssueDetails({
        design: "Design content",
      });
      render(
        <IssueDetailPanel isOpen={true} issue={mockIssue} onClose={() => {}} />,
      );
      const designSection = screen.getByTestId("design-section");
      expect(within(designSection).getByText("Design")).toBeInTheDocument();
    });

    it("renders notes section when notes provided", () => {
      const mockIssue = createTestIssueDetails({
        notes: "Some notes content",
      });
      render(
        <IssueDetailPanel isOpen={true} issue={mockIssue} onClose={() => {}} />,
      );
      expect(screen.getByTestId("notes-section")).toBeInTheDocument();
      expect(screen.getByText("Notes")).toBeInTheDocument();
    });
  });

  describe("BlockingBanner", () => {
    it("shows blocking banner when issue status is blocked and has open dependencies", () => {
      const mockIssue = createTestIssueDetails({
        status: "blocked",
        dependencies: [
          createTestDependency({ id: "dep-1", status: "open" }),
          createTestDependency({ id: "dep-2", status: "open" }),
        ],
      });
      render(
        <IssueDetailPanel isOpen={true} issue={mockIssue} onClose={() => {}} />,
      );
      const banner = screen.getByTestId("blocking-banner");
      expect(banner).toBeInTheDocument();
      expect(banner).toHaveTextContent("Blocked by 2 issues");
    });

    it("shows singular text when blocked by 1 issue", () => {
      const mockIssue = createTestIssueDetails({
        status: "blocked",
        dependencies: [createTestDependency({ id: "dep-1", status: "open" })],
      });
      render(
        <IssueDetailPanel isOpen={true} issue={mockIssue} onClose={() => {}} />,
      );
      const banner = screen.getByTestId("blocking-banner");
      expect(banner).toHaveTextContent("Blocked by 1 issue");
    });

    it("does not show banner when status is not blocked even with open dependencies", () => {
      const mockIssue = createTestIssueDetails({
        status: "open",
        dependencies: [
          createTestDependency({ id: "dep-1", status: "open" }),
          createTestDependency({ id: "dep-2", status: "open" }),
        ],
      });
      render(
        <IssueDetailPanel isOpen={true} issue={mockIssue} onClose={() => {}} />,
      );
      expect(screen.queryByTestId("blocking-banner")).not.toBeInTheDocument();
    });

    it("does not show banner when all dependencies are closed", () => {
      const mockIssue = createTestIssueDetails({
        status: "blocked",
        dependencies: [
          createTestDependency({ id: "dep-1", status: "closed" }),
          createTestDependency({ id: "dep-2", status: "closed" }),
        ],
      });
      render(
        <IssueDetailPanel isOpen={true} issue={mockIssue} onClose={() => {}} />,
      );
      expect(screen.queryByTestId("blocking-banner")).not.toBeInTheDocument();
    });

    it("does not show banner when no dependencies", () => {
      const mockIssue = createTestIssueDetails({
        status: "blocked",
        dependencies: [],
      });
      render(
        <IssueDetailPanel isOpen={true} issue={mockIssue} onClose={() => {}} />,
      );
      expect(screen.queryByTestId("blocking-banner")).not.toBeInTheDocument();
    });

    it("counts only open dependencies (excludes closed)", () => {
      const mockIssue = createTestIssueDetails({
        status: "blocked",
        dependencies: [
          createTestDependency({ id: "dep-1", status: "open" }),
          createTestDependency({ id: "dep-2", status: "closed" }),
          createTestDependency({ id: "dep-3", status: "in_progress" }),
        ],
      });
      render(
        <IssueDetailPanel isOpen={true} issue={mockIssue} onClose={() => {}} />,
      );
      const banner = screen.getByTestId("blocking-banner");
      // Only open and in_progress count as blockers (not closed)
      expect(banner).toHaveTextContent("Blocked by 2 issues");
    });
  });

  describe("Metadata bar", () => {
    it("renders issue type", () => {
      const mockIssue = createTestIssueDetails({
        issue_type: "bug",
      });
      render(
        <IssueDetailPanel isOpen={true} issue={mockIssue} onClose={() => {}} />,
      );
      const typeItem = screen.getByTestId("metadata-type");
      expect(typeItem).toHaveTextContent("Bug");
    });

    it("defaults to Task when issue_type is undefined", () => {
      const mockIssue = createTestIssueDetails({
        issue_type: undefined,
      });
      render(
        <IssueDetailPanel isOpen={true} issue={mockIssue} onClose={() => {}} />,
      );
      const typeItem = screen.getByTestId("metadata-type");
      expect(typeItem).toHaveTextContent("Task");
    });

    it("renders owner dropdown with owner name when provided", () => {
      const mockIssue = createTestIssueDetails({
        owner: "john-doe",
      });
      render(
        <IssueDetailPanel isOpen={true} issue={mockIssue} onClose={() => {}} />,
      );
      const ownerTrigger = screen.getAllByTestId("owner-dropdown-trigger")[0];
      expect(ownerTrigger).toHaveTextContent("john-doe");
    });

    it("renders owner dropdown with 'No owner' when not provided", () => {
      const mockIssue = createTestIssueDetails({
        owner: undefined,
      });
      render(
        <IssueDetailPanel isOpen={true} issue={mockIssue} onClose={() => {}} />,
      );
      const ownerTrigger = screen.getAllByTestId("owner-dropdown-trigger")[0];
      expect(ownerTrigger).toHaveTextContent("No owner");
    });

    it("renders assignee dropdown with assignee name when provided", () => {
      const mockIssue = createTestIssueDetails({
        assignee: "jane-smith",
      });
      render(
        <IssueDetailPanel isOpen={true} issue={mockIssue} onClose={() => {}} />,
      );
      const assigneeTrigger = screen.getAllByTestId(
        "assignee-dropdown-trigger",
      )[0];
      expect(assigneeTrigger).toHaveTextContent("jane-smith");
    });

    it("renders assignee dropdown with 'Unassigned' when not provided", () => {
      const mockIssue = createTestIssueDetails({
        assignee: undefined,
      });
      render(
        <IssueDetailPanel isOpen={true} issue={mockIssue} onClose={() => {}} />,
      );
      const assigneeTrigger = screen.getAllByTestId(
        "assignee-dropdown-trigger",
      )[0];
      expect(assigneeTrigger).toHaveTextContent("Unassigned");
    });

    it("renders created date formatted correctly", () => {
      const mockIssue = createTestIssueDetails({
        created_at: "2026-01-15T10:30:00Z",
      });
      render(
        <IssueDetailPanel isOpen={true} issue={mockIssue} onClose={() => {}} />,
      );
      const createdItem = screen.getByTestId("metadata-created");
      expect(createdItem).toHaveTextContent("Created: Jan 15, 2026");
    });

    it("renders all issue types correctly", () => {
      const testCases = [
        { type: "epic", expected: "Epic" },
        { type: "feature", expected: "Feature" },
        { type: "bug", expected: "Bug" },
        { type: "task", expected: "Task" },
      ] as const;

      for (const { type, expected } of testCases) {
        const mockIssue = createTestIssueDetails({ issue_type: type });
        const { unmount } = render(
          <IssueDetailPanel
            isOpen={true}
            issue={mockIssue}
            onClose={() => {}}
          />,
        );
        expect(screen.getByTestId("metadata-type")).toHaveTextContent(expected);
        unmount();
      }
    });
  });

  describe("Design section with MarkdownRenderer", () => {
    it("renders design content using MarkdownRenderer", () => {
      const mockIssue = createTestIssueDetails({
        design: "Some **bold** design text",
      });
      render(
        <IssueDetailPanel isOpen={true} issue={mockIssue} onClose={() => {}} />,
      );
      // MarkdownRenderer uses data-testid="markdown-content"
      expect(screen.getByTestId("markdown-content")).toBeInTheDocument();
    });

    it("does not render design section when design is empty", () => {
      const mockIssue = createTestIssueDetails({
        design: undefined,
      });
      render(
        <IssueDetailPanel isOpen={true} issue={mockIssue} onClose={() => {}} />,
      );
      expect(screen.queryByTestId("design-section")).not.toBeInTheDocument();
    });

    it("renders markdown formatting in design content", () => {
      const mockIssue = createTestIssueDetails({
        design: "# Heading\n\n- List item 1\n- List item 2",
      });
      render(
        <IssueDetailPanel isOpen={true} issue={mockIssue} onClose={() => {}} />,
      );
      const designSection = screen.getByTestId("design-section");
      // Check that markdown was rendered (heading becomes h1)
      expect(
        within(designSection).getByRole("heading", { level: 1 }),
      ).toHaveTextContent("Heading");
    });
  });

  describe("ReviewActionBar", () => {
    it("renders ReviewActionBar for review items", () => {
      const mockIssue = createTestIssueDetails({
        title: "Some task",
        status: "review",
      });
      const onApprove = vi.fn();
      const onReject = vi.fn();
      render(
        <IssueDetailPanel
          isOpen={true}
          issue={mockIssue}
          onClose={() => {}}
          onApprove={onApprove}
          onReject={onReject}
        />,
      );
      expect(screen.getByTestId("review-action-bar")).toBeInTheDocument();
      expect(screen.getByTestId("panel-approve-button")).toBeInTheDocument();
      expect(screen.getByTestId("panel-reject-button")).toBeInTheDocument();
    });

    it("does NOT render ReviewActionBar for non-review items", () => {
      const mockIssue = createTestIssueDetails({
        title: "Regular task without review",
      });
      const onApprove = vi.fn();
      const onReject = vi.fn();
      render(
        <IssueDetailPanel
          isOpen={true}
          issue={mockIssue}
          onClose={() => {}}
          onApprove={onApprove}
          onReject={onReject}
        />,
      );
      expect(screen.queryByTestId("review-action-bar")).not.toBeInTheDocument();
    });

    it("does NOT render ReviewActionBar when onApprove is not provided", () => {
      const mockIssue = createTestIssueDetails({
        title: "Some task",
        status: "review",
      });
      const onReject = vi.fn();
      render(
        <IssueDetailPanel
          isOpen={true}
          issue={mockIssue}
          onClose={() => {}}
          onReject={onReject}
        />,
      );
      expect(screen.queryByTestId("review-action-bar")).not.toBeInTheDocument();
    });

    it("does NOT render ReviewActionBar when onReject is not provided", () => {
      const mockIssue = createTestIssueDetails({
        title: "Some task",
        status: "review",
      });
      const onApprove = vi.fn();
      render(
        <IssueDetailPanel
          isOpen={true}
          issue={mockIssue}
          onClose={() => {}}
          onApprove={onApprove}
        />,
      );
      expect(screen.queryByTestId("review-action-bar")).not.toBeInTheDocument();
    });

    it("clicking Approve button calls onApprove", async () => {
      const mockIssue = createTestIssueDetails({
        title: "Some task",
        status: "review",
      });
      const onApprove = vi.fn().mockResolvedValue(undefined);
      const onReject = vi.fn();
      render(
        <IssueDetailPanel
          isOpen={true}
          issue={mockIssue}
          onClose={() => {}}
          onApprove={onApprove}
          onReject={onReject}
        />,
      );
      fireEvent.click(screen.getByTestId("panel-approve-button"));
      await waitFor(() => {
        expect(onApprove).toHaveBeenCalledTimes(1);
        expect(onApprove).toHaveBeenCalledWith(mockIssue);
      });
    });

    it("clicking Reject button shows the reject comment form", () => {
      const mockIssue = createTestIssueDetails({
        title: "Some task",
        status: "review",
      });
      const onApprove = vi.fn();
      const onReject = vi.fn();
      render(
        <IssueDetailPanel
          isOpen={true}
          issue={mockIssue}
          onClose={() => {}}
          onApprove={onApprove}
          onReject={onReject}
        />,
      );
      fireEvent.click(screen.getByTestId("panel-reject-button"));
      expect(screen.getByTestId("reject-comment-form")).toBeInTheDocument();
    });

    it("ReviewActionBar hides when reject form is shown", () => {
      const mockIssue = createTestIssueDetails({
        title: "Some task",
        status: "review",
      });
      const onApprove = vi.fn();
      const onReject = vi.fn();
      render(
        <IssueDetailPanel
          isOpen={true}
          issue={mockIssue}
          onClose={() => {}}
          onApprove={onApprove}
          onReject={onReject}
        />,
      );
      // Initially the action bar is visible
      expect(screen.getByTestId("review-action-bar")).toBeInTheDocument();

      // Click reject to show form
      fireEvent.click(screen.getByTestId("panel-reject-button"));

      // Action bar should be hidden, form should be visible
      expect(screen.queryByTestId("review-action-bar")).not.toBeInTheDocument();
      expect(screen.getByTestId("reject-comment-form")).toBeInTheDocument();
    });

    it("reject form submit calls onReject with comment", async () => {
      const mockIssue = createTestIssueDetails({
        title: "Some task",
        status: "review",
      });
      const onApprove = vi.fn();
      const onReject = vi.fn().mockResolvedValue(undefined);
      render(
        <IssueDetailPanel
          isOpen={true}
          issue={mockIssue}
          onClose={() => {}}
          onApprove={onApprove}
          onReject={onReject}
        />,
      );
      // Click reject to show form
      fireEvent.click(screen.getByTestId("panel-reject-button"));

      // Type a comment in the textarea
      const textarea = screen.getByTestId("reject-textarea");
      fireEvent.change(textarea, { target: { value: "Needs more work" } });

      // Submit the form
      fireEvent.click(screen.getByTestId("reject-submit"));

      await waitFor(() => {
        expect(onReject).toHaveBeenCalledTimes(1);
        expect(onReject).toHaveBeenCalledWith(mockIssue, "Needs more work");
      });
    });

    it("reject form cancel shows ReviewActionBar again", () => {
      const mockIssue = createTestIssueDetails({
        title: "Some task",
        status: "review",
      });
      const onApprove = vi.fn();
      const onReject = vi.fn();
      render(
        <IssueDetailPanel
          isOpen={true}
          issue={mockIssue}
          onClose={() => {}}
          onApprove={onApprove}
          onReject={onReject}
        />,
      );
      // Click reject to show form
      fireEvent.click(screen.getByTestId("panel-reject-button"));
      expect(screen.queryByTestId("review-action-bar")).not.toBeInTheDocument();
      expect(screen.getByTestId("reject-comment-form")).toBeInTheDocument();

      // Click cancel
      fireEvent.click(screen.getByTestId("reject-cancel"));

      // Action bar should be back, form should be gone
      expect(screen.getByTestId("review-action-bar")).toBeInTheDocument();
      expect(
        screen.queryByTestId("reject-comment-form"),
      ).not.toBeInTheDocument();
    });
  });

  describe("Details tab", () => {
    it("always shows Details tab", () => {
      const mockIssue = createTestIssueDetails({
        description: "Test description",
      });
      render(
        <IssueDetailPanel isOpen={true} issue={mockIssue} onClose={() => {}} />,
      );
      expect(screen.getByRole("tab", { name: "Details" })).toBeInTheDocument();
    });

    it("defaults to Details tab and shows detail content", () => {
      const mockIssue = createTestIssueDetails({
        assignee: "agent-1",
        description: "Test issue description",
        design: "# Design content",
      });
      render(
        <IssueDetailPanel isOpen={true} issue={mockIssue} onClose={() => {}} />,
      );

      const detailsTab = screen.getByRole("tab", { name: "Details" });
      expect(detailsTab).toHaveAttribute("aria-selected", "true");
    });

    it("Details tab close button is not shown", () => {
      const mockIssue = createTestIssueDetails({
        description: "Test description",
      });
      render(
        <IssueDetailPanel isOpen={true} issue={mockIssue} onClose={() => {}} />,
      );
      expect(screen.queryByTestId("close-tab-details")).not.toBeInTheDocument();
    });
  });

  describe("Start Work", () => {
    it("shows plan agents for issues that need planning", () => {
      const agentStore = createAgentStore();
      agentStore.setState({
        agents: [
          {
            name: "planner",
            branch: "main",
            status: "idle",
            ahead: 0,
            behind: 0,
            role: "plan",
            workspace: "Desktop QA",
          },
          {
            name: "desktopqa",
            branch: "main",
            status: "idle",
            ahead: 0,
            behind: 0,
            role: "task",
            workspace: "Desktop QA",
          },
        ],
        isConnected: true,
        wasEverConnected: true,
        connectionState: "connected",
      });
      mockUseAgentStoreInstance.mockReturnValue(agentStore);
      mockUseWorkspaceContext.mockImplementation(() =>
        createWorkspaceContext({ workspaceId: "DESKTOP-QA" }),
      );

      const issue = createTestIssueDetails({
        id: "DESKTOP-QA-3",
        status: "open",
        issue_type: "task",
        assignee: "",
        design: "",
      });

      render(
        <IssueDetailPanel isOpen={true} issue={issue} onClose={() => {}} />,
      );

      fireEvent.click(screen.getByTestId("start-work-button"));

      expect(screen.getByTestId("agent-option-planner")).toBeInTheDocument();
      expect(
        screen.queryByTestId("agent-option-desktopqa"),
      ).not.toBeInTheDocument();
    });

    it("assigns the issue and asks the daemon to claim the requested task", async () => {
      const mockUpdateIssue = updateIssue as ReturnType<typeof vi.fn>;
      const mockStartAgent = startAgent as ReturnType<typeof vi.fn>;
      mockUpdateIssue.mockReset();
      mockStartAgent.mockReset();
      mockStartAgent.mockResolvedValue(undefined);
      const refetchAgents = vi.fn().mockResolvedValue(undefined);
      const agentStore = createAgentStore();
      agentStore.setState({
        agents: [
          {
            name: "desktopqa",
            branch: "main",
            status: "idle",
            ahead: 0,
            behind: 0,
            role: "task",
            workspace: "Desktop QA",
          },
        ],
        isConnected: true,
        wasEverConnected: true,
        connectionState: "connected",
        fetchData: refetchAgents,
      });
      mockUseAgentStoreInstance.mockReturnValue(agentStore);
      mockUseWorkspaceContext.mockImplementation(() =>
        createWorkspaceContext({ workspaceId: "DESKTOP-QA" }),
      );

      const issue = createTestIssueDetails({
        id: "DESKTOP-QA-3",
        status: "open",
        issue_type: "task",
        assignee: "",
        design: "Implementation plan is ready.",
      });
      const updatedIssue = { ...issue, assignee: "desktopqa" };
      mockUpdateIssue.mockResolvedValueOnce(updatedIssue);
      const onIssueUpdate = vi.fn();

      render(
        <IssueDetailPanel
          isOpen={true}
          issue={issue}
          onClose={() => {}}
          onIssueUpdate={onIssueUpdate}
        />,
      );

      fireEvent.click(screen.getByTestId("start-work-button"));
      fireEvent.click(screen.getByTestId("agent-option-desktopqa"));

      await waitFor(() => {
        expect(mockUpdateIssue).toHaveBeenCalledWith(
          "DESKTOP-QA",
          "DESKTOP-QA-3",
          { assignee: "desktopqa", status: "in_progress" },
        );
      });
      await waitFor(() => {
        expect(mockStartAgent).toHaveBeenCalledWith("DESKTOP-QA", "desktopqa", {
          taskId: "DESKTOP-QA-3",
        });
      });
      expect(onIssueUpdate).toHaveBeenCalledWith(updatedIssue);
      expect(refetchAgents).toHaveBeenCalled();
    });

    it("rolls back the assignee when daemon start fails", async () => {
      const mockUpdateIssue = updateIssue as ReturnType<typeof vi.fn>;
      const mockStartAgent = startAgent as ReturnType<typeof vi.fn>;
      mockUpdateIssue.mockReset();
      mockStartAgent.mockReset();
      mockStartAgent.mockRejectedValueOnce(new Error("daemon unavailable"));
      const agentStore = createAgentStore();
      agentStore.setState({
        agents: [
          {
            name: "desktopqa",
            branch: "main",
            status: "idle",
            ahead: 0,
            behind: 0,
            role: "task",
            workspace: "Desktop QA",
          },
        ],
        isConnected: true,
        wasEverConnected: true,
        connectionState: "connected",
      });
      mockUseAgentStoreInstance.mockReturnValue(agentStore);
      mockUseWorkspaceContext.mockImplementation(() =>
        createWorkspaceContext({ workspaceId: "DESKTOP-QA" }),
      );

      const issue = createTestIssueDetails({
        id: "DESKTOP-QA-3",
        status: "open",
        issue_type: "task",
        assignee: "",
        design: "Implementation plan is ready.",
      });
      const assignedIssue = {
        ...issue,
        assignee: "desktopqa",
        status: "in_progress" as const,
      };
      const rolledBackIssue = {
        ...issue,
        assignee: "",
        status: "open" as const,
      };
      mockUpdateIssue
        .mockResolvedValueOnce(assignedIssue)
        .mockResolvedValueOnce(rolledBackIssue);
      const onIssueUpdate = vi.fn();

      render(
        <IssueDetailPanel
          isOpen={true}
          issue={issue}
          onClose={() => {}}
          onIssueUpdate={onIssueUpdate}
        />,
      );

      fireEvent.click(screen.getByTestId("start-work-button"));
      fireEvent.click(screen.getByTestId("agent-option-desktopqa"));

      await waitFor(() => {
        expect(mockStartAgent).toHaveBeenCalledWith("DESKTOP-QA", "desktopqa", {
          taskId: "DESKTOP-QA-3",
        });
      });
      await waitFor(() => {
        expect(mockUpdateIssue).toHaveBeenLastCalledWith(
          "DESKTOP-QA",
          "DESKTOP-QA-3",
          { assignee: "", status: "open" },
        );
      });
      expect(onIssueUpdate).toHaveBeenCalledWith(rolledBackIssue);
      expect(await screen.findByTestId("start-work-error")).toHaveTextContent(
        "daemon unavailable",
      );
    });
  });

  describe("PR links via IssueHeader", () => {
    it("passes PR props to IssueHeader when issue has PR external_ref", () => {
      const mockIssue = createTestIssueDetails({
        external_ref: "https://github.com/owner/repo/pull/42",
      });
      render(
        <IssueDetailPanel isOpen={true} issue={mockIssue} onClose={() => {}} />,
      );
      expect(screen.getByTestId("header-pr-view-link")).toBeInTheDocument();
      expect(screen.getByTestId("header-pr-merge-link")).toBeInTheDocument();
      expect(screen.getByTestId("header-pr-view-link")).toHaveTextContent(
        "↗ #42",
      );
      expect(screen.getByTestId("header-pr-merge-link")).toHaveTextContent(
        "→ merge #42",
      );
    });

    it("does not pass PR props when external_ref is null", () => {
      const mockIssue = createTestIssueDetails({
        external_ref: null,
      });
      render(
        <IssueDetailPanel isOpen={true} issue={mockIssue} onClose={() => {}} />,
      );
      expect(
        screen.queryByTestId("header-pr-view-link"),
      ).not.toBeInTheDocument();
      expect(
        screen.queryByTestId("header-pr-merge-link"),
      ).not.toBeInTheDocument();
    });

    it("does not pass PR props when external_ref is undefined", () => {
      const mockIssue = createTestIssueDetails({
        external_ref: undefined,
      });
      render(
        <IssueDetailPanel isOpen={true} issue={mockIssue} onClose={() => {}} />,
      );
      expect(
        screen.queryByTestId("header-pr-view-link"),
      ).not.toBeInTheDocument();
      expect(
        screen.queryByTestId("header-pr-merge-link"),
      ).not.toBeInTheDocument();
    });

    it("does not pass PR props when external_ref is a non-PR URL", () => {
      const mockIssue = createTestIssueDetails({
        external_ref: "JIRA-123",
      });
      render(
        <IssueDetailPanel isOpen={true} issue={mockIssue} onClose={() => {}} />,
      );
      expect(
        screen.queryByTestId("header-pr-view-link"),
      ).not.toBeInTheDocument();
      expect(
        screen.queryByTestId("header-pr-merge-link"),
      ).not.toBeInTheDocument();
    });

    it("extracts PR number correctly from /pull/42 URL", () => {
      const mockIssue = createTestIssueDetails({
        external_ref: "https://github.com/owner/repo/pull/42",
      });
      render(
        <IssueDetailPanel isOpen={true} issue={mockIssue} onClose={() => {}} />,
      );
      const viewLink = screen.getByTestId("header-pr-view-link");
      expect(viewLink).toHaveTextContent("↗ #42");
      expect(viewLink).toHaveAttribute(
        "href",
        "https://github.com/owner/repo/pull/42",
      );
    });

    it("extracts PR number correctly from /pulls/123 URL", () => {
      const mockIssue = createTestIssueDetails({
        external_ref: "https://github.com/owner/repo/pulls/123",
      });
      render(
        <IssueDetailPanel isOpen={true} issue={mockIssue} onClose={() => {}} />,
      );
      const viewLink = screen.getByTestId("header-pr-view-link");
      expect(viewLink).toHaveTextContent("↗ #123");
      expect(viewLink).toHaveAttribute(
        "href",
        "https://github.com/owner/repo/pulls/123",
      );
    });
  });

  describe("handleTitleSave error handling", () => {
    const mockUpdateIssue = updateIssue as ReturnType<typeof vi.fn>;

    beforeEach(() => {
      mockUpdateIssue.mockReset();
    });

    it("shows error toast when title save fails", async () => {
      mockUpdateIssue.mockRejectedValueOnce(new Error("Network error"));

      const mockIssue = createTestIssueDetails({ title: "Original Title" });
      render(
        <IssueDetailPanel isOpen={true} issue={mockIssue} onClose={() => {}} />,
      );

      // Click the title display to enter edit mode
      const titleDisplay = screen.getByTestId("editable-title-display");
      fireEvent.click(titleDisplay);

      // Change the title and trigger save via Enter
      const input = screen.getByTestId("editable-title-input");
      fireEvent.change(input, { target: { value: "New Title" } });
      fireEvent.keyDown(input, { key: "Enter" });

      // Error toast should appear
      await waitFor(() => {
        expect(screen.getByTestId("title-error-toast")).toBeInTheDocument();
      });
      expect(screen.getByTestId("title-error-toast")).toHaveTextContent(
        "Network error",
      );
    });

    it("shows generic error message for non-Error exceptions", async () => {
      mockUpdateIssue.mockRejectedValueOnce("string error");

      const mockIssue = createTestIssueDetails({ title: "Original Title" });
      render(
        <IssueDetailPanel isOpen={true} issue={mockIssue} onClose={() => {}} />,
      );

      const titleDisplay = screen.getByTestId("editable-title-display");
      fireEvent.click(titleDisplay);

      const input = screen.getByTestId("editable-title-input");
      fireEvent.change(input, { target: { value: "New Title" } });
      fireEvent.keyDown(input, { key: "Enter" });

      await waitFor(() => {
        expect(screen.getByTestId("title-error-toast")).toBeInTheDocument();
      });
      expect(screen.getByTestId("title-error-toast")).toHaveTextContent(
        "Failed to update title",
      );
    });

    it("clears title error on next save attempt", async () => {
      // First save fails
      mockUpdateIssue.mockRejectedValueOnce(new Error("Network error"));

      const mockIssue = createTestIssueDetails({ title: "Original Title" });
      render(
        <IssueDetailPanel isOpen={true} issue={mockIssue} onClose={() => {}} />,
      );

      // Enter edit mode and trigger failed save
      const titleDisplay = screen.getByTestId("editable-title-display");
      fireEvent.click(titleDisplay);

      const input = screen.getByTestId("editable-title-input");
      fireEvent.change(input, { target: { value: "New Title" } });
      fireEvent.keyDown(input, { key: "Enter" });

      await waitFor(() => {
        expect(screen.getByTestId("title-error-toast")).toBeInTheDocument();
      });

      // Second save succeeds
      mockUpdateIssue.mockResolvedValueOnce({
        ...mockIssue,
        title: "New Title",
      });

      // EditableTitle stays in edit mode after error, so input should still be there
      const inputAfterError = screen.getByTestId("editable-title-input");
      fireEvent.change(inputAfterError, { target: { value: "New Title" } });
      fireEvent.keyDown(inputAfterError, { key: "Enter" });

      await waitFor(() => {
        expect(
          screen.queryByTestId("title-error-toast"),
        ).not.toBeInTheDocument();
      });
    });
  });

  describe("terminal session cleanup on implicit tab discard", () => {
    const TERMINAL_TABS_PERSISTED = {
      savedState: {
        issue_id: "test-123",
        tabs: [
          {
            id: "details",
            type: "details" as const,
            label: "Details",
            sort_order: 0,
          },
          {
            id: "sessions",
            type: "sessions" as const,
            label: "Runs",
            sort_order: 1,
          },
          {
            id: "terminal-sess-1",
            type: "terminal" as const,
            label: "Terminal (shell)",
            session_name: "sess-1",
            sort_order: 2,
          },
        ],
        active_tab_id: "terminal-sess-1",
        updated_at: "2026-01-23T00:00:00Z",
      },
      isLoading: false,
      saveTabs: vi.fn(),
      clearTabs: vi.fn(),
    };

    beforeEach(() => {
      mockDeleteTabMetadata.mockClear();
      mockScheduleSessionKill.mockClear();
      // Provide workspace ID so deleteTabMetadata is called
      mockUseWorkspaceContext.mockReturnValue({
        workspace: { id: "ws-1", name: "default" },
        repos: [],
        groups: [],
        agents: [],
        isLoading: false,
        error: null,
        refetch: () => {},
        getRepoByName: () => undefined,
        getReposByGroup: () => [],
        getAgentByName: () => undefined,
        workspaceId: "ws-1",
        activeWorkspaceName: "default",
        setActiveWorkspace: () => {},
        defaultWorkspaceName: "default",
        setDefaultWorkspace: () => Promise.resolve(),
      });
    });

    afterEach(() => {
      // Reset to defaults
      mockUseIssueTabPersistence.mockReturnValue({
        savedState: null,
        isLoading: true,
        saveTabs: vi.fn(),
        clearTabs: vi.fn(),
      });
      mockUseWorkspaceContext.mockReturnValue({
        workspace: null,
        repos: [],
        groups: [],
        agents: [],
        isLoading: false,
        error: null,
        refetch: () => {},
        getRepoByName: () => undefined,
        getReposByGroup: () => [],
        getAgentByName: () => undefined,
        workspaceId: "",
        activeWorkspaceName: null,
        setActiveWorkspace: () => {},
        defaultWorkspaceName: null,
        setDefaultWorkspace: () => Promise.resolve(),
      });
    });

    it("cleans up terminal sessions when issue ID changes", async () => {
      // Return persisted state with a terminal tab for issue A
      mockUseIssueTabPersistence.mockReturnValue(TERMINAL_TABS_PERSISTED);

      const issueA = createTestIssue({ id: "issue-a" });
      const { rerender } = render(
        <IssueDetailPanel isOpen={true} issue={issueA} onClose={() => {}} />,
      );

      // Wait for terminal tab to be restored
      await waitFor(() => {
        expect(
          screen.getByRole("tab", { name: /Terminal/ }),
        ).toBeInTheDocument();
      });

      // Clear mocks from initial render (cleanup fires on first mount too)
      mockDeleteTabMetadata.mockClear();
      mockScheduleSessionKill.mockClear();

      // Change issue — should trigger cleanup of terminal tabs
      const issueB = createTestIssue({ id: "issue-b" });
      rerender(
        <IssueDetailPanel isOpen={true} issue={issueB} onClose={() => {}} />,
      );

      await waitFor(() => {
        expect(mockDeleteTabMetadata).toHaveBeenCalledWith("ws-1", "sess-1");
      });
    });

    it("cleans up terminal sessions on component unmount", async () => {
      mockUseIssueTabPersistence.mockReturnValue(TERMINAL_TABS_PERSISTED);

      const issue = createTestIssue({ id: "test-123" });
      const { unmount } = render(
        <IssueDetailPanel isOpen={true} issue={issue} onClose={() => {}} />,
      );

      // Wait for terminal tab to be restored
      await waitFor(() => {
        expect(
          screen.getByRole("tab", { name: /Terminal/ }),
        ).toBeInTheDocument();
      });

      mockDeleteTabMetadata.mockClear();
      mockScheduleSessionKill.mockClear();

      unmount();

      expect(mockDeleteTabMetadata).toHaveBeenCalledWith("ws-1", "sess-1");
    });

    it("does not call cleanup when no terminal tabs exist", () => {
      // Default persistence: no saved state, no terminal tabs
      mockUseIssueTabPersistence.mockReturnValue({
        savedState: null,
        isLoading: false,
        saveTabs: vi.fn(),
        clearTabs: vi.fn(),
      });

      const issueA = createTestIssue({ id: "issue-a" });
      const { rerender } = render(
        <IssueDetailPanel isOpen={true} issue={issueA} onClose={() => {}} />,
      );

      mockDeleteTabMetadata.mockClear();
      mockScheduleSessionKill.mockClear();

      const issueB = createTestIssue({ id: "issue-b" });
      rerender(
        <IssueDetailPanel isOpen={true} issue={issueB} onClose={() => {}} />,
      );

      expect(mockDeleteTabMetadata).not.toHaveBeenCalled();
    });

    it("cleans up multiple terminal tabs on issue change", async () => {
      const multiTerminalPersisted = {
        ...TERMINAL_TABS_PERSISTED,
        savedState: {
          issue_id: "test-123",
          tabs: [
            {
              id: "details",
              type: "details" as const,
              label: "Details",
              sort_order: 0,
            },
            {
              id: "sessions",
              type: "sessions" as const,
              label: "Runs",
              sort_order: 1,
            },
            {
              id: "terminal-sess-1",
              type: "terminal" as const,
              label: "Terminal (shell)",
              session_name: "sess-1",
              sort_order: 2,
            },
            {
              id: "terminal-sess-2",
              type: "terminal" as const,
              label: "Terminal (shell)",
              session_name: "sess-2",
              sort_order: 3,
            },
          ],
          active_tab_id: "terminal-sess-1",
          updated_at: "2026-01-23T00:00:00Z",
        },
      };
      mockUseIssueTabPersistence.mockReturnValue(multiTerminalPersisted);

      const issueA = createTestIssue({ id: "issue-a" });
      const { rerender } = render(
        <IssueDetailPanel isOpen={true} issue={issueA} onClose={() => {}} />,
      );

      // Wait for terminal tabs to be restored
      await waitFor(() => {
        const terminalTabs = screen.getAllByRole("tab", { name: /Terminal/ });
        expect(terminalTabs).toHaveLength(2);
      });

      mockDeleteTabMetadata.mockClear();
      mockScheduleSessionKill.mockClear();

      const issueB = createTestIssue({ id: "issue-b" });
      rerender(
        <IssueDetailPanel isOpen={true} issue={issueB} onClose={() => {}} />,
      );

      await waitFor(() => {
        expect(mockDeleteTabMetadata).toHaveBeenCalledWith("ws-1", "sess-1");
        expect(mockDeleteTabMetadata).toHaveBeenCalledWith("ws-1", "sess-2");
      });
    });
  });

  describe("tab reset on issue change", () => {
    it("includes Runs tab on initial render", () => {
      const mockIssue = createTestIssue();
      render(
        <IssueDetailPanel isOpen={true} issue={mockIssue} onClose={() => {}} />,
      );
      expect(screen.getByRole("tab", { name: "Runs" })).toBeInTheDocument();
    });

    it("includes Runs tab after issue ID changes", () => {
      const issueA = createTestIssue({ id: "issue-a" });
      const issueB = createTestIssue({ id: "issue-b" });
      const { rerender } = render(
        <IssueDetailPanel isOpen={true} issue={issueA} onClose={() => {}} />,
      );
      rerender(
        <IssueDetailPanel isOpen={true} issue={issueB} onClose={() => {}} />,
      );
      expect(screen.getByRole("tab", { name: "Runs" })).toBeInTheDocument();
    });

    it("resets active tab to Details when issue changes", () => {
      const issueA = createTestIssueDetails({ id: "issue-a" });
      const issueB = createTestIssueDetails({ id: "issue-b" });
      const { rerender } = render(
        <IssueDetailPanel isOpen={true} issue={issueA} onClose={() => {}} />,
      );

      // Click Runs tab
      const runsTab = screen.getByRole("tab", { name: "Runs" });
      fireEvent.click(runsTab);
      expect(runsTab).toHaveAttribute("aria-selected", "true");

      // Rerender with different issue
      rerender(
        <IssueDetailPanel isOpen={true} issue={issueB} onClose={() => {}} />,
      );

      // Details tab should be active again, Runs tab still present
      expect(screen.getByRole("tab", { name: "Runs" })).toBeInTheDocument();
      expect(screen.getByRole("tab", { name: "Details" })).toHaveAttribute(
        "aria-selected",
        "true",
      );
    });

    it("falls back to Details when restored active tab has no renderer", async () => {
      mockUseIssueTabPersistence.mockReturnValue({
        savedState: {
          issue_id: "test-123",
          tabs: [
            {
              id: "details",
              type: "details" as const,
              label: "Details",
              sort_order: 0,
            },
            {
              id: "sessions",
              type: "sessions" as const,
              label: "Runs",
              sort_order: 1,
            },
            {
              id: "terminal-sess-unknown",
              type: "terminal" as const,
              label: "Terminal",
              session_name: "sess-unknown",
              sort_order: 2,
            },
          ],
          active_tab_id: "terminal-sess-unknown",
          updated_at: "2026-01-23T00:00:00Z",
        },
        isLoading: false,
        saveTabs: vi.fn(),
        clearTabs: vi.fn(),
      });

      const mockIssue = createTestIssueDetails({
        description: "Visible details content",
      });
      render(
        <IssueDetailPanel isOpen={true} issue={mockIssue} onClose={() => {}} />,
      );

      await waitFor(() => {
        expect(screen.getByRole("tab", { name: "Details" })).toHaveAttribute(
          "aria-selected",
          "true",
        );
      });
      expect(screen.getByText("Visible details content")).toBeInTheDocument();
    });

    it("preserves both tabs across multiple issue changes", () => {
      const issueA = createTestIssue({ id: "issue-a" });
      const issueB = createTestIssue({ id: "issue-b" });
      const issueC = createTestIssue({ id: "issue-c" });
      const { rerender } = render(
        <IssueDetailPanel isOpen={true} issue={issueA} onClose={() => {}} />,
      );
      rerender(
        <IssueDetailPanel isOpen={true} issue={issueB} onClose={() => {}} />,
      );
      rerender(
        <IssueDetailPanel isOpen={true} issue={issueC} onClose={() => {}} />,
      );
      expect(screen.getByRole("tab", { name: "Details" })).toBeInTheDocument();
      expect(screen.getByRole("tab", { name: "Runs" })).toBeInTheDocument();
    });
  });
});
