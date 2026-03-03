/**
 * TypeScript types for usage/cost tracking data.
 * Mirrors Go types from internal/cli/serve.go (UsageResponse, UsageAgentSummary, etc.)
 */

/** A single agent session's usage record. */
export interface UsageSession {
  agent_name: string;
  backend: string;
  task_id?: string;
  epic_id?: string;
  input_tokens: number;
  output_tokens: number;
  cache_read_tokens: number;
  cache_write_tokens: number;
  estimated_cost_usd: number;
  started_at: string;
  ended_at: string;
  exit_code: number;
  model?: string;
}

/** Per-agent aggregated usage. */
export interface UsageAgentSummary {
  name: string;
  sessions: number;
  input_tokens: number;
  output_tokens: number;
  total_cost: number;
}

/** Per-backend aggregated usage. */
export interface UsageBackendSummary {
  name: string;
  sessions: number;
  total_cost: number;
}

/** Daily cost entry. */
export interface UsageDailyCost {
  date: string;
  cost: number;
  sessions: number;
}

/** Full usage API response from GET /api/usage. */
export interface UsageResponse {
  total_input_tokens: number;
  total_output_tokens: number;
  total_cache_read_tokens: number;
  total_cache_write_tokens: number;
  total_cost: number;
  session_count: number;
  by_agent: UsageAgentSummary[];
  by_backend: UsageBackendSummary[];
  daily_costs: UsageDailyCost[];
  sessions: UsageSession[];
  timestamp: string;
}

/** Query parameters for the usage endpoint. */
export interface UsageParams {
  agent?: string;
  backend?: string;
  epic?: string;
  since?: string;
  until?: string;
}
