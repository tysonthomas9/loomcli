/**
 * @vitest-environment jsdom
 */

/**
 * Unit tests for RedirectToWorkspace component.
 *
 * Covers the redirect-loop fix: reading failedWorkspaceId from location.state
 * and filtering that workspace out of the candidate list before redirecting.
 */

import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { describe, it, expect, vi, beforeEach } from "vitest";
import "@testing-library/jest-dom";
import { MemoryRouter } from "react-router-dom";

import { RedirectToWorkspace } from "../RedirectToWorkspace";

// ── Hoisted mocks ────────────────────────────────────────────────────────────

const { mockNavigate, mockLocation } = vi.hoisted(() => ({
  mockNavigate: vi.fn(),
  mockLocation: { state: null as unknown },
}));

vi.mock("react-router-dom", async (importOriginal) => {
  const actual = await importOriginal<typeof import("react-router-dom")>();
  return {
    ...actual,
    useNavigate: vi.fn(() => mockNavigate),
    useLocation: vi.fn(() => mockLocation),
  };
});

const { mockFetchWorkspace } = vi.hoisted(() => ({
  mockFetchWorkspace: vi.fn(),
}));

vi.mock("@/hooks/api", () => ({
  fetchWorkspaceApi: mockFetchWorkspace,
}));

const { mockUseAuth } = vi.hoisted(() => ({
  mockUseAuth: vi.fn(() => ({
    mode: "open" as const,
    isAuthenticated: true,
    isLoading: false,
    user: null,
    authServiceDown: false,
    signIn: vi.fn(),
    signUp: vi.fn(),
    signOut: vi.fn(),
  })),
}));

vi.mock("@/contexts/AuthContext", () => ({
  useAuth: mockUseAuth,
}));

const { mockCreateWorkspaceModalProps } = vi.hoisted(() => ({
  mockCreateWorkspaceModalProps: vi.fn(),
}));

vi.mock("@/components/WorkspaceRepoWizard", () => ({
  WorkspaceRepoWizard: (props: {
    isOpen: boolean;
    onSuccess: (
      data: ReturnType<typeof makeWorkspaceData>,
      createdName: string,
    ) => void;
  }) => {
    mockCreateWorkspaceModalProps(props);
    if (!props.isOpen) return null;
    return (
      <button
        type="button"
        onClick={() =>
          props.onSuccess(
            makeWorkspaceData([
              { id: "ws-old", is_default: false },
              { id: "ws-created", is_default: true },
            ]),
            "ws-created",
          )
        }
      >
        Mock create success
      </button>
    );
  },
}));

// Stub the OnboardingFlow — it has its own dedicated tests and
// requires a fetch mock that would distract from the redirect logic
// under test here. The stub still exposes a button labelled
// "Create Workspace" so existing assertions around that button keep
// reading naturally.
const { mockDispatchWorkspaceRepo } = vi.hoisted(() => ({
  mockDispatchWorkspaceRepo: vi.fn(),
}));
vi.mock("@/components/OnboardingFlow", () => ({
  OnboardingFlow: () => (
    <button
      type="button"
      onClick={() => mockDispatchWorkspaceRepo()}
    >
      Create Workspace
    </button>
  ),
}));

// Capture handler registration so the create-workspace test can
// trigger the registered action without depending on the real
// OnboardingActionsProvider.
const { mockRegisterAction } = vi.hoisted(() => ({
  mockRegisterAction: vi.fn(),
}));
vi.mock("@/contexts/OnboardingActionsContext", () => ({
  useRegisterOnboardingAction: (
    action: string,
    handler: () => void,
  ) => {
    mockRegisterAction(action, handler);
  },
}));

const { mockGetLastWorkspaceId, mockClearLastWorkspaceId } = vi.hoisted(() => ({
  mockGetLastWorkspaceId: vi.fn(() => null as string | null),
  mockClearLastWorkspaceId: vi.fn(),
}));

vi.mock("@/utils/scopedStorage", () => ({
  getLastWorkspaceId: mockGetLastWorkspaceId,
  clearLastWorkspaceId: mockClearLastWorkspaceId,
}));

// ── Helpers ──────────────────────────────────────────────────────────────────

function makeWorkspaceData(workspaces: { id: string; is_default: boolean }[]) {
  return {
    id: workspaces[0]?.id ?? "",
    name: "Test",
    path: "/test",
    repos: [],
    groups: [],
    agents: [],
    workspaces: workspaces.map((ws) => ({
      id: ws.id,
      name: ws.id,
      path: "/test",
      active: true,
      repo_count: 0,
      is_default: ws.is_default,
    })),
    workspace_order: [],
    default_workspace: workspaces.find((ws) => ws.is_default)?.id ?? "",
  };
}

