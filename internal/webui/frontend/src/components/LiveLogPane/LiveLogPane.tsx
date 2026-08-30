import { useCallback, useEffect, useRef } from "react";

import { useLogStream } from "@/hooks/terminal/useLogStream";

import styles from "./LiveLogPane.module.css";

export interface LiveLogPaneProps {
  workspaceId: string;
  streamPath: string;
  enabled: boolean;
}

export function LiveLogPane({
  workspaceId,
  streamPath,
  enabled,
}: LiveLogPaneProps): JSX.Element {
  const stream = useLogStream({ workspaceId, streamPath, enabled });

  // Stick to the tail unless the reader has scrolled up to inspect history.
  const outputRef = useRef<HTMLPreElement | null>(null);
  const pinnedRef = useRef(true);
  const handleScroll = useCallback(() => {
    const el = outputRef.current;
    if (!el) return;
    pinnedRef.current = el.scrollHeight - el.scrollTop - el.clientHeight < 40;
  }, []);
  useEffect(() => {
    const el = outputRef.current;
    if (el && pinnedRef.current) {
      el.scrollTop = el.scrollHeight;
    }
  }, [stream.content]);

  const viewState =
    stream.state === "connected" && stream.content === ""
      ? "empty"
      : stream.state;
  const stateLabel = viewState === "empty" ? "no logs" : viewState;

  return (
    <div className={styles.liveLogPane} data-testid="log-viewer">
      <div className={styles.liveLogStatus}>
        <span data-testid="agent-log-state" data-state={viewState}>
          {stateLabel}
        </span>
      </div>
      <pre
        ref={outputRef}
        onScroll={handleScroll}
        className={styles.liveLogOutput}
        data-testid="agent-log-content"
      >
        {stream.content}
      </pre>
    </div>
  );
}
