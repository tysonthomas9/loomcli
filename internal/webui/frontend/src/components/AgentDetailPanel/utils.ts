/**
 * Helper utilities for AgentDetailPanel.
 */

const AVATAR_COLORS = [
  "#9DC08B",
  "#F59E87",
  "#B6B2DF",
  "#95CBE9",
  "#F5C28E",
  "#E8A5B3",
  "#A5D4C8",
  "#D4A5D8",
];

export function getAvatarColor(name: string): string {
  let hash = 0;
  for (let i = 0; i < name.length; i++) {
    hash = ((hash << 5) - hash + name.charCodeAt(i)) | 0;
  }
  return AVATAR_COLORS[Math.abs(hash) % AVATAR_COLORS.length] ?? "#9DC08B";
}

export function shouldUseWhiteText(hex: string): boolean {
  const r = parseInt(hex.slice(1, 3), 16);
  const g = parseInt(hex.slice(3, 5), 16);
  const b = parseInt(hex.slice(5, 7), 16);
  const brightness = (r * 299 + g * 587 + b * 114) / 1000;
  return brightness < 160;
}

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
