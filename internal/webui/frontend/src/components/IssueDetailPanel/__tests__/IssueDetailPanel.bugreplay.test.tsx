/**
 * @vitest-environment jsdom
 */

/**
 * Unit tests for IssueDetailPanel component.
 */

import { act, render, screen, waitFor } from "@testing-library/react";
import { describe, it, expect, vi, beforeEach } from "vitest";
import "@testing-library/jest-dom";

import type { Event, IssueDetails } from "@/types";
import {
  startWorkflowRun,
  createWorkspaceAgent,
  deleteWorkspaceAgent,
  getIssueEvents,
} from "@/api";
import { createAgentStore } from "@/stores/agentStore";

import { IssueDetailPanel } from "../IssueDetailPanel";

// Create hoisted mocks
const {
  mockUseRegisterEscapeLayer,
  mockDeleteTabMetadata,
  mockScheduleSessionKill,
  mockUseIssueTabPersistence,
  mockUseLocalSettings,
  mockUseWorkspaceContext,
  mockUseAgentStoreInstance,
  mockGetTaskSessions,
  mockShowToast,
  mockUseToast,
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
  mockUseLocalSettings: vi.fn(() => ({
    settings: {
      version: 1,
      fleetdb_redis: {
        enabled: false,
        db: 0,
        tls: false,
        password_set: false,
      },
      agent_runtime: { default: "local" },
      local_task_runner: {},
      runtime_credentials: {
        daytona: { configured: false },
        github: { configured: false },
      },
    },
    isLoading: false,
    isSaving: false,
    error: null,
    updateRedis: vi.fn(),
    updateAgentRuntime: vi.fn(),
    updateLocalTaskRunner: vi.fn(),
    updateRuntimeCredentials: vi.fn(),
    refetch: vi.fn(),
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
  mockShowToast: vi.fn(),
  mockUseToast: vi.fn(() => ({
    toasts: [],
    showToast: vi.fn(),
    dismissToast: vi.fn(),
    dismissAll: vi.fn(),
  })),
}));

// Mock the API module
vi.mock("@/api", () => ({
  EPIC_RUNNER_WORKFLOW_NAME: "epic-runner",
  updateIssue: vi.fn(),
  createWorkspaceAgent: vi.fn(),
  deleteWorkspaceAgent: vi.fn().mockResolvedValue(undefined),
  startAgent: vi.fn().mockResolvedValue(undefined),
  startWorkflowRun: vi.fn().mockResolvedValue({
    workspace_key: "DESKTOP-QA",
    run_id: "run-1",
    driver_id: "driver-1",
    driver_version_id: "version-1",
    status: "queued",
    created_at: "2026-01-23T00:00:00Z",
    updated_at: "2026-01-23T00:00:00Z",
  }),
  addDependency: vi.fn(),
  removeDependency: vi.fn(),
  getIssueEvents: vi.fn().mockImplementation(() => new Promise(() => {})),
  getTaskLogPhases: vi.fn().mockResolvedValue([]),
}));

vi.mock("@/hooks/ui", async () => {
  const actual =
    await vi.importActual<typeof import("@/hooks/ui")>("@/hooks/ui");
  return { ...actual, useToast: mockUseToast };
});

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
  return {
    ...actual,
    useLocalSettings: mockUseLocalSettings,
    useWorkspaceContext: mockUseWorkspaceContext,
  };
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

function createTestEvent(overrides: Partial<Event> = {}): Event {
  return {
    id: "event-1",
    issue_id: "test-123",
    event_type: "issue.create",
    actor: "alice",
    created_at: "2026-01-23T00:00:00Z",
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

function createLocalSettingsHookReturn(
  overrides: Record<string, unknown> = {},
) {
  return {
    settings: {
      version: 1,
      fleetdb_redis: {
        enabled: false,
        db: 0,
        tls: false,
        password_set: false,
      },
      agent_runtime: { default: "local" },
      local_task_runner: {},
      runtime_credentials: {
        daytona: { configured: false },
        github: { configured: false },
      },
    },
    isLoading: false,
    isSaving: false,
    error: null,
    updateRedis: vi.fn(),
    updateAgentRuntime: vi.fn(),
    updateLocalTaskRunner: vi.fn(),
    updateRuntimeCredentials: vi.fn(),
    refetch: vi.fn(),
    ...overrides,
  };
}

describe("IssueDetailPanel SSE bug replay", () => {
  beforeEach(() => {
    const agentStore = createAgentStore();
    mockUseAgentStoreInstance.mockReset();
    mockUseAgentStoreInstance.mockReturnValue(agentStore);
    mockUseLocalSettings.mockReset();
    mockUseLocalSettings.mockImplementation(() =>
      createLocalSettingsHookReturn(),
    );
    mockUseWorkspaceContext.mockReset();
    mockUseWorkspaceContext.mockImplementation(() => createWorkspaceContext());
    mockGetTaskSessions.mockReset();
    mockGetTaskSessions.mockResolvedValue([]);
    mockShowToast.mockReset();
    mockUseToast.mockReset();
    mockUseToast.mockImplementation(() => ({
      toasts: [],
      showToast: mockShowToast,
      dismissToast: vi.fn(),
      dismissAll: vi.fn(),
    }));
    const mockStartWorkflowRun = startWorkflowRun as ReturnType<typeof vi.fn>;
    mockStartWorkflowRun.mockReset();
    mockStartWorkflowRun.mockResolvedValue({
      workspace_key: "DESKTOP-QA",
      run_id: "run-1",
      driver_id: "driver-1",
      driver_version_id: "version-1",
      status: "queued",
      created_at: "2026-01-23T00:00:00Z",
      updated_at: "2026-01-23T00:00:00Z",
    });
    const mockCreateWorkspaceAgent = createWorkspaceAgent as ReturnType<
      typeof vi.fn
    >;
    mockCreateWorkspaceAgent.mockReset();
    mockCreateWorkspaceAgent.mockImplementation(
      async (
        _workspaceId: string,
        request: {
          name: string;
          role_name: string;
          repos?: string[];
          repo_groups?: string[];
          cross_repo?: boolean;
        },
      ) => ({
        name: request.name,
        repos: request.repos ?? [],
        repo_groups: request.repo_groups ?? [],
        cross_repo: request.cross_repo ?? false,
        role_name: request.role_name,
      }),
    );
    const mockDeleteWorkspaceAgent = deleteWorkspaceAgent as ReturnType<
      typeof vi.fn
    >;
    mockDeleteWorkspaceAgent.mockReset();
    mockDeleteWorkspaceAgent.mockResolvedValue(undefined);
    const mockGetIssueEvents = vi.mocked(getIssueEvents);
    mockGetIssueEvents.mockReset();
    mockGetIssueEvents.mockImplementation(() => new Promise(() => {}));
  });

  it("#666 a failed history refresh does not empty the Journey tab", async () => {
    const mockGetIssueEvents = vi.mocked(getIssueEvents);
    mockGetIssueEvents
      .mockResolvedValueOnce([
        createTestEvent(),
        createTestEvent({
          id: "event-2",
          event_type: "issue.close",
          actor: "worker-1",
          created_at: "2026-01-23T00:00:01Z",
        }),
      ])
      .mockRejectedValueOnce(new Error("history read failed"));
    mockUseWorkspaceContext.mockImplementation(() =>
      createWorkspaceContext({ workspaceId: "workspace-1" }),
    );
    const loaded = createTestIssueDetails({
      status: "closed",
      updated_at: "2026-01-23T00:00:01Z",
    });
    const { rerender } = render(
      <IssueDetailPanel isOpen={true} issue={loaded} onClose={() => {}} />,
    );
    await waitFor(() => {
      expect(screen.getByTestId("journey-tail")).toHaveTextContent("Done");
    });
    const journeyBefore = screen.getByTestId("journey-tail").textContent;

    rerender(
      <IssueDetailPanel
        isOpen={true}
        issue={{ ...loaded, updated_at: "2026-01-23T00:00:02Z" }}
        onClose={() => {}}
      />,
    );
    await waitFor(() => {
      expect(mockGetIssueEvents).toHaveBeenCalledTimes(2);
    });
    await act(async () => {
      await new Promise((resolve) => setTimeout(resolve, 20));
    });

    expect(
      screen.queryByTestId("journey-tail")?.textContent,
      "a failed history read replaced the loaded Journey with an empty history",
    ).toBe(journeyBefore);
  });
});
