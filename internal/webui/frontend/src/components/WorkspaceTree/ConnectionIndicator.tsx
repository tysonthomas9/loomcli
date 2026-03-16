/**
 * Per-repo connection status indicator dot with optional elapsed time.
 * Renders only for disconnected/reconnecting states.
 */

import type { ConnectionState } from "@/api/sse";
import { useElapsedTime } from "@/hooks/useElapsedTime";

import styles from "./WorkspaceTree.module.css";

interface ConnectionIndicatorProps {
  state: ConnectionState;
  disconnectedSince: number | null;
}

export function ConnectionIndicator({
  state,
  disconnectedSince,
}: ConnectionIndicatorProps): JSX.Element | null {
  const elapsed = useElapsedTime(disconnectedSince);

  if (state === "connected" || state === "connecting") {
    return null;
  }

  return (
    <>
      <span className={styles.connectionDot} data-state={state} />
      {elapsed && <span className={styles.elapsedTime}>{elapsed}</span>}
    </>
  );
}
