/**
 * LogViewer component.
 * Terminal-style streaming log display using wterm for ANSI rendering.
 */

import { Terminal, useTerminal } from "@wterm/react";
import { useCallback, useEffect, useRef, useState } from "react";

import type { LogChunk, LogStreamState } from "@/hooks/logTypes";

import styles from "./LogViewer.module.css";

/** Static-mode cap — keeps tmux pipe-pane archives producing scrollback
 *  regardless of the container height. */
const STATIC_MAX_ROWS = 30;

export interface LogViewerProps {
  /** Raw log chunks to display */
  chunks: LogChunk[];
  /** Connection state for status indicator */
  connectionState: LogStreamState;
  /** Whether auto-scroll is enabled. Default: true */
  autoScroll?: boolean;
  /** Callback when auto-scroll preference changes */
  onAutoScrollChange?: (enabled: boolean) => void;
  /** Whether to show line numbers (unused — kept for back-compat) */
  showLineNumbers?: boolean;
  /** Additional CSS class name */
  className?: string;
  /** Error message to display */
  error?: string | null;
  /** Height constraint (e.g., "400px", "100%"). Default: "100%" */
  height?: string;
  /** Incremented to force terminal reset (e.g. file truncation) */
  resetVersion?: number;
  /** Optional callback invoked when terminal dimensions change */
  onTerminalResize?: (cols: number, rows: number) => void;
  /** Optional callback to forward terminal data to the backend stream */
  onTerminalData?: (data: string) => void;
  /** Terminal mode: 'interactive' autosizes; 'static' caps rows for archive replay. */
  mode?: "interactive" | "static";
  /** Callback when user scrolls to the top of the buffer (for loading older content) */
  onScrollToTop?: (() => void) | undefined;
  /** Whether older content is currently being loaded */
  isLoadingMore?: boolean;
  /** Whether there is older content available to load */
  hasMoreOlder?: boolean;
}

function getStatusInfo(state: LogStreamState): {
  label: string;
  color: string;
} {
  switch (state) {
    case "connected":
      return { label: "Connected", color: "var(--color-status-done, #22c55e)" };
    case "connecting":
      return {
        label: "Connecting...",
        color: "var(--color-status-working, #facc15)",
      };
    case "reconnecting":
      return {
        label: "Reconnecting...",
        color: "var(--color-status-working, #facc15)",
      };
    case "disconnected":
    default:
      return {
        label: "Disconnected",
        color: "var(--color-status-error, #ef4444)",
      };
  }
}

