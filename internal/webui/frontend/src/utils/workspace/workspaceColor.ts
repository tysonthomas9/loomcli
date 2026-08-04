/**
 * Deterministic workspace color generation.
 * Maps workspace names to a curated palette using djb2 hash.
 */

const PALETTE = [
  "#3b82f6", // blue
  "#22c55e", // green
  "#8b5cf6", // purple
  "#f97316", // orange
  "#ec4899", // pink
  "#06b6d4", // cyan
  "#f59e0b", // amber
  "#ef4444", // red
];

/**
 * Hash a string using djb2 algorithm.
 */
function djb2(str: string): number {
  let hash = 5381;
  for (let i = 0; i < str.length; i++) {
    hash = ((hash * 33) | 0) + str.charCodeAt(i);
  }
  return hash;
}

/**
 * Get a deterministic color for a workspace name.
 * Same name always returns the same color from the palette.
 */
export function getWorkspaceColor(name: string): string {
  const index = Math.abs(djb2(name)) % PALETTE.length;
  // Index is always valid since we mod by PALETTE.length
  return PALETTE[index]!;
}
