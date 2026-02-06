/**
 * API functions for log streaming endpoints.
 */

/**
 * Response from GET /api/tasks/{id}/logs
 */
interface LogPhaseResponse {
  success: boolean;
  data: { phases: string[] };
}

/**
 * Fetch available log phases for a task.
 * @param taskId The task ID (e.g., "beads-abc123")
 * @returns Array of available phases (e.g., ["planning", "implementation"])
 */
export async function getTaskLogPhases(taskId: string): Promise<string[]> {
  const response = await fetch(`/api/tasks/${encodeURIComponent(taskId)}/logs`);

  if (!response.ok) {
    if (response.status === 404) {
      // No logs yet for this task
      return [];
    }
    throw new Error('Failed to fetch log phases');
  }

  const data: LogPhaseResponse = await response.json();
  return data.data.phases;
}

/**
 * Get the SSE URL for agent log streaming.
 * @param agentName The agent name (e.g., "spark")
 * @returns The SSE endpoint URL
 */
export function getAgentLogStreamUrl(agentName: string): string {
  return `/api/agents/${encodeURIComponent(agentName)}/logs/stream`;
}

/**
 * Get the SSE URL for task log streaming.
 * @param taskId The task ID (e.g., "beads-abc123")
 * @param phase The log phase ("planning" or "implementation")
 * @returns The SSE endpoint URL
 */
export function getTaskLogStreamUrl(taskId: string, phase: string): string {
  return `/api/tasks/${encodeURIComponent(taskId)}/logs/${encodeURIComponent(phase)}/stream`;
}
