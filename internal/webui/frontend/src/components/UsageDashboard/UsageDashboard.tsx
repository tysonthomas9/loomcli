/**
 * UsageDashboard component.
 * Displays token usage summaries with per-agent bar charts,
 * daily cost table, and session detail.
 * Uses CSS-only visualizations (no chart libraries).
 */

import { useState, useMemo } from "react";

import { useUsage } from "@/hooks";
import type { UsageResponse, UsageParams } from "@/types";
import { formatCost, formatTokens } from "@/utils/sessionUsage";

import styles from "./UsageDashboard.module.css";

/** Props for the UsageDashboard component. */
export interface UsageDashboardProps {
  className?: string;
}

type DateRange = "today" | "week" | "month" | "all";

function formatDate(iso: string): string {
  const d = new Date(iso);
  return d.toLocaleDateString(undefined, {
    month: "short",
    day: "numeric",
    hour: "2-digit",
    minute: "2-digit",
  });
}

function dateRangeToParams(range: DateRange): UsageParams {
  const now = new Date();
  const fmt = (d: Date) => d.toISOString().slice(0, 10);

  switch (range) {
    case "today":
      return { since: fmt(now) };
    case "week": {
      const weekAgo = new Date(now);
      weekAgo.setDate(weekAgo.getDate() - 7);
      return { since: fmt(weekAgo) };
    }
    case "month": {
      const monthAgo = new Date(now);
      monthAgo.setDate(monthAgo.getDate() - 30);
      return { since: fmt(monthAgo) };
    }
    case "all":
      return {};
  }
}

export function UsageDashboard({
  className,
}: UsageDashboardProps): JSX.Element {
  const [dateRange, setDateRange] = useState<DateRange>("week");
  const [agentFilter, setAgentFilter] = useState<string>("");
  const [backendFilter, setBackendFilter] = useState<string>("");

  const params = useMemo<UsageParams>(() => {
    const base = dateRangeToParams(dateRange);
    if (agentFilter) base.agent = agentFilter;
    if (backendFilter) base.backend = backendFilter;
    return base;
  }, [dateRange, agentFilter, backendFilter]);

  const { data, isLoading, error } = useUsage({ params, pollInterval: 30000 });

  // Derive unique agent/backend names for filter dropdowns
  const agentNames = useMemo(() => {
    if (!data) return [];
    return data.by_agent.map((a) => a.name).sort();
  }, [data]);

  const backendNames = useMemo(() => {
    if (!data) return [];
    return data.by_backend.map((b) => b.name).sort();
  }, [data]);

  const rootClassName = className
    ? `${styles.dashboard} ${className}`
    : styles.dashboard;

  if (error && !data) {
    return (
      <div className={rootClassName} data-testid="usage-dashboard">
        <div className={styles.errorState}>
          Failed to load usage data: {error.message}
        </div>
      </div>
    );
  }

  if (isLoading && !data) {
    return (
      <div className={rootClassName} data-testid="usage-dashboard">
        <div className={styles.loading}>Loading usage data...</div>
      </div>
    );
  }

  if (!data || data.session_count === 0) {
    return (
      <div className={rootClassName} data-testid="usage-dashboard">
        <FilterBar
          dateRange={dateRange}
          agentFilter={agentFilter}
          backendFilter={backendFilter}
          agentNames={agentNames}
          backendNames={backendNames}
          onDateRangeChange={setDateRange}
          onAgentFilterChange={setAgentFilter}
          onBackendFilterChange={setBackendFilter}
        />
        <div className={styles.emptyState}>
          No usage data found. Run agents in auto-mode to generate usage data.
        </div>
      </div>
    );
  }

  return (
    <div className={rootClassName} data-testid="usage-dashboard">
      <FilterBar
        dateRange={dateRange}
        agentFilter={agentFilter}
        backendFilter={backendFilter}
        agentNames={agentNames}
        backendNames={backendNames}
        onDateRangeChange={setDateRange}
        onAgentFilterChange={setAgentFilter}
        onBackendFilterChange={setBackendFilter}
      />

      <SummaryCards data={data} />

      <div className={styles.columns}>
        <div className={styles.column}>
          <h3 className={styles.sectionLabel}>Cost by Agent</h3>
          <AgentBarChart agents={data.by_agent} />
        </div>
        <div className={styles.column}>
          <h3 className={styles.sectionLabel}>Daily Costs</h3>
          <DailyCostTable costs={data.daily_costs} />
        </div>
      </div>

      <SessionTable sessions={data.sessions} />
    </div>
  );
}

/* Sub-components */

interface FilterBarProps {
  dateRange: DateRange;
  agentFilter: string;
  backendFilter: string;
  agentNames: string[];
  backendNames: string[];
  onDateRangeChange: (r: DateRange) => void;
  onAgentFilterChange: (a: string) => void;
  onBackendFilterChange: (b: string) => void;
}

