import { useEffect, useMemo, useRef, useState } from "react";

import type { Event } from "@/types";

import {
  foldJourney,
  type JourneyStage,
} from "./journeyFold";
import styles from "./Journey.module.css";

export interface JourneyProps {
  events: Event[];
  eventLimit: number;
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

export function Journey({ events, eventLimit }: JourneyProps): JSX.Element {
  const [nowMs, setNowMs] = useState(() => Date.now());
  const railRef = useRef<HTMLDivElement>(null);
  const liveSpanRef = useRef<HTMLElement>(null);
  const spans = useMemo(() => foldJourney(events, nowMs), [events, nowMs]);
  const liveSpanStart = spans.find((span) => span.end === null)?.start ?? null;
  const isLive = liveSpanStart !== null;
  const isWindowPossiblyTruncated =
    eventLimit > 0 && events.length >= eventLimit;

  useEffect(() => {
    if (!isLive) return undefined;
    const interval = setInterval(() => setNowMs(Date.now()), LIVE_TICK_MS);
    return () => clearInterval(interval);
  }, [isLive]);

  useEffect(() => {
    if (!liveSpanStart) return;
    const rail = railRef.current;
    const liveSpan = liveSpanRef.current;
    if (!rail || !liveSpan) return;

    const liveRight = liveSpan.offsetLeft + liveSpan.offsetWidth;
    const visibleRight = rail.scrollLeft + rail.clientWidth;
    if (liveRight > visibleRight) {
      rail.scrollLeft = liveRight - rail.clientWidth;
    }
  }, [liveSpanStart]);

  return (
    <section className={styles.section} data-testid="task-journey">
      <h3 className={styles.sectionTitle}>Journey</h3>

      {spans.length === 0 ? (
        <p className={styles.emptyState}>No journey stages in this window.</p>
      ) : (
        <div
          ref={railRef}
          className={styles.rail}
          data-live={isLive || undefined}
        >
          {spans.map((span, index) => {
            const isLiveSpan = span.end === null;
            return (
              <article
                // Repeated stages are valid after release/reopen, so position
                // is part of this derived span's stable identity.
                key={`${span.start}-${span.stage}-${index}`}
                ref={isLiveSpan ? liveSpanRef : undefined}
                className={styles.span}
                data-live={isLiveSpan || undefined}
                data-stage={stageToken(span.stage)}
                data-testid="journey-span"
                title={`${span.stage} · ${span.owner ?? "Unassigned"} · ${formatDuration(span.durationMs)}`}
              >
                <span className={styles.stage}>{span.stage}</span>
                <span className={styles.owner}>
                  {span.owner ?? "Unassigned"}
                </span>
                <span className={styles.duration}>
                  {formatDuration(span.durationMs)}
                </span>
                {isLiveSpan && (
                  <div
                    className={styles.nowLine}
                    data-testid="journey-now-line"
                    aria-label="Now"
                  >
                    <span>Now</span>
                  </div>
                )}
              </article>
            );
          })}
        </div>
      )}

      <p className={styles.windowNote} data-testid="journey-window-note">
        Stages derived from {isWindowPossiblyTruncated && "the most recent "}
        {events.length} {events.length === 1 ? "event" : "events"} returned.
        {isWindowPossiblyTruncated && " Earlier history may not be included."}
      </p>
    </section>
  );
}
