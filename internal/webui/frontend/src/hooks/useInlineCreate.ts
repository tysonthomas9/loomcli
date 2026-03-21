/**
 * useInlineCreate - State machine hook for inline issue creation.
 * Manages idle → adding → submitting → idle/error lifecycle.
 */

import { useState, useCallback, useRef, useEffect } from "react";

import type { Issue } from "@/types";

export interface UseInlineCreateOptions {
  createFn: (title: string) => Promise<Issue>;
  onSuccess?: (issue: Issue) => void;
  onError?: (error: string) => void;
}

export interface UseInlineCreateReturn {
  isAdding: boolean;
  isSubmitting: boolean;
  error: string | null;
  startAdding: () => void;
  cancelAdding: () => void;
  submitTitle: (title: string) => Promise<void>;
}

export function useInlineCreate({
  createFn,
  onSuccess,
  onError,
}: UseInlineCreateOptions): UseInlineCreateReturn {
  const [isAdding, setIsAdding] = useState(false);
  const [isSubmitting, setIsSubmitting] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const mountedRef = useRef(true);
  useEffect(() => {
    return () => {
      mountedRef.current = false;
    };
  }, []);

  const startAdding = useCallback(() => {
    setIsAdding(true);
    setError(null);
  }, []);

  const cancelAdding = useCallback(() => {
    setIsAdding(false);
    setIsSubmitting(false);
    setError(null);
  }, []);

  const submitTitle = useCallback(
    async (title: string) => {
      const trimmed = title.trim();
      if (!trimmed || isSubmitting) return;

      setIsSubmitting(true);
      setError(null);

      try {
        const issue = await createFn(trimmed);
        if (!mountedRef.current) return;
        setIsAdding(false);
        setIsSubmitting(false);
        setError(null);
        onSuccess?.(issue);
      } catch (err) {
        if (!mountedRef.current) return;
        const message = err instanceof Error ? err.message : "Failed to create";
        setIsSubmitting(false);
        setError(message);
        onError?.(message);
      }
    },
    [createFn, isSubmitting, onSuccess, onError],
  );

  return {
    isAdding,
    isSubmitting,
    error,
    startAdding,
    cancelAdding,
    submitTitle,
  };
}
