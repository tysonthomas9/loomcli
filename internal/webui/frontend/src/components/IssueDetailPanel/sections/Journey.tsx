import { useEffect, useMemo, useState } from "react";

import type { Event } from "@/types";

import {
  foldJourney,
  type JourneySpan,
  type JourneyStage,
} from "./journeyFold";
import styles from "./Journey.module.css";

export interface JourneyProps {
  events: Event[];
}

const LIVE_TICK_MS = 1_000;

function formatDuration(durationMs: number): string {
  const totalSeconds = Math.max(0, Math.floor(durationMs / 1_000));
  if (totalSeconds < 60) return `${totalSeconds}s`;

  const totalMinutes = Math.floor(totalSeconds / 60);
  if (totalMinutes < 60) return `${totalMinutes}m`;

  const totalHours = Math.floor(totalMinutes / 60);
  const minutes = totalMinutes % 60;
  if (totalHours < 24) {
    return minutes === 0 ? `${totalHours}h` : `${totalHours}h ${minutes}m`;
  }

  const days = Math.floor(totalHours / 24);
  const hours = totalHours % 24;
  return hours === 0 ? `${days}d` : `${days}d ${hours}h`;
}

function stageToken(stage: JourneyStage): string {
  return stage.toLowerCase().replace(/\s+/g, "-");
}

function spanWeight(span: JourneySpan): number {
  // Keep terminal and very short stages visible without letting them dominate
  // a long-running journey.
  return Math.max(span.durationMs, 60_000);
}

export function Journey({ events }: JourneyProps): JSX.Element {
  const [nowMs, setNowMs] = useState(() => Date.now());
  const spans = useMemo(() => foldJourney(events, nowMs), [events, nowMs]);
  const isLive = spans.some((span) => span.end === null);

  useEffect(() => {
    if (!isLive) return undefined;
    const interval = setInterval(() => setNowMs(Date.now()), LIVE_TICK_MS);
    return () => clearInterval(interval);
  }, [isLive]);

  return (
    <section className={styles.section} data-testid="task-journey">
      <h3 className={styles.sectionTitle}>Journey</h3>

      {spans.length === 0 ? (
        <p className={styles.emptyState}>No journey stages in this window.</p>
      ) : (
        <div className={styles.rail} data-live={isLive || undefined}>
          {spans.map((span, index) => (
            <article
              // Repeated stages are valid after release/reopen, so position is
              // part of this derived span's stable identity.
              key={`${span.start}-${span.stage}-${index}`}
              className={styles.span}
              data-stage={stageToken(span.stage)}
              data-testid="journey-span"
              style={{ flexGrow: spanWeight(span) }}
              title={`${span.stage} · ${span.owner ?? "Unassigned"} · ${formatDuration(span.durationMs)}`}
            >
              <span className={styles.stage}>{span.stage}</span>
              <span className={styles.owner}>{span.owner ?? "Unassigned"}</span>
              <span className={styles.duration}>
                {formatDuration(span.durationMs)}
              </span>
            </article>
          ))}
          {isLive && (
            <div
              className={styles.nowLine}
              data-testid="journey-now-line"
              aria-label="Now"
            >
              <span>Now</span>
            </div>
          )}
        </div>
      )}

      <p className={styles.windowNote} data-testid="journey-window-note">
        Stages derived from the most recent {events.length}{" "}
        {events.length === 1 ? "event" : "events"} returned. Earlier history may
        not be included.
      </p>
    </section>
  );
}
