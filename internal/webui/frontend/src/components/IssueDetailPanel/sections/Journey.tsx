import { useEffect, useLayoutEffect, useMemo, useRef, useState } from "react";

import type { Event } from "@/types";

import { foldJourney, type JourneyStage } from "./journeyFold";
import {
  formatJourneyDuration,
  hasDisplayableJourneyDuration,
} from "./journeyPresentation";
import styles from "./Journey.module.css";

export interface JourneyProps {
  events: Event[];
  eventLimit: number;
}

const LIVE_TICK_MS = 1_000;
const OVERFLOW_TOLERANCE_PX = 1;

interface OverflowEdges {
  left: boolean;
  right: boolean;
}

interface StageAnnouncement {
  id: string;
  message: string;
}

function overflowEdgesFor(rail: HTMLElement): OverflowEdges {
  const maxScrollLeft = Math.max(0, rail.scrollWidth - rail.clientWidth);
  return {
    left: rail.scrollLeft > OVERFLOW_TOLERANCE_PX,
    right: rail.scrollLeft < maxScrollLeft - OVERFLOW_TOLERANCE_PX,
  };
}

function sameOverflowEdges(a: OverflowEdges, b: OverflowEdges): boolean {
  return a.left === b.left && a.right === b.right;
}

function stageToken(stage: JourneyStage): string {
  return stage.toLowerCase().replace(/\s+/g, "-");
}

function AttentionIcon(): JSX.Element {
  return (
    <svg
      className={styles.attentionIcon}
      viewBox="0 0 16 16"
      aria-hidden="true"
      data-testid="journey-attention-icon"
    >
      <path
        d="M8 2.4 14 13H2L8 2.4z"
        fill="none"
        stroke="currentColor"
        strokeWidth="1.4"
        strokeLinejoin="round"
      />
      <path
        d="M8 6v3.2M8 11.5h.01"
        fill="none"
        stroke="currentColor"
        strokeWidth="1.4"
        strokeLinecap="round"
      />
    </svg>
  );
}

