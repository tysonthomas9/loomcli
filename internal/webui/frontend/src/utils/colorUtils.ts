/**
 * Shared color utilities for agent avatars and repo badges.
 */

/**
 * Saturated color palette for agent avatars and repo badges (Aether design).
 * All shades are dark enough that shouldUseWhiteText() returns true → white text.
 */
export const AVATAR_COLORS = [
  "#7c3aed", // violet-600
  "#ea580c", // orange-600
  "#0d9488", // teal-600
  "#2563eb", // blue-600
  "#db2777", // pink-600
  "#d97706", // amber-600
  "#0891b2", // cyan-600
  "#16a34a", // green-600
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
