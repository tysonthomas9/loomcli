/**
 * AgentRow - Compact agent info row for IssueCard.
 * Shows mini avatar with status dot, agent name, and activity text.
 *
 * Activity slot resolution order (first non-null wins):
 *   1. agentMissing → red "agent missing", enriched with the agent's last
 *      failure reason when known ("agent missing · launch failed")
 *   2. lastErrorClass alone (idle agent whose last run failed) → red "<reason>"
 *   3. activity prop with lastActivityAt → "<activity> · active Ns ago"
 *   4. activity prop alone → unchanged (existing behavior)
 *   5. lastActivityAt alone → "active Ns ago" / "last seen Xm ago"
 *   6. agent present but no activity yet → "awaiting activity"
 *   7. nothing → no activity text rendered
 *
 * The relative-time label self-refreshes every 10s so a stuck card
 * reads "12s ago", "22s ago", "32s ago…" without a parent re-render.
 */

import { useEffect, useState } from "react";

import type { ParsedLoomStatus } from "@/types";

import styles from "./AgentRow.module.css";

/**
 * Props for the AgentRow component.
 */
export interface AgentRowProps {
  /** Agent display name */
  agentName: string;
  /** Parsed loom status (null if agent not found in loom) */
  status: ParsedLoomStatus | null;
  /** Avatar background color */
  avatarColor: string;
  /** Status dot color (only shown when status is available) */
  dotColor?: string | undefined;
  /** Activity text (e.g., "Working: loomcli-123") */
  activity?: string | undefined;
  /**
   * ISO-8601 timestamp of the most recent PTY-output observation forwarded
   * over IPC. Used to render relative-time ("active 3s ago"). Null/undefined
   * when the agent hasn't reported yet or isn't daemon-supervised.
   */
  lastActivityAt?: string | null | undefined;
  /**
   * True when the rendered task believes it has an assignee but no live
   * agent is currently claiming it — i.e., orphaned in_progress. Renders
   * a red "agent missing" label in the activity slot.
   */
  agentMissing?: boolean | undefined;
  /**
   * Fleet-db's derived error_class of the agent's most recent terminal run when
   * that run failed (idle agents only; a later success clears it). Used to
   * explain a stalled agent in the red activity slot — on its own, or appended
   * to "agent missing". Undefined when the last run was fine or not computed.
   */
  lastErrorClass?: string | undefined;
  /**
   * Test seam: clock used for relative-time formatting. Defaults to
   * `() => Date.now()`. Providing a frozen clock from tests removes the
   * need to advance real wall time.
   */
  now?: () => number;
}

/** Re-render the relative-time label this often. */
const RELATIVE_TIME_TICK_MS = 10_000;

/**
 * Human labels for the agent error_class values fleet-db derives (which mirror
 * loom's agenterr.ErrorClass). Anything unmapped falls back to "run failed" so
 * a new class still reads as an error rather than vanishing. The raw class is
 * kept as the badge's hover title for precision.
 */
const ERROR_CLASS_LABELS: Record<string, string> = {
  SpawnFailure: "launch failed",
  BackendUnavailable: "backend unavailable",
  RateLimited: "rate limited",
  AuthFailure: "auth failed",
  BillingError: "billing error",
  Timeout: "timed out",
  ContextOverflow: "context overflow",
  ModelNotFound: "model not found",
  LockConflict: "lock conflict",
};

/** Map an error_class to a short badge label, or undefined when absent. */
function errorClassLabel(cls: string | undefined): string | undefined {
  if (!cls) return undefined;
  return ERROR_CLASS_LABELS[cls] ?? "run failed";
}

/**
 * Format a relative-time label suitable for the activity slot.
 * Examples: "active 3s ago", "active 4m ago", "last seen 2h ago",
 * "last seen 3d ago". Switches the verb from "active" to "last seen"
 * past ~5 minutes — that's where the timestamp stops feeling live.
 */
function formatRelativeAgo(fromMs: number, nowMs: number): string {
  const diffMs = Math.max(0, nowMs - fromMs);
  const diffSec = Math.floor(diffMs / 1000);
  if (diffSec < 60) {
    return `active ${diffSec}s ago`;
  }
  const diffMin = Math.floor(diffSec / 60);
  if (diffMin < 5) {
    return `active ${diffMin}m ago`;
  }
  if (diffMin < 60) {
    return `last seen ${diffMin}m ago`;
  }
  const diffHour = Math.floor(diffMin / 60);
  if (diffHour < 24) {
    return `last seen ${diffHour}h ago`;
  }
  const diffDay = Math.floor(diffHour / 24);
  return `last seen ${diffDay}d ago`;
}

/**
 * AgentRow displays a compact agent info row on an IssueCard.
 */
export function AgentRow({
  agentName,
  status,
  avatarColor,
  dotColor,
  activity,
  lastActivityAt,
  agentMissing,
  lastErrorClass,
  now = () => Date.now(),
}: AgentRowProps): JSX.Element {
  // Strip [H] prefix for human assignees
  const displayName = agentName.replace(/^\[H\]\s*/, "");
  const initial = displayName.charAt(0).toUpperCase() || "?";

  // Self-refresh: bump local state every RELATIVE_TIME_TICK_MS so the
  // "Xs ago" label updates without a parent re-render. Mirrors the
  // ConnectionBanner.tsx ticker pattern.
  const [, setTick] = useState(0);
  useEffect(() => {
    if (!lastActivityAt) return undefined;
    const id = setInterval(() => setTick((t) => t + 1), RELATIVE_TIME_TICK_MS);
    return () => clearInterval(id);
  }, [lastActivityAt]);

  const errorLabel = errorClassLabel(lastErrorClass);

  let activityState: "missing" | "neutral" | undefined;
  let activityText: string | undefined;
  // Full error_class shown on hover when an error label is rendered; falls back
  // to the visible text otherwise.
  let activityTitle: string | undefined;

  if (agentMissing) {
    activityState = "missing";
    activityText = errorLabel
      ? `agent missing · ${errorLabel}`
      : "agent missing";
    activityTitle = lastErrorClass;
  } else if (errorLabel) {
    // Idle agent whose most recent run failed and is not on a live session.
    activityState = "missing";
    activityText = errorLabel;
    activityTitle = lastErrorClass;
  } else {
    const parsedAt = lastActivityAt ? Date.parse(lastActivityAt) : NaN;
    const ago = Number.isFinite(parsedAt)
      ? formatRelativeAgo(parsedAt, now())
      : undefined;
    if (activity && ago) {
      activityText = `${activity} · ${ago}`;
      activityState = "neutral";
    } else if (activity) {
      activityText = activity;
      activityState = "neutral";
    } else if (ago) {
      activityText = ago;
      activityState = "neutral";
    } else if (lastActivityAt === null) {
      // Agent is supervised but no PTY output observed yet.
      activityText = "awaiting activity";
      activityState = "neutral";
    }
  }

  return (
    <div className={styles.agentRow}>
      <div className={styles.avatarContainer}>
        <div className={styles.avatar} style={{ backgroundColor: avatarColor }}>
          {initial}
        </div>
        {status && dotColor && !agentMissing && (
          <span
            className={styles.statusDot}
            style={{ backgroundColor: dotColor }}
            aria-hidden="true"
          />
        )}
      </div>
      <span className={styles.name}>{displayName}</span>
      {activityText && (
        <span
          className={styles.activity}
          data-state={activityState}
          title={activityTitle ?? activityText}
        >
          {activityText}
        </span>
      )}
    </div>
  );
}
