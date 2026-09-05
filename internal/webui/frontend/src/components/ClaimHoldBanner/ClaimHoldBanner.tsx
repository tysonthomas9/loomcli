/**
 * ClaimHoldBanner - persistent notice that the workspace is quiesced.
 *
 * A claim hold means the daemon is refusing to START new work while every run
 * already in flight continues untouched. That is an easy state to forget you
 * left on: nothing crashes, no agent errors, the board simply stops advancing.
 * So the banner is persistent rather than dismissible, names who holds it and
 * why, and escalates its wording once the hold is old enough to be an
 * oversight rather than a deploy.
 */

import { useClaimHold } from "@/hooks/agents";

import styles from "./ClaimHoldBanner.module.css";

/** Past this age a still-active hold is more likely forgotten than intended. */
const STALE_AFTER_MS = 2 * 60 * 60 * 1000;

export interface ClaimHoldBannerProps {
  /** Additional CSS class name */
  className?: string;
}

/** Format an age in ms as `2h14m` / `14m` / `40s`. */
export function formatHoldAge(ms: number): string {
  const seconds = Math.max(0, Math.floor(ms / 1000));
  if (seconds < 60) return `${seconds}s`;
  const minutes = Math.floor(seconds / 60);
  if (minutes < 60) return `${minutes}m`;
  return `${Math.floor(minutes / 60)}h${minutes % 60}m`;
}

export function ClaimHoldBanner({
  className,
}: ClaimHoldBannerProps): JSX.Element | null {
  const { hold, gated, busy, error, release } = useClaimHold();

  if (!hold?.held) return null;

  const since = new Date(hold.since);
  const ageMs = Date.now() - since.getTime();
  const stale = ageMs >= STALE_AFTER_MS;
  const age = formatHoldAge(ageMs);
  const sinceLabel = Number.isNaN(since.getTime())
    ? hold.since
    : since.toLocaleString();

  const rootClassName = [styles.banner, className].filter(Boolean).join(" ");

  return (
    <div
      className={rootClassName}
      role="status"
      aria-live="polite"
      data-stale={stale ? "true" : undefined}
    >
      <span className={styles.icon} aria-hidden="true">
        {"⚠"}
      </span>
      <span className={styles.message}>
        {stale ? (
          <strong className={styles.escalated}>
            {`⚠ HELD ${age} — forgotten?`}
          </strong>
        ) : null}
        {"Claims held by "}
        <strong>{hold.actor}</strong>
        {" since "}
        <time dateTime={hold.since}>{sinceLabel}</time>
        {` — ${hold.reason}`}
        {gated > 0 ? (
          <span
            className={styles.gated}
          >{` (${gated} agent${gated === 1 ? "" : "s"} gated)`}</span>
        ) : null}
      </span>
      {error && <span className={styles.error}>{error}</span>}
      <button
        type="button"
        className={styles.releaseButton}
        onClick={() => void release()}
        disabled={busy}
        aria-label="Release the claim hold"
      >
        {busy ? "Releasing…" : "Release"}
      </button>
    </div>
  );
}
