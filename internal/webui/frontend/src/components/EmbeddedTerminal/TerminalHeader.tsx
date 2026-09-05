/**
 * TerminalHeader component.
 * Header bar for embedded terminal tabs showing backend label,
 * connection state and worktree breadcrumb.
 */

import type { ConnectionState } from "@/components/TerminalView";
import styles from "./EmbeddedTerminal.module.css";

const BACKEND_DISPLAY: Record<string, { label: string; color: string }> = {
  claude: { label: "Claude", color: "#D97706" },
  codex: { label: "Codex", color: "#10B981" },
  opencode: { label: "OpenCode", color: "#6366F1" },
};

export interface TerminalHeaderProps {
  backend: string;
  worktreePath?: string | undefined;
  agentName: string | null;
  connectionState: ConnectionState;
  onMaximize?: (() => void) | undefined;
  isMaximized?: boolean | undefined;
}

/**
 * Truncate a path to the last 2-3 segments with ".../" prefix.
 */
function truncatePath(path: string): string {
  const segments = path.replace(/\/+$/, "").split("/").filter(Boolean);
  if (segments.length <= 3) return path;
  return "\u2026/" + segments.slice(-3).join("/");
}

export function TerminalHeader({
  backend,
  worktreePath,
  connectionState,
  onMaximize,
  isMaximized,
}: TerminalHeaderProps): JSX.Element {
  const display = BACKEND_DISPLAY[backend] ?? {
    label: backend,
    color: "#9ca3af",
  };

  return (
    <div className={styles.header} data-testid="terminal-header">
      {/* Left: backend label + connection dot */}
      <div className={styles.backendLabel}>
        <span
          className={styles.brandDot}
          style={{ backgroundColor: display.color }}
          data-testid="brand-dot"
        />
        {display.label}
        <span
          className={styles.connectionDot}
          data-state={connectionState}
          data-testid="connection-dot"
        />
      </div>

      {/* Center: worktree breadcrumb */}
      {worktreePath && (
        <span className={styles.breadcrumb} data-testid="worktree-breadcrumb">
          {truncatePath(worktreePath)}
        </span>
      )}

      {/* Right: maximize button */}
      {onMaximize && (
        <button
          type="button"
          className={styles.actionBtn}
          onClick={onMaximize}
          aria-label={isMaximized ? "Restore terminal" : "Maximize terminal"}
          data-testid="maximize-btn"
          style={{ marginLeft: "auto" }}
        >
          {isMaximized ? (
            <svg
              width="14"
              height="14"
              viewBox="0 0 14 14"
              fill="none"
              stroke="currentColor"
              strokeWidth="1.5"
              strokeLinecap="round"
            >
              <path d="M9 1h4v4M5 13H1V9M13 1L8.5 5.5M1 13l4.5-4.5" />
            </svg>
          ) : (
            <svg
              width="14"
              height="14"
              viewBox="0 0 14 14"
              fill="none"
              stroke="currentColor"
              strokeWidth="1.5"
              strokeLinecap="round"
            >
              <path d="M9 5h4V1M5 9H1v4M13 1L9 5M1 13l4-4" />
            </svg>
          )}
        </button>
      )}
    </div>
  );
}
