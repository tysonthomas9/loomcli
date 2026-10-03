// Ported from T3 Code apps/web/src/components/chat/MessageCopyButton.tsx at
// commit 2daff8c25. Copyright (c) 2026 T3 Tools Inc. MIT License; see
// THIRD_PARTY_NOTICES.md. Changes: an icon button (inline copy and check
// glyphs, as T3's lucide icons) with a "Copied" label in place of T3's
// tooltip and anchored toast.

import { memo, useCallback, useEffect, useRef, useState } from "react";
import styles from "./ChatMarkdown.module.css";

/** How long a copy shows as done, as in T3's anchored toast. */
const COPIED_MS = 1200;

/**
 * Copies text to the clipboard; copied is true for a moment after it
 * succeeds. A failed copy changes nothing.
 */
export function useCopy(): { copied: boolean; copy: (text: string) => void } {
  const [copied, setCopied] = useState(false);
  const timer = useRef<ReturnType<typeof setTimeout> | null>(null);
  useEffect(
    () => () => {
      if (timer.current) clearTimeout(timer.current);
    },
    [],
  );
  const copy = useCallback((text: string) => {
    if (typeof navigator === "undefined" || !navigator.clipboard) return;
    void navigator.clipboard
      .writeText(text)
      .then(() => {
        if (timer.current) clearTimeout(timer.current);
        setCopied(true);
        timer.current = setTimeout(() => setCopied(false), COPIED_MS);
      })
      .catch(() => {});
  }, []);
  return { copied, copy };
}

export const MessageCopyButton = memo(function MessageCopyButton({
  text,
  label = "Copy message",
}: {
  text: string;
  label?: string;
}) {
  const { copied, copy } = useCopy();
  return (
    <button
      type="button"
      className={styles.chromeAction}
      aria-label={copied ? "Copied" : label}
      title={copied ? "Copied" : label}
      disabled={copied}
      onClick={() => copy(text)}
    >
      <svg
        width="14"
        height="14"
        viewBox="0 0 24 24"
        fill="none"
        stroke="currentColor"
        strokeWidth="2"
        strokeLinecap="round"
        strokeLinejoin="round"
        aria-hidden="true"
      >
        {copied ? (
          <path d="M20 6 9 17l-5-5" />
        ) : (
          <>
            <rect x="9" y="9" width="13" height="13" rx="2" />
            <path d="M5 15H4a2 2 0 0 1-2-2V4a2 2 0 0 1 2-2h9a2 2 0 0 1 2 2v1" />
          </>
        )}
      </svg>
    </button>
  );
});
