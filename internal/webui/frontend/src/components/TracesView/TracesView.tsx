import { useEffect, useMemo, useState } from "react";
import { useSearchParams } from "react-router-dom";

import { CodeMirrorEditor } from "@/components/CodeMirrorEditor";
import {
  TranscriptView,
  formatDuration,
  formatTokens,
} from "@/components/TranscriptView";
import {
  useWorkspaceSession,
  useWorkspaceSessionDiff,
  useWorkspaceSessionSubagents,
  useWorkspaceSessionTranscript,
  useWorkspaceSessions,
  useWorkspaceSubagentTranscript,
} from "@/hooks/terminal";
import type {
  WorkspaceSessionFilters,
  WorkspaceSessionKind,
  WorkspaceSessionListItem,
  WorkspaceSessionStatusFilter,
} from "@/types/agent";
import { formatStatusLabel } from "@/utils/issue";

import { TraceEvalPanel } from "./TraceEvalPanel";
import styles from "./TracesView.module.css";

type RangePreset = "24h" | "7d" | "30d" | "custom";
type DetailTab = "transcript" | "diff" | "eval";

const DEFAULT_LIMIT = 200;
const RANGE_OPTIONS: Array<{ value: RangePreset; label: string }> = [
  { value: "24h", label: "24h" },
  { value: "7d", label: "7d" },
  { value: "30d", label: "30d" },
  { value: "custom", label: "Custom" },
];

const STATUS_OPTIONS: Array<{
  value: WorkspaceSessionStatusFilter;
  label: string;
}> = [
  { value: "queued", label: "Queued" },
  { value: "leased", label: "Leased" },
  { value: "starting", label: "Starting" },
  { value: "running", label: "Running" },
  { value: "idle", label: "Idle" },
  { value: "yielded", label: "Yielded" },
  { value: "completed", label: "Completed" },
  { value: "failed", label: "Failed" },
  { value: "cancelled", label: "Cancelled" },
  { value: "expired", label: "Expired" },
];

const KIND_OPTIONS: Array<{ value: WorkspaceSessionKind; label: string }> = [
  { value: "task", label: "Task" },
  { value: "orchestration", label: "Orchestration" },
  { value: "terminal", label: "Terminal" },
  { value: "maintenance", label: "Maintenance" },
  { value: "ad_hoc", label: "Ad hoc" },
];

export function getTruncationBannerText(
  total: number,
  shown: number,
  limit: number,
): string | null {
  if (total <= shown) return null;
  return `showing newest ${limit} of ${total} in this range — narrow the time range`;
}

function parseRange(value: string | null): RangePreset {
  if (value === "24h" || value === "30d" || value === "custom") return value;
  return "7d";
}

function sinceForRange(range: RangePreset): string | undefined {
  if (range === "custom") return undefined;
  const hours = range === "24h" ? 24 : range === "30d" ? 24 * 30 : 24 * 7;
  return new Date(Date.now() - hours * 60 * 60 * 1000).toISOString();
}

function toLocalInputValue(iso: string | null): string {
  if (!iso) return "";
  const date = new Date(iso);
  if (isNaN(date.getTime())) return "";
  const offsetMs = date.getTimezoneOffset() * 60 * 1000;
  return new Date(date.getTime() - offsetMs).toISOString().slice(0, 16);
}

function localInputToIso(value: string): string | undefined {
  if (!value) return undefined;
  const date = new Date(value);
  if (isNaN(date.getTime())) return undefined;
  return date.toISOString();
}

function shortId(id: string): string {
  if (id.length <= 16) return id;
  return id.slice(0, 8);
}

function formatDateTime(value: string | null | undefined): string {
  if (!value) return "-";
  const date = new Date(value);
  if (isNaN(date.getTime())) return value;
  return date.toLocaleString(undefined, {
    month: "short",
    day: "numeric",
    hour: "2-digit",
    minute: "2-digit",
  });
}

