/**
 * Display helpers for timestamped GitHub PR readiness evidence.
 *
 * Contract (STACKED-PRS-18 / OpenAPI):
 * - `snapshot` is last-known history once not fresh.
 * - `current_verdict` is the only verdict that may be shown as current.
 * - Never present "Ready" unless freshness is "fresh".
 */

import type {
  PullRequestReadinessPreview,
  PullRequestReadinessView,
} from "@/api/workspace/pullRequests";

export type ReadinessDisplayKey =
  | "ready"
  | "blocked"
  | "waiting"
  | "queued"
  | "merged"
  | "closed"
  | "unknown"
  | "stale"
  | "draft";

export interface ReadinessDisplay {
  key: ReadinessDisplayKey;
  /** User-facing badge label. Never "Ready" for non-fresh evidence. */
  label: string;
  /** True when the badge reflects last-known (not current) evidence. */
  isHistorical: boolean;
  freshness: PullRequestReadinessView["freshness"];
  ageSeconds: number;
  observedAt?: string;
  reasons: string[];
  lastErrorCode?: string;
  retryAfterS?: number;
}

const VERDICT_LABEL: Record<
  PullRequestReadinessView["current_verdict"],
  string
> = {
  ready: "Ready",
  blocked: "Blocked",
  waiting: "Waiting",
  queued: "Queued",
  merged: "Merged",
  closed: "Closed",
  unknown: "Unknown",
};

/**
 * Map a readiness view to a badge. Stale/aging/unknown freshness never yields
 * a current "Ready" label even if the snapshot once said ready.
 */
export function readinessDisplay(
  view: PullRequestReadinessView | null | undefined,
): ReadinessDisplay {
  if (!view) {
    return {
      key: "unknown",
      label: "Not observed",
      isHistorical: true,
      freshness: "unknown",
      ageSeconds: 0,
      reasons: ["not_observed"],
    };
  }

  const observedAt = view.snapshot?.observed_at;
  const lastErrorCode = view.last_error?.code;
  const retryAfterS = view.last_error?.retry_after_s;
  const reasons = view.current_reasons ?? [];

  if (lastErrorCode === "rate_limited") {
    return {
      key: "unknown",
      label: "Rate limited",
      isHistorical: true,
      freshness: view.freshness,
      ageSeconds: view.age_seconds,
      ...(observedAt ? { observedAt } : {}),
      reasons,
      lastErrorCode,
      ...(retryAfterS != null ? { retryAfterS } : {}),
    };
  }
  if (lastErrorCode === "timeout") {
    return {
      key: "unknown",
      label: "Timed out",
      isHistorical: true,
      freshness: view.freshness,
      ageSeconds: view.age_seconds,
      ...(observedAt ? { observedAt } : {}),
      reasons,
      lastErrorCode,
    };
  }

  if (view.freshness === "stale") {
    const historical = view.snapshot?.verdict
      ? `Stale · was ${VERDICT_LABEL[view.snapshot.verdict]}`
      : "Stale evidence";
    return {
      key: "stale",
      label: historical,
      isHistorical: true,
      freshness: "stale",
      ageSeconds: view.age_seconds,
      ...(observedAt ? { observedAt } : {}),
      reasons,
      ...(lastErrorCode ? { lastErrorCode } : {}),
    };
  }

  if (view.freshness === "aging" && view.current_verdict === "ready") {
    // Aging ready evidence must not be labeled currently Ready.
    return {
      key: "unknown",
      label: "Evidence aging",
      isHistorical: true,
      freshness: "aging",
      ageSeconds: view.age_seconds,
      ...(observedAt ? { observedAt } : {}),
      reasons,
    };
  }

  if (view.freshness !== "fresh" && view.current_verdict === "ready") {
    return {
      key: "unknown",
      label: "Unknown",
      isHistorical: true,
      freshness: view.freshness,
      ageSeconds: view.age_seconds,
      ...(observedAt ? { observedAt } : {}),
      reasons,
    };
  }

  const verdict = view.current_verdict;
  const key: ReadinessDisplayKey =
    verdict === "ready" && view.freshness === "fresh"
      ? "ready"
      : verdict === "blocked"
        ? "blocked"
        : verdict === "waiting"
          ? "waiting"
          : verdict === "queued"
            ? "queued"
            : verdict === "merged"
              ? "merged"
              : verdict === "closed"
                ? "closed"
                : "unknown";

  return {
    key,
    label: VERDICT_LABEL[verdict],
    isHistorical: view.freshness !== "fresh",
    freshness: view.freshness,
    ageSeconds: view.age_seconds,
    ...(observedAt ? { observedAt } : {}),
    reasons,
    ...(lastErrorCode ? { lastErrorCode } : {}),
  };
}

