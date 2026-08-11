import { useVirtualizer, type VirtualItem } from "@tanstack/react-virtual";
import type {
  CSSProperties,
  KeyboardEvent,
  ReactNode,
  WheelEvent,
} from "react";
import {
  forwardRef,
  useCallback,
  useEffect,
  useImperativeHandle,
  useMemo,
  useRef,
  useState,
} from "react";

import {
  getTerminalHistory,
  getTerminalHistoryMeta,
  type TerminalHistoryLine,
  type TerminalHistoryMeta,
  type TerminalHistoryRun,
} from "@/hooks/api";
import { useWorkspaceContext } from "@/hooks/workspace";

import styles from "./TerminalInstance.module.css";

const HISTORY_LINE_HEIGHT = 20;
// Frames to keep re-asserting the live-tail pin, long enough to outlast a
// wheel/trackpad fling that is still animating on the compositor.
const PIN_REASSERT_FRAMES = 20;

// `scroll-behavior: smooth` is set globally (styles/base.css), which would
// otherwise animate every pin and leave the terminal lagging behind its own
// output. Pinning the live tail must be instant, so the behavior is stated
// explicitly rather than inherited from the cascade.
function pinToBottom(element: HTMLElement): void {
  if (typeof element.scrollTo === "function") {
    element.scrollTo({ top: element.scrollHeight, behavior: "instant" });
    return;
  }
  element.scrollTop = element.scrollHeight;
}
const HISTORY_WINDOW_SIZE = 200;
const MAX_RANGE_CACHE = 12;

interface VirtualTerminalHistoryProps {
  sessionName: string;
  isActive: boolean;
  recordingEpoch: number;
  firstScreenLine?: number | undefined;
  children: ReactNode;
}

export interface VirtualTerminalHistoryHandle {
  scrollToBottom: () => void;
}

interface CachedRange {
  from: number;
  count: number;
}

interface IdentityBoundMeta {
  identity: string;
  value: TerminalHistoryMeta;
}

export function historyWindowForVirtualItems(
  items: Pick<VirtualItem, "index">[],
): CachedRange | null {
  const first = items[0];
  const last = items[items.length - 1];
  if (!first || !last) return null;
  const from =
    Math.floor(first.index / HISTORY_WINDOW_SIZE) * HISTORY_WINDOW_SIZE;
  const lastWindow = Math.floor(last.index / HISTORY_WINDOW_SIZE);
  const firstWindow = Math.floor(first.index / HISTORY_WINDOW_SIZE);
  return {
    from,
    count: (lastWindow - firstWindow + 1) * HISTORY_WINDOW_SIZE,
  };
}

export const VirtualTerminalHistory = forwardRef<
  VirtualTerminalHistoryHandle,
  VirtualTerminalHistoryProps
