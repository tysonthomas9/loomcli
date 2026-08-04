/**
 * Helper utilities for AgentDetailPanel.
 */

export { getAvatarColor, shouldUseWhiteText } from "@/utils/colorUtils";

export function getStatusDotColor(type: string): string {
  switch (type) {
    case "working":
    case "planning":
    case "dirty":
    case "changes":
      return "var(--color-status-working, #facc15)";
    case "error":
      return "var(--color-status-error, #ef4444)";
    case "done":
      return "var(--color-status-done, #22c55e)";
    case "review":
      return "var(--color-status-review, #3b82f6)";
    case "idle":
    case "ready":
    default:
      return "var(--color-status-idle, #9ca3af)";
  }
}

export function getStatusLabel(type: string): string {
  switch (type) {
    case "working":
      return "Working";
    case "planning":
      return "Planning";
    case "done":
      return "Done";
    case "review":
      return "Awaiting Review";
    case "idle":
      return "Idle";
    case "error":
      return "Error";
    case "dirty":
      return "Uncommitted Changes";
    case "changes":
      return "Has Changes";
    case "ready":
      return "Ready";
    default:
      return "Unknown";
  }
}

export function getPriorityLabel(priority: number): string {
  switch (priority) {
    case 0:
      return "P0 Critical";
    case 1:
      return "P1 High";
    case 2:
      return "P2 Medium";
    case 3:
      return "P3 Low";
    case 4:
      return "P4 Backlog";
    default:
      return `P${priority}`;
  }
}
