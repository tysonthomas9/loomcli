// One stable colour per agent (CL1): the Lead chat's Started marker and
// result cards, the agent tray's avatars and the sidebar's agent rows all
// take an agent's colour from here, so the same agent looks the same
// everywhere and across reloads.

/**
 * The palette's size. Each slot is a CSS variable, --agent-color-<n>, set
 * per theme in styles/variables.css: orange, rose, fuchsia, violet, blue,
 * cyan, green and olive; no yellow or grey, so each stays readable at 55%
 * on the beige light background and on the dark one.
 */
export const AGENT_COLOR_COUNT = 8;

/** The agent's palette slot, from its id (FNV-1a), 0 to AGENT_COLOR_COUNT-1. */
export function agentColorIndex(id: string): number {
  let h = 0x811c9dc5;
  for (let i = 0; i < id.length; i++) {
    h ^= id.charCodeAt(i);
    h = Math.imul(h, 0x01000193);
  }
  return (h >>> 0) % AGENT_COLOR_COUNT;
}

/** The agent's colour as a CSS value, such as var(--agent-color-3). */
export function agentColor(id: string): string {
  return `var(--agent-color-${agentColorIndex(id)})`;
}

/**
 * Two initials for an agent's avatar: the first characters of its name's
 * first and last parts, so ui-test-agent-1 is U1 and Local-Coder is LC; a
 * one-part name gives its first two characters.
 */
export function agentInitials(name: string): string {
  const parts = name
    .trim()
    .split(/[-_.\s/]+/)
    .map((p) => p.replace(/[^a-zA-Z0-9]/g, ""))
    .filter(Boolean);
  if (parts.length === 0) return "?";
  const first = parts[0]!;
  const last = parts[parts.length - 1]!;
  return (
    parts.length > 1 ? first[0]! + last[0]! : first.slice(0, 2)
  ).toUpperCase();
}
