import {
  useCallback,
  useEffect,
  useLayoutEffect,
  useRef,
  useState,
} from "react";
import { Link } from "react-router-dom";
import {
  elapsed,
  startedAgo,
  trayCounts,
  trayLabel,
  type TrayRow,
  type TrayWave,
} from "@/hooks";
import { ProviderIcon } from "./ProviderIcon";
import tray from "./AgentTray.module.css";

/** The catalog provider id whose glyph a harness shows. */
const PROVIDER_OF: Record<string, string> = {
  claude: "anthropic",
  codex: "codex",
  opencode: "opencode",
};

export function HarnessIcon({ harness }: { harness: string }) {
  return (
    <ProviderIcon
      providerId={PROVIDER_OF[harness] ?? harness}
      providerName={harness}
      size="sm"
    />
  );
}

export const chatPath = (ws: string, id: string) =>
  `/ws/${encodeURIComponent(ws)}/chat/${encodeURIComponent(id)}`;

/** Now, every second while on. */
function useNow(on: boolean): number {
  const [now, setNow] = useState(() => Date.now());
  useEffect(() => {
    if (!on) return;
    setNow(Date.now());
    const t = setInterval(() => setNow(Date.now()), 1000);
    return () => clearInterval(t);
  }, [on]);
  return now;
}

const since = (at: string, now: number) => {
  const t = Date.parse(at);
  return Number.isNaN(t) ? 0 : now - t;
};

export interface AgentTrayProps {
  workspaceId: string;
  rows: readonly TrayRow[];
  waves: readonly TrayWave[];
  open: boolean;
  onOpenChange: (open: boolean) => void;
  /** Short labels, no previews (R8). */
  narrow: boolean;
  /** Sits on the composer's top edge; false when the ask drawer is there. */
  tucked: boolean;
}

/**
 * The agent tray pinned above the composer (DF1): the children that are
 * working, and finished ones whose result still waits for the Lead (with
 * the unread dot). Hidden when there are none.
 */
export function AgentTray({
  workspaceId,
  rows,
  waves,
  open,
  onOpenChange,
  narrow,
  tucked,
}: AgentTrayProps) {
  const running = rows.some((r) => r.status === "running");
  const now = useNow(running);
  const newest = waves[0];
  // A wave that started after the tray first showed is new until opened.
  const [seen, setSeen] = useState<number | null>(null);
  useEffect(() => {
    if (seen === null && newest) setSeen(newest.wave);
    if (open && newest && seen !== null && newest.wave > seen)
      setSeen(newest.wave);
  }, [open, newest, seen]);
  const fresh =
    waves.length > 1 && newest && seen !== null && newest.wave > seen
      ? newest.rows.length
      : 0;

  const listRef = useRef<HTMLUListElement>(null);
  const [below, setBelow] = useState(0);
  const measure = useCallback(() => {
    const el = listRef.current;
    if (!el) return setBelow(0);
    const end = el.scrollTop + el.clientHeight;
    const hidden = [
      ...el.querySelectorAll<HTMLElement>("[data-tray-row]"),
    ].filter((r) => r.offsetTop + r.offsetHeight / 2 > end).length;
    setBelow(hidden);
  }, []);
  useLayoutEffect(() => {
    if (open) measure();
  }, [open, rows, measure]);

  if (rows.length === 0) return null;
  const counts = trayCounts(rows);
  const oldestRun = rows
    .filter((r) => r.status === "running")
    .reduce((m, r) => Math.max(m, since(r.startedAt, now)), 0);
  const shown = rows.slice(0, 3);

  return (
    <div
      className={tray.tray}
      data-testid="agent-tray"
      data-open={open}
      data-tucked={tucked}
      data-narrow={narrow}
    >
      <button
        type="button"
        className={tray.header}
        aria-expanded={open}
        aria-label={`${rows.length} ${rows.length === 1 ? "agent" : "agents"}: ${trayLabel(counts, false)}`}
        onClick={() => onOpenChange(!open)}
      >
        <span className={tray.avatars} aria-hidden="true">
          {shown.map((r) => (
            <span key={r.id} className={tray.avatar}>
              <HarnessIcon harness={r.harness} />
            </span>
          ))}
          {rows.length > shown.length && (
            <span className={tray.avatar}>+{rows.length - shown.length}</span>
          )}
        </span>
        <span className={tray.summary}>
          <span className={tray.count}>
            {rows.length} {rows.length === 1 ? "agent" : "agents"}
          </span>
          <span className={tray.counts}> · {trayLabel(counts, narrow)}</span>
          {fresh > 0 && (
            <span className={tray.newChip} data-testid="tray-new">
              +{fresh} new
            </span>
          )}
        </span>
        {running && !narrow && (
          <span className={tray.clock}>{elapsed(oldestRun, true)}</span>
        )}
        <svg
          className={tray.chevron}
          width="14"
          height="14"
          viewBox="0 0 24 24"
          fill="none"
          stroke="currentColor"
          strokeWidth="2"
          strokeLinecap="round"
          strokeLinejoin="round"
          aria-hidden="true"
        >
          <path d="m18 15-6-6-6 6" />
        </svg>
      </button>
      {open && (
        <div className={tray.listWrap}>
          <ul ref={listRef} className={tray.list} onScroll={measure}>
            {waves.map((w) => (
              <WaveRows
                key={w.wave}
                wave={w}
                divided={waves.length > 1}
                now={now}
                workspaceId={workspaceId}
                narrow={narrow}
              />
            ))}
          </ul>
          {below > 0 && (
            <div className={tray.more} aria-hidden="true">
              ↓ {below} more · scroll
            </div>
          )}
        </div>
      )}
    </div>
  );
}

