/**
 * @vitest-environment jsdom
 */
import "@testing-library/jest-dom";
import { render, screen, fireEvent } from "@testing-library/react";
import { describe, expect, it, vi, beforeEach } from "vitest";

import type { UsageResponse } from "@/types";

import { UsageDashboard } from "../UsageDashboard";

// ---- Mocks ----

vi.mock("@/hooks", () => ({
  useUsage: vi.fn(),
}));

import { useUsage } from "@/hooks";

const mockUseUsage = vi.mocked(useUsage);

// ---- Helpers ----

function makeUsageData(overrides: Partial<UsageResponse> = {}): UsageResponse {
  return {
    total_input_tokens: 50000,
    total_output_tokens: 30000,
    total_cache_read_tokens: 1000,
    total_cache_write_tokens: 500,
    total_cost: 12.5,
    session_count: 5,
    by_agent: [
      {
        name: "alpha",
        sessions: 3,
        input_tokens: 30000,
        output_tokens: 20000,
        total_cost: 8.0,
      },
      {
        name: "beta",
        sessions: 2,
        input_tokens: 20000,
        output_tokens: 10000,
        total_cost: 4.5,
      },
    ],
    by_backend: [
      { name: "anthropic", sessions: 4, total_cost: 10.0 },
      { name: "openai", sessions: 1, total_cost: 2.5 },
    ],
    daily_costs: [
      { date: "2026-03-20", cost: 7.0, sessions: 3 },
      { date: "2026-03-21", cost: 5.5, sessions: 2 },
    ],
    sessions: [
      {
        agent_name: "alpha",
        backend: "anthropic",
        task_id: "task-1",
        input_tokens: 10000,
        output_tokens: 5000,
        cache_read_tokens: 0,
        cache_write_tokens: 0,
        estimated_cost_usd: 3.0,
        started_at: "2026-03-21T10:00:00Z",
        ended_at: "2026-03-21T10:30:00Z",
        exit_code: 0,
      },
    ],
    timestamp: "2026-03-21T12:00:00Z",
    ...overrides,
  };
}

// ---- Tests ----