function renderComponent() {
  return render(
    <MemoryRouter>
      <RedirectToWorkspace />
    </MemoryRouter>,
  );
}

// ── Test setup ───────────────────────────────────────────────────────────────

beforeEach(() => {
  vi.clearAllMocks();
  mockLocation.state = null;
  mockGetLastWorkspaceId.mockReturnValue(null);
});

// ── Tests ────────────────────────────────────────────────────────────────────

describe("RedirectToWorkspace", () => {
  describe("happy path — first workspace redirect", () => {
    it("navigates to the first workspace when no lastId is stored", async () => {
      mockFetchWorkspace.mockResolvedValueOnce(
        makeWorkspaceData([
          { id: "ws-first", is_default: false },
          { id: "ws-second", is_default: true },
        ]),
      );

      renderComponent();

      await waitFor(() => {
        expect(mockNavigate).toHaveBeenCalledWith("/ws/ws-first/kanban", {
          replace: true,
        });
      });
    });

    it("navigates to the first workspace when no default is set", async () => {
      mockFetchWorkspace.mockResolvedValueOnce(
        makeWorkspaceData([
          { id: "ws-first", is_default: false },
          { id: "ws-second", is_default: false },
        ]),
      );

      renderComponent();

      await waitFor(() => {
        expect(mockNavigate).toHaveBeenCalledWith("/ws/ws-first/kanban", {
          replace: true,
        });
      });
    });
  });

  describe("stale localStorage — workspace fallback present", () => {
    it("clears stale lastId and navigates to the first workspace", async () => {
      mockGetLastWorkspaceId.mockReturnValue("ws-stale");
      mockFetchWorkspace.mockResolvedValueOnce(
        makeWorkspaceData([{ id: "ws-default", is_default: true }]),
      );

      renderComponent();

      await waitFor(() => {
        expect(mockClearLastWorkspaceId).toHaveBeenCalledWith("ws-stale");
        expect(mockNavigate).toHaveBeenCalledWith("/ws/ws-default/kanban", {
          replace: true,
        });
      });
    });

    it("navigates directly to lastId when it exists in the workspace list", async () => {
      mockGetLastWorkspaceId.mockReturnValue("ws-known");
      mockFetchWorkspace.mockResolvedValueOnce(
        makeWorkspaceData([
          { id: "ws-known", is_default: false },
          { id: "ws-default", is_default: true },
        ]),
      );

      renderComponent();

      await waitFor(() => {
        expect(mockNavigate).toHaveBeenCalledWith("/ws/ws-known/kanban", {
          replace: true,
        });
        expect(mockClearLastWorkspaceId).not.toHaveBeenCalled();
      });
    });
  });

  describe("failedWorkspaceId filtering — redirect-loop fix", () => {
    it("excludes failedWorkspaceId and navigates to the remaining workspace", async () => {
      mockLocation.state = { failedWorkspaceId: "ws-1" };
      mockFetchWorkspace.mockResolvedValueOnce(
        makeWorkspaceData([
          { id: "ws-1", is_default: false },
          { id: "ws-2", is_default: false },
        ]),
      );

      renderComponent();

      await waitFor(() => {
        expect(mockNavigate).toHaveBeenCalledWith("/ws/ws-2/kanban", {
          replace: true,
        });
        // Must NOT navigate to the failed workspace
        expect(mockNavigate).not.toHaveBeenCalledWith(
          expect.stringContaining("ws-1"),
          expect.anything(),
        );
      });
    });

    it("uses the first surviving workspace when failedWorkspaceId is filtered out", async () => {
      mockLocation.state = { failedWorkspaceId: "ws-stale" };
      mockFetchWorkspace.mockResolvedValueOnce(
        makeWorkspaceData([
          { id: "ws-stale", is_default: false },
          { id: "ws-a", is_default: false },
          { id: "ws-default", is_default: true },
        ]),
      );

      renderComponent();

      await waitFor(() => {
        expect(mockNavigate).toHaveBeenCalledWith("/ws/ws-a/kanban", {
          replace: true,
        });
      });
    });
  });

  describe("all workspaces stale after failedWorkspaceId filter", () => {
    it("shows empty state when the only workspace matches failedWorkspaceId", async () => {
      mockLocation.state = { failedWorkspaceId: "ws-only" };
      mockFetchWorkspace.mockResolvedValueOnce(
        makeWorkspaceData([{ id: "ws-only", is_default: true }]),
      );

      renderComponent();

      await waitFor(() => {
        expect(screen.getByText(/No workspaces found/i)).toBeInTheDocument();
      });

      expect(mockNavigate).not.toHaveBeenCalled();
    });

    it("clears localStorage for the stale lastId before showing empty state", async () => {
      mockGetLastWorkspaceId.mockReturnValue("ws-only");
      mockLocation.state = { failedWorkspaceId: "ws-only" };
      mockFetchWorkspace.mockResolvedValueOnce(
        makeWorkspaceData([{ id: "ws-only", is_default: false }]),
      );

      renderComponent();

      await waitFor(() => {
        expect(screen.getByText(/No workspaces found/i)).toBeInTheDocument();
      });

      expect(mockClearLastWorkspaceId).toHaveBeenCalledWith("ws-only");
    });
  });

  describe("API error (catch path)", () => {
    it("shows empty state when fetchWorkspace rejects", async () => {
      mockFetchWorkspace.mockRejectedValueOnce(new Error("Network error"));

      renderComponent();

      await waitFor(() => {
        expect(screen.getByText(/No workspaces found/i)).toBeInTheDocument();
      });
    });

    it("does NOT navigate when fetchWorkspace rejects", async () => {
      mockFetchWorkspace.mockRejectedValueOnce(new Error("Network error"));

      renderComponent();

      await waitFor(() => {
        expect(screen.getByText(/No workspaces found/i)).toBeInTheDocument();
      });

      expect(mockNavigate).not.toHaveBeenCalled();
    });

    it("does NOT clear localStorage when fetchWorkspace rejects", async () => {
      mockGetLastWorkspaceId.mockReturnValue("ws-last");
      mockFetchWorkspace.mockRejectedValueOnce(new Error("Network error"));

      renderComponent();

      await waitFor(() => {
        expect(screen.getByText(/No workspaces found/i)).toBeInTheDocument();
      });

      expect(mockClearLastWorkspaceId).not.toHaveBeenCalled();
    });
  });

  describe("no failedWorkspaceId in state", () => {
    it("uses the full workspace list when location.state is null", async () => {
      mockLocation.state = null;
      mockFetchWorkspace.mockResolvedValueOnce(
        makeWorkspaceData([
          { id: "ws-a", is_default: false },
          { id: "ws-b", is_default: true },
        ]),
      );

      renderComponent();

      await waitFor(() => {
        // All workspaces available, should pick the first returned workspace.
        expect(mockNavigate).toHaveBeenCalledWith("/ws/ws-a/kanban", {
          replace: true,
        });
      });
    });

    it("uses the full workspace list when location.state has no failedWorkspaceId key", async () => {
      mockLocation.state = { someOtherKey: "value" };
      mockFetchWorkspace.mockResolvedValueOnce(
        makeWorkspaceData([{ id: "ws-only", is_default: false }]),
      );

      renderComponent();

      await waitFor(() => {
        expect(mockNavigate).toHaveBeenCalledWith("/ws/ws-only/kanban", {
          replace: true,
        });
      });
    });

    it("shows empty state when workspace list is empty with no state", async () => {
      mockLocation.state = null;
      mockFetchWorkspace.mockResolvedValueOnce({
        id: "",
        name: "",
        path: "",
        repos: [],
        groups: [],
        agents: [],
        workspaces: [],
        workspace_order: [],
        default_workspace: "",
      });

      renderComponent();

      await waitFor(() => {
        expect(screen.getByText(/No workspaces found/i)).toBeInTheDocument();
      });

      expect(mockNavigate).not.toHaveBeenCalled();
    });
  });

  describe("empty state workspace creation", () => {
    it("opens the create modal when the registered onboarding action fires", async () => {
      mockFetchWorkspace.mockResolvedValueOnce(makeWorkspaceData([]));

      renderComponent();

      // Wait for the OnboardingFlow stub to render so the action has
      // been registered.
      await screen.findByRole("button", { name: /create workspace/i });

      // Find the registered handler and invoke it directly — this is
      // the path a real OnboardingFlow CTA would take through the
      // dispatch context.
      const registration = mockRegisterAction.mock.calls.find(
        ([action]) => action === "open_workspace_repo_wizard",
      );
      expect(registration).toBeDefined();
      const handler = registration![1] as () => void;
      handler();

      const successButton = await screen.findByRole("button", {
        name: "Mock create success",
      });
      fireEvent.click(successButton);

      expect(mockNavigate).toHaveBeenCalledWith("/ws/ws-created/", {
        replace: true,
      });
      expect(mockNavigate).not.toHaveBeenCalledWith("/ws/ws-old/", {
        replace: true,
      });
    });
  });

  describe("loading state", () => {
    it("renders nothing while resolving", () => {
      // fetchWorkspace never resolves during this test
      mockFetchWorkspace.mockReturnValue(new Promise(() => {}));

      const { container } = renderComponent();

      // Should render null (nothing visible) while the promise is pending
      expect(container.firstChild).toBeNull();
    });
  });
});
