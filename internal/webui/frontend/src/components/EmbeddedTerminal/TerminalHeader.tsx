/**
 * TerminalHeader component.
 * Header bar for embedded terminal tabs showing backend label,
 * connection state, worktree breadcrumb, and git action buttons.
 */

import type { ConnectionState } from "@/components/TerminalView";
import type { UseGitActionsReturn } from "@/hooks/useGitActions";

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
  gitActions?: UseGitActionsReturn | undefined;
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
  agentName,
  connectionState,
  gitActions,
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

      {/* Right: git actions (only when agentName is non-null) */}
      {agentName !== null && gitActions && (
        <div className={styles.actions} data-testid="git-actions">
          <button
            type="button"
            className={styles.actionBtn}
            disabled={gitActions.anyLoading}
            onClick={() => gitActions.sync()}
            data-testid="action-review-changes"
          >
            Review Changes
          </button>
          <button
            type="button"
            className={styles.actionBtn}
            disabled={gitActions.anyLoading}
            onClick={() => gitActions.push()}
            data-testid="action-merge"
          >
            Merge
          </button>
        </div>
      )}
    </div>
  );
}
