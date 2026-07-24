/**
 * Hook that polls an async workspace mutation job until it completes or fails.
 *
 * Encapsulates the ref-sync pattern so that callback identity changes from
 * parent re-renders do not restart the polling timer.
 */

import { useState, useRef, useCallback, useEffect } from "react";

import { pollWorkspaceJob, fetchWorkspaceApi } from "@/api/workspace";
import type { WorkspaceData } from "@/api/workspace";
import { useElapsedTime } from "@/hooks/common";

export interface UseJobPollingCallbacks {
  onSuccess: (data: WorkspaceData, createdName: string) => void;
  onClose: () => void;
  /** Called when polling terminates for any reason (success, failure, or network error). */
  onFinish: () => void;
}

export interface UseJobPollingMessages {
  initialProgress: string;
  loadError: string;
  connectionError: string;
  terminalError: string;
}

export interface UseJobPollingReturn {
  /** Whether a job is currently being polled. */
  isPolling: boolean;
  /** Human-readable progress message from the backend. */
  progress: string;
  /** Formatted elapsed time string (e.g. "5s", "2m"). */
  elapsed: string;
  /** Error message if the job failed or connection was lost; empty string otherwise. */
  error: string;
  /** Start polling a job. Resets any previous error. */
  startJob: (jobId: string) => void;
  /** Clear polling state (called when the modal resets). */
  reset: () => void;
}

/**
 * Poll an async workspace mutation job.
 *
 * @param name - Current workspace name (read at completion time via ref).
 * @param callbacks - onSuccess and onClose, kept in refs to avoid timer restarts.
 * @param messages - Optional operation-specific progress and error copy.
 */
export function useJobPolling(
  name: string,
  callbacks: UseJobPollingCallbacks,
  messages: Partial<UseJobPollingMessages> = {},
): UseJobPollingReturn {
  const initialProgress = messages.initialProgress ?? "Cloning repositories...";
  const loadError =
    messages.loadError ??
    "Workspace was created but failed to load. Please refresh the page.";
  const connectionError =
    messages.connectionError ??
    "Lost connection while creating workspace. The clone may still be running.";
  const terminalError = messages.terminalError ?? "Workspace creation failed";
  const [jobId, setJobId] = useState<string | null>(null);
  const [progress, setProgress] = useState(initialProgress);
  const [startTime, setStartTime] = useState<number | null>(null);
  const [error, setError] = useState("");
  const elapsed = useElapsedTime(startTime);

  // Refs to avoid restarting the polling timer when parent re-renders change
  // callback identity or the name field updates.
  const onSuccessRef = useRef(callbacks.onSuccess);
  const onCloseRef = useRef(callbacks.onClose);
  const onFinishRef = useRef(callbacks.onFinish);
  const nameRef = useRef(name);
  const messagesRef = useRef<UseJobPollingMessages>({
    initialProgress,
    loadError,
    connectionError,
    terminalError,
  });
  useEffect(() => {
    onSuccessRef.current = callbacks.onSuccess;
  }, [callbacks.onSuccess]);
  useEffect(() => {
    onCloseRef.current = callbacks.onClose;
  }, [callbacks.onClose]);
  useEffect(() => {
    onFinishRef.current = callbacks.onFinish;
  }, [callbacks.onFinish]);
  useEffect(() => {
    nameRef.current = name;
  }, [name]);
  useEffect(() => {
    messagesRef.current = {
      initialProgress,
      loadError,
      connectionError,
      terminalError,
    };
  }, [initialProgress, loadError, connectionError, terminalError]);

  const startJob = useCallback((id: string) => {
    setJobId(id);
    setStartTime(Date.now());
    setProgress(messagesRef.current.initialProgress);
    setError("");
  }, []);

  const reset = useCallback(() => {
    setJobId(null);
    setStartTime(null);
    setProgress(messagesRef.current.initialProgress);
    setError("");
  }, []);

  // Poll job status
  useEffect(() => {
    if (!jobId) return;

    let cancelled = false;
    let timer: ReturnType<typeof setTimeout> | null = null;

    const poll = async () => {
      if (cancelled) return;
      try {
        const state = await pollWorkspaceJob(jobId);
        if (cancelled) return;

        if (state.progress) {
          setProgress(state.progress);
        }

        if (state.status === "done") {
          try {
            const data = await fetchWorkspaceApi(state.workspace_id);
            if (cancelled) return;
            setJobId(null);
            setStartTime(null);
            onFinishRef.current();
            onSuccessRef.current(data, nameRef.current.trim());
            onCloseRef.current();
          } catch {
            if (cancelled) return;
            setJobId(null);
            setStartTime(null);
            onFinishRef.current();
            setError(messagesRef.current.loadError);
          }
          return;
        }

        if (state.status === "failed") {
          setJobId(null);
          setStartTime(null);
          onFinishRef.current();
          setError(state.error || messagesRef.current.terminalError);
          return;
        }

        // Still running: schedule next poll
        timer = setTimeout(poll, 2000);
      } catch {
        if (cancelled) return;
        setJobId(null);
        setStartTime(null);
        onFinishRef.current();
        setError(messagesRef.current.connectionError);
      }
    };

    // Start first poll after a short delay (job was just created)
    timer = setTimeout(poll, 1000);

    return () => {
      cancelled = true;
      if (timer) clearTimeout(timer);
    };
  }, [jobId]);

  return {
    isPolling: startTime !== null,
    progress,
    elapsed,
    error,
    startJob,
    reset,
  };
}
