/**
 * LogViewer component.
 * Terminal-style log display with auto-scroll, line numbers, and connection status.
 */

import { useRef, useEffect, useCallback, useState } from 'react';

import type { LogLine, LogStreamState } from '@/hooks/useLogStream';

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
  /** Whether to show line numbers. Default: true */
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
 * LogViewer displays streaming logs in a terminal-style interface.
 */
export function LogViewer({
  lines,
  connectionState,
  autoScroll: autoScrollProp = true,
  onAutoScrollChange,
  showLineNumbers = true,
  className,
  error,
  height = '100%',
}: LogViewerProps): JSX.Element {
  const scrollContainerRef = useRef<HTMLDivElement>(null);
  const [autoScrollEnabled, setAutoScrollEnabled] = useState(autoScrollProp);
  const isUserScrollingRef = useRef(false);
  const lastScrollTopRef = useRef(0);

  // Sync autoScrollEnabled with prop
  useEffect(() => {
    setAutoScrollEnabled(autoScrollProp);
  }, [autoScrollProp]);

  // Handle scroll event to detect manual scrolling
  const handleScroll = useCallback(() => {
    const container = scrollContainerRef.current;
    if (!container) return;

    const { scrollTop, scrollHeight, clientHeight } = container;
    const isAtBottom = scrollHeight - scrollTop - clientHeight < 50;

    // User scrolled up
    if (scrollTop < lastScrollTopRef.current && !isAtBottom) {
      isUserScrollingRef.current = true;
      if (autoScrollEnabled) {
        setAutoScrollEnabled(false);
        onAutoScrollChange?.(false);
      }
    }

    // User scrolled to bottom
    if (isAtBottom && !autoScrollEnabled) {
      isUserScrollingRef.current = false;
    }

    lastScrollTopRef.current = scrollTop;
  }, [autoScrollEnabled, onAutoScrollChange]);

  // Auto-scroll to bottom when new lines arrive
  useEffect(() => {
    if (!autoScrollEnabled || !scrollContainerRef.current) return;

    const container = scrollContainerRef.current;
    container.scrollTop = container.scrollHeight;
  }, [lines, autoScrollEnabled]);

  // Re-enable auto-scroll
  const handleScrollToBottom = useCallback(() => {
    setAutoScrollEnabled(true);
    onAutoScrollChange?.(true);
    isUserScrollingRef.current = false;

    if (scrollContainerRef.current) {
      scrollContainerRef.current.scrollTop = scrollContainerRef.current.scrollHeight;
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

      {/* Scrollable log content - tabIndex needed for keyboard navigation in log region */}
      {/* eslint-disable jsx-a11y/no-noninteractive-tabindex */}
      <div
        ref={scrollContainerRef}
        className={styles.scrollContainer}
        onScroll={handleScroll}
        tabIndex={0}
        role="log"
        aria-live="polite"
        aria-label="Log output"
      >
        {/* eslint-enable jsx-a11y/no-noninteractive-tabindex */}
        <div className={styles.logContent}>
          {lines.length === 0 ? (
            <div className={styles.empty}>
              No logs available yet. Logs appear when the agent starts working.
            </div>
          ) : (
            lines.map((logLine) => (
              <div key={logLine.lineNumber} className={styles.line}>
                {showLineNumbers && <span className={styles.lineNumber}>{logLine.lineNumber}</span>}
                <span className={styles.lineContent}>{logLine.line}</span>
              </div>
            ))
          )}
        </div>
      </div>
    </div>
  );
}
