/**
 * Observability types matching Go MetricsSnapshot (events.MetricsSnapshot).
 */

export interface HourlyBucket {
  hour: string;
  completed: number;
  failed: number;
  avg_duration: number;
}

export interface MetricsSnapshot {
  timestamp: string;
  tasks_completed_last_hour: number;
  tasks_completed_24h: number;
  avg_task_duration_sec: number;
  lines_changed_last_hour: number;
  error_rate_pct: number;
  restart_count_24h: number;
  restarts_by_agent: Record<string, number>;
  agent_utilization: Record<string, number>;
  tasks_by_role: Record<string, number>;
  tasks_by_epic: Record<string, number>;
  tasks_by_agent: Record<string, number>;
  hourly_completions: HourlyBucket[];
  total_tasks_completed: number;
  total_tasks_failed: number;
  total_restarts: number;
}

export interface ObservabilityMetricsResponse {
  success: boolean;
  data?: MetricsSnapshot;
  error?: string;
}
