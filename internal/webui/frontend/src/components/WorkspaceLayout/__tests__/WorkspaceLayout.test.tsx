/**
 * @vitest-environment jsdom
 */

/**
 * Unit tests for WorkspaceLayout component.
 *
 * Covers the redirect-loop fix: 404 errors and invalid workspace data now pass
 * { failedWorkspaceId } in the navigation state so RedirectToWorkspace can
 * exclude the stale ID from its candidate list.
 */

import { render, screen, waitFor } from "@testing-library/react";
import { describe, it, expect, vi, beforeEach } from "vitest";
import "@testing-library/jest-dom";
import { MemoryRouter, Routes, Route } from "react-router-dom";

import { WorkspaceLayout } from "../WorkspaceLayout";

// ── Hoisted mocks ────────────────────────────────────────────────────────────

const { mockNavigate } = vi.hoisted(() => ({
  mockNavigate: vi.fn(),
}));

vi.mock("react-router-dom", async (importOriginal) => {
  const actual = await importOriginal<typeof import("react-router-dom")>();
  return {
    ...actual,
    useNavigate: vi.fn(() => mockNavigate),
  };
});

const { mockFetchWorkspace } = vi.hoisted(() => ({
  mockFetchWorkspace: vi.fn(),
}));

vi.mock("@/api/workspace", () => ({
  fetchWorkspaceApi: mockFetchWorkspace,
}));

const { mockClearLastWorkspaceId } = vi.hoisted(() => ({
  mockClearLastWorkspaceId: vi.fn(),
}));

vi.mock("@/utils/scopedStorage", () => ({
  clearLastWorkspaceId: mockClearLastWorkspaceId,
}));

const { mockUseIssueSessionMap, mockUseRouteView } = vi.hoisted(() => ({
  mockUseIssueSessionMap: vi.fn(() => ({})),
  mockUseRouteView: vi.fn(() => ({
    view: "kanban",
    setView: vi.fn(),
    navigateToView: vi.fn(),
  })),
}));

// Mock @/hooks to avoid rendering WorkspaceProvider / StoreProvider internals
vi.mock("@/hooks", () => ({
  WorkspaceProvider: ({ children }: { children: React.ReactNode }) => (
    <div data-testid="workspace-provider">{children}</div>
  ),
  StoreProvider: ({ children }: { children: React.ReactNode }) => (
    <div data-testid="store-provider">{children}</div>
  ),
  useIssueSessionMap: mockUseIssueSessionMap,
  useRouteView: mockUseRouteView,
  useWorkspaceContext: vi.fn(() => ({ workspace: { id: "ws-1" } })),
}));

// Mock IssueSessionContext to avoid its internal dependencies
vi.mock("@/contexts/IssueSessionContext", () => ({
  IssueSessionProvider: ({ children }: { children: React.ReactNode }) => (
    <div data-testid="issue-session-provider">{children}</div>
  ),
}));

// ── Helpers ──────────────────────────────────────────────────────────────────

function makeWorkspaceData(id: string) {
  return {
    id,
    name: "Test Workspace",
    path: "/test",
    repos: [],
    groups: [],
    agents: [],
    workspaces: [
      {
        id,
        name: "Test",
        path: "/test",
        active: true,
        repo_count: 0,
        is_default: false,
      },
    ],
    workspace_order: [],
    default_workspace: id,
  };
}

/**
 * Render WorkspaceLayout under a MemoryRouter with the :workspaceId param set.
 * Outlet renders a simple sentinel so we can verify children mount.
 */
function renderWithWorkspaceId(workspaceId: string) {
  return render(
    <MemoryRouter initialEntries={[`/ws/${workspaceId}/`]}>
      <Routes>
        <Route path="/ws/:workspaceId/" element={<WorkspaceLayout />}>
          <Route
            index
            element={<div data-testid="outlet-content">Outlet Content</div>}
          />
        </Route>
      </Routes>
    </MemoryRouter>,
  );
}

// ── Test setup ───────────────────────────────────────────────────────────────