>(function VirtualTerminalHistory(
  { sessionName, isActive, recordingEpoch, firstScreenLine, children },
  ref,
): JSX.Element {
  const { workspaceId } = useWorkspaceContext();
  const identity = useMemo(
    () => JSON.stringify([workspaceId, sessionName, recordingEpoch]),
    [workspaceId, sessionName, recordingEpoch],
  );
  const scrollRef = useRef<HTMLDivElement | null>(null);
  const lineCacheRef = useRef<Map<number, TerminalHistoryLine>>(new Map());
  const rangeCacheRef = useRef<Map<string, CachedRange>>(new Map());
  const requestedRef = useRef<Set<string>>(new Set());
  const generationRef = useRef<string | null>(null);
  const identityRef = useRef(0);
  const nextMetaRequestRef = useRef(0);
  const appliedMetaRequestRef = useRef(0);
  const atBottomRef = useRef(true);
  const pinRafRef = useRef<number | null>(null);
  const pinFramesRef = useRef(0);
  const initializedRef = useRef(false);
  const [metaState, setMetaState] = useState<IdentityBoundMeta | null>(null);
  const [cacheVersion, setCacheVersion] = useState(0);
  const [viewportHeight, setViewportHeight] = useState(1);
  const [atBottom, setAtBottom] = useState(true);
  const meta = metaState?.identity === identity ? metaState.value : null;

  const scrollToBottom = useCallback(() => {
    const element = scrollRef.current;
    if (!element) return;
    atBottomRef.current = true;
    setAtBottom(true);
    pinToBottom(element);
    // A wheel or trackpad fling keeps animating on the compositor after a
    // programmatic scroll, so a single assignment wins the frame and then
    // loses: the fling drags the view back off the live tail, handleScroll
    // reads that as "user is browsing history", and auto-follow stays off
    // until the user manually scrolls down again. Re-assert the pin for a
    // short window so an in-flight fling cannot outlive it.
    pinFramesRef.current = PIN_REASSERT_FRAMES;
    if (pinRafRef.current !== null) return;
    const step = () => {
      const current = scrollRef.current;
      pinFramesRef.current -= 1;
      if (!current || !atBottomRef.current || pinFramesRef.current <= 0) {
        pinRafRef.current = null;
        return;
      }
      pinToBottom(current);
      pinRafRef.current = requestAnimationFrame(step);
    };
    pinRafRef.current = requestAnimationFrame(step);
  }, []);

  useEffect(
    () => () => {
      if (pinRafRef.current !== null) cancelAnimationFrame(pinRafRef.current);
    },
    [],
  );

  useImperativeHandle(ref, () => ({ scrollToBottom }), [scrollToBottom]);

  const historyCount = Math.max(
    0,
    meta?.closed
      ? meta.totalLines
      : Math.min(
          meta?.totalLines ?? 0,
          Math.max(meta?.firstScreenLine ?? 0, firstScreenLine ?? 0),
        ),
  );

  const virtualizer = useVirtualizer({
    count: historyCount,
    getScrollElement: () => scrollRef.current,
    estimateSize: () => HISTORY_LINE_HEIGHT,
    overscan: 20,
  });
  const virtualItems = virtualizer.getVirtualItems();
  const requestedWindow = useMemo(
    () => historyWindowForVirtualItems(virtualItems),
    [virtualItems],
  );

  const resetCachedRows = useCallback(() => {
    lineCacheRef.current = new Map();
    rangeCacheRef.current = new Map();
    requestedRef.current = new Set();
    initializedRef.current = false;
    setCacheVersion((version) => version + 1);
  }, []);

  const refreshMeta = useCallback(
    async (signal?: AbortSignal) => {
      const identityEpoch = identityRef.current;
      const request = ++nextMetaRequestRef.current;
      try {
        const next = await getTerminalHistoryMeta(
          workspaceId,
          sessionName,
          signal,
        );
        if (
          signal?.aborted ||
          identityEpoch !== identityRef.current ||
          request < appliedMetaRequestRef.current
        ) {
          return;
        }
        appliedMetaRequestRef.current = request;
        if (generationRef.current !== next.generation) {
          generationRef.current = next.generation;
          resetCachedRows();
        }
        setMetaState({ identity, value: next });
      } catch {
        // A fresh terminal can mount before its PTY recorder is created. The
        // active poll retries without obscuring the live xterm.
      }
    },
    [workspaceId, sessionName, identity, resetCachedRows],
  );

  useEffect(() => {
    identityRef.current += 1;
    appliedMetaRequestRef.current = nextMetaRequestRef.current;
    generationRef.current = null;
    resetCachedRows();
    setMetaState(null);
    const controller = new AbortController();
    void refreshMeta(controller.signal);
    return () => controller.abort();
  }, [identity, refreshMeta, resetCachedRows]);

  useEffect(() => {
    if (!isActive) return;
    void refreshMeta();
    if (meta?.closed) return;
    const timer = window.setInterval(() => void refreshMeta(), 1000);
    return () => window.clearInterval(timer);
  }, [isActive, meta?.closed, refreshMeta]);

  useEffect(() => {
    const element = scrollRef.current;
    if (!element || typeof ResizeObserver === "undefined") return;
    const observer = new ResizeObserver(() => {
      setViewportHeight(Math.max(1, element.clientHeight));
    });
    observer.observe(element);
    setViewportHeight(Math.max(1, element.clientHeight));
    return () => observer.disconnect();
  }, []);

  useEffect(() => {
    const generation = meta?.generation;
    if (!generation || !requestedWindow || historyCount === 0) return;
    const count = Math.min(
      requestedWindow.count,
      historyCount - requestedWindow.from,
    );
    if (count <= 0) return;
    const key = `${generation}:${requestedWindow.from}:${count}`;
    if (rangeCacheRef.current.has(key) || requestedRef.current.has(key)) return;
    requestedRef.current.add(key);
    const controller = new AbortController();
    void getTerminalHistory(
      workspaceId,
      sessionName,
      generation,
      requestedWindow.from,
      count,
      controller.signal,
    )
      .then((history) => {
        if (
          generationRef.current !== generation ||
          history.generation !== generation
        ) {
          return;
        }
        for (const line of history.lines) {
          lineCacheRef.current.set(line.i, line);
        }
        const ranges = rangeCacheRef.current;
        ranges.set(key, { from: requestedWindow.from, count });
        if (ranges.size > MAX_RANGE_CACHE) {
          const oldest = ranges.keys().next().value as string | undefined;
          if (oldest) ranges.delete(oldest);
        }
        setCacheVersion((version) => version + 1);
      })
      .catch(() => undefined)
      .finally(() => requestedRef.current.delete(key));
    return () => controller.abort();
  }, [
    requestedWindow,
    historyCount,
    workspaceId,
    sessionName,
    meta?.generation,
  ]);

  const historyHeight = virtualizer.getTotalSize();
  const totalHeight = historyHeight + viewportHeight;

  useEffect(() => {
    const element = scrollRef.current;
    if (!element || initializedRef.current || !meta) return;
    initializedRef.current = true;
    requestAnimationFrame(() => {
      pinToBottom(element);
    });
  }, [meta]);

  useEffect(() => {
    const element = scrollRef.current;
    if (!element || !atBottomRef.current) return;
    requestAnimationFrame(() => {
      pinToBottom(element);
    });
  }, [totalHeight]);

  const handleScroll = useCallback(() => {
    const element = scrollRef.current;
    if (!element) return;
    // While the pin is re-asserting, scroll events are echoes of the fling
    // being overridden, not the user asking to browse history.
    if (pinRafRef.current !== null) return;
    const next = element.scrollTop >= Math.max(0, historyHeight - 24);
    atBottomRef.current = next;
    setAtBottom(next);
  }, [historyHeight]);

  const handleKeyDownCapture = useCallback(
    (event: KeyboardEvent<HTMLDivElement>) => {
      // Preserve unmodified navigation keys for the shell/TUI. Shift is the
      // terminal convention for browsing scrollback while input stays focused.
      if (!event.shiftKey || event.altKey || event.ctrlKey || event.metaKey) {
        return;
      }

      const element = scrollRef.current;
      if (!element) return;
      const page = Math.max(
        HISTORY_LINE_HEIGHT,
        element.clientHeight - HISTORY_LINE_HEIGHT,
      );
      let nextTop: number;
      switch (event.key) {
        case "PageUp":
          nextTop = element.scrollTop - page;
          break;
        case "PageDown":
          nextTop = element.scrollTop + page;
          break;
        case "Home":
          nextTop = 0;
          break;
        case "End":
          nextTop = element.scrollHeight;
          break;
        default:
          return;
      }

      event.preventDefault();
      event.stopPropagation();
      element.scrollTop = nextTop;
    },
    [],
  );

  const handleWheelCapture = useCallback(
    (event: WheelEvent<HTMLDivElement>) => {
      if (event.deltaY === 0) return;
      // Stop xterm's target listeners without cancelling the browser default.
      // This also covers applications that enable terminal mouse reporting,
      // whose xterm wheel path cancels unconditionally after reporting input.
      event.stopPropagation();
    },
    [],
  );

  return (
    <div
      ref={scrollRef}
      className={styles.historyScroller}
      data-testid="terminal-history-scroller"
      onScroll={handleScroll}
      onKeyDownCapture={handleKeyDownCapture}
      onWheelCapture={handleWheelCapture}
    >
      <div className={styles.historyCanvas} style={{ height: totalHeight }}>
        <div
          className={styles.historyRows}
          style={{ height: historyHeight }}
          data-cache-version={cacheVersion}
        >
          {virtualItems.map((item) => (
            <HistoryRow
              key={item.key}
              line={meta ? lineCacheRef.current.get(item.index) : undefined}
              style={{
                height: item.size,
                transform: `translateY(${item.start}px)`,
              }}
            />
          ))}
        </div>
        <div
          className={styles.liveScreen}
          style={{ top: historyHeight, height: viewportHeight }}
          data-testid="terminal-live-screen"
        >
          {children}
        </div>
      </div>
      {!atBottom && meta ? (
        <div className={styles.historyNotice} role="status">
          Current size {meta.cols}×{meta.rows}
          {meta.altScreen ? " · fullscreen history is limited" : ""}
          {meta.historyLimited ? " · layout history is limited" : ""}
          {meta.gaps > 0 ? ` · ${meta.gaps} output gaps` : ""}
          {meta.unhandledSequences.count > 0
            ? ` · ${meta.unhandledSequences.count} unsupported terminal sequences`
            : ""}
        </div>
      ) : null}
    </div>
  );
});

function HistoryRow({
  line,
  style,
}: {
  line: TerminalHistoryLine | undefined;
  style: CSSProperties;
}): JSX.Element {
  return (
    <div
      className={styles.historyRow}
      style={{
        ...style,
        minWidth: line ? `calc(${line.cols}ch + 16px)` : undefined,
      }}
      data-line-index={line?.i}
      data-line-cols={line?.cols}
    >
      {line?.runs.map((run, index) => (
        <HistoryRun key={`${line.i}-${index}`} run={run} />
      ))}
    </div>
  );
}

function HistoryRun({ run }: { run: TerminalHistoryRun }): JSX.Element {
  const style: CSSProperties = {
    color: run.inverse ? run.bg || "var(--terminal-bg)" : run.fg || undefined,
    backgroundColor: run.inverse
      ? run.fg || "var(--terminal-fg)"
      : run.bg || undefined,
    fontWeight: run.bold ? 700 : undefined,
    fontStyle: run.italic ? "italic" : undefined,
    textDecoration: run.underline ? "underline" : undefined,
  };
  return <span style={style}>{run.text}</span>;
}
