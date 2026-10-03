// Ported from T3 Code apps/web/src/components/chat/ComposerControl.tsx at
// commit 2daff8c25. Copyright (c) 2026 T3 Tools Inc. MIT License; see
// THIRD_PARTY_NOTICES.md. Changes: a plain button styled by CSS modules in
// place of T3's Button and Tailwind classes, and an inline chevron glyph in
// place of lucide's ChevronDownIcon.

import {
  forwardRef,
  useEffect,
  useRef,
  useState,
  type ButtonHTMLAttributes,
} from "react";
import styles from "./ModelPicker.module.css";

/**
 * Open state for a composer popover (Loom has no popover primitive; T3 uses
 * Base UI's): a click outside root or Escape closes it.
 */
export function useComposerPopover() {
  const [open, setOpen] = useState(false);
  const rootRef = useRef<HTMLDivElement>(null);
  useEffect(() => {
    if (!open) return;
    const onDown = (e: MouseEvent) => {
      if (!rootRef.current?.contains(e.target as Node)) setOpen(false);
    };
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") setOpen(false);
    };
    document.addEventListener("mousedown", onDown);
    document.addEventListener("keydown", onKey);
    return () => {
      document.removeEventListener("mousedown", onDown);
      document.removeEventListener("keydown", onKey);
    };
  }, [open]);
  return { open, setOpen, rootRef };
}

/** A composer bar control: a small ghost button that opens a menu. */
export const ComposerControl = forwardRef<
  HTMLButtonElement,
  ButtonHTMLAttributes<HTMLButtonElement>
>(function ComposerControl({ className, ...props }, ref) {
  return (
    <button
      ref={ref}
      type="button"
      className={className ? `${styles.control} ${className}` : styles.control}
      {...props}
    />
  );
});

export function ComposerControlChevron() {
  return (
    <svg
      aria-hidden
      className={styles.chevron}
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      strokeWidth={2.25}
      strokeLinecap="round"
      strokeLinejoin="round"
    >
      <path d="m6 9 6 6 6-6" />
    </svg>
  );
}
