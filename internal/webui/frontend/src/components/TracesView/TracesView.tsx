import { useEffect, useMemo, useState } from "react";
import {
  Link,
  useNavigate,
  useParams,
  useSearchParams,
} from "react-router-dom";

import { CodeMirrorEditor } from "@/components/CodeMirrorEditor";
import {
  TranscriptView,
  formatDuration,
  formatTokens,
} from "@/components/TranscriptView";
import { useSessionEval } from "@/hooks/evals";
import {
  useWorkspaceSession,
  useWorkspaceSessionDiff,
  useWorkspaceSessionSubagents,
  useWorkspaceSessionTranscript,
  useWorkspaceSessions,
  useWorkspaceSubagentTranscript,
  useWorkspaceTraceRun,
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
type DetailTab = "eval" | "transcript" | "diff" | "judge";

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
  { value: "judge", label: "Judge" },
];

export function getTruncationBannerText(
  total: number,
  shown: number,
  limit: number,
): string | null {
  if (total <= shown) return null;
  return `showing newest ${limit} of ${total} in this range — narrow the time range`;
}

export function scoreDimensionsForSessions(
  sessions: WorkspaceSessionListItem[],
): string[] {
  const dimensions = new Set<string>();
  for (const session of sessions) {
    for (const dimension of Object.keys(session.eval_scores ?? {})) {
      dimensions.add(dimension);
    }
  }
  return [...dimensions].sort();
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

function formatScoreDimension(dimension: string): string {
  return dimension.replace(/_/g, " ");
}

function totalTokens(session: WorkspaceSessionListItem): number {
  return (
    (session.input_tokens ?? 0) +
    (session.output_tokens ?? 0) +
    (session.cache_read_tokens ?? 0) +
    (session.cache_write_tokens ?? 0)
  );
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

function updateTags(
  current: URLSearchParams,
  tags: readonly string[],
): URLSearchParams {
  const next = new URLSearchParams(current);
  next.delete("tag");
  for (const tag of tags) next.append("tag", tag);
  return next;
}

function DetailMetaGrid({
  session,
}: {
  session: WorkspaceSessionListItem;
}): JSX.Element {
  const runId = session.task_run_id || "-";
  const hasInvocation = runId !== "-" && Boolean(session.invocation_key);
  const cells = [
    ["Session", session.session_id],
    ["Run", runId],
    [
      "Attempt",
      !hasInvocation || session.attempt == null ? "-" : String(session.attempt),
    ],
    ["Invocation", runId === "-" ? "-" : session.invocation_key || "-"],
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

function SessionRows({
  sessions,
  scoreDimensions,
  selectedId,
  includeRun,
  onSelect,
  onRunClick,
  onTagClick,
}: {
  sessions: WorkspaceSessionListItem[];
  scoreDimensions: string[];
  selectedId: string | null;
  includeRun: boolean;
  onSelect: (sessionId: string) => void;
  onRunClick?: (taskRunId: string) => void;
  onTagClick?: (tag: string) => void;
}): JSX.Element {
  return (
    <div className={styles.tableWrap}>
      <table className={styles.table} data-testid="trace-session-table">
        <thead>
          <tr>
            <th>Session</th>
            {includeRun && <th>Run</th>}
            <th className={styles.attemptColumn}>Attempt</th>
            <th>Invocation</th>
            <th>Agent</th>
            <th>Kind</th>
            <th>Status</th>
            <th>Started</th>
            <th>Duration</th>
            <th>Tokens</th>
            <th>Files</th>
            <th>Tags</th>
            {scoreDimensions.map((dimension) => (
              <th key={dimension} title={dimension}>
                {formatScoreDimension(dimension)}
              </th>
            ))}
            <th title="Transcript">T</th>
            <th title="Diff">D</th>
          </tr>
        </thead>
        <tbody>
          {sessions.map((session) => {
            const runId = session.task_run_id || "";
            const hasInvocation = Boolean(runId && session.invocation_key);
            return (
              <tr
                key={session.session_id}
                className={styles.row}
                data-selected={selectedId === session.session_id || undefined}
                onClick={() => onSelect(session.session_id)}
              >
                <td className={styles.mono} title={session.session_id}>
                  {shortId(session.session_id)}
                </td>
                {includeRun && (
                  <td className={styles.mono}>
                    {runId && onRunClick ? (
                      <button
                        type="button"
                        className={styles.tableLink}
                        title={runId}
                        onClick={(event) => {
                          event.stopPropagation();
                          onRunClick(runId);
                        }}
                      >
                        {shortId(runId)}
                      </button>
                    ) : (
                      "-"
                    )}
                  </td>
                )}
                <td className={`${styles.mono} ${styles.attemptColumn}`}>
                  {hasInvocation && session.attempt != null
                    ? session.attempt
                    : "-"}
                </td>
                <td className={styles.mono} title={session.invocation_key}>
                  {runId ? session.invocation_key || "-" : "-"}
                </td>
                <td className={styles.agentCell} title={session.agent_name}>
                  {session.agent_name}
                </td>
                <td>
                  <span className={styles.kindBadge}>
                    {session.kind ?? "-"}
                  </span>
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
                  <div className={styles.tagList}>
                    {(session.tags ?? []).length === 0 && (
                      <span className={styles.indicator}>-</span>
                    )}
                    {(session.tags ?? []).map((tag) =>
                      onTagClick ? (
                        <button
                          type="button"
                          className={styles.tagPill}
                          key={tag}
                          onClick={(event) => {
                            event.stopPropagation();
                            onTagClick(tag);
                          }}
                        >
                          {tag}
                        </button>
                      ) : (
                        <span className={styles.tagPillStatic} key={tag}>
                          {tag}
                        </span>
                      ),
                    )}
                  </div>
                </td>
                {scoreDimensions.map((dimension) => (
                  <td className={styles.scoreCell} key={dimension}>
                    {session.eval_scores?.[dimension] ?? "-"}
                  </td>
                ))}
                <td>
                  <span
                    className={styles.indicator}
                    data-on={session.has_transcript || undefined}
                    title={
                      session.has_transcript
                        ? "Has transcript"
                        : "No transcript"
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
            );
          })}
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

function JudgeTranscript({
  judgeSessionId,
}: {
  judgeSessionId: string | null;
}): JSX.Element {
  const { entries, isLoading, error } = useWorkspaceSessionTranscript(
    judgeSessionId,
    false,
  );

  if (!judgeSessionId) {
    return (
      <div className={styles.detailEmpty}>
        No judge transcript is linked to the current eval.
      </div>
    );
  }

  return (
    <TranscriptView
      entries={entries}
      isLoading={isLoading}
      error={error}
      emptyMessage="No judge transcript entries"
      toolbar={
        <div className={styles.judgeTranscriptHeader}>
          Judge session <span className={styles.mono}>{judgeSessionId}</span>
        </div>
      }
    />
  );
}

function TraceDetail({
  sessionId,
  initialSession,
  onFollowSession,
}: {
  sessionId: string | null;
  initialSession: WorkspaceSessionListItem | null;
  onFollowSession: (sessionId: string) => void;
}): JSX.Element {
  const [tab, setTab] = useState<DetailTab>("transcript");
  const {
    session: detailSession,
    isLoading: detailLoading,
    error: detailError,
  } = useWorkspaceSession(sessionId);
  const merged = useMemo<WorkspaceSessionListItem | null>(() => {
    const base = initialSession ?? detailSession;
    if (!base) return null;
    const kindValue = initialSession?.kind ?? detailSession?.kind;
    const next = { ...base, ...(detailSession ?? {}) };
    return kindValue ? { ...next, kind: kindValue } : next;
  }, [initialSession, detailSession]);
  const defaultKind = initialSession?.kind ?? detailSession?.kind;

  const {
    evalState,
    isLoading: evalLoading,
    isRejudging,
    error: evalError,
    requestRejudge,
  } = useSessionEval(sessionId, Boolean(sessionId));
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
    setTab(defaultKind === "task" ? "eval" : "transcript");
  }, [sessionId, defaultKind]);

  if (!sessionId) {
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
  if (!merged || (detailLoading && !initialSession)) {
    return <div className={styles.detailEmpty}>Loading session...</div>;
  }

  const judgeSessionId = evalState?.eval?.judge_session_id || null;
  const toolbar = (
    <>
      <DetailMetaGrid session={merged} />
      {merged.judged_session_id && (
        <div className={styles.detailLinks}>
          <button
            type="button"
            className={styles.inlineLink}
            onClick={() => onFollowSession(merged.judged_session_id!)}
          >
            Judged session: {shortId(merged.judged_session_id)}
          </button>
        </div>
      )}
      <div className={styles.tabBar} data-testid="trace-detail-tabs">
        <button
          type="button"
          className={styles.tab}
          data-active={tab === "eval" || undefined}
          onClick={() => setTab("eval")}
        >
          Eval
        </button>
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
          data-active={tab === "judge" || undefined}
          disabled={!evalState?.eval}
          onClick={() => setTab("judge")}
        >
          Judge
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
        <TraceEvalPanel
          sessionId={sessionId}
          {...(merged.kind ? { kind: merged.kind } : {})}
          evalState={evalState}
          isLoading={evalLoading}
          isRejudging={isRejudging}
          error={evalError}
          requestRejudge={requestRejudge}
          onOpenJudge={() => setTab("judge")}
        />
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
      {tab === "judge" && <JudgeTranscript judgeSessionId={judgeSessionId} />}
    </>
  );
}

function FilterChip({
  label,
  onClear,
}: {
  label: string;
  onClear: () => void;
}): JSX.Element {
  return (
    <span className={styles.filterChip}>
      {label}
      <button type="button" aria-label={`Clear ${label}`} onClick={onClear}>
        ×
      </button>
    </span>
  );
}

function TracesListView(): JSX.Element {
  const navigate = useNavigate();
  const { workspaceId = "" } = useParams<{ workspaceId: string }>();
  const [searchParams, setSearchParams] = useSearchParams();
  const [selectedId, setSelectedId] = useState<string | null>(null);
  const range = parseRange(searchParams.get("range"));
  const status = searchParams.get(
    "status",
  ) as WorkspaceSessionStatusFilter | null;
  const agentId = searchParams.get("agent_id") ?? "";
  const kind = searchParams.get("kind") as WorkspaceSessionKind | null;
  const taskRunId = searchParams.get("task_run_id") ?? "";
  const tags = useMemo(() => searchParams.getAll("tag"), [searchParams]);
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
    if (taskRunId) next.task_run_id = taskRunId;
    if (tags.length > 0) next.tags = tags;
    return next;
  }, [range, customSince, customUntil, status, agentId, kind, taskRunId, tags]);

  const { sessions, total, limit, scoreDimensions, isLoading, error, refetch } =
    useWorkspaceSessions(filters);
  const visibleSessions = useMemo(
    () =>
      kind ? sessions : sessions.filter((session) => session.kind !== "judge"),
    [kind, sessions],
  );
  const selected =
    sessions.find((session) => session.session_id === selectedId) ?? null;
  const banner = getTruncationBannerText(total, sessions.length, limit);

  const setParam = (updates: Record<string, string | null>) => {
    setSearchParams((prev) => updateSearchParams(prev, updates));
  };
  const addTag = (tag: string) => {
    if (tags.includes(tag)) return;
    setSearchParams((prev) => updateTags(prev, [...tags, tag]));
  };
  const removeTag = (tag: string) => {
    setSearchParams((prev) =>
      updateTags(
        prev,
        tags.filter((item) => item !== tag),
      ),
    );
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
            <option value="">All except Judge</option>
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
        {(taskRunId || tags.length > 0) && (
          <div className={styles.filterChips} aria-label="Active filters">
            {taskRunId && (
              <FilterChip
                label={`Run: ${taskRunId}`}
                onClear={() => navigate(`/ws/${workspaceId}/traces`)}
              />
            )}
            {tags.map((tag) => (
              <FilterChip
                key={tag}
                label={`Tag: ${tag}`}
                onClear={() => removeTag(tag)}
              />
            ))}
          </div>
        )}
      </div>

      <div
        className={styles.content}
        data-panel-open={Boolean(selectedId) || undefined}
      >
        <section className={styles.listPane} aria-label="Sessions">
          {banner && <div className={styles.banner}>{banner}</div>}
          {isLoading && sessions.length === 0 && (
            <div className={styles.listStatus}>Loading traces...</div>
          )}
          {error && sessions.length === 0 && (
            <div className={styles.listError}>{error.message}</div>
          )}
          {!isLoading && !error && visibleSessions.length === 0 && (
            <div className={styles.listStatus}>
              No sessions matched this range.
            </div>
          )}
          {visibleSessions.length > 0 && (
            <SessionRows
              sessions={visibleSessions}
              scoreDimensions={scoreDimensions}
              selectedId={selectedId}
              includeRun
              onSelect={setSelectedId}
              onRunClick={(runId) =>
                navigate(
                  `/ws/${workspaceId}/traces/runs/${encodeURIComponent(runId)}`,
                )
              }
              onTagClick={addTag}
            />
          )}
        </section>
        {selectedId && (
          <aside className={styles.detailPane} aria-label="Session detail">
            <div className={styles.panelHeader}>
              <strong>Trace detail</strong>
              <div className={styles.panelActions}>
                {selected?.task_run_id && (
                  <button
                    type="button"
                    className={styles.panelButton}
                    onClick={() =>
                      navigate(
                        `/ws/${workspaceId}/traces/runs/${encodeURIComponent(selected.task_run_id!)}` +
                          `?session=${encodeURIComponent(selectedId)}`,
                      )
                    }
                  >
                    Expand
                  </button>
                )}
                <button
                  type="button"
                  className={styles.panelButton}
                  aria-label="Close trace detail"
                  onClick={() => setSelectedId(null)}
                >
                  ×
                </button>
              </div>
            </div>
            <TraceDetail
              sessionId={selectedId}
              initialSession={selected}
              onFollowSession={setSelectedId}
            />
          </aside>
        )}
      </div>
    </div>
  );
}

function TraceRunView({ taskRunId }: { taskRunId: string }): JSX.Element {
  const navigate = useNavigate();
  const { workspaceId = "" } = useParams<{ workspaceId: string }>();
  const [searchParams, setSearchParams] = useSearchParams();
  const selectedId = searchParams.get("session");
  const { run, isLoading, error } = useWorkspaceTraceRun(taskRunId);
  const sessions = useMemo(() => run?.sessions ?? [], [run?.sessions]);
  const scoreDimensions = useMemo(
    () => scoreDimensionsForSessions(sessions),
    [sessions],
  );
  const selected =
    sessions.find((session) => session.session_id === selectedId) ?? null;

  const selectSession = (sessionId: string | null) => {
    setSearchParams((prev) => updateSearchParams(prev, { session: sessionId }));
  };

  return (
    <div className={styles.runPage} data-testid="trace-run-view">
      <div className={styles.runNav}>
        <Link to={`/ws/${workspaceId}/traces`}>← All traces</Link>
        <Link
          to={`/ws/${workspaceId}/traces?task_run_id=${encodeURIComponent(taskRunId)}`}
        >
          view in list ↗
        </Link>
      </div>
      {isLoading && !run && (
        <div className={styles.listStatus}>Loading run...</div>
      )}
      {error && !run && (
        <div className={styles.listError}>
          Failed to load run: {error.message}
        </div>
      )}
      {run && (
        <>
          <header className={styles.runHeader}>
            <div className={styles.runTitleRow}>
              <div>
                <span className={styles.runEyebrow}>Task run</span>
                <h2 className={styles.runTitle}>{run.task_run_id}</h2>
              </div>
              {run.task_id && (
                <Link
                  className={styles.taskLink}
                  to={`/ws/${workspaceId}/issues/${encodeURIComponent(run.task_id)}`}
                >
                  {run.task_id}
                </Link>
              )}
            </div>
            {run.task_run_missing && (
              <div className={styles.missingNotice}>
                task run record missing
              </div>
            )}
            <div className={styles.runStats}>
              <div>
                <span>Status</span>
                <strong>
                  {run.task_run ? formatStatusLabel(run.task_run.status) : "-"}
                </strong>
              </div>
              <div>
                <span>Attempts</span>
                <strong>{run.attempt_count}</strong>
              </div>
              <div>
                <span>Tokens</span>
                <strong>
                  {run.task_run ? formatTokens(run.total_tokens) : "-"}
                </strong>
              </div>
              <div>
                <span>Duration</span>
                <strong>
                  {run.task_run ? formatDuration(run.duration_seconds) : "-"}
                </strong>
              </div>
              <div>
                <span>Files changed</span>
                <strong>{run.files_changed}</strong>
              </div>
            </div>
          </header>
          <section className={styles.runSessions} aria-label="Run sessions">
            {sessions.length === 0 ? (
              <div className={styles.runEmpty}>
                No sessions were recorded for this run.
              </div>
            ) : (
              <SessionRows
                sessions={sessions}
                scoreDimensions={scoreDimensions}
                selectedId={selectedId}
                includeRun={false}
                onSelect={(sessionId) => selectSession(sessionId)}
                onTagClick={(tag) =>
                  navigate(
                    `/ws/${workspaceId}/traces?tag=${encodeURIComponent(tag)}`,
                  )
                }
              />
            )}
          </section>
          {selectedId && (
            <section
              className={styles.runDetail}
              aria-label="Selected session detail"
            >
              <div className={styles.panelHeader}>
                <strong>Trace detail</strong>
                <button
                  type="button"
                  className={styles.panelButton}
                  aria-label="Close trace detail"
                  onClick={() => selectSession(null)}
                >
                  ×
                </button>
              </div>
              <TraceDetail
                sessionId={selectedId}
                initialSession={selected}
                onFollowSession={(sessionId) => selectSession(sessionId)}
              />
            </section>
          )}
        </>
      )}
    </div>
  );
}

export function TracesView(): JSX.Element {
  const { taskRunId } = useParams<{ taskRunId: string }>();
  return taskRunId ? (
    <TraceRunView taskRunId={taskRunId} />
  ) : (
    <TracesListView />
  );
}
