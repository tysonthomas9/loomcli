/**
 * AgentRow - Compact agent info row for IssueCard.
 * Shows mini avatar with status dot, agent name, and activity text.
 *
 * A TOTAL FUNCTION of a single CardAgentView: the kind decides everything, via
 * an exhaustive switch. No `.find`, no boolean-precedence soup — the resolution
 * and precedence live in cardAgentView.ts; this component only renders.
 *
 * Activity slot by kind:
 *   - claimed → "<label>" / "<label> · active Ns ago" / "awaiting activity"
 *   - missing → red "agent missing", enriched with the failure reason when known
 *     ("agent missing · launch failed"); raw class is the hover title
 *   - review  → "Submitted for review"
 *   - none    → renders nothing
 *
 * The relative-time label self-refreshes every 10s (claimed only) so a stuck
 * card reads "12s ago", "22s ago", "32s ago…" without a parent re-render.
 */

import { memo, useEffect, useState } from "react";

import { getAvatarColor } from "@/utils/colorUtils";
import { getStatusDotColor, getStatusLabel } from "@/utils/agent";

import type { CardAgentView } from "./cardAgentView";
import styles from "./AgentRow.module.css";

/**
 * Props for the AgentRow component.
 */
export interface AgentRowProps {
  /** The resolved, semantic agent state for this card. */
  view: CardAgentView;
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

/** What fills the activity slot, plus its visual state and hover title. */
interface ActivitySlot {
  text: string | undefined;
  state: "missing" | "neutral" | undefined;
  title: string | undefined;
}

function assertNever(x: never): never {
  throw new Error(`Unhandled CardAgentView kind: ${JSON.stringify(x)}`);
}

/** Compute the activity slot for a renderable view kind. */
function computeSlot(
  view: Exclude<CardAgentView, { kind: "none" }>,
  now: () => number,
): ActivitySlot {
  switch (view.kind) {
    case "review":
      return { text: "Submitted for review", state: "neutral", title: undefined };
    case "missing": {
      const label = errorClassLabel(view.errorClass);
      return {
        text: label ? `agent missing · ${label}` : "agent missing",
        state: "missing",
        title: view.errorClass,
      };
    }
    case "claimed": {
      const label = getStatusLabel(view.status);
      const parsedAt = view.lastActivityAt ? Date.parse(view.lastActivityAt) : NaN;
      const ago = Number.isFinite(parsedAt)
        ? formatRelativeAgo(parsedAt, now())
        : undefined;
      if (label && ago) {
        return { text: `${label} · ${ago}`, state: "neutral", title: undefined };
      }
      if (label) {
        return { text: label, state: "neutral", title: undefined };
      }
      if (ago) {
        return { text: ago, state: "neutral", title: undefined };
      }
      if (view.lastActivityAt === null) {
        return { text: "awaiting activity", state: "neutral", title: undefined };
      }
      return { text: undefined, state: undefined, title: undefined };
    }
    default:
      return assertNever(view);
  }
}

/**
 * AgentRow displays a compact agent info row on an IssueCard. Total function of
 * the view-model: returns null for `kind: "none"`.
 */
export const AgentRow = memo(function AgentRow({
  view,
  now = () => Date.now(),
}: AgentRowProps): JSX.Element | null {
  // Self-refresh: bump local state every RELATIVE_TIME_TICK_MS so the
  // "Xs ago" label updates without a parent re-render. Only the claimed kind
  // carries a live timestamp. Mirrors the ConnectionBanner.tsx ticker pattern.
  const lastActivityAt = view.kind === "claimed" ? view.lastActivityAt : null;
  const [, setTick] = useState(0);
  useEffect(() => {
    if (!lastActivityAt) return undefined;
    const id = setInterval(() => setTick((t) => t + 1), RELATIVE_TIME_TICK_MS);
    return () => clearInterval(id);
  }, [lastActivityAt]);

  if (view.kind === "none") return null;

  // Strip [H] prefix for human assignees.
  const displayName = view.displayName.replace(/^\[H\]\s*/, "");
  const initial = displayName.charAt(0).toUpperCase() || "?";
  const avatarColor = getAvatarColor(displayName);
  const dotColor =
    view.kind === "claimed" ? getStatusDotColor(view.status.type) : undefined;

  const slot = computeSlot(view, now);

  return (
    <div className={styles.agentRow}>
      <div className={styles.avatarContainer}>
        <div className={styles.avatar} style={{ backgroundColor: avatarColor }}>
          {initial}
        </div>
        {dotColor && (
          <span
            className={styles.statusDot}
            style={{ backgroundColor: dotColor }}
            aria-hidden="true"
          />
        )}
      </div>
      <span className={styles.name}>{displayName}</span>
      {slot.text && (
        <span
          className={styles.activity}
          data-state={slot.state}
          title={slot.title ?? slot.text}
        >
          {slot.text}
        </span>
      )}
    </div>
  );
});
