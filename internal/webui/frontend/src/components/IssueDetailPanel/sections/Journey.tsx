import { useEffect, useMemo, useRef, useState } from "react";

import type { Event } from "@/types";

import { AuthorAvatar } from "./AuthorAvatar";
import {
  foldJourney,
  type JourneyAuditEvent,
  type JourneyHalt,
  type JourneySpan,
  type JourneyStage,
} from "./journeyFold";
import {
  formatJourneyClock,
  formatJourneyDuration,
} from "./journeyPresentation";
import styles from "./Journey.module.css";

export interface JourneyProps {
  events: Event[];
  eventLimit: number;
}

const LIVE_TICK_MS = 1_000;

interface StageAnnouncement {
  id: string;
  message: string;
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

function fractionPercent(fraction: number): string {
  const percent = Math.round(Math.min(1, Math.max(0, fraction)) * 10_000) / 100;
  return `${percent}%`;
}

function spineBackground(span: JourneySpan): string {
  if (span.halts.length === 0) {
    if (span.stage === "Open" && span.owner === null) {
      return "repeating-linear-gradient(180deg, var(--color-journey-unowned-rail) 0 4px, transparent 4px 8px)";
    }
    return "var(--journey-stage-color)";
  }

  const stops: string[] = [];
  let previousEnd = 0;
  for (const halt of span.halts) {
    const start = Math.max(previousEnd, halt.startFraction);
    const end = Math.max(start, halt.endFraction);
    if (start > previousEnd) {
      stops.push(
        `var(--journey-stage-color) ${fractionPercent(previousEnd)} ${fractionPercent(start)}`,
      );
    }
    stops.push(
      `var(--color-journey-halt) ${fractionPercent(start)} ${fractionPercent(end)}`,
    );
    previousEnd = end;
  }
  if (previousEnd < 1) {
    stops.push(
      `var(--journey-stage-color) ${fractionPercent(previousEnd)} 100%`,
    );
  }

  return `linear-gradient(180deg, ${stops.join(", ")})`;
}

function auditTime(event: JourneyAuditEvent): number {
  const parsed = Date.parse(event.at);
  return Number.isFinite(parsed) ? parsed : Number.POSITIVE_INFINITY;
}

function AuditEvents({
  events,
}: {
  events: JourneyAuditEvent[];
}): JSX.Element | null {
  if (events.length === 0) return null;

  return (
    <ul className={styles.evs}>
      {events.map((event) => (
        <li
          key={event.id}
          className={styles.ev}
          data-actor-kind={event.actorKind}
          data-testid="journey-audit-event"
        >
          <time className={styles.evT} dateTime={event.at}>
            {formatJourneyClock(event.at)}
          </time>
          <span className={styles.evText}>{event.text}</span>
        </li>
      ))}
    </ul>
  );
}

function HaltBand({
  halt,
  trailing = false,
}: {
  halt: JourneyHalt;
  trailing?: boolean;
}): JSX.Element {
  const duration = formatJourneyDuration(halt.durationMs);
  const label =
    halt.end === null
      ? "Halted — still blocked"
      : "Halted — blocker declared by the agent";

  return (
    <div
      className={styles.halt}
      role="group"
      aria-label={`${label}, ${duration}`}
      data-testid="journey-halt"
      data-trailing={trailing || undefined}
    >
      <div className={styles.haltHead}>
        <AttentionIcon />
        <span className={styles.haltTitle}>{label}</span>
        <span className={styles.haltDur}>{duration}</span>
      </div>
      {halt.note && <p className={styles.haltNote}>“{halt.note}”</p>}
      {halt.clearedNote && (
        <p className={styles.haltCleared}>Cleared — {halt.clearedNote}</p>
      )}
      <AuditEvents events={halt.events} />
    </div>
  );
}

function SpanTimeline({ span }: { span: JourneySpan }): JSX.Element {
  if (span.halts.length === 0) {
    return <AuditEvents events={span.events} />;
  }

  const remainingEvents = [...span.events].sort(
    (a, b) => auditTime(a) - auditTime(b),
  );
  let nextEventIndex = 0;

  return (
    <>
      {span.halts.map((halt, haltIndex) => {
        const haltStart = Date.parse(halt.start);
        const beforeHalt: JourneyAuditEvent[] = [];
        while (
          nextEventIndex < remainingEvents.length &&
          auditTime(remainingEvents[nextEventIndex]!) < haltStart
        ) {
          beforeHalt.push(remainingEvents[nextEventIndex]!);
          nextEventIndex += 1;
        }
        const isTrailingHalt =
          haltIndex === span.halts.length - 1 &&
          nextEventIndex === remainingEvents.length;
        return (
          <div key={`${halt.start}-${halt.end ?? "live"}`}>
            <AuditEvents events={beforeHalt} />
            <HaltBand halt={halt} trailing={isTrailingHalt} />
          </div>
        );
      })}
      <AuditEvents events={remainingEvents.slice(nextEventIndex)} />
    </>
  );
}

function plural(count: number, singular: string): string {
  return `${count} ${singular}${count === 1 ? "" : "s"}`;
}

export function Journey({ events, eventLimit }: JourneyProps): JSX.Element {
  const [nowMs, setNowMs] = useState(() => Date.now());
  const [stageAnnouncement, setStageAnnouncement] =
    useState<StageAnnouncement | null>(null);
  const previousStageIdentityRef = useRef<string | null | undefined>(undefined);
  const shownJourneyIdentityRef = useRef<string | null | undefined>(undefined);
  const spans = useMemo(() => foldJourney(events, nowMs), [events, nowMs]);
  const latestSpan = spans[spans.length - 1] ?? null;
  const liveSpan = latestSpan?.end === null ? latestSpan : null;
  const liveSpanStart = liveSpan?.start ?? null;
  const liveSpanStartMs = useMemo(
    () => (liveSpanStart === null ? null : Date.parse(liveSpanStart)),
    [liveSpanStart],
  );
  const journeyIdentity = events[0]?.issue_id ?? null;
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
    const nextTickDelayMs = LIVE_TICK_MS - (elapsedMs % LIVE_TICK_MS);
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

  const contentSpans = spans.filter((span) => span.stage !== "Closed");
  const closedMarker = latestSpan?.stage === "Closed" ? latestSpan : null;
  const displayContentSpans: JourneySpan[] = [];
  let leadingMarkerEvents: JourneyAuditEvent[] = [];
  let leadingMarkerHalts: JourneyHalt[] = [];
  for (const span of spans) {
    if (span.stage === "Closed") {
      const lastIndex = displayContentSpans.length - 1;
      const previousSpan = displayContentSpans[lastIndex];
      if (previousSpan) {
        displayContentSpans[lastIndex] = {
          ...previousSpan,
          haltedMs: previousSpan.haltedMs + span.haltedMs,
          halts: [...previousSpan.halts, ...span.halts],
          events: [...previousSpan.events, ...span.events],
        };
      } else {
        leadingMarkerEvents = [...leadingMarkerEvents, ...span.events];
        leadingMarkerHalts = [...leadingMarkerHalts, ...span.halts];
      }
      continue;
    }

    displayContentSpans.push({
      ...span,
      haltedMs:
        span.haltedMs +
        leadingMarkerHalts.reduce(
          (duration, halt) => duration + halt.durationMs,
          0,
        ),
      halts: [...leadingMarkerHalts, ...span.halts],
      events: [...leadingMarkerEvents, ...span.events],
    });
    leadingMarkerEvents = [];
    leadingMarkerHalts = [];
  }
  const terminalEvents =
    displayContentSpans.length === 0 ? leadingMarkerEvents : [];
  const terminalAt = closedMarker?.start ?? new Date(nowMs).toISOString();
  const haltCount = spans.reduce((count, span) => count + span.halts.length, 0);
  const firstStartMs = Date.parse(contentSpans[0]?.start ?? terminalAt);
  const terminalMs = Date.parse(terminalAt);
  const totalDurationMs =
    Number.isFinite(firstStartMs) && Number.isFinite(terminalMs)
      ? Math.max(0, terminalMs - firstStartMs)
      : 0;
  const liveHalt = liveSpan?.halts.find((halt) => halt.end === null) ?? null;
  const terminalSummary = closedMarker
    ? `closed after ${formatJourneyDuration(totalDurationMs)} · ${plural(contentSpans.length, "stage")}${haltCount > 0 ? ` · ${plural(haltCount, "halt")}` : ""}`
    : liveSpan
      ? liveHalt
        ? `halted for ${formatJourneyDuration(liveHalt.durationMs)}`
        : `in this stage for ${formatJourneyDuration(liveSpan.durationMs)}`
      : "";

  return (
    <section
      className={styles.section}
      data-testid="task-journey"
      role="region"
      aria-label="Task journey"
    >
      <h3 className={styles.sectionTitle}>Journey</h3>

      {spans.length === 0 ? (
        <p className={styles.emptyState}>No journey stages in this window.</p>
      ) : (
        <ol className={styles.trace} data-testid="journey-trace">
          {displayContentSpans.map((span, index) => {
            const duration = formatJourneyDuration(span.durationMs);
            const haltedDuration = formatJourneyDuration(span.haltedMs);
            return (
              <li
                key={`${span.start}-${span.stage}-${index}`}
                className={styles.seg}
                data-live={span.end === null || undefined}
                data-stage={stageToken(span.stage)}
                data-testid="journey-span"
              >
                <time className={styles.segTime} dateTime={span.start}>
                  {formatJourneyClock(span.start)}
                </time>
                <div className={styles.spine} aria-hidden="true">
                  <span className={styles.node} />
                  <span
                    className={styles.rail}
                    style={{ background: spineBackground(span) }}
                    data-testid="journey-spine-rail"
                  />
                </div>
                <article
                  className={styles.panel}
                  data-quiet={
                    (span.stage === "Open" && span.owner === null) || undefined
                  }
                >
                  <div className={styles.pHead}>
                    <span className={styles.pName}>{span.stage}</span>
                    <span className={styles.pOwner}>
                      {span.owner ? (
                        <>
                          <AuthorAvatar name={span.owner} size="compact" />
                          <span>{span.owner}</span>
                        </>
                      ) : (
                        <span className={styles.unclaimed}>unclaimed</span>
                      )}
                    </span>
                    <span
                      className={styles.pDur}
                      data-halt={span.halts.length > 0 || undefined}
                    >
                      {duration}
                      {span.halts.length > 0 && ` · ${haltedDuration} halted`}
                    </span>
                  </div>
                  <SpanTimeline span={span} />
                </article>
              </li>
            );
          })}

          <li
            className={`${styles.seg} ${styles.terminal}`}
            // The last span is a Closed marker or a live span today, but that
            // invariant lives in the fold, not here. Fall back rather than
            // assert, so a future fold change cannot blank the panel.
            data-stage={stageToken(
              closedMarker?.stage ??
                liveSpan?.stage ??
                latestSpan?.stage ??
                "Open",
            )}
            data-live={
              (closedMarker === null && liveSpan !== null) || undefined
            }
            data-testid="journey-tail"
          >
            <time className={styles.segTime} dateTime={terminalAt}>
              {formatJourneyClock(terminalAt)}
            </time>
            <div className={styles.spine} aria-hidden="true">
              <span className={styles.node} />
            </div>
            <div className={styles.terminalContent}>
              <AuditEvents events={terminalEvents} />
              <div className={styles.tail}>
                <span className={styles.tailLabel}>
                  {closedMarker ? "Done" : "Now"}
                </span>
                <span className={styles.tailSummary}>{terminalSummary}</span>
              </div>
            </div>
          </li>
        </ol>
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
