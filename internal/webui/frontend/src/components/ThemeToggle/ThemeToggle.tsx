/**
 * ThemeToggle component.
 * Aether-style pill switch: blue track in dark mode, amber in light, with a
 * sliding white knob. Toggles between light and dark themes.
 */

import type { Theme } from "@/hooks/ui";
import styles from "./ThemeToggle.module.css";

export interface ThemeToggleProps {
  theme: Theme;
  onToggle: () => void;
}

export function ThemeToggle({ theme, onToggle }: ThemeToggleProps) {
  const isDark = theme === "dark";
  const label = isDark ? "Switch to light mode" : "Switch to dark mode";

  return (
    <button
      className={styles.toggle}
      data-dark={isDark || undefined}
      onClick={onToggle}
      aria-label={label}
      title={label}
      aria-pressed={!isDark}
      type="button"
    >
      <span className={styles.knob} aria-hidden="true" />
    </button>
  );
}