export function LogViewer({
  chunks,
  connectionState,
  autoScroll: autoScrollProp = true,
  onAutoScrollChange,
  className,
  error,
  height = "100%",
  resetVersion = 0,
  onTerminalResize,
  onTerminalData,
  mode = "interactive",
  onScrollToTop,
  isLoadingMore = false,
  hasMoreOlder = false,
}: LogViewerProps): JSX.Element {
  const { ref: wtermRef, write, resize } = useTerminal();
  const writeRef = useRef(write);
  writeRef.current = write;
  const resizeRef = useRef(resize);
  resizeRef.current = resize;

  const wrapperRef = useRef<HTMLDivElement>(null);

  const [mountKey, setMountKey] = useState(0);
  const [isReady, setIsReady] = useState(false);

  const lastWrittenIndexRef = useRef(0);
  const lastResetVersionRef = useRef(resetVersion);
  const [autoScrollEnabled, setAutoScrollEnabled] = useState(autoScrollProp);
  const autoScrollRef = useRef(autoScrollEnabled);
  autoScrollRef.current = autoScrollEnabled;
  const modeRef = useRef(mode);
  modeRef.current = mode;

  // Scroll-to-top detection refs
  const scrollToTopTimerRef = useRef<ReturnType<typeof setTimeout>>();
  const onScrollToTopRef = useRef(onScrollToTop);
  onScrollToTopRef.current = onScrollToTop;
  const hasMoreOlderRef = useRef(hasMoreOlder);
  hasMoreOlderRef.current = hasMoreOlder;
  const isLoadingMoreRef = useRef(isLoadingMore);
  isLoadingMoreRef.current = isLoadingMore;

  // Track scroll geometry for prepend-restoration
  const prevScrollHeightRef = useRef(0);
  const prevScrollTopRef = useRef(0);
  const suppressTopDetectionRef = useRef(false);

  const onTerminalDataRef = useRef(onTerminalData);
  onTerminalDataRef.current = onTerminalData;
  const onTerminalResizeRef = useRef(onTerminalResize);
  onTerminalResizeRef.current = onTerminalResize;
  const onAutoScrollChangeRef = useRef(onAutoScrollChange);
  onAutoScrollChangeRef.current = onAutoScrollChange;

  // Re-sync auto-scroll when the prop value changes.
  const prevAutoScrollPropRef = useRef(autoScrollProp);
  useEffect(() => {
    if (autoScrollProp !== prevAutoScrollPropRef.current) {
      setAutoScrollEnabled(autoScrollProp);
      prevAutoScrollPropRef.current = autoScrollProp;
    }
  }, [autoScrollProp]);

  // Find the wterm element after mount/remount so we can attach scroll tracking.
  const getScrollEl = useCallback((): HTMLElement | null => {
    return wrapperRef.current?.querySelector(".wterm") as HTMLElement | null;
  }, []);

  // Scroll listener for auto-scroll toggle + scroll-to-top infinite-loading trigger.
  useEffect(() => {
    if (!isReady) return;
    const el = getScrollEl();
    if (!el) return;

    const handleScroll = () => {
      const atBottom =
        el.scrollHeight - el.scrollTop - el.clientHeight < 4;
      if (!atBottom && autoScrollRef.current) {
        setAutoScrollEnabled(false);
        onAutoScrollChangeRef.current?.(false);
      }
      const atTop = el.scrollTop === 0;
      if (
        atTop &&
        hasMoreOlderRef.current &&
        !isLoadingMoreRef.current &&
        !suppressTopDetectionRef.current
      ) {
        if (scrollToTopTimerRef.current)
          clearTimeout(scrollToTopTimerRef.current);
        scrollToTopTimerRef.current = setTimeout(() => {
          onScrollToTopRef.current?.();
        }, 300);
      }
    };

    el.addEventListener("scroll", handleScroll);
    return () => el.removeEventListener("scroll", handleScroll);
  }, [isReady, mountKey, getScrollEl]);

  // Static-mode row cap — when the wterm reports a resize above STATIC_MAX_ROWS,
  // force rows back down so archive content becomes scrollback.
  const handleResize = useCallback((cols: number, rows: number) => {
    let effectiveRows = rows;
    if (modeRef.current === "static" && rows > STATIC_MAX_ROWS) {
      effectiveRows = STATIC_MAX_ROWS;
      resizeRef.current(cols, STATIC_MAX_ROWS);
    }
    onTerminalResizeRef.current?.(cols, effectiveRows);
  }, []);

  const handleReady = useCallback(() => {
    setIsReady(true);
    lastWrittenIndexRef.current = 0;
  }, []);

  const handleData = useCallback((data: string) => {
    onTerminalDataRef.current?.(data);
  }, []);

  // Write new chunks to terminal incrementally; handle reset / buffer-replace
  // by forcing a remount so wterm's grid starts fresh.
  useEffect(() => {
    if (!isReady) return;
    const el = getScrollEl();
    if (!el) return;

    const isReset =
      resetVersion !== lastResetVersionRef.current ||
      chunks.length < lastWrittenIndexRef.current;

    if (isReset) {
      prevScrollHeightRef.current = el.scrollHeight;
      prevScrollTopRef.current = el.scrollTop;
      suppressTopDetectionRef.current = true;
      lastResetVersionRef.current = resetVersion;
      lastWrittenIndexRef.current = 0;
      setIsReady(false);
      setMountKey((k) => k + 1);
      return;
    }

    const newChunks = chunks.slice(lastWrittenIndexRef.current);
    lastWrittenIndexRef.current = chunks.length;

    for (const logChunk of newChunks) {
      if (logChunk) writeRef.current(logChunk.chunk);
    }

    // Finalize scroll position. Double-RAF: wterm commits its DOM on its own
    // RAF, so we must wait a full frame past the write to measure height.
    const afterWtermPaint = (fn: () => void) =>
      requestAnimationFrame(() => requestAnimationFrame(fn));

    if (autoScrollRef.current) {
      afterWtermPaint(() => {
        const scrollEl = getScrollEl();
        if (scrollEl) scrollEl.scrollTop = scrollEl.scrollHeight;
      });
    } else if (prevScrollHeightRef.current > 0) {
      // Prepended content — restore visual position so the view doesn't jump.
      afterWtermPaint(() => {
        const scrollEl = getScrollEl();
        if (!scrollEl) return;
        const added = scrollEl.scrollHeight - prevScrollHeightRef.current;
        if (added > 0) {
          scrollEl.scrollTop = prevScrollTopRef.current + added;
        }
        prevScrollHeightRef.current = 0;
        prevScrollTopRef.current = 0;
      });
    }
    suppressTopDetectionRef.current = false;
  }, [chunks, resetVersion, isReady, getScrollEl]);

  const handleScrollToBottom = useCallback(() => {
    setAutoScrollEnabled(true);
    onAutoScrollChangeRef.current?.(true);
    const el = getScrollEl();
    if (el) el.scrollTop = el.scrollHeight;
  }, [getScrollEl]);

  const statusInfo = getStatusInfo(connectionState);
  const isPulsing =
    connectionState === "connecting" || connectionState === "reconnecting";

  const containerClassName = [styles.container, className]
    .filter(Boolean)
    .join(" ");

  const isStatic = mode === "static";

  return (
    <div
      className={containerClassName}
      style={{ height }}
      data-testid="log-viewer"
    >
      <div className={styles.header}>
        <div className={styles.statusContainer}>
          <span
            className={styles.statusDot}
            style={{ backgroundColor: statusInfo.color }}
            data-state={connectionState}
            data-pulsing={isPulsing}
            aria-hidden="true"
          />
          <span className={styles.statusLabel}>{statusInfo.label}</span>
        </div>
        <div className={styles.controls}>
          {!autoScrollEnabled && (
            <button
              type="button"
              className={styles.autoScrollButton}
              onClick={handleScrollToBottom}
              aria-label="Scroll to bottom and enable auto-scroll"
            >
              <svg viewBox="0 0 16 16" fill="none" aria-hidden="true">
                <path
                  d="M8 2v12M4 10l4 4 4-4"
                  stroke="currentColor"
                  strokeWidth="2"
                  strokeLinecap="round"
                  strokeLinejoin="round"
                />
              </svg>
              Scroll to bottom
            </button>
          )}
        </div>
      </div>

      <div
        className={styles.loadingBanner}
        data-visible={isLoadingMore ? "true" : "false"}
        data-testid="loading-banner"
        role="status"
        aria-live="polite"
        aria-hidden={!isLoadingMore}
      >
        Loading older logs...
      </div>

      {error && (
        <div className={styles.errorBanner} role="alert">
          {error}
        </div>
      )}

      <div
        ref={wrapperRef}
        className={styles.terminalContainer}
        data-testid="terminal-container"
      >
        <Terminal
          key={mountKey}
          ref={wtermRef as React.RefObject<React.ComponentRef<typeof Terminal>>}
          cols={80}
          rows={isStatic ? STATIC_MAX_ROWS : 24}
          autoResize={!isStatic}
          cursorBlink={false}
          {...(onTerminalData ? { onData: handleData } : {})}
          onResize={handleResize}
          onReady={handleReady}
        />
      </div>
    </div>
  );
}