function WaveRows({
  wave,
  divided,
  now,
  workspaceId,
  narrow,
}: {
  wave: TrayWave;
  divided: boolean;
  now: number;
  workspaceId: string;
  narrow: boolean;
}) {
  const n = wave.rows.length;
  return (
    <>
      {divided && (
        <li className={tray.divider} data-testid="tray-divider">
          Started {startedAgo(wave.at, now)} · {n}{" "}
          {n === 1 ? "agent" : "agents"}
        </li>
      )}
      {wave.rows.map((r) => (
        <li key={r.id} data-tray-row={r.id}>
          <Row row={r} now={now} workspaceId={workspaceId} narrow={narrow} />
        </li>
      ))}
    </>
  );
}

function StatusIcon({ row }: { row: TrayRow }) {
  if (row.status === "running")
    return <span className={tray.spin} aria-label="running" />;
  const ok = !row.record || row.record.outcome === "completed";
  return ok ? (
    <span className={tray.ok} aria-label="done">
      ✓
    </span>
  ) : (
    <span className={tray.fail} aria-label={row.record?.outcome ?? "failed"}>
      ✕
    </span>
  );
}

function Row({
  row,
  now,
  workspaceId,
  narrow,
}: {
  row: TrayRow;
  now: number;
  workspaceId: string;
  narrow: boolean;
}) {
  const head = row.record?.head?.slice(0, 7);
  const branch = row.record?.branch || row.branch;
  const meta = [row.model, branch && `${branch}${head ? `@${head}` : ""}`]
    .filter(Boolean)
    .join(" · ");
  const ok = !row.record || row.record.outcome === "completed";
  const result =
    row.status === "running"
      ? row.state === "active"
        ? "Working…"
        : row.state
      : (row.record?.summary?.split("\n")[0] ??
        (ok ? "Done" : (row.record?.outcome ?? "")));
  return (
    <div className={tray.row} data-status={row.status}>
      <span className={tray.status}>
        <StatusIcon row={row} />
      </span>
      <span className={tray.title}>
        <span className={tray.name}>{row.name}</span>
        {row.unread && (
          <span
            className={tray.dot}
            title="The Lead hasn't read this result yet"
            aria-label="unread by the Lead"
          />
        )}
        {row.status === "waiting_for_lead" && (
          <span className={tray.tag} data-delivery="waiting">
            {narrow
              ? "waiting"
              : `${ok ? "done" : "failed"} · waiting for Lead`}
          </span>
        )}
        {row.attempt > 0 && (
          <span className={tray.chip} data-chip="attempt">
            attempt {row.attempt + 1}
          </span>
        )}
        {!narrow && meta && <span className={tray.meta}>{meta}</span>}
      </span>
      <span className={tray.time}>
        {row.status === "running" ? elapsed(since(row.startedAt, now)) : ""}
      </span>
      <Link className={tray.open} to={chatPath(workspaceId, row.id)}>
        Open
      </Link>
      {result && <span className={tray.result}>{result}</span>}
    </div>
  );
}
