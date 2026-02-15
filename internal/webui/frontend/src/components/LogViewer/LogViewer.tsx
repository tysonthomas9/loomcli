/**
 * LogViewer component.
 * Terminal-style log display using xterm.js for proper ANSI rendering.
 */

import { FitAddon } from '@xterm/addon-fit';
import { Terminal } from '@xterm/xterm';
import { useRef, useEffect, useCallback, useState } from 'react';

import type { LogChunk, LogStreamState } from '@/hooks/logTypes';

import '@xterm/xterm/css/xterm.css';
import styles from './LogViewer.module.css';

/**
 * Props for the LogViewer component.
 */
export interface LogViewerProps {
  /** Raw log chunks to display */
  chunks: LogChunk[];
  /** Connection state for status indicator */
  connectionState: LogStreamState;
  /** Whether auto-scroll is enabled. Default: true */
  autoScroll?: boolean;
  /** Callback when auto-scroll preference changes */
  onAutoScrollChange?: (enabled: boolean) => void;
  /** Whether to show line numbers (kept for backward compat, not used with xterm) */
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
  /** Terminal mode: 'interactive' resizes with container, 'static' fixes cols at initial fit. Default: 'interactive' */
  mode?: 'interactive' | 'static';
  /** Callback when user scrolls to the top of the buffer (for loading older content) */
  onScrollToTop?: (() => void) | undefined;
  /** Whether older content is currently being loaded */
  isLoadingMore?: boolean;
  /** Whether there is older content available to load */
  hasMoreOlder?: boolean;
}

/**
 * Get connection status display info.
 */
function getStatusInfo(state: LogStreamState): { label: string; color: string } {
  switch (state) {
    case 'connected':
      return { label: 'Connected', color: 'var(--color-status-done, #22c55e)' };
    case 'connecting':
      return { label: 'Connecting...', color: 'var(--color-status-working, #facc15)' };
    case 'reconnecting':
      return { label: 'Reconnecting...', color: 'var(--color-status-working, #facc15)' };
    case 'disconnected':
    default:
      return { label: 'Disconnected', color: 'var(--color-status-error, #ef4444)' };
  }
}

/**
 * LogViewer displays streaming logs using xterm.js for proper terminal rendering.
 */
