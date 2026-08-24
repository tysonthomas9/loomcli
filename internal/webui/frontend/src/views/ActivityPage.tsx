import {
  useCallback,
  useEffect,
  useMemo,
  useReducer,
  useRef,
  useState,
} from "react";

import { fetchAuditEvents } from "@/api/workspace";
import { ErrorDisplay } from "@/components/ErrorDisplay";
import { useEventSubscription } from "@/hooks/common/useEventProvider";
import { useWorkspaceContext } from "@/hooks/workspace/useWorkspaceContext";
import type { ActivityFilters, AuditEvent } from "@/types/activity";
import {
  activityEventsMatch,
  activityTimelineReducer,
  describeActivityEvent,
  EMPTY_ACTIVITY_TIMELINE,
  mergeActivityEvents,
  filterActivityEvents,
  toAuditEvent,
} from "@/utils/activityTimeline";

import styles from "./ActivityPage.module.css";

const PAGE_SIZE = 50;

function fetchOptions(filters: ActivityFilters, since?: string) {
  return {
    limit: PAGE_SIZE,
    ...(since ? { since } : {}),
    ...(filters.actor ? { actor: filters.actor } : {}),
    ...(filters.entity ? { entity: filters.entity } : {}),
  };
}

function relativeTime(timestamp: string): string {
  const milliseconds = Date.now() - new Date(timestamp).getTime();
  if (!Number.isFinite(milliseconds) || milliseconds <= 0) return "just now";

  const seconds = Math.floor(milliseconds / 1000);
  if (seconds < 60) return seconds < 5 ? "just now" : `${seconds}s ago`;
  const minutes = Math.floor(seconds / 60);
  if (minutes < 60) return `${minutes}m ago`;
  const hours = Math.floor(minutes / 60);
  if (hours < 24) return `${hours}h ago`;
  const days = Math.floor(hours / 24);
  if (days < 7) return `${days}d ago`;
  return new Date(timestamp).toLocaleDateString();
}

function absoluteTime(timestamp: string): string {
  const date = new Date(timestamp);
  return Number.isNaN(date.getTime()) ? timestamp : date.toLocaleString();
}

interface ActivityRowProps {
  event: AuditEvent;
  isAgent: boolean;
  isLive: boolean;
}

function ActivityRow({
  event,
  isAgent,
  isLive,
}: ActivityRowProps): JSX.Element {
  const description = describeActivityEvent(event);

  return (
    <li
      className={styles.row}
      aria-label={description.text}
      data-live={isLive || undefined}
    >
      <time
        className={styles.time}
        dateTime={event.timestamp}
        title={absoluteTime(event.timestamp)}
      >
        {relativeTime(event.timestamp)}
      </time>
      <span className={styles.marker} aria-hidden="true" />
      <p className={styles.sentence}>
        <span
          className={isAgent ? styles.agentActor : styles.operatorActor}
          data-actor-kind={isAgent ? "agent" : "operator"}
        >
          {description.actor}
        </span>{" "}
        <span>{description.beforeEntity}</span>{" "}
        <span className={styles.entityChip}>{description.entityId}</span>
        {description.afterEntity ? (
          <>
            {" "}
            <span>{description.afterEntity}</span>
          </>
        ) : null}
      </p>
    </li>
  );
}

