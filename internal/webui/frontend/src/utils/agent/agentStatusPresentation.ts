/**
 * Presentation helpers mapping a ParsedLoomStatus to display primitives
 * (status-dot color, status label). Pure, framework-free.
 *
 * Relocated out of components/AgentCard so the components that need them
 * (AgentCard, AgentRow, AgentStatusBadge) import a shared utility rather than
 * reaching into a sibling component.
 */

import type { ParsedLoomStatus } from "@/types";

/**
 * Get status dot color based on parsed status type.
 */
export function getStatusDotColor(type: ParsedLoomStatus["type"]): string {
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
    case "unknown":
    default:
      return "var(--color-status-idle, #9ca3af)";
  }
}

/**
 * Build the status label text for the right-hand meta column.
 */
export function getStatusLabel(parsed: ParsedLoomStatus): string {
  switch (parsed.type) {
    case "working":
      return "Working";
    case "planning":
      return "Planning";
    case "done":
      return "Done";
    case "review":
      return "Review";
    case "idle":
      return "Idle";
    case "error":
      return "Error";
    case "dirty":
      return "Uncommitted changes";
    case "changes":
      return `${parsed.changeCount ?? 0} change${parsed.changeCount === 1 ? "" : "s"}`;
    case "ready":
      return "Ready";
    case "unknown":
    default:
      return "Unknown";
  }
}