function totalTokens(session: WorkspaceSessionListItem): number {
  return (session.input_tokens ?? 0) + (session.output_tokens ?? 0);
}

function diffStats(session: WorkspaceSessionListItem): string {
  if (
    session.files_changed === 0 &&
    session.lines_added === 0 &&
    session.lines_removed === 0
  ) {
    return "0 files";
  }
  return `${session.files_changed} files, +${session.lines_added} -${session.lines_removed}`;
}

function updateSearchParams(
  current: URLSearchParams,
  updates: Record<string, string | null>,
): URLSearchParams {
  const next = new URLSearchParams(current);
  for (const [key, value] of Object.entries(updates)) {
    if (value == null || value === "") next.delete(key);
    else next.set(key, value);
  }
  return next;
}

function DetailMetaGrid({
  session,
}: {
  session: WorkspaceSessionListItem;
}): JSX.Element {
  const cells = [
    ["Session", session.session_id],
    ["Kind", session.kind ?? "-"],
    ["Started", formatDateTime(session.started_at)],
    ["Ended", formatDateTime(session.ended_at)],
    ["Error", session.error_class ?? session.last_error ?? "-"],
    ["Diff", diffStats(session)],
  ];

  return (
    <div className={styles.detailMeta}>
      {cells.map(([label, value]) => (
        <div className={styles.metaCell} key={label}>
          <div className={styles.metaLabel}>{label}</div>
          <div className={styles.metaValue} title={value}>
            {value}
          </div>
        </div>
      ))}
    </div>
  );
}

function EvalScoresCell({
  session,
}: {
  session: WorkspaceSessionListItem;
}): JSX.Element {
  const scores = session.eval_scores;
  if (scores) {
    const title = `Outcome ${scores.outcome_success} · Adherence ${scores.instruction_adherence} · Efficiency ${scores.efficiency} · Tool use ${scores.tool_use_quality}`;
    return (
      <span
        className={styles.evalScores}
        title={title}
        data-testid="trace-eval-scores"
      >
        {scores.outcome_success}/{scores.instruction_adherence}/
        {scores.efficiency}/{scores.tool_use_quality}
      </span>
    );
  }
  if (session.eval_status === "failed") {
    return (
      <span className={styles.evalScoresFailed} title="Eval failed">
        failed
      </span>
    );
  }
  return <span className={styles.indicator}>-</span>;
}