beforeEach(() => {
  vi.clearAllMocks();
  mockUseRouteView.mockReturnValue({
    view: "kanban",
    setView: vi.fn(),
    navigateToView: vi.fn(),
  });
  mockUseIssueSessionMap.mockReturnValue({});
});

// ── Tests ────────────────────────────────────────────────────────────────────

describe("WorkspaceLayout", () => {
  describe("404 error — passes failedWorkspaceId in navigation state", () => {
    it("navigates to / with failedWorkspaceId state on 404", async () => {
      mockFetchWorkspace.mockRejectedValueOnce({ status: 404 });

      renderWithWorkspaceId("stale-id");

      await waitFor(() => {
        expect(mockNavigate).toHaveBeenCalledWith("/", {
          replace: true,
          state: { failedWorkspaceId: "stale-id" },
        });
      });
    });

    it("clears localStorage for the stale workspace ID on 404", async () => {
      mockFetchWorkspace.mockRejectedValueOnce({ status: 404 });

      renderWithWorkspaceId("stale-id");

      await waitFor(() => {
        expect(mockClearLastWorkspaceId).toHaveBeenCalledWith("stale-id");
      });
    });

    it("does NOT proceed to render children on 404", async () => {
      mockFetchWorkspace.mockRejectedValueOnce({ status: 404 });

      renderWithWorkspaceId("stale-id");

      await waitFor(() => {
        expect(mockNavigate).toHaveBeenCalled();
      });

      expect(screen.queryByTestId("outlet-content")).not.toBeInTheDocument();
    });
  });

  describe("success path — renders children (Outlet)", () => {
    it("renders children when fetchWorkspace returns valid data with id", async () => {
      mockFetchWorkspace.mockResolvedValueOnce(makeWorkspaceData("ws-1"));

      renderWithWorkspaceId("ws-1");

      await waitFor(() => {
        expect(screen.getByTestId("outlet-content")).toBeInTheDocument();
      });
    });

    it("does NOT navigate away when fetchWorkspace succeeds", async () => {
      mockFetchWorkspace.mockResolvedValueOnce(makeWorkspaceData("ws-1"));

      renderWithWorkspaceId("ws-1");

      await waitFor(() => {
        expect(screen.getByTestId("outlet-content")).toBeInTheDocument();
      });

      expect(mockNavigate).not.toHaveBeenCalled();
    });

    it("does NOT clear localStorage when fetchWorkspace succeeds", async () => {
      mockFetchWorkspace.mockResolvedValueOnce(makeWorkspaceData("ws-1"));

      renderWithWorkspaceId("ws-1");

      await waitFor(() => {
        expect(screen.getByTestId("outlet-content")).toBeInTheDocument();
      });

      expect(mockClearLastWorkspaceId).not.toHaveBeenCalled();
    });
  });

  describe("non-404 error — proceeds to render children", () => {
    it("renders children when fetchWorkspace rejects with a 500 error", async () => {
      mockFetchWorkspace.mockRejectedValueOnce({ status: 500 });

      renderWithWorkspaceId("ws-1");

      await waitFor(() => {
        expect(screen.getByTestId("outlet-content")).toBeInTheDocument();
      });
    });

    it("renders children when fetchWorkspace rejects with a network error (no status)", async () => {
      mockFetchWorkspace.mockRejectedValueOnce(new Error("Network failure"));

      renderWithWorkspaceId("ws-1");

      await waitFor(() => {
        expect(screen.getByTestId("outlet-content")).toBeInTheDocument();
      });
    });

    it("does NOT navigate away on non-404 error", async () => {
      mockFetchWorkspace.mockRejectedValueOnce({ status: 503 });

      renderWithWorkspaceId("ws-1");

      await waitFor(() => {
        expect(screen.getByTestId("outlet-content")).toBeInTheDocument();
      });

      expect(mockNavigate).not.toHaveBeenCalled();
    });
  });

  describe("invalid data — passes failedWorkspaceId in navigation state", () => {
    it("navigates to / with failedWorkspaceId when response has no id field", async () => {
      // fetchWorkspace resolved but data has no id (invalid)
      mockFetchWorkspace.mockResolvedValueOnce({});

      renderWithWorkspaceId("ws-ghost");

      await waitFor(() => {
        expect(mockNavigate).toHaveBeenCalledWith("/", {
          replace: true,
          state: { failedWorkspaceId: "ws-ghost" },
        });
      });
    });

    it("clears localStorage for the workspace when response has no id field", async () => {
      mockFetchWorkspace.mockResolvedValueOnce({});

      renderWithWorkspaceId("ws-ghost");

      await waitFor(() => {
        expect(mockClearLastWorkspaceId).toHaveBeenCalledWith("ws-ghost");
      });
    });

    it("navigates to / with failedWorkspaceId when response is null-ish", async () => {
      mockFetchWorkspace.mockResolvedValueOnce(null);

      renderWithWorkspaceId("ws-null");

      await waitFor(() => {
        expect(mockNavigate).toHaveBeenCalledWith("/", {
          replace: true,
          state: { failedWorkspaceId: "ws-null" },
        });
      });
    });
  });

  describe("StoreProvider in provider tree", () => {
    it("renders StoreProvider inside WorkspaceProvider", async () => {
      mockFetchWorkspace.mockResolvedValueOnce(makeWorkspaceData("ws-1"));
      renderWithWorkspaceId("ws-1");
      await waitFor(() => {
        expect(screen.getByTestId("store-provider")).toBeInTheDocument();
      });
      // StoreProvider should be inside WorkspaceProvider
      const wsProvider = screen.getByTestId("workspace-provider");
      expect(wsProvider).toContainElement(screen.getByTestId("store-provider"));
      // IssueSessionProvider should be inside StoreProvider
      const storeProvider = screen.getByTestId("store-provider");
      expect(storeProvider).toContainElement(
        screen.getByTestId("issue-session-provider"),
      );
    });
  });

  describe("issue session map scope", () => {
    // Regression: previously gated to view === "terminal", which left
    // kanban/table IssueCards with an always-empty map and hidden badges
    // until the user visited Terminal. Hook is now enabled on every route.
    it("fetches issue sessions on the kanban route so cards can show the active-session badge", async () => {
      mockUseRouteView.mockReturnValue({
        view: "kanban",
        setView: vi.fn(),
        navigateToView: vi.fn(),
      });
      mockFetchWorkspace.mockResolvedValueOnce(makeWorkspaceData("ws-1"));

      renderWithWorkspaceId("ws-1");

      await waitFor(() => {
        expect(screen.getByTestId("outlet-content")).toBeInTheDocument();
      });

      expect(mockUseIssueSessionMap).toHaveBeenCalled();
      const firstArg = mockUseIssueSessionMap.mock.calls[0]?.[0];
      // No args, or args without { enabled: false } — fetch must not be gated off.
      expect(firstArg?.enabled).not.toBe(false);
    });

    it("fetches issue sessions on the terminal route", async () => {
      mockUseRouteView.mockReturnValue({
        view: "terminal",
        setView: vi.fn(),
        navigateToView: vi.fn(),
      });
      mockFetchWorkspace.mockResolvedValueOnce(makeWorkspaceData("ws-1"));

      renderWithWorkspaceId("ws-1");

      await waitFor(() => {
        expect(screen.getByTestId("outlet-content")).toBeInTheDocument();
      });

      expect(mockUseIssueSessionMap).toHaveBeenCalled();
      const firstArg = mockUseIssueSessionMap.mock.calls[0]?.[0];
      expect(firstArg?.enabled).not.toBe(false);
    });
  });

  describe("missing workspaceId param", () => {
    it("redirects to / immediately when workspaceId param is missing", async () => {
      render(
        <MemoryRouter initialEntries={["/ws/"]}>
          <Routes>
            {/* Route without :workspaceId param */}
            <Route path="/ws/" element={<WorkspaceLayout />} />
          </Routes>
        </MemoryRouter>,
      );

      await waitFor(() => {
        expect(mockNavigate).toHaveBeenCalledWith("/", { replace: true });
      });
    });
  });
});
