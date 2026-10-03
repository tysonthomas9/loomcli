import { useEffect, useRef } from "react";
import { useEventContext } from "@/hooks";

interface RefreshTriggers {
  isActive: boolean;
  refreshCheckouts: () => Promise<void>;
  refreshGitStatus: () => Promise<void>;
  refreshBranchDiffs: () => Promise<void>;
  invalidateSkillsCatalog: () => void;
}

/**
 * Re-reads the file browser's checkouts, git status and branch diffs when
 * work outside it may have changed them: the window regains focus or the
 * event stream reconnects (which also re-reads the skills catalog), or a
 * hidden tab pane that keeps the browser mounted is shown again, so an
 * agent's edits made meanwhile show in its Changes count and groups.
 */
export function useRefreshTriggers({
  isActive,
  refreshCheckouts,
  refreshGitStatus,
  refreshBranchDiffs,
  invalidateSkillsCatalog,
}: RefreshTriggers): void {
  const eventContext = useEventContext();
  const reconnectAttemptsRef = useRef(eventContext.reconnectAttempts);
  const wasActiveRef = useRef(isActive);

  useEffect(() => {
    const wasActive = wasActiveRef.current;
    wasActiveRef.current = isActive;
    if (isActive && !wasActive) {
      void refreshCheckouts();
      void refreshGitStatus();
      void refreshBranchDiffs();
    }
  }, [isActive, refreshBranchDiffs, refreshCheckouts, refreshGitStatus]);

  useEffect(() => {
    const handleFocus = () => {
      void refreshCheckouts();
      void refreshGitStatus();
      void refreshBranchDiffs();
      invalidateSkillsCatalog();
    };
    window.addEventListener("focus", handleFocus);
    return () => window.removeEventListener("focus", handleFocus);
  }, [
    refreshBranchDiffs,
    refreshCheckouts,
    refreshGitStatus,
    invalidateSkillsCatalog,
  ]);

  useEffect(() => {
    const previous = reconnectAttemptsRef.current;
    reconnectAttemptsRef.current = eventContext.reconnectAttempts;
    if (
      eventContext.reconnectAttempts > 0 ||
      (previous > 0 && eventContext.state === "connected")
    ) {
      void refreshCheckouts();
      void refreshGitStatus();
      void refreshBranchDiffs();
      invalidateSkillsCatalog();
    }
  }, [
    eventContext.reconnectAttempts,
    eventContext.state,
    refreshBranchDiffs,
    refreshCheckouts,
    refreshGitStatus,
    invalidateSkillsCatalog,
  ]);
}