function SessionRows({
  sessions,
  selectedId,
  onSelect,
}: {
  sessions: WorkspaceSessionListItem[];
  selectedId: string | null;
  onSelect: (sessionId: string) => void;
}): JSX.Element {
  return (
    <div className={styles.tableWrap}>
      <table className={styles.table}>
        <thead>
          <tr>
            <th>Session</th>
            <th>Agent</th>
            <th>Kind</th>
            <th>Status</th>
            <th>Started</th>
            <th>Duration</th>
            <th>Tokens</th>
            <th>Files</th>
            <th title="Eval scores: outcome / adherence / efficiency / tool use">
              Eval
            </th>
            <th>T</th>
            <th>D</th>
          </tr>
        </thead>
        <tbody>
          {sessions.map((session) => (
            <tr
              key={session.session_id}
              className={styles.row}
              data-selected={selectedId === session.session_id || undefined}
              onClick={() => onSelect(session.session_id)}
            >
              <td className={styles.mono} title={session.session_id}>
                {shortId(session.session_id)}
              </td>
              <td className={styles.agentCell} title={session.agent_name}>
                {session.agent_name}
              </td>
              <td>
                <span className={styles.kindBadge}>{session.kind ?? "-"}</span>
              </td>
              <td>
                <span
                  className={styles.statusChip}
                  data-status={session.status}
                >
                  <span className={styles.statusDot} />
                  {formatStatusLabel(session.status)}
                </span>
              </td>
              <td>{formatDateTime(session.started_at)}</td>
              <td>{formatDuration(session.duration_s)}</td>
              <td>{formatTokens(totalTokens(session))}</td>
              <td>{session.files_changed}</td>
              <td>
                <EvalScoresCell session={session} />
              </td>
              <td>
                <span
                  className={styles.indicator}
                  data-on={session.has_transcript || undefined}
                  title={
                    session.has_transcript ? "Has transcript" : "No transcript"
                  }
                >
                  {session.has_transcript ? "yes" : "-"}
                </span>
              </td>
              <td>
                <span
                  className={styles.indicator}
                  data-on={session.has_diff || undefined}
                  title={session.has_diff ? "Has diff" : "No diff"}
                >
                  {session.has_diff ? "yes" : "-"}
                </span>
              </td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}

function SubagentTranscript({
  sessionId,
  subagentId,
  enabled,
}: {
  sessionId: string;
  subagentId: string;
  enabled: boolean;
}): JSX.Element {
  const { entries, isLoading, error } = useWorkspaceSubagentTranscript(
    sessionId,
    subagentId,
    enabled,
  );

  return (
    <div className={styles.subagentBody}>
      <TranscriptView
        entries={entries}
        isLoading={isLoading}
        error={error}
        emptyMessage="No subagent transcript entries"
      />
    </div>
  );
}

function SubagentsSection({
  sessionId,
  enabled,
}: {
  sessionId: string | null;
  enabled: boolean;
}): JSX.Element | null {
  const { subagentIds, isLoading, error } = useWorkspaceSessionSubagents(
    sessionId,
    enabled,
  );
  const [openIds, setOpenIds] = useState<Set<string>>(() => new Set());

  if (!sessionId) return null;
  if (isLoading && subagentIds.length === 0) {
    return <div className={styles.subagents}>Loading subagents...</div>;
  }
  if (error) {
    return (
      <div className={styles.subagents}>
        Failed to load subagents: {error.message}
      </div>
    );
  }
  if (subagentIds.length === 0) return null;

  return (
    <section className={styles.subagents}>
      <h3 className={styles.subagentsTitle}>Subagents</h3>
      {subagentIds.map((id) => {
        const isOpen = openIds.has(id);
        return (
          <details
            key={id}
            className={styles.subagentItem}
            open={isOpen}
            onToggle={(event) => {
              const open = event.currentTarget.open;
              setOpenIds((prev) => {
                const next = new Set(prev);
                if (open) next.add(id);
                else next.delete(id);
                return next;
              });
            }}
          >
            <summary className={styles.subagentSummary}>{id}</summary>
            <SubagentTranscript
              sessionId={sessionId}
              subagentId={id}
              enabled={isOpen}
            />
          </details>
        );
      })}
    </section>
  );
}

function TraceDetail({
  selected,
}: {
  selected: WorkspaceSessionListItem | null;
}): JSX.Element {
  const [tab, setTab] = useState<DetailTab>("transcript");
  const sessionId = selected?.session_id ?? null;
  const {
    session: detailSession,
    isLoading: detailLoading,
    error: detailError,
  } = useWorkspaceSession(sessionId);
  const merged = useMemo<WorkspaceSessionListItem | null>(() => {
    const base = selected ?? detailSession;
    if (!base) return null;
    const kindValue = selected?.kind ?? detailSession?.kind;
    const next = {
      ...base,
      ...(detailSession ?? {}),
    };
    return kindValue ? { ...next, kind: kindValue } : next;
  }, [selected, detailSession]);

  const {
    entries,
    isLoading: transcriptLoading,
    error: transcriptError,
  } = useWorkspaceSessionTranscript(sessionId, merged?.is_active ?? false);
  const {
    diff,
    isLoading: diffLoading,
    error: diffError,
  } = useWorkspaceSessionDiff(
    sessionId,
    tab === "diff" && Boolean(merged?.has_diff),
  );

  useEffect(() => {
    setTab("transcript");
  }, [sessionId]);

  if (!selected) {
    return (
      <div className={styles.detailEmpty}>Select a trace to inspect it.</div>
    );
  }

  if (detailError && !merged) {
    return (
      <div className={styles.detailEmpty}>
        Failed to load session: {detailError.message}
      </div>
    );
  }

  if (!merged || detailLoading) {
    return <div className={styles.detailEmpty}>Loading session...</div>;
  }

  const toolbar = (
    <>
      <DetailMetaGrid session={merged} />
      <div className={styles.tabBar}>
        <button
          type="button"
          className={styles.tab}
          data-active={tab === "transcript" || undefined}
          onClick={() => setTab("transcript")}
        >
          Transcript
        </button>
        <button
          type="button"
          className={styles.tab}
          data-active={tab === "diff" || undefined}
          disabled={!merged.has_diff}
          onClick={() => setTab("diff")}
        >
          Diff
        </button>
        <button
          type="button"
          className={styles.tab}
          data-active={tab === "eval" || undefined}
          onClick={() => setTab("eval")}
        >
          Eval
        </button>
      </div>
    </>
  );

  return (
    <>
      <TranscriptView
        entries={entries}
        session={merged}
        isLoading={transcriptLoading}
        error={transcriptError}
        toolbar={toolbar}
        showTranscript={tab === "transcript"}
        footer={
          <SubagentsSection
            sessionId={sessionId}
            enabled={tab === "transcript"}
          />
        }
      />
      {tab === "eval" && (
        <TraceEvalPanel sessionId={sessionId} enabled={tab === "eval"} />
      )}
      {tab === "diff" && (
        <div className={styles.diffPane} data-testid="trace-session-diff">
          {diffLoading && (
            <div className={styles.listStatus}>Loading diff...</div>
          )}
          {diffError && (
            <div className={styles.listError}>
              Failed to load diff: {diffError.message}
            </div>
          )}
          {!diffLoading && !diffError && diff && (
            <div className={styles.diffEditor}>
              <CodeMirrorEditor
                value={diff}
                language="diff"
                readOnly
                hideLineNumbers
              />
            </div>
          )}
          {!diffLoading && !diffError && !diff && (
            <div className={styles.detailEmpty}>No diff available</div>
          )}
        </div>
      )}
    </>
  );
}

export function TracesView(): JSX.Element {
  const [searchParams, setSearchParams] = useSearchParams();
  const [selectedId, setSelectedId] = useState<string | null>(null);
  const range = parseRange(searchParams.get("range"));
  const status = searchParams.get(
    "status",
  ) as WorkspaceSessionStatusFilter | null;
  const agentId = searchParams.get("agent_id") ?? "";
  const kind = searchParams.get("kind") as WorkspaceSessionKind | null;
  const customSince = searchParams.get("since");
  const customUntil = searchParams.get("until");

  const filters = useMemo<WorkspaceSessionFilters>(() => {
    const since =
      range === "custom" ? (customSince ?? undefined) : sinceForRange(range);
    const until = range === "custom" ? (customUntil ?? undefined) : undefined;
    const next: WorkspaceSessionFilters = { limit: DEFAULT_LIMIT };
    if (since) next.since = since;
    if (until) next.until = until;
    if (status) next.status = status;
    if (agentId) next.agent_id = agentId;
    if (kind) next.kind = kind;
    return next;
  }, [range, customSince, customUntil, status, agentId, kind]);

  const { sessions, total, limit, isLoading, error, refetch } =
    useWorkspaceSessions(filters);

  useEffect(() => {
    if (sessions.length === 0) {
      setSelectedId(null);
      return;
    }
    if (!selectedId || !sessions.some((s) => s.session_id === selectedId)) {
      const firstSession = sessions[0];
      if (firstSession) setSelectedId(firstSession.session_id);
    }
  }, [sessions, selectedId]);

  const selected = sessions.find((s) => s.session_id === selectedId) ?? null;
  const banner = getTruncationBannerText(total, sessions.length, limit);

  const setParam = (updates: Record<string, string | null>) => {
    setSearchParams((prev) => updateSearchParams(prev, updates));
  };

  return (
    <div className={styles.page} data-testid="traces-view">
      <div className={styles.filterBar}>
        <div className={styles.filterGroup}>
          <span className={styles.filterLabel}>Range</span>
          <div
            className={styles.segmented}
            role="group"
            aria-label="Time range"
          >
            {RANGE_OPTIONS.map((option) => (
              <button
                key={option.value}
                type="button"
                className={styles.segment}
                data-active={range === option.value || undefined}
                onClick={() =>
                  setParam({
                    range: option.value === "7d" ? null : option.value,
                    since: option.value === "custom" ? customSince : null,
                    until: option.value === "custom" ? customUntil : null,
                  })
                }
              >
                {option.label}
              </button>
            ))}
          </div>
        </div>
        {range === "custom" && (
          <div className={styles.customRange}>
            <label className={styles.filterGroup}>
              <span className={styles.filterLabel}>Since</span>
              <input
                className={styles.input}
                type="datetime-local"
                value={toLocalInputValue(customSince)}
                onChange={(event) =>
                  setParam({
                    since: localInputToIso(event.target.value) ?? null,
                  })
                }
              />
            </label>
            <label className={styles.filterGroup}>
              <span className={styles.filterLabel}>Until</span>
              <input
                className={styles.input}
                type="datetime-local"
                value={toLocalInputValue(customUntil)}
                onChange={(event) =>
                  setParam({
                    until: localInputToIso(event.target.value) ?? null,
                  })
                }
              />
            </label>
          </div>
        )}
        <label className={styles.filterGroup}>
          <span className={styles.filterLabel}>Status</span>
          <select
            className={styles.select}
            value={status ?? ""}
            onChange={(event) =>
              setParam({ status: event.target.value || null })
            }
          >
            <option value="">All statuses</option>
            {STATUS_OPTIONS.map((option) => (
              <option key={option.value} value={option.value}>
                {option.label}
              </option>
            ))}
          </select>
        </label>
        <label className={styles.filterGroup}>
          <span className={styles.filterLabel}>Agent</span>
          <input
            className={styles.input}
            type="text"
            value={agentId}
            placeholder="agent id"
            onChange={(event) => setParam({ agent_id: event.target.value })}
          />
        </label>
        <label className={styles.filterGroup}>
          <span className={styles.filterLabel}>Kind</span>
          <select
            className={styles.select}
            value={kind ?? ""}
            onChange={(event) => setParam({ kind: event.target.value || null })}
          >
            <option value="">All kinds</option>
            {KIND_OPTIONS.map((option) => (
              <option key={option.value} value={option.value}>
                {option.label}
              </option>
            ))}
          </select>
        </label>
        <button
          type="button"
          className={styles.refreshButton}
          onClick={() => void refetch()}
        >
          Refresh
        </button>
      </div>

      <div className={styles.content}>
        <section className={styles.listPane} aria-label="Sessions">
          {banner && <div className={styles.banner}>{banner}</div>}
          {isLoading && sessions.length === 0 && (
            <div className={styles.listStatus}>Loading traces...</div>
          )}
          {error && sessions.length === 0 && (
            <div className={styles.listError}>{error.message}</div>
          )}
          {!isLoading && !error && sessions.length === 0 && (
            <div className={styles.listStatus}>
              No sessions matched this range.
            </div>
          )}
          {sessions.length > 0 && (
            <SessionRows
              sessions={sessions}
              selectedId={selectedId}
              onSelect={setSelectedId}
            />
          )}
        </section>
        <section className={styles.detailPane} aria-label="Session detail">
          <TraceDetail selected={selected} />
        </section>
      </div>
    </div>
  );
}