export function LogViewer({
  chunks,
  connectionState,
  autoScroll: autoScrollProp = true,
  onAutoScrollChange,
  className,
  error,
  height = '100%',
  resetVersion = 0,
  onTerminalResize,
  onTerminalData,
  mode = 'interactive',
  onScrollToTop,
  isLoadingMore = false,
  hasMoreOlder = false,
}: LogViewerProps): JSX.Element {
  const terminalContainerRef = useRef<HTMLDivElement>(null);
  const terminalRef = useRef<Terminal | null>(null);
  const fitAddonRef = useRef<FitAddon | null>(null);
  const lastWrittenIndexRef = useRef(0);
  const lastResetVersionRef = useRef(resetVersion);
  const [autoScrollEnabled, setAutoScrollEnabled] = useState(autoScrollProp);
  const autoScrollRef = useRef(autoScrollEnabled);
  useEffect(() => { autoScrollRef.current = autoScrollEnabled; }, [autoScrollEnabled]);
  const modeRef = useRef(mode);
  useEffect(() => { modeRef.current = mode; }, [mode]);

  // Refs for scroll-to-top detection (stable across renders)
  const scrollToTopTimerRef = useRef<ReturnType<typeof setTimeout>>();
  const onScrollToTopRef = useRef(onScrollToTop);
  useEffect(() => { onScrollToTopRef.current = onScrollToTop; }, [onScrollToTop]);
  const hasMoreOlderRef = useRef(hasMoreOlder);
  useEffect(() => { hasMoreOlderRef.current = hasMoreOlder; }, [hasMoreOlder]);
  const isLoadingMoreRef = useRef(isLoadingMore);
  useEffect(() => { isLoadingMoreRef.current = isLoadingMore; }, [isLoadingMore]);

  // Track previous buffer length for scroll position restoration after prepend
  const prevBufferLengthRef = useRef(0);

  // Sync autoScrollEnabled with prop
  useEffect(() => {
    setAutoScrollEnabled(autoScrollProp);
  }, [autoScrollProp]);

  // Create terminal on mount, destroy on unmount
  useEffect(() => {
    const container = terminalContainerRef.current;
    if (!container) return;

    const terminal = new Terminal({
      disableStdin: !onTerminalData,
      fontSize: 14,
      fontFamily: 'Menlo, Monaco, "Courier New", monospace',
      scrollback: 50000,
      cursorBlink: false,
      cursorStyle: 'bar',
      cursorWidth: 0,
      theme: {
        background: '#1e1e1e',
        foreground: '#d4d4d4',
      },
    });

    const fitAddon = new FitAddon();
    terminal.loadAddon(fitAddon);

    terminalRef.current = terminal;
    fitAddonRef.current = fitAddon;

    terminal.open(container);

    const runFit = () => {
      if (!fitAddonRef.current || !terminalRef.current) return;
      fitAddonRef.current.fit();
      onTerminalResize?.(terminalRef.current.cols, terminalRef.current.rows);
    };

    // Fit once immediately, then again after layout settles (panel slide-in/resize).
    runFit();
    const initialFitFrame = requestAnimationFrame(runFit);
    const initialFitTimeoutA = setTimeout(runFit, 180);
    const initialFitTimeoutB = setTimeout(runFit, 360);
    const initialFitTimeoutC = setTimeout(runFit, 720);

    const inputDisposable = onTerminalData
      ? terminal.onData((data: string) => onTerminalData(data))
      : null;

    // Detect user scroll for auto-scroll toggle and scroll-to-top detection
    const scrollDisposable = terminal.onScroll(() => {
      if (!terminalRef.current) return;
      const term = terminalRef.current;
      const buffer = term.buffer.active;
      const isAtBottom = buffer.viewportY >= buffer.baseY;

      if (!isAtBottom) {
        setAutoScrollEnabled(false);
        onAutoScrollChange?.(false);
      }

      // Scroll-to-top detection for infinite scroll
      const isAtTop = buffer.viewportY === 0;
      if (isAtTop && hasMoreOlderRef.current && !isLoadingMoreRef.current) {
        clearTimeout(scrollToTopTimerRef.current);
        scrollToTopTimerRef.current = setTimeout(() => {
          onScrollToTopRef.current?.();
        }, 300);
      }
    });

    // ResizeObserver for dynamic sizing (skipped in static mode to prevent re-wrapping)
    let resizeTimer: ReturnType<typeof setTimeout>;
    const observer = new ResizeObserver(() => {
      if (modeRef.current === 'static') return;
      clearTimeout(resizeTimer);
      resizeTimer = setTimeout(() => {
        runFit();
      }, 100);
    });
    observer.observe(container);

    // Reset write index since terminal is fresh
    lastWrittenIndexRef.current = 0;

    return () => {
      cancelAnimationFrame(initialFitFrame);
      clearTimeout(initialFitTimeoutA);
      clearTimeout(initialFitTimeoutB);
      clearTimeout(initialFitTimeoutC);
      clearTimeout(resizeTimer);
      clearTimeout(scrollToTopTimerRef.current);
      observer.disconnect();
      scrollDisposable.dispose();
      inputDisposable?.dispose();
      terminal.dispose();
      terminalRef.current = null;
      fitAddonRef.current = null;
      lastWrittenIndexRef.current = 0;
    };
    // onAutoScrollChange/onTerminalResize/onTerminalData excluded from deps — terminal is created once.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  // Refit when stream resets/reconnects to keep wrapping in sync with panel layout changes.
  // Skip in static mode — archive content should keep its initial column width.
  useEffect(() => {
    if (modeRef.current === 'static') return;
    const fitAddon = fitAddonRef.current;
    const terminal = terminalRef.current;
    if (!fitAddon || !terminal) return;

    const runFit = () => {
      fitAddon.fit();
      onTerminalResize?.(terminal.cols, terminal.rows);
    };

    const frame = requestAnimationFrame(runFit);
    const timeout = setTimeout(runFit, 80);
    return () => {
      cancelAnimationFrame(frame);
      clearTimeout(timeout);
    };
  }, [connectionState, onTerminalResize, resetVersion]);

  // Write new chunks to terminal incrementally
  useEffect(() => {
    const terminal = terminalRef.current;
    if (!terminal) return;

    if (resetVersion !== lastResetVersionRef.current) {
      // Capture previous buffer length before reset (for scroll position restoration)
      prevBufferLengthRef.current = terminal.buffer.active.length;
      terminal.reset();
      lastWrittenIndexRef.current = 0;
      lastResetVersionRef.current = resetVersion;
    }

    // Stream was reset by replacing buffered chunks
    if (chunks.length < lastWrittenIndexRef.current) {
      prevBufferLengthRef.current = terminal.buffer.active.length;
      terminal.reset();
      lastWrittenIndexRef.current = 0;
    }

    // Write only new chunks
    for (let i = lastWrittenIndexRef.current; i < chunks.length; i++) {
      const logChunk = chunks[i];
      if (logChunk) {
        terminal.write(logChunk.chunk);
      }
    }
    lastWrittenIndexRef.current = chunks.length;

    // After writing, check if we should restore scroll position (after prepend)
    // or auto-scroll to bottom. Use ref to avoid re-running effect on scroll toggle.
    if (autoScrollRef.current) {
      terminal.scrollToBottom();
    } else if (prevBufferLengthRef.current > 0) {
      // Content was prepended — restore scroll position so the view doesn't jump
      const newBufferLength = terminal.buffer.active.length;
      const addedLines = newBufferLength - prevBufferLengthRef.current;
      if (addedLines > 0) {
        terminal.scrollLines(addedLines);
      }
      prevBufferLengthRef.current = 0;
    }
  }, [chunks, resetVersion]);

  // Re-enable auto-scroll
  const handleScrollToBottom = useCallback(() => {
    setAutoScrollEnabled(true);
    onAutoScrollChange?.(true);

    if (terminalRef.current) {
      terminalRef.current.scrollToBottom();
    }
  }, [onAutoScrollChange]);

  const statusInfo = getStatusInfo(connectionState);
  const isPulsing = connectionState === 'connecting' || connectionState === 'reconnecting';

  const containerClassName = [styles.container, className].filter(Boolean).join(' ');

  return (
    <div className={containerClassName} style={{ height }} data-testid="log-viewer">
      {/* Header with status and controls */}
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

      {/* Loading older logs banner */}
      {isLoadingMore && (
        <div className={styles.loadingBanner}>Loading older logs...</div>
      )}

      {/* Error banner */}
      {error && (
        <div className={styles.errorBanner} role="alert">
          {error}
        </div>
      )}

      {/* Terminal container for xterm.js */}
      <div
        ref={terminalContainerRef}
        className={styles.terminalContainer}
        data-testid="terminal-container"
      />
    </div>
  );
}
