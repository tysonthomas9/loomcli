/**
 * @vitest-environment jsdom
 */

/**
 * Unit tests for ObservabilityDashboard container component.
 */

import { render, screen } from "@testing-library/react";
import { describe, it, expect, vi, beforeEach } from "vitest";
import "@testing-library/jest-dom";

import type { MetricsSnapshot } from "@/types";
import type { UseObservabilityMetricsResult } from "@/hooks/useObservabilityMetrics";

/**
 * Create a complete mock MetricsSnapshot for testing.
 */
function createMetrics(
  overrides: Partial<MetricsSnapshot> = {},
): MetricsSnapshot {
  return {
    timestamp: "2026-03-05T12:00:00Z",
    tasks_completed_last_hour: 8,
    tasks_completed_24h: 120,
    avg_task_duration_sec: 95,
    lines_changed_last_hour: 450,
    error_rate_pct: 3.5,
    restart_count_24h: 2,
    restarts_by_agent: { alpha: 1, beta: 1 },
    agent_utilization: { alpha: 0.8, beta: 0.5 },
    tasks_by_role: { developer: 80, reviewer: 40 },
    tasks_by_epic: { "epic-auth": 50, "epic-ui": 30 },
    tasks_by_agent: { alpha: 70, beta: 50 },
    hourly_completions: [
      {
        hour: "2026-03-05T10:00:00Z",
        completed: 5,
        failed: 1,
        avg_duration: 60,
      },
      {
        hour: "2026-03-05T11:00:00Z",
        completed: 3,
        failed: 0,
        avg_duration: 45,
      },
    ],
    total_tasks_completed: 200,
    total_tasks_failed: 10,
    total_restarts: 5,
    ...overrides,
  };
}

let mockHookResult: UseObservabilityMetricsResult;

vi.mock("@/hooks", () => ({
  useObservabilityMetrics: () => mockHookResult,
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
  LAYER_TERMINAL_SEARCH: 5,
}));

describe("ObservabilityDashboard", () => {
  beforeEach(() => {
    mockHookResult = {
      metrics: createMetrics(),
      isLoading: false,
      error: null,
      isConnected: true,
      lastUpdated: new Date("2026-03-05T12:00:00Z"),
      refetch: vi.fn(),
    };
  });

  // Dynamic import to ensure mock is set up before module loads
  async function renderDashboard(className?: string) {
    const { ObservabilityDashboard } =
      await import("../ObservabilityDashboard");
    return render(<ObservabilityDashboard className={className} />);
  }

  describe("rendering all panels", () => {
    it("renders MetricsCards values", async () => {
      await renderDashboard();

      expect(screen.getByText("Tasks / Hour")).toBeInTheDocument();
      expect(screen.getByText("8")).toBeInTheDocument();
      expect(screen.getByText("Avg Duration")).toBeInTheDocument();
      expect(screen.getByText("Lines / Hour")).toBeInTheDocument();
      expect(screen.getByText("Error Rate")).toBeInTheDocument();
    });

    it("renders Task Timeline section", async () => {
      await renderDashboard();

      expect(screen.getByText("Hourly Completions (24h)")).toBeInTheDocument();
    });

    it("renders Agent Utilization section", async () => {
      await renderDashboard();

      expect(screen.getByLabelText("Agent Utilization")).toBeInTheDocument();
    });

    it("renders Errors & Restarts section", async () => {
      await renderDashboard();

      expect(screen.getByLabelText("Errors & Restarts")).toBeInTheDocument();
    });

    it("renders Epic Progress section", async () => {
      await renderDashboard();

      expect(screen.getByLabelText("Epic Progress")).toBeInTheDocument();
    });
  });

  describe("loading state", () => {
    it("shows LoadingSkeleton.Observability when isLoading=true and no metrics yet", async () => {
      mockHookResult = {
        metrics: null,
        isLoading: true,
        error: null,
        isConnected: false,
        lastUpdated: null,
        refetch: vi.fn(),
      };

      await renderDashboard();

      expect(
        screen.getByTestId("loading-skeleton-observability"),
      ).toBeInTheDocument();
    });

    it("does not show skeleton when metrics exist even if isLoading", async () => {
      mockHookResult = {
        metrics: createMetrics(),
        isLoading: true,
        error: null,
        isConnected: true,
        lastUpdated: new Date(),
        refetch: vi.fn(),
      };

      await renderDashboard();

      expect(
        screen.queryByTestId("loading-skeleton-observability"),
      ).not.toBeInTheDocument();
      expect(screen.getByText("Tasks / Hour")).toBeInTheDocument();
    });
  });

  describe("error state", () => {
    it("shows ErrorDisplay when error and no metrics", async () => {
      mockHookResult = {
        metrics: null,
        isLoading: false,
        error: new Error("Network failure"),
        isConnected: false,
        lastUpdated: null,
        refetch: vi.fn(),
      };

      await renderDashboard();

      expect(screen.getByTestId("error-display")).toBeInTheDocument();
      expect(
        screen.getByRole("button", { name: "Try again" }),
      ).toBeInTheDocument();
    });

    it("shows error details with the error message", async () => {
      mockHookResult = {
        metrics: null,
        isLoading: false,
        error: new Error("Network failure"),
        isConnected: false,
        lastUpdated: null,
        refetch: vi.fn(),
      };

      await renderDashboard();

      expect(screen.getByText("Network failure")).toBeInTheDocument();
    });

    it("does not show error state when metrics exist despite error", async () => {
      mockHookResult = {
        metrics: createMetrics(),
        isLoading: false,
        error: new Error("Stale data"),
        isConnected: false,
        lastUpdated: new Date(),
        refetch: vi.fn(),
      };

      await renderDashboard();

      expect(screen.queryByTestId("error-display")).not.toBeInTheDocument();
      expect(screen.getByText("Tasks / Hour")).toBeInTheDocument();
    });
  });

  describe("503 state", () => {
    it("shows observability-not-configured ErrorDisplay for 503 errors", async () => {
      mockHookResult = {
        metrics: null,
        isLoading: false,
        error: new Error("Observability metrics: 503 Service Unavailable"),
        isConnected: false,
        lastUpdated: null,
        refetch: vi.fn(),
      };

      await renderDashboard();

      expect(screen.getByTestId("error-display")).toBeInTheDocument();
      expect(
        screen.getByRole("heading", { name: /Observability not configured/i }),
      ).toBeInTheDocument();
    });
  });

  describe("stale data indicator", () => {
    it("shows stale data indicator when disconnected but have metrics", async () => {
      mockHookResult = {
        metrics: createMetrics(),
        isLoading: false,
        error: null,
        isConnected: false,
        lastUpdated: new Date("2026-03-05T12:00:00Z"),
        refetch: vi.fn(),
      };

      await renderDashboard();

      expect(screen.getByText(/Data may be stale/)).toBeInTheDocument();
      expect(
        screen.getByRole("button", { name: /retry/i }),
      ).toBeInTheDocument();
    });

    it("does not show stale indicator when connected", async () => {
      mockHookResult = {
        metrics: createMetrics(),
        isLoading: false,
        error: null,
        isConnected: true,
        lastUpdated: new Date(),
        refetch: vi.fn(),
      };

      await renderDashboard();

      expect(screen.queryByText(/Data may be stale/)).not.toBeInTheDocument();
    });
  });

  describe("className", () => {
    it("applies custom className", async () => {
      const { container } = await renderDashboard("my-custom-class");

      expect(container.firstChild).toHaveClass("my-custom-class");
    });
  });
});