describe("UsageDashboard", () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  it("renders loading state when isLoading is true and no data", () => {
    mockUseUsage.mockReturnValue({
      data: null,
      isLoading: true,
      isConnected: true,
      error: null,
    } as ReturnType<typeof useUsage>);

    render(<UsageDashboard />);
    expect(screen.getByText("Loading usage data...")).toBeInTheDocument();
    expect(screen.getByTestId("usage-dashboard")).toBeInTheDocument();
  });

  it("renders error state when error is set and no data", () => {
    mockUseUsage.mockReturnValue({
      data: null,
      isLoading: false,
      isConnected: false,
      error: new Error("Connection refused"),
    } as ReturnType<typeof useUsage>);

    render(<UsageDashboard />);
    expect(
      screen.getByText(/Failed to load usage data.*Connection refused/),
    ).toBeInTheDocument();
  });

  it("renders empty state when data has session_count 0", () => {
    mockUseUsage.mockReturnValue({
      data: makeUsageData({ session_count: 0 }),
      isLoading: false,
      isConnected: true,
      error: null,
    } as ReturnType<typeof useUsage>);

    render(<UsageDashboard />);
    expect(screen.getByText(/No usage data found/)).toBeInTheDocument();
  });

  it("renders empty state when data is null", () => {
    mockUseUsage.mockReturnValue({
      data: null,
      isLoading: false,
      isConnected: true,
      error: null,
    } as ReturnType<typeof useUsage>);

    render(<UsageDashboard />);
    expect(screen.getByText(/No usage data found/)).toBeInTheDocument();
  });

  it("renders summary cards with correct values", () => {
    mockUseUsage.mockReturnValue({
      data: makeUsageData(),
      isLoading: false,
      isConnected: true,
      error: null,
    } as ReturnType<typeof useUsage>);

    render(<UsageDashboard />);

    // Total cost
    expect(screen.getByText("$12.50")).toBeInTheDocument();
    // Total tokens: 50000 + 30000 = 80000 => 80.0K (shared formatTokens)
    expect(screen.getByText("80.0K")).toBeInTheDocument();
    // Session count
    expect(screen.getByText("5")).toBeInTheDocument();
    // Avg cost: 12.5 / 5 = 2.50
    expect(screen.getByText("$2.50")).toBeInTheDocument();
  });

  it("renders summary card labels", () => {
    mockUseUsage.mockReturnValue({
      data: makeUsageData(),
      isLoading: false,
      isConnected: true,
      error: null,
    } as ReturnType<typeof useUsage>);

    render(<UsageDashboard />);
    expect(screen.getByText("Total Cost")).toBeInTheDocument();
    expect(screen.getByText("Total Tokens")).toBeInTheDocument();
    // "Sessions" appears in both summary card and daily cost table header,
    // so use getAllByText to verify at least one is present
    expect(screen.getAllByText("Sessions").length).toBeGreaterThanOrEqual(1);
    expect(screen.getByText("Avg/Session")).toBeInTheDocument();
  });

  it("renders agent bar chart with agent names and costs", () => {
    mockUseUsage.mockReturnValue({
      data: makeUsageData(),
      isLoading: false,
      isConnected: true,
      error: null,
    } as ReturnType<typeof useUsage>);

    render(<UsageDashboard />);
    expect(screen.getByText("Cost by Agent")).toBeInTheDocument();
    // Agent names appear in multiple places (filter dropdown, bar chart, session table)
    // so use getAllByText to verify they are present
    expect(screen.getAllByText("alpha").length).toBeGreaterThanOrEqual(1);
    expect(screen.getAllByText("beta").length).toBeGreaterThanOrEqual(1);
    expect(screen.getByText("$8.00")).toBeInTheDocument();
    expect(screen.getByText("$4.50")).toBeInTheDocument();
  });

  it("renders daily cost table with dates and costs", () => {
    mockUseUsage.mockReturnValue({
      data: makeUsageData(),
      isLoading: false,
      isConnected: true,
      error: null,
    } as ReturnType<typeof useUsage>);

    render(<UsageDashboard />);
    expect(screen.getByText("Daily Costs")).toBeInTheDocument();
    expect(screen.getByText("2026-03-20")).toBeInTheDocument();
    expect(screen.getByText("$7.00")).toBeInTheDocument();
    expect(screen.getByText("2026-03-21")).toBeInTheDocument();
    expect(screen.getByText("$5.50")).toBeInTheDocument();
  });

  it("renders session table with session data", () => {
    mockUseUsage.mockReturnValue({
      data: makeUsageData(),
      isLoading: false,
      isConnected: true,
      error: null,
    } as ReturnType<typeof useUsage>);

    render(<UsageDashboard />);
    expect(screen.getByText(/Recent Sessions/)).toBeInTheDocument();
    expect(screen.getByText("task-1")).toBeInTheDocument();
    expect(screen.getByText("$3.00")).toBeInTheDocument();
  });

  it("renders date range selector with default value 'week'", () => {
    mockUseUsage.mockReturnValue({
      data: makeUsageData(),
      isLoading: false,
      isConnected: true,
      error: null,
    } as ReturnType<typeof useUsage>);

    render(<UsageDashboard />);
    const select = screen.getByLabelText("Date range") as HTMLSelectElement;
    expect(select.value).toBe("week");
  });

  it("changes date range when a different option is selected", () => {
    mockUseUsage.mockReturnValue({
      data: makeUsageData(),
      isLoading: false,
      isConnected: true,
      error: null,
    } as ReturnType<typeof useUsage>);

    render(<UsageDashboard />);
    const select = screen.getByLabelText("Date range") as HTMLSelectElement;
    fireEvent.change(select, { target: { value: "today" } });
    expect(select.value).toBe("today");
  });

  it("shows agent filter when there are multiple agents", () => {
    mockUseUsage.mockReturnValue({
      data: makeUsageData(),
      isLoading: false,
      isConnected: true,
      error: null,
    } as ReturnType<typeof useUsage>);

    render(<UsageDashboard />);
    expect(screen.getByLabelText("Agent filter")).toBeInTheDocument();
  });

  it("hides agent filter when there is only one agent", () => {
    mockUseUsage.mockReturnValue({
      data: makeUsageData({
        by_agent: [
          {
            name: "solo",
            sessions: 5,
            input_tokens: 50000,
            output_tokens: 30000,
            total_cost: 12.5,
          },
        ],
      }),
      isLoading: false,
      isConnected: true,
      error: null,
    } as ReturnType<typeof useUsage>);

    render(<UsageDashboard />);
    expect(screen.queryByLabelText("Agent filter")).not.toBeInTheDocument();
  });

  it("shows backend filter when there are multiple backends", () => {
    mockUseUsage.mockReturnValue({
      data: makeUsageData(),
      isLoading: false,
      isConnected: true,
      error: null,
    } as ReturnType<typeof useUsage>);

    render(<UsageDashboard />);
    expect(screen.getByLabelText("Backend filter")).toBeInTheDocument();
  });

  it("hides backend filter when there is only one backend", () => {
    mockUseUsage.mockReturnValue({
      data: makeUsageData({
        by_backend: [{ name: "anthropic", sessions: 5, total_cost: 12.5 }],
      }),
      isLoading: false,
      isConnected: true,
      error: null,
    } as ReturnType<typeof useUsage>);

    render(<UsageDashboard />);
    expect(screen.queryByLabelText("Backend filter")).not.toBeInTheDocument();
  });

  it("applies custom className", () => {
    mockUseUsage.mockReturnValue({
      data: makeUsageData(),
      isLoading: false,
      isConnected: true,
      error: null,
    } as ReturnType<typeof useUsage>);

    render(<UsageDashboard className="custom-class" />);
    const dashboard = screen.getByTestId("usage-dashboard");
    expect(dashboard.className).toContain("custom-class");
  });

  it("renders 'No agent data' when by_agent is empty", () => {
    mockUseUsage.mockReturnValue({
      data: makeUsageData({ by_agent: [] }),
      isLoading: false,
      isConnected: true,
      error: null,
    } as ReturnType<typeof useUsage>);

    render(<UsageDashboard />);
    expect(screen.getByText("No agent data")).toBeInTheDocument();
  });

  it("renders 'No daily data' when daily_costs is empty", () => {
    mockUseUsage.mockReturnValue({
      data: makeUsageData({ daily_costs: [] }),
      isLoading: false,
      isConnected: true,
      error: null,
    } as ReturnType<typeof useUsage>);

    render(<UsageDashboard />);
    expect(screen.getByText("No daily data")).toBeInTheDocument();
  });

  it("shows dash for session without task_id", () => {
    mockUseUsage.mockReturnValue({
      data: makeUsageData({
        sessions: [
          {
            agent_name: "alpha",
            backend: "anthropic",
            task_id: "",
            input_tokens: 1000,
            output_tokens: 500,
            cache_read_tokens: 0,
            cache_write_tokens: 0,
            estimated_cost_usd: 0.5,
            started_at: "2026-03-21T10:00:00Z",
            ended_at: "2026-03-21T10:10:00Z",
            exit_code: 0,
          },
        ],
      }),
      isLoading: false,
      isConnected: true,
      error: null,
    } as ReturnType<typeof useUsage>);

    render(<UsageDashboard />);
    expect(screen.getByText("-")).toBeInTheDocument();
  });

  it("formats large token counts with M suffix", () => {
    mockUseUsage.mockReturnValue({
      data: makeUsageData({
        total_input_tokens: 1_500_000,
        total_output_tokens: 500_000,
      }),
      isLoading: false,
      isConnected: true,
      error: null,
    } as ReturnType<typeof useUsage>);

    render(<UsageDashboard />);
    expect(screen.getByText("2.0M")).toBeInTheDocument();
  });

  it("renders progressbar roles for agent bar chart", () => {
    mockUseUsage.mockReturnValue({
      data: makeUsageData(),
      isLoading: false,
      isConnected: true,
      error: null,
    } as ReturnType<typeof useUsage>);

    render(<UsageDashboard />);
    const bars = screen.getAllByRole("progressbar");
    expect(bars).toHaveLength(2);
  });
});
