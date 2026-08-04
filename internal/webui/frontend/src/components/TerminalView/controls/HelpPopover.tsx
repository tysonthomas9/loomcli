/**
 * HelpPopover component.
 * Shows browser/terminal-native shortcuts and slash commands.
 */

import { useCallback, useEffect, useRef } from "react";

import styles from "./HelpPopover.module.css";

interface HelpPopoverProps {
  isOpen: boolean;
  onClose: () => void;
}

const SHORTCUTS: Array<{ label: string; keys: string }> = [
  { label: "Search in terminal", keys: "Ctrl+F" },
  { label: "Copy", keys: "Ctrl+Shift+C" },
  { label: "Paste", keys: "Ctrl+Shift+V" },
];

const SLASH_COMMANDS: Array<{ command: string; description: string }> = [
  { command: "/help", description: "Show available commands" },
  { command: "/clear", description: "Clear terminal output" },
];

export function HelpPopover({
  isOpen,
  onClose,
}: HelpPopoverProps): JSX.Element | null {
  const popoverRef = useRef<HTMLDivElement>(null);

  const handleKeyDown = useCallback(
    (e: KeyboardEvent) => {
      if (e.key === "Escape") {
        e.preventDefault();
        onClose();
      }
    },
    [onClose],
  );

  const handleClickOutside = useCallback(
    (e: MouseEvent) => {
      if (
        popoverRef.current &&
        !popoverRef.current.contains(e.target as Node)
      ) {
        onClose();
      }
    },
    [onClose],
  );

  useEffect(() => {
    if (!isOpen) return;
    document.addEventListener("keydown", handleKeyDown);
    document.addEventListener("mousedown", handleClickOutside);
    return () => {
      document.removeEventListener("keydown", handleKeyDown);
      document.removeEventListener("mousedown", handleClickOutside);
    };
  }, [isOpen, handleKeyDown, handleClickOutside]);

  // Focus the popover when it opens
  useEffect(() => {
    if (isOpen) popoverRef.current?.focus();
  }, [isOpen]);

  if (!isOpen) return null;

  return (
    <div
      ref={popoverRef}
      className={styles.popover}
      role="dialog"
      aria-label="Terminal help"
      aria-modal="true"
      tabIndex={-1}
      data-testid="terminal-help-popover"
    >
      <div className={styles.sectionTitle}>Keyboard Shortcuts</div>
      {SHORTCUTS.map((s) => (
        <div key={s.keys} className={styles.row}>
          <span>{s.label}</span>
          <kbd className={styles.kbd}>{s.keys}</kbd>
        </div>
      ))}
      <div className={styles.sectionTitle}>Slash Commands</div>
      {SLASH_COMMANDS.map((c) => (
        <div key={c.command} className={styles.row}>
          <code className={styles.command}>{c.command}</code>
          <span className={styles.commandDesc}>{c.description}</span>
        </div>
      ))}
    </div>
  );
}
