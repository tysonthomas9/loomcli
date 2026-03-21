/**
 * useClipboard hook.
 * Manages copy-toast display and multi-line paste confirmation state
 * for the TerminalView component.
 */

import { useState, useCallback, useRef } from "react";

import type { TerminalInstanceHandle } from "./TerminalInstance";

interface UseClipboardResult {
  showCopyToast: boolean;
  pendingPasteText: string | null;
  handleCopyNotify: () => void;
  handlePasteRequest: () => void;
  handlePasteConfirm: () => void;
  handlePasteCancel: () => void;
}

export function useClipboard(
  instanceRefs: React.MutableRefObject<Map<string, TerminalInstanceHandle>>,
  activeTabIdRef: React.MutableRefObject<string>,
): UseClipboardResult {
  const [showCopyToast, setShowCopyToast] = useState(false);
  const [pendingPasteText, setPendingPasteText] = useState<string | null>(null);
  const copyToastTimerRef = useRef<ReturnType<typeof setTimeout> | undefined>();

  const handleCopyNotify = useCallback(() => {
    clearTimeout(copyToastTimerRef.current);
    setShowCopyToast(true);
    copyToastTimerRef.current = setTimeout(() => setShowCopyToast(false), 1500);
  }, []);

  const handlePasteRequest = useCallback(() => {
    if (pendingPasteText !== null) return;
    navigator.clipboard
      .readText()
      .then((text) => {
        if (!text) return;
        if (text.trimEnd().includes("\n")) {
          setPendingPasteText(text);
        } else {
          instanceRefs.current.get(activeTabIdRef.current)?.pasteText(text);
        }
      })
      .catch(() => {});
  }, [pendingPasteText, instanceRefs, activeTabIdRef]);

  const handlePasteConfirm = useCallback(() => {
    if (pendingPasteText) {
      instanceRefs.current
        .get(activeTabIdRef.current)
        ?.pasteText(pendingPasteText);
    }
    setPendingPasteText(null);
  }, [pendingPasteText, instanceRefs, activeTabIdRef]);

  const handlePasteCancel = useCallback(() => {
    setPendingPasteText(null);
  }, []);

  return {
    showCopyToast,
    pendingPasteText,
    handleCopyNotify,
    handlePasteRequest,
    handlePasteConfirm,
    handlePasteCancel,
  };
}