function FilterBar({
  dateRange,
  agentFilter,
  backendFilter,
  agentNames,
  backendNames,
  onDateRangeChange,
  onAgentFilterChange,
  onBackendFilterChange,
}: FilterBarProps) {
  return (
    <div className={styles.filterBar}>
      <select
        className={styles.filterSelect}
        value={dateRange}
        onChange={(e) => onDateRangeChange(e.target.value as DateRange)}
        aria-label="Date range"
      >
        <option value="today">Today</option>
        <option value="week">Last 7 days</option>
        <option value="month">Last 30 days</option>
        <option value="all">All time</option>
      </select>
      {agentNames.length > 1 && (
        <select
          className={styles.filterSelect}
          value={agentFilter}
          onChange={(e) => onAgentFilterChange(e.target.value)}
          aria-label="Agent filter"
        >
          <option value="">All agents</option>
          {agentNames.map((name) => (
            <option key={name} value={name}>
              {name}
            </option>
          ))}
        </select>
      )}
      {backendNames.length > 1 && (
        <select
          className={styles.filterSelect}
          value={backendFilter}
          onChange={(e) => onBackendFilterChange(e.target.value)}
          aria-label="Backend filter"
        >
          <option value="">All backends</option>
          {backendNames.map((name) => (
            <option key={name} value={name}>
              {name}
            </option>
          ))}
        </select>
      )}
    </div>
  );
}

function SummaryCards({ data }: { data: UsageResponse }) {
  const avgCost =
    data.session_count > 0 ? data.total_cost / data.session_count : 0;
  return (
    <div className={styles.summaryRow}>
      <div className={styles.summaryCard}>
        <span className={styles.summaryValue}>
          {formatCost(data.total_cost)}
        </span>
        <span className={styles.summaryLabel}>Total Cost</span>
      </div>
      <div className={styles.summaryCard}>
        <span className={styles.summaryValue}>
          {formatTokens(data.total_input_tokens + data.total_output_tokens)}
        </span>
        <span className={styles.summaryLabel}>Total Tokens</span>
      </div>
      <div className={styles.summaryCard}>
        <span className={styles.summaryValue}>{data.session_count}</span>
        <span className={styles.summaryLabel}>Sessions</span>
      </div>
      <div className={styles.summaryCard}>
        <span className={styles.summaryValue}>{formatCost(avgCost)}</span>
        <span className={styles.summaryLabel}>Avg/Session</span>
      </div>
    </div>
  );
}

function AgentBarChart({ agents }: { agents: UsageResponse["by_agent"] }) {
  if (agents.length === 0)
    return <div className={styles.emptyState}>No agent data</div>;
  const maxCost = Math.max(...agents.map((a) => a.total_cost), 0.01);

  return (
    <div className={styles.barChart}>
      {agents.map((agent) => (
        <div key={agent.name} className={styles.barRow}>
          <span className={styles.barLabel} title={agent.name}>
            {agent.name}
          </span>
          <div
            className={styles.barTrack}
            role="progressbar"
            aria-valuenow={agent.total_cost}
            aria-valuemin={0}
            aria-valuemax={maxCost}
          >
            <div
              className={styles.barFill}
              style={{ width: `${(agent.total_cost / maxCost) * 100}%` }}
            />
          </div>
          <span className={styles.barValue}>
            {formatCost(agent.total_cost)}
          </span>
        </div>
      ))}
    </div>
  );
}

function DailyCostTable({ costs }: { costs: UsageResponse["daily_costs"] }) {
  if (costs.length === 0)
    return <div className={styles.emptyState}>No daily data</div>;

  return (
    <table className={styles.dailyTable}>
      <thead>
        <tr>
          <th>Date</th>
          <th>Cost</th>
          <th>Sessions</th>
        </tr>
      </thead>
      <tbody>
        {costs.map((day) => (
          <tr key={day.date}>
            <td>{day.date}</td>
            <td>{formatCost(day.cost)}</td>
            <td>{day.sessions}</td>
          </tr>
        ))}
      </tbody>
    </table>
  );
}

function SessionTable({ sessions }: { sessions: UsageResponse["sessions"] }) {
  // Show most recent 20 sessions
  const recent = useMemo(() => {
    return [...sessions]
      .sort(
        (a, b) =>
          new Date(b.started_at).getTime() - new Date(a.started_at).getTime(),
      )
      .slice(0, 20);
  }, [sessions]);

  if (recent.length === 0) return null;

  return (
    <div>
      <h3 className={styles.sectionLabel}>
        Recent Sessions ({sessions.length} total)
      </h3>
      <table className={styles.sessionTable}>
        <thead>
          <tr>
            <th>Time</th>
            <th>Agent</th>
            <th>Backend</th>
            <th>Task</th>
            <th>Tokens</th>
            <th>Cost</th>
            <th>Exit</th>
          </tr>
        </thead>
        <tbody>
          {recent.map((s) => (
            <tr
              key={`${s.started_at}-${s.agent_name}-${s.backend}-${s.task_id}`}
            >
              <td>{formatDate(s.started_at)}</td>
              <td>{s.agent_name}</td>
              <td>{s.backend}</td>
              <td title={s.task_id}>{s.task_id || "-"}</td>
              <td className={styles.monoCell}>
                {formatTokens(s.input_tokens + s.output_tokens)}
              </td>
              <td className={styles.monoCell}>
                {formatCost(s.estimated_cost_usd)}
              </td>
              <td className={styles.monoCell}>{s.exit_code}</td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}
