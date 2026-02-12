/**
 * LogViewer component.
 * Terminal-style log display using xterm.js for proper ANSI rendering.
 */

import { FitAddon } from '@xterm/addon-fit';
import { Terminal } from '@xterm/xterm';
import { useRef, useEffect, useCallback, useState } from 'react';

import type { LogLine, LogStreamState } from '@/hooks/useLogStream';

import '@xterm/xterm/css/xterm.css';
import styles from './LogViewer.module.css';

/**
 * Props for the LogViewer component.
 */
export interface LogViewerProps {
  /** Log lines to display */
  lines: LogLine[];
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
  lines,
  connectionState,
  autoScroll: autoScrollProp = true,
  onAutoScrollChange,
  className,
  error,
  height = '100%',
}: LogViewerProps): JSX.Element {
  const terminalContainerRef = useRef<HTMLDivElement>(null);
  const terminalRef = useRef<Terminal | null>(null);
  const fitAddonRef = useRef<FitAddon | null>(null);
  const lastWrittenIndexRef = useRef(0);
  const [autoScrollEnabled, setAutoScrollEnabled] = useState(autoScrollProp);

  // Sync autoScrollEnabled with prop
  useEffect(() => {
    setAutoScrollEnabled(autoScrollProp);
  }, [autoScrollProp]);

  // Create terminal on mount, destroy on unmount
  useEffect(() => {
    const container = terminalContainerRef.current;
    if (!container) return;

    const terminal = new Terminal({
      disableStdin: true,
      fontSize: 14,
      fontFamily: 'Menlo, Monaco, "Courier New", monospace',
      scrollback: 5000,
      convertEol: true,
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
    fitAddon.fit();

    // Detect user scroll to disable auto-scroll
    terminal.onScroll(() => {
      if (!terminalRef.current) return;
      const term = terminalRef.current;
      const buffer = term.buffer.active;
      const isAtBottom = buffer.viewportY >= buffer.baseY;
      if (!isAtBottom) {
        setAutoScrollEnabled(false);
        onAutoScrollChange?.(false);
      }
    });

    // ResizeObserver for dynamic sizing
    let resizeTimer: ReturnType<typeof setTimeout>;
    const observer = new ResizeObserver(() => {
      clearTimeout(resizeTimer);
      resizeTimer = setTimeout(() => {
        if (fitAddonRef.current) {
          fitAddonRef.current.fit();
        }
      }, 100);
    });
    observer.observe(container);

    // Reset write index since terminal is fresh
    lastWrittenIndexRef.current = 0;

    return () => {
      clearTimeout(resizeTimer);
      observer.disconnect();
      terminal.dispose();
      terminalRef.current = null;
      fitAddonRef.current = null;
      lastWrittenIndexRef.current = 0;
    };
    // onAutoScrollChange excluded from deps — we only want to create terminal once
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  // Write new lines to terminal incrementally
  useEffect(() => {
    const terminal = terminalRef.current;
    if (!terminal) return;

    // Stream was reset (agent/task changed) — clear and rewrite
    if (lines.length < lastWrittenIndexRef.current) {
      terminal.clear();
      lastWrittenIndexRef.current = 0;
    }

    // Write only new lines
    for (let i = lastWrittenIndexRef.current; i < lines.length; i++) {
      const logLine = lines[i];
      if (logLine) {
        terminal.write(logLine.line + '\n');
      }
    }
    lastWrittenIndexRef.current = lines.length;

    // Auto-scroll to bottom
    if (autoScrollEnabled) {
      terminal.scrollToBottom();
    }
  }, [lines, autoScrollEnabled]);

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
