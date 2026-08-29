/**
 * AuthorAvatar component.
 * Renders a colored initial-letter avatar that distinguishes humans from agents.
 */

import styles from "./AuthorAvatar.module.css";

const AVATAR_COLORS = [
  "#e06c75",
  "#e5c07b",
  "#98c379",
  "#56b6c2",
  "#61afef",
  "#c678dd",
  "#d19a66",
  "#be5046",
];

const AGENT_PATTERNS = [
  "web-ui",
  "bot",
  "agent",
  "claude",
  "drift",
  "scout",
  "spark",
  "forge",
  "pulse",
  "auto",
];

function hashString(str: string): number {
  let hash = 0;
  for (let i = 0; i < str.length; i++) {
    hash = (hash << 5) - hash + str.charCodeAt(i);
    hash |= 0;
  }
  return Math.abs(hash);
}

function getAvatarColor(name: string): string {
  return (
    AVATAR_COLORS[hashString(name) % AVATAR_COLORS.length] ?? AVATAR_COLORS[0]!
  );
}

export function detectAgent(name: string): boolean {
  const lower = name.toLowerCase();
  return AGENT_PATTERNS.some((p) => lower.includes(p));
}

export interface AuthorAvatarProps {
  name: string;
  isAgent?: boolean;
  size?: "compact" | "standard";
}

export function AuthorAvatar({
  name,
  isAgent,
  size = "standard",
}: AuthorAvatarProps): JSX.Element {
  const isAgentResolved = isAgent ?? detectAgent(name);
  const color = getAvatarColor(name);
  const initial = (name[0] || "?").toUpperCase();
  const sizeClass = size === "compact" ? styles.compact : styles.standard;
  const shapeClass = isAgentResolved ? styles.agent : styles.human;

  return (
    <span
      className={`${styles.avatar} ${sizeClass} ${shapeClass}`}
      style={{ backgroundColor: color }}
      title={name}
      data-testid="author-avatar"
    >
      {initial}
    </span>
  );
}