export function Journey({ events, eventLimit }: JourneyProps): JSX.Element {
  const [nowMs, setNowMs] = useState(() => Date.now());
  const [overflowEdges, setOverflowEdges] = useState<OverflowEdges>({
    left: false,
    right: false,
  });
  const [stageAnnouncement, setStageAnnouncement] =
    useState<StageAnnouncement | null>(null);
  const railRef = useRef<HTMLDivElement>(null);
  const liveSpanRef = useRef<HTMLElement>(null);
  const positionedJourneyRef = useRef<string | null>(null);
  const previousStageIdentityRef = useRef<string | null | undefined>(undefined);
  const shownJourneyIdentityRef = useRef<string | null | undefined>(undefined);
  const foldedSpans = useMemo(
    () => foldJourney(events, nowMs),
    [events, nowMs],
  );
  const spans = useMemo(
    () =>
      foldedSpans.filter(
        (span) =>
          span.end === null ||
          span.stage === "Closed" ||
          hasDisplayableJourneyDuration(span.durationMs),
      ),
    [foldedSpans],
  );
  const liveSpan = spans.find((span) => span.end === null) ?? null;
  const liveSpanStart = liveSpan?.start ?? null;
  const liveSpanStartMs = useMemo(
    () => (liveSpanStart === null ? null : Date.parse(liveSpanStart)),
    [liveSpanStart],
  );
  const isLive = liveSpan !== null;
  const journeyIdentity = events[0]?.issue_id ?? null;
  const latestSpan = spans[spans.length - 1] ?? null;
  const latestStage = latestSpan?.stage ?? null;
  const latestOwner = latestSpan?.owner ?? null;
  const latestStageIdentity = latestSpan
    ? `${latestSpan.start}:${latestSpan.stage}:${latestSpan.owner ?? ""}`
    : null;
  const isWindowPossiblyTruncated =
    eventLimit > 0 && events.length >= eventLimit;

  useEffect(() => {
    if (liveSpanStartMs === null || !Number.isFinite(liveSpanStartMs)) {
      return undefined;
    }

    const tickerStartedAtMs = Date.now();
    setNowMs(tickerStartedAtMs);
    const elapsedMs = Math.max(0, tickerStartedAtMs - liveSpanStartMs);
    const nextTickDelayMs =
      LIVE_TICK_MS - (elapsedMs % LIVE_TICK_MS);
    let interval: ReturnType<typeof setInterval> | undefined;
    const timeout = setTimeout(() => {
      setNowMs(Date.now());
      interval = setInterval(() => setNowMs(Date.now()), LIVE_TICK_MS);
    }, nextTickDelayMs);

    return () => {
      clearTimeout(timeout);
      if (interval !== undefined) clearInterval(interval);
    };
  }, [liveSpanStartMs]);

  useLayoutEffect(() => {
    const rail = railRef.current;
    if (!rail) return;

    const isFirstPositionForJourney =
      journeyIdentity !== null &&
      positionedJourneyRef.current !== journeyIdentity;
    if (isFirstPositionForJourney) {
      positionedJourneyRef.current = journeyIdentity;
      if (!isLive) {
        rail.scrollLeft = Math.max(0, rail.scrollWidth - rail.clientWidth);
      }
    }

    if (liveSpanStart) {
      const liveSpan = liveSpanRef.current;
      if (liveSpan) {
        const liveRight = liveSpan.offsetLeft + liveSpan.offsetWidth;
        const visibleRight = rail.scrollLeft + rail.clientWidth;
        if (liveRight > visibleRight) {
          rail.scrollLeft = liveRight - rail.clientWidth;
        }
      }
    }

    const nextEdges = overflowEdgesFor(rail);
    setOverflowEdges((current) =>
      sameOverflowEdges(current, nextEdges) ? current : nextEdges,
    );
  }, [isLive, journeyIdentity, liveSpanStart, spans.length]);

  useEffect(() => {
    if (latestStage === null || latestStageIdentity === null) {
      previousStageIdentityRef.current = latestStageIdentity;
      shownJourneyIdentityRef.current = undefined;
      setStageAnnouncement(null);
      return;
    }

    const previousIdentity = previousStageIdentityRef.current;
    previousStageIdentityRef.current = latestStageIdentity;

    // The first non-empty Journey for an issue describes existing history.
    // Announce only a stage that appears after that baseline is visible.
    const isFirstShownJourney =
      shownJourneyIdentityRef.current !== journeyIdentity;
    shownJourneyIdentityRef.current = journeyIdentity;
    if (
      isFirstShownJourney ||
      previousIdentity === undefined ||
      latestStageIdentity === previousIdentity
    ) {
      if (isFirstShownJourney) setStageAnnouncement(null);
      return;
    }

    const owner = latestOwner ? `owned by ${latestOwner}` : "unassigned";
    setStageAnnouncement({
      id: latestStageIdentity,
      message: `Journey updated: ${latestStage}, ${owner}.`,
    });
  }, [journeyIdentity, latestOwner, latestStage, latestStageIdentity]);

  return (
    <section className={styles.section} data-testid="task-journey">
      <h3 className={styles.sectionTitle}>Journey</h3>

      {spans.length === 0 ? (
        <p className={styles.emptyState}>No journey stages in this window.</p>
      ) : (
        <div className={styles.railFrame}>
          <div
            ref={railRef}
            className={styles.rail}
            data-live={isLive || undefined}
            role="region"
            aria-label="Task journey stages"
            tabIndex={0}
            onScroll={(scrollEvent) => {
              const nextEdges = overflowEdgesFor(scrollEvent.currentTarget);
              setOverflowEdges((current) =>
                sameOverflowEdges(current, nextEdges) ? current : nextEdges,
              );
            }}
          >
            <div className={styles.track}>
              {spans.map((span, index) => {
                const isLiveSpan = span.end === null;
                const isBlocked = span.stage === "Blocked";
                const duration = formatJourneyDuration(span.durationMs);
                const accessibleName = `${span.stage}${isBlocked ? " · Human action required" : ""} · ${span.owner ?? "Unassigned"} · ${duration}`;
                return (
                  <article
                    // Repeated stages are valid after release/reopen, so
                    // position is part of this derived span's stable identity.
                    key={`${span.start}-${span.stage}-${index}`}
                    ref={isLiveSpan ? liveSpanRef : undefined}
                    className={styles.span}
                    data-live={isLiveSpan || undefined}
                    data-stage={stageToken(span.stage)}
                    data-testid="journey-span"
                    title={accessibleName}
                  >
                    <span className={styles.stageRow}>
                      {isBlocked && <AttentionIcon />}
                      <span className={styles.stage}>{span.stage}</span>
                    </span>
                    <span className={styles.owner}>
                      {span.owner ?? "Unassigned"}
                    </span>
                    <span className={styles.duration}>{duration}</span>
                    {isLiveSpan && (
                      <div
                        className={styles.nowLine}
                        data-testid="journey-now-line"
                        role="img"
                        aria-label="Now, current stage"
                      >
                        <span>Now</span>
                      </div>
                    )}
                  </article>
                );
              })}
            </div>
          </div>
          <span
            className={`${styles.edgeCue} ${styles.edgeCueLeft}`}
            data-testid="journey-overflow-left"
            data-visible={overflowEdges.left || undefined}
            aria-hidden="true"
          />
          <span
            className={`${styles.edgeCue} ${styles.edgeCueRight}`}
            data-testid="journey-overflow-right"
            data-visible={overflowEdges.right || undefined}
            aria-hidden="true"
          />
        </div>
      )}

      <div
        className={styles.stageAnnouncement}
        data-testid="journey-stage-announcement"
        aria-live="polite"
        aria-atomic="true"
      >
        {stageAnnouncement && (
          <span key={stageAnnouncement.id}>{stageAnnouncement.message}</span>
        )}
      </div>

      <p className={styles.windowNote} data-testid="journey-window-note">
        Stages derived from {isWindowPossiblyTruncated && "the most recent "}
        {events.length} {events.length === 1 ? "event" : "events"} returned.
        {isWindowPossiblyTruncated && " Earlier history may not be included."}
      </p>
    </section>
  );
}