/** Format age relative to server clock using age_seconds. */
export function formatAgeSeconds(ageSeconds: number): string {
  if (!Number.isFinite(ageSeconds) || ageSeconds < 0) return "unknown age";
  if (ageSeconds < 60) return `${Math.floor(ageSeconds)}s ago`;
  if (ageSeconds < 3600) return `${Math.floor(ageSeconds / 60)}m ago`;
  if (ageSeconds < 86400) return `${Math.floor(ageSeconds / 3600)}h ago`;
  return `${Math.floor(ageSeconds / 86400)}d ago`;
}

/** Observed-at line for evidence panels. */
export function formatObservedAtLine(
  display: ReadinessDisplay,
  serverNow?: string,
): string {
  if (!display.observedAt) {
    if (display.lastErrorCode === "rate_limited") {
      const retry =
        display.retryAfterS != null
          ? ` Retry after ${display.retryAfterS}s.`
          : "";
      return `GitHub rate limited.${retry}`;
    }
    if (display.lastErrorCode === "timeout") {
      return "GitHub observation timed out.";
    }
    return "GitHub has not been observed for this PR yet.";
  }
  const when = formatClock(display.observedAt, serverNow);
  const age = formatAgeSeconds(display.ageSeconds);
  if (display.freshness === "fresh") {
    return `Observed ${age} · ${when}`;
  }
  if (display.freshness === "stale") {
    return `Last observed ${age} · ${when}. Not current.`;
  }
  if (display.freshness === "aging") {
    return `Observed ${age} · ${when}. Aging — not currently Ready.`;
  }
  return `Last observed ${age} · ${when}.`;
}

function formatClock(iso: string, serverNow?: string): string {
  const at = Date.parse(iso);
  if (!Number.isFinite(at)) return iso;
  const base = serverNow ? Date.parse(serverNow) : Date.now();
  const d = new Date(at);
  // Prefer HH:MM on the observation day relative to server day when possible.
  const hh = String(d.getUTCHours()).padStart(2, "0");
  const mm = String(d.getUTCMinutes()).padStart(2, "0");
  if (Number.isFinite(base)) {
    return `${hh}:${mm} UTC`;
  }
  return `${hh}:${mm} UTC`;
}

export interface MergePreviewSummary {
  readyCount: number;
  stopLabel: string | null;
  stopReasons: string[];
  hasStaleMembers: boolean;
  staleKeys: string[];
  cannotEstablish: boolean;
}

/**
 * Summarize a read-only ordered merge preview for UI copy.
 * Uses preview.ready_count as the only ready-count source.
 */
export function summarizeMergePreview(
  preview: PullRequestReadinessPreview | null | undefined,
): MergePreviewSummary {
  if (!preview) {
    return {
      readyCount: 0,
      stopLabel: "Cannot establish readiness",
      stopReasons: ["no_preview"],
      hasStaleMembers: false,
      staleKeys: [],
      cannotEstablish: true,
    };
  }

  const staleKeys: string[] = [];
  for (const member of preview.members) {
    if (
      member.readiness.freshness === "stale" ||
      member.readiness.freshness === "unknown" ||
      (member.readiness.freshness !== "fresh" &&
        member.readiness.current_verdict === "ready")
    ) {
      staleKeys.push(member.readiness.pr_key);
    }
  }

  const stop = preview.stopped_by;
  const cannotEstablish =
    stop != null &&
    (stop.verdict === "unknown" ||
      stop.reasons.some(
        (r) =>
          r.includes("aging") ||
          r.includes("stale") ||
          r.includes("rate_limited") ||
          r.includes("timeout") ||
          r.includes("not_observed"),
      ));

  let stopLabel: string | null = null;
  if (stop) {
    stopLabel = cannotEstablish
      ? `Cannot establish readiness at ${shortPrKey(stop.pr_key)}`
      : `First blocker: ${VERDICT_LABEL[stop.verdict]} at ${shortPrKey(stop.pr_key)}`;
  }

  return {
    readyCount: preview.ready_count,
    stopLabel,
    stopReasons: stop?.reasons ?? [],
    hasStaleMembers: staleKeys.length > 0,
    staleKeys,
    cannotEstablish: Boolean(cannotEstablish && stop),
  };
}

export function shortPrKey(prKey: string): string {
  const stripped = prKey.replace(/^github:/i, "");
  return stripped;
}

/** Mint a client intent key for delivery-group writes. */
export function newDeliveryGroupIntentKey(prefix = "dg-ui"): string {
  if (typeof crypto !== "undefined" && "randomUUID" in crypto) {
    return `${prefix}-${crypto.randomUUID()}`;
  }
  return `${prefix}-${Date.now()}-${Math.random().toString(36).slice(2, 10)}`;
}
