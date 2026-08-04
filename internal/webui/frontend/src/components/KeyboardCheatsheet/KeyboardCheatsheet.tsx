/**
 * Keyboard shortcut cheatsheet overlay.
 * Displays all available shortcuts in a two-column grid, grouped by section.
 * Opens via ? key, closes on Escape or backdrop click.
 */

import { createPortal } from "react-dom";

import {
  useKeyboardShortcuts,
  useRegisterEscapeLayer,
  LAYER_CHEATSHEET,
} from "@/hooks/ui";

import styles from "./KeyboardCheatsheet.module.css";

const SHORTCUT_SECTIONS = [
  {
    title: "Navigation",
    shortcuts: [
      { key: "1", description: "Workspaces" },
      { key: "2", description: "Monitor" },
      { key: "3", description: "Observability" },
      { key: "4", description: "Files" },
      { key: "5", description: "Workspace" },
      { key: "0", description: "Settings" },
    ],
  },
  {
    title: "Actions",
    shortcuts: [
      { key: "Esc", description: "Close panel / modal / dropdown" },
      { key: "⌘K", description: "Focus search" },
      { key: "?", description: "Toggle this cheatsheet" },
    ],
  },
  {
    title: "Views",
    shortcuts: [
      { key: "↑ ↓", description: "Navigate items" },
      { key: "← →", description: "Navigate columns" },
    ],
  },
] as const;

export function KeyboardCheatsheet() {
  const { isCheatsheetOpen, closeCheatsheet } = useKeyboardShortcuts();

  useRegisterEscapeLayer(LAYER_CHEATSHEET, closeCheatsheet, isCheatsheetOpen);

  if (!isCheatsheetOpen) return null;

  return createPortal(
    <div
      className={styles.backdrop}
      onMouseDown={(e) => {
        if (e.target === e.currentTarget) closeCheatsheet();
      }}
    >
      <div
        className={styles.modal}
        role="dialog"
        aria-label="Keyboard shortcuts"
      >
        <h2 className={styles.title}>Keyboard Shortcuts</h2>
        {SHORTCUT_SECTIONS.map((section) => (
          <div key={section.title} className={styles.section}>
            <h3 className={styles.sectionTitle}>{section.title}</h3>
            <div className={styles.grid}>
              {section.shortcuts.map((shortcut) => (
                <div key={shortcut.key} className={styles.row}>
                  <kbd className={styles.kbd}>{shortcut.key}</kbd>
                  <span className={styles.description}>
                    {shortcut.description}
                  </span>
                </div>
              ))}
            </div>
          </div>
        ))}
      </div>
    </div>,
    document.body,
  );
}
