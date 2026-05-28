/**
 * AgentRow - Compact agent info row for IssueCard.
 * Shows mini avatar with status dot, agent name, and activity text.
 *
 * Activity slot resolution order (first non-null wins):
 *   1. agentMissing → red "agent missing"
 *   2. activity prop with lastActivityAt → "<activity> · active Ns ago"
 *   3. activity prop alone → unchanged (existing behavior)
 *   4. lastActivityAt alone → "active Ns ago" / "last seen Xm ago"
 *   5. agent present but no activity yet → "awaiting activity"
 *   6. nothing → no activity text rendered
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
   * Test seam: clock used for relative-time formatting. Defaults to
   * `() => Date.now()`. Providing a frozen clock from tests removes the
   * need to advance real wall time.
   */
  now?: () => number;
}

/** Re-render the relative-time label this often. */
const RELATIVE_TIME_TICK_MS = 10_000;

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

  let activityState: "missing" | "neutral" | undefined;
  let activityText: string | undefined;

  if (agentMissing) {
    activityState = "missing";
    activityText = "agent missing";
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
          title={activityText}
        >
          {activityText}
        </span>
      )}
    </div>
  );
}