export function ActivityPage(): JSX.Element {
  const { workspaceId, agents } = useWorkspaceContext();
  const [state, dispatch] = useReducer(
    activityTimelineReducer,
    EMPTY_ACTIVITY_TIMELINE,
  );
  const [actor, setActor] = useState("");
  const [entity, setEntity] = useState("");
  const [loading, setLoading] = useState(true);
  const [loadingMore, setLoadingMore] = useState(false);
  const [error, setError] = useState<Error | null>(null);
  const [retryKey, setRetryKey] = useState(0);
  const requestGeneration = useRef(0);
  const [, setClockTick] = useState(0);

  useEffect(() => {
    const interval = window.setInterval(
      () => setClockTick((tick) => tick + 1),
      30_000,
    );
    return () => window.clearInterval(interval);
  }, []);

  const filters = useMemo<ActivityFilters>(
    () => ({
      ...(actor ? { actor } : {}),
      ...(entity ? { entity } : {}),
    }),
    [actor, entity],
  );

  useEffect(() => {
    const generation = ++requestGeneration.current;
    let cancelled = false;
    setLoading(true);
    setError(null);

    fetchAuditEvents(workspaceId, fetchOptions(filters))
      .then((result) => {
        if (cancelled || generation !== requestGeneration.current) return;
        dispatch({
          type: "history",
          events: result.events,
          nextCursor: result.next_cursor,
          append: false,
        });
      })
      .catch((reason: unknown) => {
        if (cancelled || generation !== requestGeneration.current) return;
        setError(
          reason instanceof Error
            ? reason
            : new Error("Failed to load workspace activity"),
        );
      })
      .finally(() => {
        if (!cancelled && generation === requestGeneration.current)
          setLoading(false);
      });

    return () => {
      cancelled = true;
    };
  }, [filters, retryKey, workspaceId]);

  useEventSubscription(
    useCallback((mutation) => {
      if (
        mutation.action === "label.add" ||
        mutation.action === "label.remove"
      ) {
        setRetryKey((key) => key + 1);
        return;
      }
      const auditEvent = toAuditEvent(mutation);
      if (auditEvent) dispatch({ type: "live", event: auditEvent });
    }, []),
  );

  const allEvents = useMemo(
    () => mergeActivityEvents(state.history, state.live),
    [state.history, state.live],
  );
  const visibleEvents = useMemo(
    () => filterActivityEvents(allEvents, filters),
    [allEvents, filters],
  );
  const agentNames = useMemo(
    () => new Set(agents.map((agent) => agent.name)),
    [agents],
  );
  const actorOptions = useMemo(
    () =>
      Array.from(
        new Set([
          ...agents.map((agent) => agent.name),
          ...allEvents.map((event) => event.actor),
          ...(actor ? [actor] : []),
        ]),
      ).sort(),
    [actor, agents, allEvents],
  );
  const entityOptions = useMemo(
    () =>
      Array.from(
        new Set([
          ...allEvents
            .filter((activityEvent) => activityEvent.entity_type === "issue")
            .map((activityEvent) => activityEvent.entity_id),
          ...(entity ? [entity] : []),
        ]),
      ).sort(),
    [allEvents, entity],
  );

  const loadMore = useCallback(async () => {
    if (!state.nextCursor || loadingMore) return;
    const generation = requestGeneration.current;
    setLoadingMore(true);
    setError(null);
    try {
      const result = await fetchAuditEvents(
        workspaceId,
        fetchOptions(filters, state.nextCursor),
      );
      if (generation !== requestGeneration.current) return;
      dispatch({
        type: "history",
        events: result.events,
        nextCursor:
          result.next_cursor === state.nextCursor ? "" : result.next_cursor,
        append: true,
      });
    } catch (reason: unknown) {
      if (generation !== requestGeneration.current) return;
      setError(
        reason instanceof Error
          ? reason
          : new Error("Failed to load more workspace activity"),
      );
    } finally {
      if (generation === requestGeneration.current) setLoadingMore(false);
    }
  }, [filters, loadingMore, state.nextCursor, workspaceId]);

  return (
    <section className={styles.page} aria-labelledby="activity-heading">
      <header className={styles.header}>
        <div>
          <h1 id="activity-heading" className={styles.title}>
            Activity
          </h1>
          <p className={styles.subtitle}>
            Claims, changes, and handoffs across this workspace.
          </p>
        </div>
        <div className={styles.filters} aria-label="Activity filters">
          <label className={styles.filter}>
            <span>Actor</span>
            <select
              aria-label="Filter by actor"
              value={actor}
              onChange={(event) => setActor(event.target.value)}
            >
              <option value="">All actors</option>
              {actorOptions.map((name) => (
                <option key={name} value={name}>
                  {name}
                </option>
              ))}
            </select>
          </label>
          <label className={styles.filter}>
            <span>Issue</span>
            <select
              aria-label="Filter by issue"
              value={entity}
              onChange={(event) => setEntity(event.target.value)}
            >
              <option value="">All issues</option>
              {entityOptions.map((id) => (
                <option key={id} value={id}>
                  {id}
                </option>
              ))}
            </select>
          </label>
        </div>
      </header>

      <div className={styles.panel}>
        {loading ? (
          <div
            className={styles.loading}
            role="status"
            aria-label="Loading activity"
          >
            <span className={styles.loadingLine} />
            <span className={styles.loadingLine} />
            <span className={styles.loadingLine} />
          </div>
        ) : error && visibleEvents.length === 0 ? (
          <ErrorDisplay
            variant="fetch-error"
            title="Activity did not load"
            description="The workspace audit history could not be loaded."
            error={error}
            onRetry={() => setRetryKey((key) => key + 1)}
          />
        ) : visibleEvents.length === 0 ? (
          <div className={styles.empty}>
            <h2>No activity yet.</h2>
            <p>Workspace changes will appear here as they happen.</p>
          </div>
        ) : (
          <>
            {error ? (
              <div className={styles.inlineError} role="alert">
                Older activity did not load. Try again.
              </div>
            ) : null}
            <ul className={styles.timeline} aria-label="Workspace activity">
              {visibleEvents.map((activityEvent) => (
                <ActivityRow
                  key={
                    activityEvent.cursor ||
                    `${activityEvent.action}-${activityEvent.entity_id}-${activityEvent.timestamp}`
                  }
                  event={activityEvent}
                  isAgent={agentNames.has(activityEvent.actor)}
                  isLive={state.live.some((liveEvent) =>
                    activityEventsMatch(liveEvent, activityEvent),
                  )}
                />
              ))}
            </ul>
            {state.nextCursor ? (
              <div className={styles.loadMore}>
                <button
                  type="button"
                  onClick={loadMore}
                  disabled={loadingMore}
                  aria-label="Load more activity"
                >
                  {loadingMore ? "Loading…" : "Load more"}
                </button>
              </div>
            ) : null}
          </>
        )}
      </div>
    </section>
  );
}
