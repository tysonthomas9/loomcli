/**
 * Shared color utilities for agent avatars and repo badges.
 */

/**
 * Pastel color palette for agent avatars and repo badges.
 */
export const AVATAR_COLORS = [
  "#9DC08B", // sage green
  "#F59E87", // peach
  "#B6B2DF", // lavender
  "#95CBE9", // sky blue
  "#F5C28E", // apricot
  "#E8A5B3", // rose
  "#A5D4C8", // mint
  "#D4A5D8", // orchid
];

/**
 * Get a deterministic avatar background color from a name string.
 */
export function getAvatarColor(name: string): string {
  let hash = 0;
  for (let i = 0; i < name.length; i++) {
    hash = ((hash << 5) - hash + name.charCodeAt(i)) | 0;
  }
  return AVATAR_COLORS[Math.abs(hash) % AVATAR_COLORS.length] ?? "#9DC08B";
}

/**
 * Determine if white text has sufficient contrast on the given background.
 * Uses perceived brightness (ITU-R BT.601); returns true if bg is dark enough for white text.
 */
export function shouldUseWhiteText(hex: string): boolean {
  const r = parseInt(hex.slice(1, 3), 16);
  const g = parseInt(hex.slice(3, 5), 16);
  const b = parseInt(hex.slice(5, 7), 16);
  const brightness = (r * 299 + g * 587 + b * 114) / 1000;
  return brightness < 160;
}
